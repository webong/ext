/* ExtIO::Host::Native: the native extension under the Ruby host (lib/ext_io/host.rb).
 *
 * It loads the C engine's shared library (libext_plugin_host) at run time, so it is built against the
 * Ruby headers and ext_plugin_host.h only and links to no engine. It moves bytes and policy decisions:
 * JSON is generated and parsed in Ruby, and every rule a host must get right is the engine's.
 *
 * Every engine call that can block runs on a helper thread while the calling Ruby thread waits
 * without the global VM lock, so other Ruby threads run meanwhile and may call the same host; the
 * engine serializes a host's calls itself. A real interrupt (Thread#raise, Thread#kill, Ctrl-C)
 * signals the call's ext_plugin_cancel, so the call ends and the interrupt is raised; one Ruby only notes,
 * such as a child process ending, leaves the call alone (see blocking_status). The engine runs
 * policies and reverse hooks on the thread making the call, the helper, which hands each to the
 * waiting Ruby thread; there it calls its Ruby callable under rb_protect: an exception denies, and
 * a policy that uses the host it decides for is refused. */
#include <ruby.h>
#include <ruby/encoding.h>
#include <ruby/thread.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define EXT_PLUGIN_HOST_STATIC /* declarations only: every engine function is resolved at run time */
#include "ext_plugin_host.h"

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#include <pthread.h>
#endif

#define STDERR_CAPACITY 65536u

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
  ext_plugin_status (*cancel_create)(ext_plugin_cancel **);
  void (*cancel_signal)(ext_plugin_cancel *);
  void (*cancel_destroy)(ext_plugin_cancel *);
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

static VALUE mNative, cEngine, cHost, cRegistry, eHostError;
static ID id_new, id_call, id_policy_key;

static const char *const status_names[] = {
    "ok",       "invalid",  "denied",   "mismatch", "unsupported", "closed",   "timeout",
    "io",       "nomem",    "draining", "not_found", "ambiguous",  "canceled", "updating",
    "capacity", "sequence", "protocol", "expired",  "exists",      "missing",
};
static const char *const state_names[] = {"created", "ready", "draining", "closed", "failed"};

static const char *status_name(ext_plugin_status s) {
  return s >= 0 && (size_t)s < sizeof status_names / sizeof *status_names ? status_names[s] : "unknown";
}

/* Raises ExtIO::Host::HostError.new(code, status, action). */
NORETURN(static void raise_status(ext_plugin_status s, const char *action));
static void raise_status(ext_plugin_status s, const char *action) {
  VALUE e = rb_funcall(eHostError, id_new, 3, INT2NUM(s), rb_str_new_cstr(status_name(s)),
                       rb_str_new_cstr(action));
  rb_exc_raise(e);
}

/* ---- engines ---- */

typedef struct {
  const engine_api *api;
} engine_t;

static const rb_data_type_t engine_type = {
    "ExtIO::Host::Native::Engine",
    {NULL, RUBY_TYPED_NEVER_FREE, NULL, NULL, {0}},
    0, 0, RUBY_TYPED_FREE_IMMEDIATELY,
};

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

/* Native::Engine.open(path) -> engine. Raises LoadError naming what failed. An engine is never
 * unloaded: its hosts' callbacks and buffers belong to it. */
static VALUE engine_open(VALUE klass, VALUE rpath) {
  const char *path = StringValueCStr(rpath);
  char why[512] = {0};
  engine_api *api;
  engine_t *e;
  VALUE obj;
  void *lib = library_open(path, why, sizeof why);
  if (!lib) rb_raise(rb_eLoadError, "the ext engine library could not be loaded from %s: %s", path, why);
  api = (engine_api *)calloc(1, sizeof *api); /* never freed, as the library is never unloaded */
  if (!api) rb_raise(rb_eNoMemError, "out of memory");
#define RESOLVE(field, name)                                                                   \
  do {                                                                                         \
    *(void **)(&api->field) = library_symbol(lib, name);                                       \
    if (!api->field) {                                                                         \
      free(api);                                                                               \
      rb_raise(rb_eLoadError, "%s is not an ext engine library: %s is missing", path, name);   \
    }                                                                                          \
  } while (0)
  RESOLVE(abi_version, "ext_plugin_host_abi_version");
  if (api->abi_version() != EXT_PLUGIN_HOST_ABI_VERSION) {
    unsigned got = api->abi_version();
    free(api);
    rb_raise(rb_eLoadError, "ext engine ABI %u, this extension needs %u", got, (unsigned)EXT_PLUGIN_HOST_ABI_VERSION);
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
  RESOLVE(cancel_create, "ext_plugin_cancel_create");
  RESOLVE(cancel_signal, "ext_plugin_cancel_signal");
  RESOLVE(cancel_destroy, "ext_plugin_cancel_destroy");
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
  obj = TypedData_Make_Struct(klass, engine_t, &engine_type, e);
  e->api = api;
  return obj;
}

/* ---- hosts ---- */

/* A guest registry, counted: its Ruby object, each host attached to it and each contract
 * registered in it hold a reference, so the native registry is freed only once none of them can
 * call into it, whatever order the garbage collector frees them in. */
typedef struct registration registration;
typedef struct {
  const engine_api *api;
  ext_plugin_registry *handle;
  long refs;
  registration *entries; /* the contracts registered, with their hosts */
} registry_ref;
typedef struct host_t host_t;
struct registration {
  registry_ref *registry;
  host_t *host;
  VALUE host_value; /* marked by the registry: a registered host stays alive with it */
  char *name, *version;
  registration *next_in_registry, *next_in_host;
};
static void registry_release(registry_ref *r) {
  if (r && --r->refs == 0) {
    r->api->registry_destroy(r->handle);
    xfree(r);
  }
}

struct host_t {
  const engine_api *api;
  ext_plugin_host *handle;
  VALUE self;
  VALUE verify, authorize; /* callables, or Qnil to allow */
  long active;             /* engine calls in flight, counted under the lock */
  /* Reverse services: callables, or Qnil. The hooks run on the thread of a call that waits on
   * this host (EXT_PLUGIN_HOST_REVERSE_CALLER_THREAD), never on an engine thread. */
  VALUE reverse_authorize, reverse_handle;
  VALUE source_of;          /* the Native::Registry this host is a source of, or Qnil */
  registry_ref *source_ref; /* its native registry, counted */
  registration *registrations;
};

static void host_mark(void *p) {
  host_t *h = (host_t *)p;
  rb_gc_mark(h->verify);
  rb_gc_mark(h->authorize);
  rb_gc_mark(h->reverse_authorize);
  rb_gc_mark(h->reverse_handle);
  rb_gc_mark(h->source_of);
}

static void unlink_from_registry(registration *e) {
  registration **at = &e->registry->entries;
  while (*at && *at != e) at = &(*at)->next_in_registry;
  if (*at) *at = e->next_in_registry;
}

/* Leaves every registry: the host is closed first, so the calls routed to it end, and
 * unregistering waits for them. */
static void leave_registries(host_t *h) {
  while (h->registrations) {
    registration *e = h->registrations;
    h->registrations = e->next_in_host;
    h->api->registry_unregister(e->registry->handle, e->name, e->version);
    unlink_from_registry(e);
    registry_release(e->registry);
    xfree(e->name);
    xfree(e->version);
    xfree(e);
  }
}

static void host_teardown(host_t *h) {
  h->api->close(h->handle);
  leave_registries(h);
  h->api->destroy(h->handle);
  h->handle = NULL;
  registry_release(h->source_ref);
  h->source_ref = NULL;
}

static void host_free(void *p) {
  host_t *h = (host_t *)p;
  /* Unreachable, so no call is in flight, and no source that could route to it is reachable. */
  if (h->handle) host_teardown(h);
  xfree(h);
}

static const rb_data_type_t host_type = {
    "ExtIO::Host::Native::Host",
    {host_mark, host_free, NULL, NULL, {0}},
    0, 0, 0,
};

/* ---- policies ---- */

typedef struct {
  host_t *host;
  VALUE callable;
  const uint8_t *json;
  size_t len;
  int allowed;
} policy_call;

static VALUE policy_invoke(VALUE arg) {
  policy_call *p = (policy_call *)arg;
  VALUE json = rb_utf8_str_new((const char *)p->json, (long)p->len);
  return rb_funcall(p->callable, id_call, 1, json);
}

/* With the lock: marks this thread as inside a policy of the host, so the host refuses it. */
/* Runs fn on the Ruby thread waiting for the engine call (see blocking_status). */
static void with_ruby(void *(*fn)(void *), void *arg);

static void *policy_with_gvl(void *arg) {
  policy_call *p = (policy_call *)arg;
  VALUE thread = rb_thread_current();
  VALUE before = rb_thread_local_aref(thread, id_policy_key);
  int state = 0;
  VALUE result;
  rb_thread_local_aset(thread, id_policy_key, p->host->self);
  result = rb_protect(policy_invoke, (VALUE)p, &state);
  rb_thread_local_aset(thread, id_policy_key, before);
  if (state) {
    rb_set_errinfo(Qnil); /* an exception denies; it never unwinds into the engine */
    p->allowed = 0;
  } else {
    p->allowed = RTEST(result);
  }
  return NULL;
}

static int32_t run_policy(host_t *h, VALUE callable, const uint8_t *json, size_t len) {
  policy_call p;
  if (NIL_P(callable)) return 0;
  p.host = h;
  p.callable = callable;
  p.json = json;
  p.len = len;
  p.allowed = 0;
  /* The engine calls policies only inside a call made without the lock (start, call, invoke). */
  with_ruby(policy_with_gvl, &p);
  return p.allowed ? 0 : 1;
}

static int32_t verify_cb(void *user, const uint8_t *json, size_t len) {
  host_t *h = (host_t *)user;
  return run_policy(h, h->verify, json, len);
}

static int32_t authorize_cb(void *user, const uint8_t *json, size_t len) {
  host_t *h = (host_t *)user;
  return run_policy(h, h->authorize, json, len);
}

/* ---- creating hosts ---- */

static engine_t *get_engine(VALUE self) {
  engine_t *e;
  TypedData_Get_Struct(self, engine_t, &engine_type, e);
  return e;
}

static VALUE new_host(const engine_api *api, VALUE verify, VALUE authorize, host_t **out) {
  host_t *h;
  VALUE obj = TypedData_Make_Struct(cHost, host_t, &host_type, h);
  h->api = api;
  h->self = obj;
  h->verify = verify;
  h->authorize = authorize;
  h->reverse_authorize = h->reverse_handle = h->source_of = Qnil;
  *out = h;
  return obj;
}

static ext_plugin_status create_host(host_t *h, VALUE descriptor, const ext_plugin_backend_options *backend) {
  ext_plugin_host_options options;
  memset(&options, 0, sizeof options);
  options.abi_version = EXT_PLUGIN_HOST_ABI_VERSION;
  options.struct_size = (uint32_t)sizeof options;
  options.descriptor = (const uint8_t *)RSTRING_PTR(descriptor);
  options.descriptor_len = (size_t)RSTRING_LEN(descriptor);
  options.verify = verify_cb;
  options.authorize = authorize_cb;
  options.user = h;
  return h->api->create(&options, backend, &h->handle); /* copies everything it keeps */
}

/* An Array of Strings as C strings, kept alive by the array for this call. */
static const char **string_array(VALUE array, size_t *count, VALUE *keep) {
  long n, i;
  const char **items;
  *count = 0;
  if (NIL_P(array)) return NULL;
  Check_Type(array, T_ARRAY);
  n = RARRAY_LEN(array);
  if (n == 0) return NULL;
  *keep = rb_str_buf_new((long)(n * (long)sizeof(char *)));
  items = (const char **)RSTRING_PTR(*keep);
  for (i = 0; i < n; i++) {
    VALUE s = rb_ary_entry(array, i);
    items[i] = StringValueCStr(s);
  }
  *count = (size_t)n;
  return items;
}

/* engine.process(descriptor, executable, arguments, environment, working_directory, stderr_bytes,
 *                verify, authorize) -> host */
static VALUE engine_process(VALUE self, VALUE descriptor, VALUE executable, VALUE arguments,
                            VALUE environment, VALUE working, VALUE stderr_bytes, VALUE verify,
                            VALUE authorize) {
  engine_t *e = get_engine(self);
  ext_plugin_jsonline_process_launch launch;
  ext_plugin_backend_options backend;
  VALUE keep_args = Qnil, keep_env = Qnil, obj;
  unsigned long bytes = NIL_P(stderr_bytes) ? 4096ul : NUM2ULONG(stderr_bytes);
  host_t *h;
  ext_plugin_status s;
  StringValue(descriptor);
  if (bytes > 0xFFFFFFFFul) rb_raise(rb_eArgError, "stderr_bytes is out of range");
  memset(&launch, 0, sizeof launch);
  launch.struct_size = (uint32_t)sizeof launch;
  launch.executable = StringValueCStr(executable);
  launch.arguments = string_array(arguments, &launch.argument_count, &keep_args);
  launch.environment = string_array(environment, &launch.environment_count, &keep_env);
  launch.working_directory = NIL_P(working) ? NULL : StringValueCStr(working);
  launch.stderr_bytes = (uint32_t)bytes;
  memset(&backend, 0, sizeof backend);
  backend.struct_size = (uint32_t)sizeof backend;
  backend.kind = EXT_PLUGIN_BACKEND_JSONLINE_PROCESS_LAUNCH;
  backend.config = &launch;
  backend.config_size = sizeof launch;
  obj = new_host(e->api, verify, authorize, &h);
  s = create_host(h, descriptor, &backend);
  RB_GC_GUARD(keep_args);
  RB_GC_GUARD(keep_env);
  RB_GC_GUARD(descriptor);
  if (s) raise_status(s, "create");
  return obj;
}

/* engine.shared_library(descriptor, path, verify, authorize) -> host */
static VALUE engine_shared_library(VALUE self, VALUE descriptor, VALUE path, VALUE verify, VALUE authorize) {
  engine_t *e = get_engine(self);
  ext_plugin_shared_library_options options;
  ext_plugin_backend_extension_context ext;
  ext_plugin_backend_options backend;
  host_t *h;
  VALUE obj;
  ext_plugin_status s;
  StringValue(descriptor);
  options.struct_size = (uint32_t)sizeof options;
  options.path = StringValueCStr(path);
  obj = new_host(e->api, verify, authorize, &h);
  memset(&ext, 0, sizeof ext);
  s = e->api->shared_library(&options, &ext);
  if (s) raise_status(s, "shared library");
  memset(&backend, 0, sizeof backend);
  backend.struct_size = (uint32_t)sizeof backend;
  backend.kind = EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT;
  backend.config = &ext;
  backend.config_size = sizeof ext;
  s = create_host(h, descriptor, &backend);
  RB_GC_GUARD(descriptor);
  RB_GC_GUARD(path);
  if (s) {
    ext.release(ext.user); /* ownership stays with the caller on a failed create */
    raise_status(s, "create");
  }
  return obj;
}

/* ---- calls without the lock ---- */

static host_t *get_host(VALUE self) {
  host_t *h;
  TypedData_Get_Struct(self, host_t, &host_type, h);
  return h;
}

/* The host, ready for an engine call from this thread: refuses a destroyed host and a call from
 * inside one of its own policies. */
static host_t *enter(VALUE self, const char *action) {
  host_t *h = get_host(self);
  if (!h->handle) raise_status(EXT_PLUGIN_CLOSED, "destroyed");
  if (rb_thread_local_aref(rb_thread_current(), id_policy_key) == self) raise_status(EXT_PLUGIN_INVALID, action);
  return h;
}

static uint32_t timeout_ms(VALUE seconds) {
  double ms = (NIL_P(seconds) ? 3.0 : NUM2DBL(seconds)) * 1000.0;
  if (!(ms >= 1.0)) rb_raise(rb_eArgError, "timeout must be at least 1 ms");
  return ms >= 4294967295.0 ? 0xFFFFFFFFu : (uint32_t)ms;
}

enum { OP_START, OP_CALL, OP_INVOKE, OP_DRAIN, OP_SERVE };

/* An engine call runs on a helper thread while the Ruby thread that made it waits without the
 * lock. Ruby calls the wait's unblocking function for every interrupt: Thread#raise, Thread#kill
 * and Ctrl-C, but also a signal it only notes, such as a child process ending (SIGCHLD), which
 * must not end an unrelated call. So an interrupt only wakes the Ruby thread, which takes the lock
 * and lets Ruby handle it (rb_thread_check_ints). If that raises, the interrupt is real: the call
 * is canceled, waited for, and the exception goes on. Otherwise the Ruby thread waits again.
 *
 * The engine runs policies, and reverse hooks in its caller-thread mode, on the thread making the
 * call, which is the helper. A hook cannot run Ruby there, so it hands itself to the waiting Ruby
 * thread (the call's job slot) and waits for it to have run. A registry forward call into another
 * host made from such a hook runs on the same helper, so its hooks reach the same Ruby thread. */
/* The helper and the waiting Ruby thread share a mutex and a condition variable of the platform:
 * Ruby's own native condition variables are not public in every Ruby the gem supports. */
#ifdef _WIN32
typedef SRWLOCK call_lock;
typedef CONDITION_VARIABLE call_cond;
static void lock_init(call_lock *m) { InitializeSRWLock(m); }
static void lock_destroy(call_lock *m) { (void)m; }
static void lock_lock(call_lock *m) { AcquireSRWLockExclusive(m); }
static void lock_unlock(call_lock *m) { ReleaseSRWLockExclusive(m); }
static void cond_init(call_cond *c) { InitializeConditionVariable(c); }
static void cond_destroy(call_cond *c) { (void)c; }
static void cond_wait(call_cond *c, call_lock *m) { SleepConditionVariableSRW(c, m, INFINITE, 0); }
static void cond_broadcast(call_cond *c) { WakeAllConditionVariable(c); }
#else
typedef pthread_mutex_t call_lock;
typedef pthread_cond_t call_cond;
static void lock_init(call_lock *m) { pthread_mutex_init(m, NULL); }
static void lock_destroy(call_lock *m) { pthread_mutex_destroy(m); }
static void lock_lock(call_lock *m) { pthread_mutex_lock(m); }
static void lock_unlock(call_lock *m) { pthread_mutex_unlock(m); }
static void cond_init(call_cond *c) { pthread_cond_init(c, NULL); }
static void cond_destroy(call_cond *c) { pthread_cond_destroy(c); }
static void cond_wait(call_cond *c, call_lock *m) { pthread_cond_wait(c, m); }
static void cond_broadcast(call_cond *c) { pthread_cond_broadcast(c); }
#endif

typedef struct job {
  void *(*fn)(void *);
  void *arg;
  int done;
} job_t;

typedef struct {
  host_t *host;
  int op;
  uint8_t *data; /* a copy: the Ruby String may move while hooks run Ruby */
  size_t len;
  ext_plugin_call_options options;
  ext_plugin_buffer out;
  ext_plugin_status status;
  call_lock mu;
  call_cond changed;
  int done;    /* the engine call returned */
  int woken;   /* Ruby asked the waiting thread to handle an interrupt */
  int waited;  /* the wait ran: rb_thread_call_without_gvl2 skips it when an interrupt is pending */
  job_t *job;  /* a hook waiting to run on the Ruby thread */
} blocking_call;

#ifdef _MSC_VER
static __declspec(thread) blocking_call *helper_call;
#else
static __thread blocking_call *helper_call;
#endif

static void run_engine_call(blocking_call *c) {
  const engine_api *api = c->host->api;
  ext_plugin_host *handle = c->host->handle;
  helper_call = c;
  switch (c->op) {
  case OP_START: c->status = api->start(handle, &c->options); break;
  case OP_CALL: c->status = api->call(handle, c->data, c->len, &c->options, &c->out); break;
  case OP_INVOKE: c->status = api->invoke(handle, c->data, c->len, &c->options, &c->out); break;
  case OP_SERVE: c->status = api->serve_reverse(handle, &c->options); break;
  default: c->status = api->drain(handle, &c->options); break;
  }
  helper_call = NULL;
  lock_lock(&c->mu);
  c->done = 1;
  cond_broadcast(&c->changed);
  lock_unlock(&c->mu);
}

#ifdef _WIN32
static DWORD WINAPI helper_main(LPVOID arg) {
  run_engine_call((blocking_call *)arg);
  return 0;
}
typedef HANDLE helper_t;
static int helper_start(helper_t *t, blocking_call *c) {
  *t = CreateThread(NULL, 0, helper_main, c, 0, NULL);
  return *t != NULL;
}
static void helper_join(helper_t t) {
  WaitForSingleObject(t, INFINITE);
  CloseHandle(t);
}
#else
static void *helper_main(void *arg) {
  run_engine_call((blocking_call *)arg);
  return NULL;
}
typedef pthread_t helper_t;
static int helper_start(helper_t *t, blocking_call *c) { return pthread_create(t, NULL, helper_main, c) == 0; }
static void helper_join(helper_t t) { pthread_join(t, NULL); }
#endif

/* Runs fn on the Ruby thread: on a helper, it is handed to the thread waiting for the call;
 * otherwise this thread made the call without the lock and takes it back. */
static void with_ruby(void *(*fn)(void *), void *arg) {
  blocking_call *c = helper_call;
  if (!c) {
    rb_thread_call_with_gvl(fn, arg);
    return;
  }
  job_t job = {fn, arg, 0};
  lock_lock(&c->mu);
  while (c->job) cond_wait(&c->changed, &c->mu); /* one hook at a time */
  c->job = &job;
  cond_broadcast(&c->changed);
  while (!job.done) cond_wait(&c->changed, &c->mu);
  lock_unlock(&c->mu);
}

/* Without the lock: until the call returns, a hook needs the Ruby thread, or Ruby interrupts. */
static void *wait_call(void *arg) {
  blocking_call *c = (blocking_call *)arg;
  lock_lock(&c->mu);
  c->waited = 1;
  while (!c->done && !c->woken && !c->job) cond_wait(&c->changed, &c->mu);
  lock_unlock(&c->mu);
  return NULL;
}

/* Called by Ruby, from another thread, for an interrupt: thread-safe, and it only wakes. */
static void wake_call(void *arg) {
  blocking_call *c = (blocking_call *)arg;
  lock_lock(&c->mu);
  c->woken = 1;
  cond_broadcast(&c->changed);
  lock_unlock(&c->mu);
}

static VALUE check_interrupts(VALUE unused) {
  (void)unused;
  rb_thread_check_ints(); /* runs a trap or finalizer; raises for Thread#raise, Thread#kill, Ctrl-C */
  return Qnil;
}

/* Waits for the call with the lock held between waits, running the hooks it hands over. Returns
 * the rb_protect state of a real interrupt, which the caller raises once the call has ended. */
static int serve_call(blocking_call *c) {
  int interrupted = 0;
  for (;;) {
    c->waited = 0;
    /* Once interrupted the call is canceled and ends soon; it is waited for without a wake. */
    rb_thread_call_without_gvl2(wait_call, c, interrupted ? NULL : wake_call, c);
    lock_lock(&c->mu);
    job_t *job = c->job;
    int done = c->done, woken = c->woken || !c->waited;
    c->woken = 0;
    lock_unlock(&c->mu);
    if (job) {
      job->fn(job->arg); /* with the lock, on this thread; the hooks catch their own exceptions */
      lock_lock(&c->mu);
      job->done = 1;
      c->job = NULL;
      cond_broadcast(&c->changed);
      lock_unlock(&c->mu);
      continue;
    }
    if (done) return interrupted;
    if (woken && !interrupted) {
      int state = 0;
      rb_protect(check_interrupts, Qnil, &state);
      if (state) {
        interrupted = state;
        c->host->api->cancel_signal((ext_plugin_cancel *)c->options.cancel);
      }
    }
  }
}

/* Runs one engine call off the lock and returns its response (or Qnil), raising its failure, or
 * the interrupt that canceled it. */
static VALUE blocking_status(VALUE self, int op, VALUE data, VALUE timeout, const char *action,
                             ext_plugin_status *status);
static VALUE blocking(VALUE self, int op, VALUE data, VALUE timeout, const char *action) {
  return blocking_status(self, op, data, timeout, action, NULL);
}
/* As blocking; with status, the call's status is stored there instead of raised. */
static VALUE blocking_status(VALUE self, int op, VALUE data, VALUE timeout, const char *action,
                             ext_plugin_status *status) {
  host_t *h = enter(self, action);
  blocking_call c;
  ext_plugin_cancel *cancel = NULL;
  ext_plugin_status s;
  helper_t helper;
  VALUE response = Qnil;
  memset(&c, 0, sizeof c);
  c.host = h;
  c.op = op;
  c.options.struct_size = (uint32_t)sizeof c.options;
  c.options.timeout_ms = timeout_ms(timeout);
  if (!NIL_P(data)) {
    StringValue(data);
    c.len = (size_t)RSTRING_LEN(data);
    c.data = (uint8_t *)malloc(c.len ? c.len : 1);
    if (!c.data) rb_raise(rb_eNoMemError, "out of memory");
    memcpy(c.data, RSTRING_PTR(data), c.len);
  }
  s = h->api->cancel_create(&cancel);
  if (s) {
    free(c.data);
    raise_status(s, action);
  }
  c.options.cancel = cancel;
  lock_init(&c.mu);
  cond_init(&c.changed);
  h->active++;
  int interrupted = 0;
  if (helper_start(&helper, &c)) {
    interrupted = serve_call(&c);
    helper_join(helper);
  } else {
    c.status = EXT_PLUGIN_NOMEM;
  }
  h->active--;
  h->api->cancel_destroy(cancel); /* the call has joined */
  cond_destroy(&c.changed);
  lock_destroy(&c.mu);
  free(c.data);
  if (c.out.data) {
    if (!interrupted) response = rb_str_new((const char *)c.out.data, (long)c.out.len);
    h->api->buffer_free(&c.out);
  }
  if (interrupted) rb_jump_tag(interrupted); /* the interrupt that canceled the call */
  if (status)
    *status = c.status;
  else if (c.status)
    raise_status(c.status, action);
  return response;
}

/* host.start(timeout) */
static VALUE host_start(VALUE self, VALUE timeout) {
  blocking(self, OP_START, Qnil, timeout, "start");
  return Qnil;
}

/* host.call_json(request, timeout, action) -> response envelope: ext_plugin_host_call, with
 * {contract, operation, payload?} in. */
static VALUE host_call_json(VALUE self, VALUE request, VALUE timeout, VALUE action) {
  VALUE response = blocking(self, OP_CALL, request, timeout, StringValueCStr(action));
  rb_enc_associate(response, rb_utf8_encoding());
  return response;
}

/* host.invoke(request, timeout) -> response envelope bytes */
static VALUE host_invoke(VALUE self, VALUE request, VALUE timeout) {
  return blocking(self, OP_INVOKE, request, timeout, "invoke");
}

/* host.drain(timeout) */
static VALUE host_drain(VALUE self, VALUE timeout) {
  blocking(self, OP_DRAIN, Qnil, timeout, "drain");
  return Qnil;
}

/* host.close: aborts the session; thread-safe, and harmless after destroy. */
static VALUE host_close(VALUE self) {
  host_t *h = get_host(self);
  if (rb_thread_local_aref(rb_thread_current(), id_policy_key) == self) raise_status(EXT_PLUGIN_INVALID, "close");
  if (h->handle) h->api->close(h->handle);
  return Qnil;
}

static VALUE host_state(VALUE self) {
  host_t *h = enter(self, "state");
  ext_plugin_host_state s = h->api->state(h->handle);
  return rb_str_new_cstr((int)s >= 0 && (size_t)s < sizeof state_names / sizeof *state_names ? state_names[s] : "unknown");
}

static VALUE host_stderr(VALUE self) {
  host_t *h = enter(self, "stderr");
  size_t len = 0;
  VALUE buf = rb_str_buf_new(STDERR_CAPACITY);
  ext_plugin_status s = h->api->stderr_copy(h->handle, (uint8_t *)RSTRING_PTR(buf), STDERR_CAPACITY, &len);
  if (s) raise_status(s, "stderr");
  rb_str_set_len(buf, (long)(len < STDERR_CAPACITY ? len : STDERR_CAPACITY));
  return buf;
}

/* host.destroy: frees the host once; again is harmless. Refused while calls are in flight. */
static VALUE host_destroy(VALUE self) {
  host_t *h = get_host(self);
  if (rb_thread_local_aref(rb_thread_current(), id_policy_key) == self) raise_status(EXT_PLUGIN_INVALID, "destroy");
  if (!h->handle) return Qnil;
  if (h->active > 0) {
    h->api->close(h->handle); /* the calls end; destroy again once they have returned */
    raise_status(EXT_PLUGIN_INVALID, "destroy with calls in flight");
  }
  host_teardown(h);
  h->verify = h->authorize = h->reverse_authorize = h->reverse_handle = h->source_of = Qnil;
  return Qnil;
}

static VALUE host_destroyed_p(VALUE self) { return get_host(self)->handle ? Qfalse : Qtrue; }

/* ---- reverse services ---- */

typedef struct {
  host_t *host;
  VALUE callable;
  const uint8_t *json;
  size_t len;
  uint32_t remaining_ms;
  int handle; /* the handler, not authorize */
  int allowed;
  /* The handler's answer, copied out under the lock (libc memory, as it is freed without it). */
  int answered;
  uint32_t kind;
  char *data;
  size_t data_len;
} reverse_call_t;

static VALUE reverse_invoke(VALUE arg) {
  reverse_call_t *r = (reverse_call_t *)arg;
  VALUE json = rb_utf8_str_new((const char *)r->json, (long)r->len);
  return rb_funcall(r->callable, id_call, 2, json, UINT2NUM(r->remaining_ms));
}

static void *reverse_with_gvl(void *arg) {
  reverse_call_t *r = (reverse_call_t *)arg;
  VALUE thread = rb_thread_current();
  VALUE before = rb_thread_local_aref(thread, id_policy_key);
  int state = 0;
  VALUE result;
  rb_thread_local_aset(thread, id_policy_key, r->host->self);
  result = rb_protect(reverse_invoke, (VALUE)r, &state);
  rb_thread_local_aset(thread, id_policy_key, before);
  if (state) {
    rb_set_errinfo(Qnil); /* never unwinds into the engine */
    r->allowed = 0;
  } else if (r->handle) {
    if (RB_TYPE_P(result, T_ARRAY) && RARRAY_LEN(result) == 2 && RB_TYPE_P(rb_ary_entry(result, 1), T_STRING)) {
      VALUE body = rb_ary_entry(result, 1);
      size_t n = (size_t)RSTRING_LEN(body);
      r->data = (char *)malloc(n ? n : 1);
      if (r->data) {
        memcpy(r->data, RSTRING_PTR(body), n);
        r->data_len = n;
        r->kind = RTEST(rb_ary_entry(result, 0)) ? EXT_PLUGIN_HOST_REVERSE_PUBLIC_ERROR : EXT_PLUGIN_HOST_REVERSE_PAYLOAD;
        r->answered = 1;
      }
    }
  } else {
    r->allowed = RTEST(result);
  }
  return NULL;
}

/* The hooks run on the thread of a call made without the lock (caller-thread mode). */
static int32_t reverse_authorize_cb(void *user, const ext_plugin_call_options *o, const uint8_t *json, size_t len) {
  host_t *h = (host_t *)user;
  reverse_call_t r;
  memset(&r, 0, sizeof r);
  r.host = h;
  r.callable = h->reverse_authorize;
  r.json = json;
  r.len = len;
  r.remaining_ms = o->timeout_ms;
  if (NIL_P(r.callable)) return 1;
  with_ruby(reverse_with_gvl, &r);
  return r.allowed ? 0 : 1;
}

static ext_plugin_status reverse_handle_cb(void *user, const ext_plugin_call_options *o, const uint8_t *json, size_t len,
                                    ext_plugin_reverse_emit emit, void *sink) {
  host_t *h = (host_t *)user;
  reverse_call_t r;
  ext_plugin_status s;
  memset(&r, 0, sizeof r);
  r.host = h;
  r.callable = h->reverse_handle;
  r.json = json;
  r.len = len;
  r.remaining_ms = o->timeout_ms;
  r.handle = 1;
  if (NIL_P(r.callable)) return EXT_PLUGIN_IO;
  with_ruby(reverse_with_gvl, &r);
  if (!r.answered) return EXT_PLUGIN_IO; /* the plugin sees operation_failed */
  s = emit(sink, r.kind, (const uint8_t *)r.data, r.data_len);
  free(r.data);
  return s;
}

static ext_plugin_host_services reverse_services(host_t *h, VALUE timeout_ms, VALUE max_payload, VALUE max_in_flight,
                                          int with_handle) {
  ext_plugin_host_services services;
  memset(&services, 0, sizeof services);
  services.struct_size = (uint32_t)sizeof services;
  services.user = h;
  services.authorize = reverse_authorize_cb;
  services.handle = with_handle ? reverse_handle_cb : NULL;
  services.timeout_ms = NUM2UINT(timeout_ms);
  services.max_payload_bytes = NUM2UINT(max_payload);
  services.max_in_flight = NUM2UINT(max_in_flight);
  return services;
}

/* host.serve(authorize, handle, timeout_ms, max_payload, max_in_flight): reverse services, before
 * start. authorize.call(json, remaining_ms) is truthy to allow; handle.call(json, remaining_ms)
 * returns [public_error?, json]. */
static VALUE host_serve(VALUE self, VALUE authorize, VALUE handle, VALUE timeout_ms, VALUE max_payload,
                        VALUE max_in_flight) {
  host_t *h = enter(self, "serve");
  ext_plugin_host_services services = reverse_services(h, timeout_ms, max_payload, max_in_flight, 1);
  ext_plugin_status s = h->api->set_reverse_mode(h->handle, EXT_PLUGIN_HOST_REVERSE_CALLER_THREAD);
  if (!s) s = h->api->set_services(h->handle, &services);
  if (s) raise_status(s, "serve");
  h->reverse_authorize = authorize;
  h->reverse_handle = handle;
  return Qnil;
}

/* host.serve_reverse(timeout) -> true when it served a request, false when none came in time. */
static VALUE host_serve_reverse(VALUE self, VALUE timeout) {
  ext_plugin_status s = EXT_PLUGIN_OK;
  blocking_status(self, OP_SERVE, Qnil, timeout, "serve_reverse", &s);
  if (s == EXT_PLUGIN_TIMEOUT) return Qfalse;
  if (s) raise_status(s, "serve_reverse");
  return Qtrue;
}

/* ---- registries ---- */

static void registry_mark(void *p) {
  registry_ref **holder = (registry_ref **)p;
  for (registration *e = (*holder)->entries; e; e = e->next_in_registry) rb_gc_mark(e->host_value);
}
static void registry_free(void *p) {
  registry_ref **holder = (registry_ref **)p;
  registry_release(*holder);
  xfree(holder);
}
static const rb_data_type_t registry_type = {
    "ExtIO::Host::Native::Registry",
    {registry_mark, registry_free, NULL, NULL, {0}},
    0, 0, 0,
};
static registry_ref *get_registry(VALUE self) {
  registry_ref **holder;
  TypedData_Get_Struct(self, registry_ref *, &registry_type, holder);
  return *holder;
}

/* engine.registry -> a new Native::Registry */
static VALUE engine_registry(VALUE self) {
  const engine_api *api = get_engine(self)->api;
  registry_ref **holder;
  registry_ref *r;
  ext_plugin_status s;
  VALUE obj = TypedData_Make_Struct(cRegistry, registry_ref *, &registry_type, holder);
  r = ALLOC(registry_ref);
  memset(r, 0, sizeof *r);
  r->api = api;
  r->refs = 1;
  s = api->registry_create(&r->handle);
  if (s) {
    xfree(r);
    raise_status(s, "registry");
  }
  *holder = r;
  return obj;
}

static char *copy_cstr(VALUE v) {
  const char *text = StringValueCStr(v);
  size_t n = strlen(text) + 1;
  char *c = ALLOC_N(char, n);
  memcpy(c, text, n);
  return c;
}

/* registry.register(name, version, host) */
static VALUE registry_register(VALUE self, VALUE name, VALUE version, VALUE host) {
  registry_ref *r = get_registry(self);
  host_t *h = enter(host, "register");
  ext_plugin_status s = r->api->registry_register(r->handle, StringValueCStr(name), StringValueCStr(version), h->handle);
  registration *e;
  if (s) raise_status(s, "register");
  e = ALLOC(registration);
  memset(e, 0, sizeof *e);
  e->registry = r;
  e->host = h;
  e->host_value = host;
  e->name = copy_cstr(name);
  e->version = copy_cstr(version);
  e->next_in_registry = r->entries;
  r->entries = e;
  e->next_in_host = h->registrations;
  h->registrations = e;
  r->refs++;
  return Qnil;
}

/* registry.unregister(name, version): waits for the calls routed to its host. */
static VALUE registry_unregister(VALUE self, VALUE name, VALUE version) {
  registry_ref *r = get_registry(self);
  const char *n = StringValueCStr(name), *v = StringValueCStr(version);
  ext_plugin_status s = r->api->registry_unregister(r->handle, n, v);
  if (s) raise_status(s, "unregister");
  for (registration **at = &r->entries; *at; at = &(*at)->next_in_registry) {
    registration *e = *at;
    if (!strcmp(e->name, n) && !strcmp(e->version, v)) {
      *at = e->next_in_registry;
      for (registration **in = &e->host->registrations; *in; in = &(*in)->next_in_host)
        if (*in == e) {
          *in = e->next_in_host;
          break;
        }
      xfree(e->name);
      xfree(e->version);
      xfree(e);
      r->refs--; /* the Ruby object still holds one */
      break;
    }
  }
  return Qnil;
}

/* registry.route(from_name, from_version, from_operation, to_name, to_version, to_operation) */
static VALUE registry_route(int argc, VALUE *argv, VALUE self) {
  registry_ref *r = get_registry(self);
  ext_plugin_registry_edge edge;
  ext_plugin_status s;
  if (argc != 6) rb_raise(rb_eArgError, "route takes six names");
  memset(&edge, 0, sizeof edge);
  edge.struct_size = (uint32_t)sizeof edge;
  edge.from_name = StringValueCStr(argv[0]);
  edge.from_version = StringValueCStr(argv[1]);
  edge.from_operation = StringValueCStr(argv[2]);
  edge.to_name = StringValueCStr(argv[3]);
  edge.to_version = StringValueCStr(argv[4]);
  edge.to_operation = StringValueCStr(argv[5]);
  s = r->api->registry_route(r->handle, &edge);
  if (s) raise_status(s, "route");
  return Qnil;
}

/* host.attach(registry, authorize, timeout_ms, max_payload, max_in_flight): a source of the
 * registry, in place of serve, before start. */
static VALUE host_attach(VALUE self, VALUE registry, VALUE authorize, VALUE timeout_ms, VALUE max_payload,
                         VALUE max_in_flight) {
  host_t *h = enter(self, "attach");
  registry_ref *r = get_registry(registry);
  ext_plugin_host_services services = reverse_services(h, timeout_ms, max_payload, max_in_flight, 0);
  ext_plugin_status s = h->api->set_reverse_mode(h->handle, EXT_PLUGIN_HOST_REVERSE_CALLER_THREAD);
  if (!s) s = r->api->registry_attach(r->handle, h->handle, &services);
  if (s) raise_status(s, "attach");
  h->reverse_authorize = authorize;
  registry_release(h->source_ref);
  h->source_ref = r;
  r->refs++;
  h->source_of = registry;
  return Qnil;
}

RUBY_FUNC_EXPORTED void Init_native(void) {
  VALUE mExtIO = rb_define_module("ExtIO");
  VALUE mHost = rb_define_module_under(mExtIO, "Host");
  VALUE names = rb_ary_new();
  size_t i;
  id_new = rb_intern("new");
  id_call = rb_intern("call");
  id_policy_key = rb_intern("__ext_io_host_policy");
  eHostError = rb_const_get(mHost, rb_intern("HostError")); /* defined by lib/ext_io/host.rb */
  rb_gc_register_mark_object(eHostError);
  mNative = rb_define_module_under(mHost, "Native");
  rb_define_const(mNative, "ABI_VERSION", INT2NUM(EXT_PLUGIN_HOST_ABI_VERSION));
  for (i = 0; i < sizeof status_names / sizeof *status_names; i++) rb_ary_push(names, rb_str_new_cstr(status_names[i]));
  rb_define_const(mNative, "STATUS_NAMES", rb_ary_freeze(names));

  cEngine = rb_define_class_under(mNative, "Engine", rb_cObject);
  rb_undef_alloc_func(cEngine);
  rb_define_singleton_method(cEngine, "open", engine_open, 1);
  rb_define_method(cEngine, "process", engine_process, 8);
  rb_define_method(cEngine, "shared_library", engine_shared_library, 4);

  cHost = rb_define_class_under(mNative, "Host", rb_cObject);
  rb_undef_alloc_func(cHost);
  rb_define_method(cHost, "start", host_start, 1);
  rb_define_method(cHost, "call_json", host_call_json, 3);
  rb_define_method(cHost, "invoke", host_invoke, 2);
  rb_define_method(cHost, "drain", host_drain, 1);
  rb_define_method(cHost, "close", host_close, 0);
  rb_define_method(cHost, "state", host_state, 0);
  rb_define_method(cHost, "stderr", host_stderr, 0);
  rb_define_method(cHost, "destroy", host_destroy, 0);
  rb_define_method(cHost, "destroyed?", host_destroyed_p, 0);
  rb_define_method(cHost, "serve", host_serve, 5);
  rb_define_method(cHost, "serve_reverse", host_serve_reverse, 1);
  rb_define_method(cHost, "attach", host_attach, 5);
  rb_define_method(cEngine, "registry", engine_registry, 0);
  cRegistry = rb_define_class_under(mNative, "Registry", rb_cObject);
  rb_undef_alloc_func(cRegistry);
  rb_define_method(cRegistry, "register", registry_register, 3);
  rb_define_method(cRegistry, "unregister", registry_unregister, 2);
  rb_define_method(cRegistry, "route", registry_route, -1);
}
