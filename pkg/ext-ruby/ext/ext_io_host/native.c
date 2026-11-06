/* ExtIO::Host::Native: the native extension under the Ruby host (lib/ext_io/host.rb).
 *
 * It loads the C engine's shared library (libext_host) at run time, so it is built against the
 * Ruby headers and ext_host.h only and links to no engine. It moves bytes and policy decisions:
 * JSON is generated and parsed in Ruby, and every rule a host must get right is the engine's.
 *
 * Every engine call that can block runs without the global VM lock (rb_thread_call_without_gvl),
 * so other Ruby threads run meanwhile and may call the same host; the engine serializes a host's
 * calls itself. An interrupt (Thread#raise, Thread#kill, Ctrl-C) signals the call's ext_cancel,
 * so the call ends with "canceled". The engine runs policies on the thread making the call, so a
 * policy takes the lock back (rb_thread_call_with_gvl) and calls its Ruby callable under
 * rb_protect: an exception denies, and a policy that uses the host it decides for is refused. */
#include <ruby.h>
#include <ruby/encoding.h>
#include <ruby/thread.h>
#include <stdint.h>
#include <string.h>

#define EXT_HOST_STATIC /* declarations only: every engine function is resolved at run time */
#include "ext_host.h"

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

#define STDERR_CAPACITY 65536u

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
  ext_status (*cancel_create)(ext_cancel **);
  void (*cancel_signal)(ext_cancel *);
  void (*cancel_destroy)(ext_cancel *);
} engine_api;

static VALUE mNative, cEngine, cHost, eHostError;
static ID id_new, id_call, id_policy_key;

static const char *const status_names[] = {
    "ok",       "invalid",  "denied",   "mismatch", "unsupported", "closed",   "timeout",
    "io",       "nomem",    "draining", "not_found", "ambiguous",  "canceled", "updating",
    "capacity", "sequence", "protocol", "expired",  "exists",      "missing",
};
static const char *const state_names[] = {"created", "ready", "draining", "closed", "failed"};

static const char *status_name(ext_status s) {
  return s >= 0 && (size_t)s < sizeof status_names / sizeof *status_names ? status_names[s] : "unknown";
}

/* Raises ExtIO::Host::HostError.new(code, status, action). */
NORETURN(static void raise_status(ext_status s, const char *action));
static void raise_status(ext_status s, const char *action) {
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
  RESOLVE(abi_version, "ext_host_abi_version");
  if (api->abi_version() != EXT_HOST_ABI_VERSION) {
    unsigned got = api->abi_version();
    free(api);
    rb_raise(rb_eLoadError, "ext engine ABI %u, this extension needs %u", got, (unsigned)EXT_HOST_ABI_VERSION);
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
  RESOLVE(cancel_create, "ext_cancel_create");
  RESOLVE(cancel_signal, "ext_cancel_signal");
  RESOLVE(cancel_destroy, "ext_cancel_destroy");
#undef RESOLVE
  obj = TypedData_Make_Struct(klass, engine_t, &engine_type, e);
  e->api = api;
  return obj;
}

/* ---- hosts ---- */

typedef struct {
  const engine_api *api;
  ext_host *handle;
  VALUE self;
  VALUE verify, authorize; /* callables, or Qnil to allow */
  long active;             /* engine calls in flight, counted under the lock */
} host_t;

static void host_mark(void *p) {
  host_t *h = (host_t *)p;
  rb_gc_mark(h->verify);
  rb_gc_mark(h->authorize);
}

static void host_free(void *p) {
  host_t *h = (host_t *)p;
  if (h->handle) { /* unreachable, so no call is in flight */
    h->api->close(h->handle);
    h->api->destroy(h->handle);
  }
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
  rb_thread_call_with_gvl(policy_with_gvl, &p);
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
  *out = h;
  return obj;
}

static ext_status create_host(host_t *h, VALUE descriptor, const ext_backend_options *backend) {
  ext_host_options options;
  memset(&options, 0, sizeof options);
  options.abi_version = EXT_HOST_ABI_VERSION;
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
  ext_jsonline_process_launch launch;
  ext_backend_options backend;
  VALUE keep_args = Qnil, keep_env = Qnil, obj;
  unsigned long bytes = NIL_P(stderr_bytes) ? 4096ul : NUM2ULONG(stderr_bytes);
  host_t *h;
  ext_status s;
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
  backend.kind = EXT_BACKEND_JSONLINE_PROCESS_LAUNCH;
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
  ext_shared_library_options options;
  ext_backend_extension_context ext;
  ext_backend_options backend;
  host_t *h;
  VALUE obj;
  ext_status s;
  StringValue(descriptor);
  options.struct_size = (uint32_t)sizeof options;
  options.path = StringValueCStr(path);
  obj = new_host(e->api, verify, authorize, &h);
  memset(&ext, 0, sizeof ext);
  s = e->api->shared_library(&options, &ext);
  if (s) raise_status(s, "shared library");
  memset(&backend, 0, sizeof backend);
  backend.struct_size = (uint32_t)sizeof backend;
  backend.kind = EXT_BACKEND_EXTENSION_CONTEXT;
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
  if (!h->handle) raise_status(EXT_CLOSED, "destroyed");
  if (rb_thread_local_aref(rb_thread_current(), id_policy_key) == self) raise_status(EXT_INVALID, action);
  return h;
}

static uint32_t timeout_ms(VALUE seconds) {
  double ms = (NIL_P(seconds) ? 3.0 : NUM2DBL(seconds)) * 1000.0;
  if (!(ms >= 1.0)) rb_raise(rb_eArgError, "timeout must be at least 1 ms");
  return ms >= 4294967295.0 ? 0xFFFFFFFFu : (uint32_t)ms;
}

enum { OP_START, OP_CALL, OP_INVOKE, OP_DRAIN };

typedef struct {
  host_t *host;
  int op;
  const uint8_t *data;
  size_t len;
  ext_call_options options;
  ext_buffer out;
  ext_status status;
} blocking_call;

static void *blocking_run(void *arg) {
  blocking_call *c = (blocking_call *)arg;
  const engine_api *api = c->host->api;
  ext_host *handle = c->host->handle;
  switch (c->op) {
  case OP_START: c->status = api->start(handle, &c->options); break;
  case OP_CALL: c->status = api->call(handle, c->data, c->len, &c->options, &c->out); break;
  case OP_INVOKE: c->status = api->invoke(handle, c->data, c->len, &c->options, &c->out); break;
  default: c->status = api->drain(handle, &c->options); break;
  }
  return NULL;
}

/* Called by Ruby, from another thread, to interrupt the call: thread-safe. */
static void blocking_interrupt(void *arg) {
  blocking_call *c = (blocking_call *)arg;
  c->host->api->cancel_signal((ext_cancel *)c->options.cancel);
}

typedef struct {
  blocking_call *call;
  ext_cancel *cancel;
} blocking_frame;

static VALUE blocking_body(VALUE arg) {
  blocking_frame *f = (blocking_frame *)arg;
  /* without_gvl2 leaves a pending interrupt to Ruby's next check instead of raising it here,
   * so the response buffer is always freed; one pending before the call skips it (canceled). */
  rb_thread_call_without_gvl2(blocking_run, f->call, blocking_interrupt, f->call);
  return Qnil;
}

static VALUE blocking_done(VALUE arg) {
  blocking_frame *f = (blocking_frame *)arg;
  f->call->host->active--;
  f->call->host->api->cancel_destroy(f->cancel); /* the call has joined */
  return Qnil;
}

/* Runs one engine call without the lock and returns its response (or Qnil), raising its failure.
 * A pending interrupt is raised by Ruby after the call returns. */
static VALUE blocking(VALUE self, int op, VALUE data, VALUE timeout, const char *action) {
  host_t *h = enter(self, action);
  blocking_call c;
  blocking_frame f;
  ext_cancel *cancel = NULL;
  ext_status s;
  VALUE response = Qnil;
  memset(&c, 0, sizeof c);
  c.host = h;
  c.op = op;
  c.status = EXT_CANCELED; /* what a call that never ran reports */
  c.options.struct_size = (uint32_t)sizeof c.options;
  c.options.timeout_ms = timeout_ms(timeout);
  if (!NIL_P(data)) {
    StringValue(data);
    data = rb_str_new_frozen(data); /* stays put while the lock is released */
    c.data = (const uint8_t *)RSTRING_PTR(data);
    c.len = (size_t)RSTRING_LEN(data);
  }
  s = h->api->cancel_create(&cancel);
  if (s) raise_status(s, action);
  c.options.cancel = cancel;
  f.call = &c;
  f.cancel = cancel;
  h->active++;
  rb_ensure(blocking_body, (VALUE)&f, blocking_done, (VALUE)&f);
  RB_GC_GUARD(data);
  if (c.out.data) {
    response = rb_str_new((const char *)c.out.data, (long)c.out.len);
    h->api->buffer_free(&c.out);
  }
  if (c.status) raise_status(c.status, action);
  return response;
}

/* host.start(timeout) */
static VALUE host_start(VALUE self, VALUE timeout) {
  blocking(self, OP_START, Qnil, timeout, "start");
  return Qnil;
}

/* host.call_json(request, timeout, action) -> response envelope: ext_host_call, with
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
  if (rb_thread_local_aref(rb_thread_current(), id_policy_key) == self) raise_status(EXT_INVALID, "close");
  if (h->handle) h->api->close(h->handle);
  return Qnil;
}

static VALUE host_state(VALUE self) {
  host_t *h = enter(self, "state");
  ext_host_state s = h->api->state(h->handle);
  return rb_str_new_cstr((int)s >= 0 && (size_t)s < sizeof state_names / sizeof *state_names ? state_names[s] : "unknown");
}

static VALUE host_stderr(VALUE self) {
  host_t *h = enter(self, "stderr");
  size_t len = 0;
  VALUE buf = rb_str_buf_new(STDERR_CAPACITY);
  ext_status s = h->api->stderr_copy(h->handle, (uint8_t *)RSTRING_PTR(buf), STDERR_CAPACITY, &len);
  if (s) raise_status(s, "stderr");
  rb_str_set_len(buf, (long)(len < STDERR_CAPACITY ? len : STDERR_CAPACITY));
  return buf;
}

/* host.destroy: frees the host once; again is harmless. Refused while calls are in flight. */
static VALUE host_destroy(VALUE self) {
  host_t *h = get_host(self);
  if (rb_thread_local_aref(rb_thread_current(), id_policy_key) == self) raise_status(EXT_INVALID, "destroy");
  if (!h->handle) return Qnil;
  if (h->active > 0) {
    h->api->close(h->handle); /* the calls end; destroy again once they have returned */
    raise_status(EXT_INVALID, "destroy with calls in flight");
  }
  h->api->close(h->handle);
  h->api->destroy(h->handle);
  h->handle = NULL;
  h->verify = h->authorize = Qnil;
  return Qnil;
}

static VALUE host_destroyed_p(VALUE self) { return get_host(self)->handle ? Qfalse : Qtrue; }

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
  rb_define_const(mNative, "ABI_VERSION", INT2NUM(EXT_HOST_ABI_VERSION));
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
}
