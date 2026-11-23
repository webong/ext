/* ext_io.host.core: the C module under the Lua host (ext_io.host).
 *
 * It loads the C engine's shared library (libext_plugin_host) at run time, so it is built against the
 * Lua headers and ext_plugin_host.h only and links to no engine. It moves bytes and policy decisions:
 * JSON is encoded and decoded in Lua (ext_io/host/init.lua), and every rule a host must get
 * right is the engine's.
 *
 * Policies run on the thread making the engine call (ext_plugin_host.h), which is the Lua thread that
 * called start or call here, so they are called on that lua_State with lua_pcall: an error or a
 * yield denies, and a policy that calls back into the same host is refused. Lua 5.1 to 5.5 and
 * LuaJIT. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "lauxlib.h"
#include "lua.h"

#define EXT_PLUGIN_HOST_STATIC /* declarations only: every engine function is resolved at run time */
#include "ext_plugin_host.h"

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

#if LUA_VERSION_NUM < 502
#define lua_rawlen lua_objlen
static void compat_setfuncs(lua_State *L, const luaL_Reg *l) {
  for (; l->name; l++) {
    lua_pushcfunction(L, l->func);
    lua_setfield(L, -2, l->name);
  }
}
#define luaL_setfuncs(L, l, n) compat_setfuncs(L, l)
#endif

#define ENGINE_MT "ext_io.host.Engine"
#define HOST_MT "ext_io.host.Host"
#define REGISTRY_MT "ext_io.host.Registry"
#define STDERR_CAPACITY 65536u

/* The engine functions a host uses, resolved from one loaded library. An engine is never
 * unloaded, as ctypes never unloads one: a host's callbacks and buffers belong to it. */
typedef struct {
  uint32_t (*abi_version)(void);
  ext_plugin_status (*create)(const ext_plugin_host_options *, const ext_plugin_backend_options *, ext_plugin_host **);
  ext_plugin_status (*start)(ext_plugin_host *, const ext_plugin_call_options *);
  ext_plugin_status (*call)(ext_plugin_host *, const uint8_t *, size_t, const ext_plugin_call_options *, ext_plugin_buffer *);
  ext_plugin_status (*invoke)(ext_plugin_host *, const uint8_t *, size_t, const ext_plugin_call_options *, ext_plugin_buffer *);
  ext_plugin_status (*drain)(ext_plugin_host *, const ext_plugin_call_options *);
  void (*close)(ext_plugin_host *);
  void (*destroy)(ext_plugin_host *);
  ext_plugin_host_state (*state)(ext_plugin_host *);
  ext_plugin_status (*stderr_copy)(ext_plugin_host *, uint8_t *, size_t, size_t *);
  void (*buffer_free)(ext_plugin_buffer *);
  ext_plugin_status (*shared_library)(const ext_plugin_shared_library_options *, ext_plugin_backend_extension_context *);
  ext_plugin_status (*set_services)(ext_plugin_host *, const ext_plugin_host_services *);
  ext_plugin_status (*set_reverse_mode)(ext_plugin_host *, uint32_t);
  ext_plugin_status (*serve_reverse)(ext_plugin_host *, const ext_plugin_call_options *);
  ext_plugin_status (*registry_create)(ext_plugin_registry **);
  ext_plugin_status (*registry_register)(ext_plugin_registry *, const char *, const char *, ext_plugin_host *);
  ext_plugin_status (*registry_unregister)(ext_plugin_registry *, const char *, const char *);
  ext_plugin_status (*registry_route)(ext_plugin_registry *, const ext_plugin_registry_edge *);
  ext_plugin_status (*registry_attach)(ext_plugin_registry *, ext_plugin_host *, const ext_plugin_host_services *);
  void (*registry_destroy)(ext_plugin_registry *);
} engine_api;

typedef struct {
  const engine_api *api;
} engine_ud;

/* A host. The policies' Lua functions are held in the registry; L is the thread of the engine
 * call in progress (NULL between calls), so a policy runs only inside one. */
/* A guest registry, counted: its userdata, each host attached to it and each contract
 * registered in it hold a reference, so the native registry is freed only once none of them can
 * call into it, whatever order the collector frees them in. */
typedef struct registration registration;
typedef struct {
  const engine_api *api;
  ext_plugin_registry *handle;
  int refs;
  registration *entries;
} registry_ref;

typedef struct host_ud host_ud;
struct registration {
  registry_ref *registry;
  host_ud *host;
  int host_ref; /* a strong reference: a registered host lives until it leaves */
  char *name, *version;
  registration *next_in_registry, *next_in_host;
};

static void registry_release(registry_ref *r) {
  if (r && --r->refs == 0) {
    r->api->registry_destroy(r->handle);
    free(r);
  }
}

struct host_ud {
  const engine_api *api;
  ext_plugin_host *handle;
  lua_State *L;
  int verify, authorize; /* registry references, or LUA_NOREF to allow */
  int busy;
  /* Reverse services: references to the hooks, or LUA_NOREF. In the engine's caller-thread mode
   * they run on the thread of an engine call in progress. */
  int reverse_authorize, reverse_handle;
  int source_ref;            /* a reference to the registry userdata this host is a source of */
  registry_ref *source_of;   /* its native registry, counted */
  registration *registrations;
};

/* The Lua thread of the innermost engine call in progress: a registry forward call reaches
 * another host from C, inside a call on this one, and that host's policies and hooks run here. */
static lua_State *active_L;

static const char *const status_names[] = {
    "ok",       "invalid",  "denied",   "mismatch", "unsupported", "closed",   "timeout",
    "io",       "nomem",    "draining", "not_found", "ambiguous",  "canceled", "updating",
    "capacity", "sequence", "protocol", "expired",  "exists",      "missing",
};
static const char *const state_names[] = {"created", "ready", "draining", "closed", "failed"};

static const char *status_name(ext_plugin_status s) {
  return s >= 0 && (size_t)s < sizeof status_names / sizeof *status_names ? status_names[s] : "unknown";
}

/* Raises the host failure as a table that ext_io.host turns into a HostError:
 * {host_error = true, code = <number>, status = <name>, action = <string>}. */
static int raise_status(lua_State *L, ext_plugin_status s, const char *action) {
  lua_newtable(L);
  lua_pushboolean(L, 1);
  lua_setfield(L, -2, "host_error");
  lua_pushinteger(L, s);
  lua_setfield(L, -2, "code");
  lua_pushstring(L, status_name(s));
  lua_setfield(L, -2, "status");
  lua_pushstring(L, action);
  lua_setfield(L, -2, "action");
  return lua_error(L);
}

/* ---- loading the engine ---- */

static void *library_open(const char *path, char *why, size_t n) {
#ifdef _WIN32
  int wide = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, NULL, 0);
  wchar_t *w;
  HMODULE m;
  if (wide <= 0) {
    snprintf(why, n, "the path is not UTF-8");
    return NULL;
  }
  w = (wchar_t *)malloc((size_t)wide * sizeof *w);
  if (!w) {
    snprintf(why, n, "out of memory");
    return NULL;
  }
  MultiByteToWideChar(CP_UTF8, 0, path, -1, w, wide);
  m = LoadLibraryExW(w, NULL, LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_DEFAULT_DIRS);
  if (!m) m = LoadLibraryW(w); /* a bare name: the default search order */
  free(w);
  if (!m) snprintf(why, n, "LoadLibrary failed (%lu)", (unsigned long)GetLastError());
  return (void *)m;
#else
  void *h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
  if (!h) {
    const char *e = dlerror();
    snprintf(why, n, "%s", e ? e : "dlopen failed");
  }
  return h;
#endif
}

static void *library_symbol(void *lib, const char *name) {
#ifdef _WIN32
  return (void *)GetProcAddress((HMODULE)lib, name);
#else
  return dlsym(lib, name);
#endif
}

/* core.open(path) -> engine. Raises a string error naming what failed. */
static int core_open(lua_State *L) {
  const char *path = luaL_checkstring(L, 1);
  char why[512] = {0};
  engine_api *api;
  engine_ud *ud;
  void *lib = library_open(path, why, sizeof why);
  if (!lib) return luaL_error(L, "the ext engine library could not be loaded from %s: %s", path, why);
  api = (engine_api *)calloc(1, sizeof *api);
  if (!api) return luaL_error(L, "out of memory");
#define RESOLVE(field, name)                                                                   \
  do {                                                                                         \
    *(void **)(&api->field) = library_symbol(lib, name);                                       \
    if (!api->field) {                                                                         \
      free(api);                                                                               \
      return luaL_error(L, "%s is not an ext engine library: %s is missing", path, name);      \
    }                                                                                          \
  } while (0)
  RESOLVE(abi_version, "ext_plugin_host_abi_version");
  if (api->abi_version() != EXT_PLUGIN_HOST_ABI_VERSION) {
    unsigned got = api->abi_version();
    free(api);
    return luaL_error(L, "ext engine ABI %d, this module needs %d", (int)got, (int)EXT_PLUGIN_HOST_ABI_VERSION);
  }
  RESOLVE(create, "ext_plugin_host_create");
  RESOLVE(start, "ext_plugin_host_start_with_options");
  RESOLVE(call, "ext_plugin_host_call");
  RESOLVE(invoke, "ext_plugin_host_invoke_with_options");
  RESOLVE(drain, "ext_plugin_host_drain_with_options");
  RESOLVE(close, "ext_plugin_host_close");
  RESOLVE(destroy, "ext_plugin_host_destroy");
  RESOLVE(state, "ext_plugin_host_get_state");
  RESOLVE(stderr_copy, "ext_plugin_host_process_stderr");
  RESOLVE(buffer_free, "ext_plugin_buffer_free");
  RESOLVE(shared_library, "ext_plugin_shared_library_backend_create");
  RESOLVE(set_services, "ext_plugin_host_set_services");
  RESOLVE(set_reverse_mode, "ext_plugin_host_set_reverse_mode");
  RESOLVE(serve_reverse, "ext_plugin_host_serve_reverse");
  RESOLVE(registry_create, "ext_plugin_registry_create");
  RESOLVE(registry_register, "ext_plugin_registry_register");
  RESOLVE(registry_unregister, "ext_plugin_registry_unregister");
  RESOLVE(registry_route, "ext_plugin_registry_route");
  RESOLVE(registry_attach, "ext_plugin_registry_attach");
  RESOLVE(registry_destroy, "ext_plugin_registry_destroy");
#undef RESOLVE
  ud = (engine_ud *)lua_newuserdata(L, sizeof *ud);
  ud->api = api; /* never freed: the library stays loaded, and so does its table */
  luaL_getmetatable(L, ENGINE_MT);
  lua_setmetatable(L, -2);
  return 1;
}

/* ---- policies ---- */

/* Calls the policy with the borrowed JSON as a string; a true result allows. Anything else, an
 * error, or a call outside an engine call denies. Never unwinds into the engine. */
static int32_t run_policy(host_ud *h, int ref, const uint8_t *json, size_t len) {
  lua_State *L = h->L ? h->L : active_L;
  int top, allowed;
  if (ref == LUA_NOREF) return 0;
  if (!L) return 1;
  top = lua_gettop(L);
  if (!lua_checkstack(L, 3)) return 1;
  lua_rawgeti(L, LUA_REGISTRYINDEX, ref);
  lua_pushlstring(L, (const char *)json, len);
  allowed = lua_pcall(L, 1, 1, 0) == 0 && lua_toboolean(L, -1);
  lua_settop(L, top);
  return allowed ? 0 : 1;
}

static int32_t verify_cb(void *user, const uint8_t *json, size_t len) {
  host_ud *h = (host_ud *)user;
  return run_policy(h, h->verify, json, len);
}

static int32_t authorize_cb(void *user, const uint8_t *json, size_t len) {
  host_ud *h = (host_ud *)user;
  return run_policy(h, h->authorize, json, len);
}

/* ---- creating hosts ---- */

static engine_ud *check_engine(lua_State *L) { return (engine_ud *)luaL_checkudata(L, 1, ENGINE_MT); }

static int policy_ref(lua_State *L, int index) {
  if (lua_isnoneornil(L, index)) return LUA_NOREF;
  luaL_checktype(L, index, LUA_TFUNCTION);
  lua_pushvalue(L, index);
  return luaL_ref(L, LUA_REGISTRYINDEX);
}

/* Pushes a new, empty host bound to engine and its policies (stack index 3 and 4). */
static host_ud *new_host(lua_State *L, const engine_api *api, int verify_index, int authorize_index) {
  host_ud *h = (host_ud *)lua_newuserdata(L, sizeof *h);
  memset(h, 0, sizeof *h);
  h->api = api;
  h->verify = LUA_NOREF;
  h->authorize = LUA_NOREF;
  h->reverse_authorize = h->reverse_handle = h->source_ref = LUA_NOREF;
  luaL_getmetatable(L, HOST_MT);
  lua_setmetatable(L, -2);
  h->verify = policy_ref(L, verify_index);
  h->authorize = policy_ref(L, authorize_index);
  return h;
}

static ext_plugin_status create_host(host_ud *h, const char *descriptor, size_t len,
                              const ext_plugin_backend_options *backend) {
  ext_plugin_host_options options;
  memset(&options, 0, sizeof options);
  options.abi_version = EXT_PLUGIN_HOST_ABI_VERSION;
  options.struct_size = (uint32_t)sizeof options;
  options.descriptor = (const uint8_t *)descriptor;
  options.descriptor_len = len;
  options.verify = verify_cb;
  options.authorize = authorize_cb;
  options.user = h;
  return h->api->create(&options, backend, &h->handle);
}

/* Reads an array of strings at index into a NULL-free array; returns its count. */
static size_t string_array(lua_State *L, int index, const char ***out) {
  size_t n, i;
  const char **items;
  *out = NULL;
  if (lua_isnoneornil(L, index)) return 0;
  luaL_checktype(L, index, LUA_TTABLE);
  n = lua_rawlen(L, index);
  if (n == 0) return 0;
  items = (const char **)lua_newuserdata(L, n * sizeof *items); /* collected with the call */
  for (i = 0; i < n; i++) {
    lua_rawgeti(L, index, (int)(i + 1));
    if (lua_type(L, -1) != LUA_TSTRING) luaL_error(L, "every entry of argument %d must be a string", index);
    items[i] = lua_tostring(L, -1);
    lua_pop(L, 1); /* still referenced by the table */
  }
  *out = items;
  return n;
}

/* engine:process(descriptor, executable, arguments, environment, working_directory,
 *                stderr_bytes, verify, authorize) -> host */
static int engine_process(lua_State *L) {
  engine_ud *e = check_engine(L);
  size_t len;
  const char *descriptor = luaL_checklstring(L, 2, &len);
  const char *executable = luaL_checkstring(L, 3);
  const char *working = luaL_optstring(L, 6, NULL);
  lua_Integer stderr_bytes = luaL_optinteger(L, 7, 4096);
  ext_plugin_jsonline_process_launch launch;
  ext_plugin_backend_options backend;
  host_ud *h;
  ext_plugin_status s;
  luaL_argcheck(L, stderr_bytes >= 0 && stderr_bytes <= 0xFFFFFFFF, 7, "out of range");
  memset(&launch, 0, sizeof launch);
  launch.struct_size = (uint32_t)sizeof launch;
  launch.executable = executable;
  launch.argument_count = string_array(L, 4, (const char ***)&launch.arguments);
  launch.environment_count = string_array(L, 5, (const char ***)&launch.environment);
  launch.working_directory = working;
  launch.stderr_bytes = (uint32_t)stderr_bytes;
  memset(&backend, 0, sizeof backend);
  backend.struct_size = (uint32_t)sizeof backend;
  backend.kind = EXT_PLUGIN_BACKEND_JSONLINE_PROCESS_LAUNCH;
  backend.config = &launch;
  backend.config_size = sizeof launch;
  h = new_host(L, e->api, 8, 9);
  s = create_host(h, descriptor, len, &backend);
  if (s) return raise_status(L, s, "create");
  return 1;
}

/* engine:shared_library(descriptor, path, verify, authorize) -> host */
static int engine_shared_library(lua_State *L) {
  engine_ud *e = check_engine(L);
  size_t len;
  const char *descriptor = luaL_checklstring(L, 2, &len);
  ext_plugin_shared_library_options options;
  ext_plugin_backend_extension_context ext;
  ext_plugin_backend_options backend;
  host_ud *h;
  ext_plugin_status s;
  options.struct_size = (uint32_t)sizeof options;
  options.path = luaL_checkstring(L, 3);
  h = new_host(L, e->api, 4, 5);
  memset(&ext, 0, sizeof ext);
  s = e->api->shared_library(&options, &ext);
  if (s) return raise_status(L, s, "shared library");
  memset(&backend, 0, sizeof backend);
  backend.struct_size = (uint32_t)sizeof backend;
  backend.kind = EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT;
  backend.config = &ext;
  backend.config_size = sizeof ext;
  s = create_host(h, descriptor, len, &backend);
  if (s) {
    ext.release(ext.user); /* ownership stays with the caller on a failed create */
    return raise_status(L, s, "create");
  }
  return 1;
}

/* ---- using hosts ---- */

static host_ud *check_host(lua_State *L) { return (host_ud *)luaL_checkudata(L, 1, HOST_MT); }

/* The host, ready for one engine call from L: refuses a destroyed host and a call from inside
 * a policy of the same host. */
static host_ud *enter(lua_State *L, const char *action) {
  host_ud *h = check_host(L);
  if (!h->handle) raise_status(L, EXT_PLUGIN_CLOSED, "destroyed");
  if (h->busy) raise_status(L, EXT_PLUGIN_INVALID, action); /* a policy may not reenter its host */
  return h;
}

static ext_plugin_call_options call_options(lua_State *L, int index) {
  ext_plugin_call_options o;
  lua_Number seconds = luaL_optnumber(L, index, 3.0);
  lua_Number ms = seconds * 1000.0;
  luaL_argcheck(L, ms >= 1.0, index, "timeout must be at least 1 ms");
  memset(&o, 0, sizeof o);
  o.struct_size = (uint32_t)sizeof o;
  o.timeout_ms = ms >= 4294967295.0 ? 0xFFFFFFFFu : (uint32_t)ms;
  return o;
}

/* host:start(timeout) */
static int host_start(lua_State *L) {
  host_ud *h = enter(L, "start");
  ext_plugin_call_options o = call_options(L, 2);
  ext_plugin_status s;
  lua_State *outer = active_L;
  h->busy = 1;
  h->L = L;
  active_L = L;
  s = h->api->start(h->handle, &o);
  h->L = NULL;
  h->busy = 0;
  active_L = outer;
  if (s) return raise_status(L, s, "start");
  return 0;
}

typedef ext_plugin_status (*exchange_fn)(ext_plugin_host *, const uint8_t *, size_t, const ext_plugin_call_options *,
                                  ext_plugin_buffer *);

static int exchange(lua_State *L, int use_call, const char *action) {
  host_ud *h = enter(L, action);
  size_t len;
  const char *data = luaL_checklstring(L, 2, &len);
  ext_plugin_call_options o = call_options(L, 3);
  exchange_fn fn = use_call ? h->api->call : h->api->invoke;
  ext_plugin_buffer out = {NULL, 0};
  ext_plugin_status s;
  lua_State *outer = active_L;
  h->busy = 1;
  h->L = L;
  active_L = L;
  s = fn(h->handle, (const uint8_t *)data, len, &o, &out);
  h->L = NULL;
  h->busy = 0;
  active_L = outer;
  if (s) {
    if (out.data) h->api->buffer_free(&out);
    return raise_status(L, s, action);
  }
  lua_pushlstring(L, out.data ? (const char *)out.data : "", out.len);
  if (out.data) h->api->buffer_free(&out);
  return 1;
}

/* host:call_json(request, timeout) -> response: ext_plugin_host_call, {contract, operation, payload?}
 * in and a complete response envelope out. */
static int host_call_json(lua_State *L) { return exchange(L, 1, luaL_optstring(L, 4, "call")); }

/* host:invoke(request, timeout) -> response envelope bytes. */
static int host_invoke(lua_State *L) { return exchange(L, 0, "invoke"); }

/* host:drain(timeout) */
static int host_drain(lua_State *L) {
  host_ud *h = enter(L, "drain");
  ext_plugin_call_options o = call_options(L, 2);
  ext_plugin_status s;
  lua_State *outer = active_L;
  h->busy = 1;
  h->L = L;
  active_L = L;
  s = h->api->drain(h->handle, &o);
  h->L = NULL;
  h->busy = 0;
  active_L = outer;
  if (s) return raise_status(L, s, "drain");
  return 0;
}

/* host:close(): aborts the session; harmless after destroy. */
static int host_close(lua_State *L) {
  host_ud *h = check_host(L);
  if (h->busy) return raise_status(L, EXT_PLUGIN_INVALID, "close");
  if (h->handle) h->api->close(h->handle);
  return 0;
}

/* host:state() -> name */
static int host_state(lua_State *L) {
  host_ud *h = enter(L, "state");
  ext_plugin_host_state s = h->api->state(h->handle);
  lua_pushstring(L, (int)s >= 0 && (size_t)s < sizeof state_names / sizeof *state_names ? state_names[s] : "unknown");
  return 1;
}

/* host:stderr() -> bytes, bounded and untrusted */
static int host_stderr(lua_State *L) {
  host_ud *h = enter(L, "stderr");
  size_t len = 0;
  uint8_t *buf = (uint8_t *)lua_newuserdata(L, STDERR_CAPACITY);
  ext_plugin_status s = h->api->stderr_copy(h->handle, buf, STDERR_CAPACITY, &len);
  if (s) return raise_status(L, s, "stderr");
  lua_pushlstring(L, (const char *)buf, len < STDERR_CAPACITY ? len : STDERR_CAPACITY);
  return 1;
}

/* host:destroy(): frees the host once; again is harmless. Also __gc and __close. */
static void unref(lua_State *L, int *ref) {
  if (*ref != LUA_NOREF) luaL_unref(L, LUA_REGISTRYINDEX, *ref);
  *ref = LUA_NOREF;
}

static void unlink_from_registry(registration *e) {
  registration **at = &e->registry->entries;
  while (*at && *at != e) at = &(*at)->next_in_registry;
  if (*at) *at = e->next_in_registry;
}

static void free_registration(lua_State *L, registration *e) {
  unref(L, &e->host_ref);
  registry_release(e->registry);
  free(e->name);
  free(e->version);
  free(e);
}

static int host_destroy(lua_State *L) {
  host_ud *h = check_host(L);
  if (h->busy) return raise_status(L, EXT_PLUGIN_INVALID, "destroy");
  if (h->handle) {
    h->api->close(h->handle);
    /* Calls routed here have ended with the close; unregistering waits for them. */
    while (h->registrations) {
      registration *e = h->registrations;
      h->registrations = e->next_in_host;
      h->api->registry_unregister(e->registry->handle, e->name, e->version);
      unlink_from_registry(e);
      free_registration(L, e);
    }
    h->api->destroy(h->handle);
    h->handle = NULL;
    registry_release(h->source_of);
    h->source_of = NULL;
  }
  unref(L, &h->verify);
  unref(L, &h->authorize);
  unref(L, &h->reverse_authorize);
  unref(L, &h->reverse_handle);
  unref(L, &h->source_ref);
  return 0;
}

/* ---- reverse services ---- */

/* Calls a hook with the request JSON and the milliseconds left. authorize returns true to allow;
 * handle returns (public_error, json). Runs only inside an engine call on this Lua state. */
static int call_hook(host_ud *h, int ref, const uint8_t *json, size_t len, uint32_t ms, int results) {
  lua_State *L = h->L ? h->L : active_L;
  if (ref == LUA_NOREF || !L || !lua_checkstack(L, 4)) return -1;
  lua_rawgeti(L, LUA_REGISTRYINDEX, ref);
  lua_pushlstring(L, (const char *)json, len);
  lua_pushnumber(L, (lua_Number)ms);
  return lua_pcall(L, 2, results, 0) == 0 ? lua_gettop(L) : -1;
}

static int32_t reverse_authorize_cb(void *user, const ext_plugin_call_options *o, const uint8_t *json, size_t len) {
  host_ud *h = (host_ud *)user;
  lua_State *L = h->L ? h->L : active_L;
  int top = L ? lua_gettop(L) : 0, allowed = 0;
  if (call_hook(h, h->reverse_authorize, json, len, o->timeout_ms, 1) > 0) allowed = lua_toboolean(L, -1);
  if (L) lua_settop(L, top);
  return allowed ? 0 : 1;
}

static ext_plugin_status reverse_handle_cb(void *user, const ext_plugin_call_options *o, const uint8_t *json, size_t len,
                                    ext_plugin_reverse_emit emit, void *sink) {
  host_ud *h = (host_ud *)user;
  lua_State *L = h->L ? h->L : active_L;
  int top = L ? lua_gettop(L) : 0;
  ext_plugin_status s = EXT_PLUGIN_IO; /* the plugin sees operation_failed */
  if (call_hook(h, h->reverse_handle, json, len, o->timeout_ms, 2) > 0 && lua_type(L, -1) == LUA_TSTRING) {
    size_t n = 0;
    const char *body = lua_tolstring(L, -1, &n);
    s = emit(sink, lua_toboolean(L, -2) ? EXT_PLUGIN_HOST_REVERSE_PUBLIC_ERROR : EXT_PLUGIN_HOST_REVERSE_PAYLOAD,
             (const uint8_t *)body, n);
  }
  if (L) lua_settop(L, top);
  return s;
}

static ext_plugin_host_services reverse_services(lua_State *L, host_ud *h, int first, int with_handle) {
  ext_plugin_host_services services;
  memset(&services, 0, sizeof services);
  services.struct_size = (uint32_t)sizeof services;
  services.user = h;
  services.authorize = reverse_authorize_cb;
  services.handle = with_handle ? reverse_handle_cb : NULL;
  services.timeout_ms = (uint32_t)luaL_optinteger(L, first, 0);
  services.max_payload_bytes = (uint32_t)luaL_optinteger(L, first + 1, 0);
  services.max_in_flight = (uint32_t)luaL_optinteger(L, first + 2, 0);
  return services;
}

/* host:serve(authorize, handle, timeout_ms, max_payload, max_in_flight): before start. */
static int host_serve(lua_State *L) {
  host_ud *h = enter(L, "serve");
  ext_plugin_host_services services;
  ext_plugin_status s;
  luaL_checktype(L, 2, LUA_TFUNCTION);
  luaL_checktype(L, 3, LUA_TFUNCTION);
  services = reverse_services(L, h, 4, 1);
  s = h->api->set_reverse_mode(h->handle, EXT_PLUGIN_HOST_REVERSE_CALLER_THREAD);
  if (!s) s = h->api->set_services(h->handle, &services);
  if (s) return raise_status(L, s, "serve");
  unref(L, &h->reverse_authorize);
  unref(L, &h->reverse_handle);
  h->reverse_authorize = policy_ref(L, 2);
  h->reverse_handle = policy_ref(L, 3);
  return 0;
}

/* host:serve_reverse(timeout) -> true when it served a request, false when none came in time. */
static int host_serve_reverse(lua_State *L) {
  host_ud *h = enter(L, "serve_reverse");
  ext_plugin_call_options o = call_options(L, 2);
  ext_plugin_status s;
  lua_State *outer = active_L;
  h->busy = 1;
  h->L = L;
  active_L = L;
  s = h->api->serve_reverse(h->handle, &o);
  h->L = NULL;
  h->busy = 0;
  active_L = outer;
  if (s == EXT_PLUGIN_TIMEOUT) {
    lua_pushboolean(L, 0);
    return 1;
  }
  if (s) return raise_status(L, s, "serve_reverse");
  lua_pushboolean(L, 1);
  return 1;
}

/* ---- registries ---- */

typedef struct {
  registry_ref *ref;
} registry_ud;

static registry_ref *check_registry(lua_State *L, int index) {
  return ((registry_ud *)luaL_checkudata(L, index, REGISTRY_MT))->ref;
}

/* engine:registry() -> a new registry */
static int engine_registry(lua_State *L) {
  const engine_api *api = check_engine(L)->api;
  registry_ud *ud = (registry_ud *)lua_newuserdata(L, sizeof *ud);
  registry_ref *r;
  ext_plugin_status s;
  ud->ref = NULL;
  luaL_getmetatable(L, REGISTRY_MT);
  lua_setmetatable(L, -2);
  r = (registry_ref *)calloc(1, sizeof *r);
  if (!r) return luaL_error(L, "out of memory");
  r->api = api;
  r->refs = 1;
  s = api->registry_create(&r->handle);
  if (s) {
    free(r);
    return raise_status(L, s, "registry");
  }
  ud->ref = r;
  return 1;
}

static int registry_gc(lua_State *L) {
  registry_ud *ud = (registry_ud *)luaL_checkudata(L, 1, REGISTRY_MT);
  registry_release(ud->ref);
  ud->ref = NULL;
  return 0;
}

static char *copy_string(const char *s) {
  size_t n = strlen(s) + 1;
  char *c = (char *)malloc(n);
  if (c) memcpy(c, s, n);
  return c;
}

/* registry:register(name, version, host) */
static int registry_register(lua_State *L) {
  registry_ref *r = check_registry(L, 1);
  const char *name = luaL_checkstring(L, 2), *version = luaL_checkstring(L, 3);
  host_ud *h = (host_ud *)luaL_checkudata(L, 4, HOST_MT);
  registration *e;
  ext_plugin_status s;
  if (!h->handle) return raise_status(L, EXT_PLUGIN_CLOSED, "destroyed");
  s = r->api->registry_register(r->handle, name, version, h->handle);
  if (s) return raise_status(L, s, "register");
  e = (registration *)calloc(1, sizeof *e);
  if (!e || !(e->name = copy_string(name)) || !(e->version = copy_string(version))) {
    if (e) free(e->name);
    free(e);
    r->api->registry_unregister(r->handle, name, version);
    return luaL_error(L, "out of memory");
  }
  e->registry = r;
  e->host = h;
  lua_pushvalue(L, 4);
  e->host_ref = luaL_ref(L, LUA_REGISTRYINDEX);
  e->next_in_registry = r->entries;
  r->entries = e;
  e->next_in_host = h->registrations;
  h->registrations = e;
  r->refs++;
  return 0;
}

/* registry:unregister(name, version): waits for the calls routed to its host. */
static int registry_unregister(lua_State *L) {
  registry_ref *r = check_registry(L, 1);
  const char *name = luaL_checkstring(L, 2), *version = luaL_checkstring(L, 3);
  ext_plugin_status s = r->api->registry_unregister(r->handle, name, version);
  if (s) return raise_status(L, s, "unregister");
  for (registration **at = &r->entries; *at; at = &(*at)->next_in_registry) {
    registration *e = *at;
    if (!strcmp(e->name, name) && !strcmp(e->version, version)) {
      *at = e->next_in_registry;
      for (registration **in = &e->host->registrations; *in; in = &(*in)->next_in_host)
        if (*in == e) {
          *in = e->next_in_host;
          break;
        }
      free_registration(L, e);
      break;
    }
  }
  return 0;
}

/* registry:route(from_name, from_version, from_operation, to_name, to_version, to_operation) */
static int registry_route(lua_State *L) {
  registry_ref *r = check_registry(L, 1);
  ext_plugin_registry_edge edge;
  ext_plugin_status s;
  memset(&edge, 0, sizeof edge);
  edge.struct_size = (uint32_t)sizeof edge;
  edge.from_name = luaL_checkstring(L, 2);
  edge.from_version = luaL_checkstring(L, 3);
  edge.from_operation = luaL_checkstring(L, 4);
  edge.to_name = luaL_checkstring(L, 5);
  edge.to_version = luaL_checkstring(L, 6);
  edge.to_operation = luaL_checkstring(L, 7);
  s = r->api->registry_route(r->handle, &edge);
  if (s) return raise_status(L, s, "route");
  return 0;
}

/* host:attach(registry, authorize, timeout_ms, max_payload, max_in_flight): a source of the
 * registry, in place of serve, before start. */
static int host_attach(lua_State *L) {
  host_ud *h = enter(L, "attach");
  registry_ref *r = check_registry(L, 2);
  ext_plugin_host_services services;
  ext_plugin_status s;
  luaL_checktype(L, 3, LUA_TFUNCTION);
  services = reverse_services(L, h, 4, 0);
  s = h->api->set_reverse_mode(h->handle, EXT_PLUGIN_HOST_REVERSE_CALLER_THREAD);
  if (!s) s = r->api->registry_attach(r->handle, h->handle, &services);
  if (s) return raise_status(L, s, "attach");
  unref(L, &h->reverse_authorize);
  h->reverse_authorize = policy_ref(L, 3);
  registry_release(h->source_of);
  h->source_of = r;
  r->refs++;
  unref(L, &h->source_ref);
  lua_pushvalue(L, 2);
  h->source_ref = luaL_ref(L, LUA_REGISTRYINDEX);
  return 0;
}

static int host_destroyed(lua_State *L) {
  lua_pushboolean(L, check_host(L)->handle == NULL);
  return 1;
}

static const luaL_Reg engine_methods[] = {
    {"process", engine_process},
    {"shared_library", engine_shared_library},
    {"registry", engine_registry},
    {NULL, NULL},
};

static const luaL_Reg host_methods[] = {
    {"start", host_start},     {"call_json", host_call_json}, {"invoke", host_invoke},
    {"drain", host_drain},     {"close", host_close},         {"state", host_state},
    {"stderr", host_stderr},   {"destroy", host_destroy},     {"destroyed", host_destroyed},
    {"serve", host_serve},     {"serve_reverse", host_serve_reverse}, {"attach", host_attach},
    {NULL, NULL},
};

static const luaL_Reg registry_methods[] = {
    {"register", registry_register},
    {"unregister", registry_unregister},
    {"route", registry_route},
    {NULL, NULL},
};

static void metatable(lua_State *L, const char *name, const luaL_Reg *methods, int closable) {
  luaL_newmetatable(L, name);
  lua_newtable(L);
  luaL_setfuncs(L, methods, 0);
  lua_setfield(L, -2, "__index");
  if (closable) {
    lua_pushcfunction(L, host_destroy);
    lua_setfield(L, -2, "__gc");
    lua_pushcfunction(L, host_destroy);
    lua_setfield(L, -2, "__close");
  }
  lua_pushstring(L, name);
  lua_setfield(L, -2, "__name");
  lua_pop(L, 1);
}

#ifdef _WIN32
#define EXPORT __declspec(dllexport)
#else
#define EXPORT __attribute__((visibility("default")))
#endif

EXPORT int luaopen_ext_io_host_core(lua_State *L) {
  static const luaL_Reg functions[] = {{"open", core_open}, {NULL, NULL}};
  size_t i;
  metatable(L, ENGINE_MT, engine_methods, 0);
  metatable(L, HOST_MT, host_methods, 1);
  metatable(L, REGISTRY_MT, registry_methods, 0);
  luaL_getmetatable(L, REGISTRY_MT);
  lua_pushcfunction(L, registry_gc);
  lua_setfield(L, -2, "__gc");
  lua_pop(L, 1);
  lua_newtable(L);
  luaL_setfuncs(L, functions, 0);
  lua_pushinteger(L, EXT_PLUGIN_HOST_ABI_VERSION);
  lua_setfield(L, -2, "ABI_VERSION");
  lua_newtable(L);
  for (i = 0; i < sizeof status_names / sizeof *status_names; i++) {
    lua_pushstring(L, status_names[i]);
    lua_rawseti(L, -2, (int)i);
  }
  lua_setfield(L, -2, "STATUS_NAMES");
  return 1;
}
