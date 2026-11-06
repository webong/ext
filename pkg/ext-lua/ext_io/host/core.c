/* ext_io.host.core: the C module under the Lua host (ext_io.host).
 *
 * It loads the C engine's shared library (libext_host) at run time, so it is built against the
 * Lua headers and ext_host.h only and links to no engine. It moves bytes and policy decisions:
 * JSON is encoded and decoded in Lua (ext_io/host/init.lua), and every rule a host must get
 * right is the engine's.
 *
 * Policies run on the thread making the engine call (ext_host.h), which is the Lua thread that
 * called start or call here, so they are called on that lua_State with lua_pcall: an error or a
 * yield denies, and a policy that calls back into the same host is refused. Lua 5.1 to 5.5 and
 * LuaJIT. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "lauxlib.h"
#include "lua.h"

#define EXT_HOST_STATIC /* declarations only: every engine function is resolved at run time */
#include "ext_host.h"

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
#define STDERR_CAPACITY 65536u

/* The engine functions a host uses, resolved from one loaded library. An engine is never
 * unloaded, as ctypes never unloads one: a host's callbacks and buffers belong to it. */
typedef struct {
  uint32_t (*abi_version)(void);
  ext_status (*create)(const ext_host_options *, const ext_backend_options *, ext_host **);
  ext_status (*start)(ext_host *, const ext_call_options *);
  ext_status (*call)(ext_host *, const uint8_t *, size_t, const ext_call_options *, ext_buffer *);
  ext_status (*invoke)(ext_host *, const uint8_t *, size_t, const ext_call_options *, ext_buffer *);
  ext_status (*drain)(ext_host *, const ext_call_options *);
  void (*close)(ext_host *);
  void (*destroy)(ext_host *);
  ext_host_state (*state)(ext_host *);
  ext_status (*stderr_copy)(ext_host *, uint8_t *, size_t, size_t *);
  void (*buffer_free)(ext_buffer *);
  ext_status (*shared_library)(const ext_shared_library_options *, ext_backend_extension_context *);
} engine_api;

typedef struct {
  const engine_api *api;
} engine_ud;

/* A host. The policies' Lua functions are held in the registry; L is the thread of the engine
 * call in progress (NULL between calls), so a policy runs only inside one. */
typedef struct {
  const engine_api *api;
  ext_host *handle;
  lua_State *L;
  int verify, authorize; /* registry references, or LUA_NOREF to allow */
  int busy;
} host_ud;

static const char *const status_names[] = {
    "ok",       "invalid",  "denied",   "mismatch", "unsupported", "closed",   "timeout",
    "io",       "nomem",    "draining", "not_found", "ambiguous",  "canceled", "updating",
    "capacity", "sequence", "protocol", "expired",  "exists",      "missing",
};
static const char *const state_names[] = {"created", "ready", "draining", "closed", "failed"};

static const char *status_name(ext_status s) {
  return s >= 0 && (size_t)s < sizeof status_names / sizeof *status_names ? status_names[s] : "unknown";
}

/* Raises the host failure as a table that ext_io.host turns into a HostError:
 * {host_error = true, code = <number>, status = <name>, action = <string>}. */
static int raise_status(lua_State *L, ext_status s, const char *action) {
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
  RESOLVE(abi_version, "ext_host_abi_version");
  if (api->abi_version() != EXT_HOST_ABI_VERSION) {
    unsigned got = api->abi_version();
    free(api);
    return luaL_error(L, "ext engine ABI %d, this module needs %d", (int)got, (int)EXT_HOST_ABI_VERSION);
  }
  RESOLVE(create, "ext_host_create");
  RESOLVE(start, "ext_host_start_with_options");
  RESOLVE(call, "ext_host_call");
  RESOLVE(invoke, "ext_host_invoke_with_options");
  RESOLVE(drain, "ext_host_drain_with_options");
  RESOLVE(close, "ext_host_close");
  RESOLVE(destroy, "ext_host_destroy");
  RESOLVE(state, "ext_host_get_state");
  RESOLVE(stderr_copy, "ext_host_process_stderr");
  RESOLVE(buffer_free, "ext_buffer_free");
  RESOLVE(shared_library, "ext_shared_library_backend_create");
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
  lua_State *L = h->L;
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
  luaL_getmetatable(L, HOST_MT);
  lua_setmetatable(L, -2);
  h->verify = policy_ref(L, verify_index);
  h->authorize = policy_ref(L, authorize_index);
  return h;
}

static ext_status create_host(host_ud *h, const char *descriptor, size_t len,
                              const ext_backend_options *backend) {
  ext_host_options options;
  memset(&options, 0, sizeof options);
  options.abi_version = EXT_HOST_ABI_VERSION;
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
  ext_jsonline_process_launch launch;
  ext_backend_options backend;
  host_ud *h;
  ext_status s;
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
  backend.kind = EXT_BACKEND_JSONLINE_PROCESS_LAUNCH;
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
  ext_shared_library_options options;
  ext_backend_extension_context ext;
  ext_backend_options backend;
  host_ud *h;
  ext_status s;
  options.struct_size = (uint32_t)sizeof options;
  options.path = luaL_checkstring(L, 3);
  h = new_host(L, e->api, 4, 5);
  memset(&ext, 0, sizeof ext);
  s = e->api->shared_library(&options, &ext);
  if (s) return raise_status(L, s, "shared library");
  memset(&backend, 0, sizeof backend);
  backend.struct_size = (uint32_t)sizeof backend;
  backend.kind = EXT_BACKEND_EXTENSION_CONTEXT;
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
  if (!h->handle) raise_status(L, EXT_CLOSED, "destroyed");
  if (h->busy) raise_status(L, EXT_INVALID, action); /* a policy may not reenter its host */
  return h;
}

static ext_call_options call_options(lua_State *L, int index) {
  ext_call_options o;
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
  ext_call_options o = call_options(L, 2);
  ext_status s;
  h->busy = 1;
  h->L = L;
  s = h->api->start(h->handle, &o);
  h->L = NULL;
  h->busy = 0;
  if (s) return raise_status(L, s, "start");
  return 0;
}

typedef ext_status (*exchange_fn)(ext_host *, const uint8_t *, size_t, const ext_call_options *,
                                  ext_buffer *);

static int exchange(lua_State *L, int use_call, const char *action) {
  host_ud *h = enter(L, action);
  size_t len;
  const char *data = luaL_checklstring(L, 2, &len);
  ext_call_options o = call_options(L, 3);
  exchange_fn fn = use_call ? h->api->call : h->api->invoke;
  ext_buffer out = {NULL, 0};
  ext_status s;
  h->busy = 1;
  h->L = L;
  s = fn(h->handle, (const uint8_t *)data, len, &o, &out);
  h->L = NULL;
  h->busy = 0;
  if (s) {
    if (out.data) h->api->buffer_free(&out);
    return raise_status(L, s, action);
  }
  lua_pushlstring(L, out.data ? (const char *)out.data : "", out.len);
  if (out.data) h->api->buffer_free(&out);
  return 1;
}

/* host:call_json(request, timeout) -> response: ext_host_call, {contract, operation, payload?}
 * in and a complete response envelope out. */
static int host_call_json(lua_State *L) { return exchange(L, 1, luaL_optstring(L, 4, "call")); }

/* host:invoke(request, timeout) -> response envelope bytes. */
static int host_invoke(lua_State *L) { return exchange(L, 0, "invoke"); }

/* host:drain(timeout) */
static int host_drain(lua_State *L) {
  host_ud *h = enter(L, "drain");
  ext_call_options o = call_options(L, 2);
  ext_status s;
  h->busy = 1;
  h->L = L;
  s = h->api->drain(h->handle, &o);
  h->L = NULL;
  h->busy = 0;
  if (s) return raise_status(L, s, "drain");
  return 0;
}

/* host:close(): aborts the session; harmless after destroy. */
static int host_close(lua_State *L) {
  host_ud *h = check_host(L);
  if (h->busy) return raise_status(L, EXT_INVALID, "close");
  if (h->handle) h->api->close(h->handle);
  return 0;
}

/* host:state() -> name */
static int host_state(lua_State *L) {
  host_ud *h = enter(L, "state");
  ext_host_state s = h->api->state(h->handle);
  lua_pushstring(L, (int)s >= 0 && (size_t)s < sizeof state_names / sizeof *state_names ? state_names[s] : "unknown");
  return 1;
}

/* host:stderr() -> bytes, bounded and untrusted */
static int host_stderr(lua_State *L) {
  host_ud *h = enter(L, "stderr");
  size_t len = 0;
  uint8_t *buf = (uint8_t *)lua_newuserdata(L, STDERR_CAPACITY);
  ext_status s = h->api->stderr_copy(h->handle, buf, STDERR_CAPACITY, &len);
  if (s) return raise_status(L, s, "stderr");
  lua_pushlstring(L, (const char *)buf, len < STDERR_CAPACITY ? len : STDERR_CAPACITY);
  return 1;
}

/* host:destroy(): frees the host once; again is harmless. Also __gc and __close. */
static int host_destroy(lua_State *L) {
  host_ud *h = check_host(L);
  if (h->busy) return raise_status(L, EXT_INVALID, "destroy");
  if (h->handle) {
    h->api->close(h->handle);
    h->api->destroy(h->handle);
    h->handle = NULL;
  }
  if (h->verify != LUA_NOREF) luaL_unref(L, LUA_REGISTRYINDEX, h->verify);
  if (h->authorize != LUA_NOREF) luaL_unref(L, LUA_REGISTRYINDEX, h->authorize);
  h->verify = h->authorize = LUA_NOREF;
  return 0;
}

static int host_destroyed(lua_State *L) {
  lua_pushboolean(L, check_host(L)->handle == NULL);
  return 1;
}

static const luaL_Reg engine_methods[] = {
    {"process", engine_process},
    {"shared_library", engine_shared_library},
    {NULL, NULL},
};

static const luaL_Reg host_methods[] = {
    {"start", host_start},     {"call_json", host_call_json}, {"invoke", host_invoke},
    {"drain", host_drain},     {"close", host_close},         {"state", host_state},
    {"stderr", host_stderr},   {"destroy", host_destroy},     {"destroyed", host_destroyed},
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
  lua_newtable(L);
  luaL_setfuncs(L, functions, 0);
  lua_pushinteger(L, EXT_HOST_ABI_VERSION);
  lua_setfield(L, -2, "ABI_VERSION");
  lua_newtable(L);
  for (i = 0; i < sizeof status_names / sizeof *status_names; i++) {
    lua_pushstring(L, status_names[i]);
    lua_rawseti(L, -2, (int)i);
  }
  lua_setfield(L, -2, "STATUS_NAMES");
  return 1;
}
