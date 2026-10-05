#define _POSIX_C_SOURCE 200809L
#include "ctx_guest.h"
#include "ctx_host.h"
#include <node_api.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static const napi_type_tag binding_tag = {0xd709f6b1ec467f01ULL,
                                          0xbac017ce5969d93fULL};
static const napi_type_tag cancel_tag = {0x1f3308617d268b41ULL,
                                         0x9650dfcb91149861ULL};
static int external(napi_env env, napi_value value, const napi_type_tag *tag,
                    void **out) {
  bool tagged = false;
  return napi_check_object_type_tag(env, value, tag, &tagged) == napi_ok &&
         tagged && napi_get_value_external(env, value, out) == napi_ok;
}
typedef struct {
  ctx_host *host;
  ctx_guest *guest;
  napi_threadsafe_function verify, authorize, handle, observe;
  napi_env env;
  int cleanup_hook;
  size_t jobs;
  int disposed;
} binding;
typedef struct {
  pthread_mutex_t mu;
  pthread_cond_t changed;
  atomic_int refs;
  int done;
  ctx_status status;
  uint32_t kind, timeout;
  uint8_t *input, *output;
  size_t input_len, output_len;
} callback;
static void release_callback(callback *r) {
  if (atomic_fetch_sub(&r->refs, 1) != 1)
    return;
  pthread_cond_destroy(&r->changed);
  pthread_mutex_destroy(&r->mu);
  free(r->input);
  free(r->output);
  free(r);
}
static char *string(napi_env env, napi_value v, size_t *len) {
  if (napi_get_value_string_utf8(env, v, NULL, 0, len) != napi_ok ||
      *len > CTX_HOST_MAX_FRAME)
    return NULL;
  char *s = malloc(*len + 1);
  if (!s)
    return NULL;
  if (napi_get_value_string_utf8(env, v, s, *len + 1, len) != napi_ok) {
    free(s);
    return NULL;
  }
  return s;
}
static napi_value undefined(napi_env env) {
  napi_value v;
  napi_get_undefined(env, &v);
  return v;
}
static napi_value failure(napi_env env, const char *message) {
  napi_throw_error(env, NULL, message);
  return NULL;
}
static int64_t now(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (int64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}
static void finish(callback *r, ctx_status status, uint32_t kind,
                   uint8_t *output, size_t len) {
  pthread_mutex_lock(&r->mu);
  if (!r->done) {
    r->done = 1;
    r->status = status;
    r->kind = kind;
    r->output = output;
    r->output_len = len;
    output = NULL;
    pthread_cond_broadcast(&r->changed);
  }
  pthread_mutex_unlock(&r->mu);
  free(output);
}
static void reply_finalizer(napi_env env, void *data, void *hint) {
  (void)env;
  (void)hint;
  release_callback(data);
}
static napi_value reply(napi_env env, napi_callback_info info) {
  size_t count = 3;
  napi_value args[3];
  void *data = NULL;
  if (napi_get_cb_info(env, info, &count, args, NULL, &data) != napi_ok)
    return NULL;
  callback *r = data;
  int32_t status = CTX_IO;
  uint32_t kind = 0;
  size_t len = 0;
  uint8_t *bytes = NULL;
  if (count != 3 || napi_get_value_int32(env, args[0], &status) != napi_ok ||
      napi_get_value_uint32(env, args[1], &kind) != napi_ok)
    status = CTX_IO;
  else if (status == CTX_OK) {
    bytes = (uint8_t *)string(env, args[2], &len);
    if (!bytes)
      status = CTX_INVALID;
  }
  finish(r, status, kind, bytes, len);
  return undefined(env);
}
static void callback_js(napi_env env, napi_value function, void *context,
                        void *data) {
  (void)context;
  callback *r = data;
  if (!env || !function) {
    finish(r, CTX_CLOSED, 0, NULL, 0);
    release_callback(r);
    return;
  }
  pthread_mutex_lock(&r->mu);
  int done = r->done;
  pthread_mutex_unlock(&r->mu);
  if (done) {
    release_callback(r);
    return;
  }
  napi_value args[3], result, receiver;
  if (napi_create_string_utf8(env, (char *)r->input, r->input_len, &args[0]) !=
          napi_ok ||
      napi_create_uint32(env, r->timeout, &args[1]) != napi_ok ||
      napi_create_function(env, "reply", NAPI_AUTO_LENGTH, reply, r,
                           &args[2]) != napi_ok) {
    finish(r, CTX_IO, 0, NULL, 0);
    release_callback(r);
    return;
  }
  /* The reply function owns the queued reference. A promise may retain it after
   * a timed-out native call. No stack or engine handle is retained in r. */
  if (napi_add_finalizer(env, args[2], r, reply_finalizer, NULL, NULL) !=
      napi_ok) {
    finish(r, CTX_IO, 0, NULL, 0);
    release_callback(r);
    return;
  }
  napi_get_undefined(env, &receiver);
  if (napi_call_function(env, receiver, function, 3, args, &result) !=
      napi_ok) {
    bool pending = false;
    napi_is_exception_pending(env, &pending);
    if (pending)
      napi_get_and_clear_last_exception(env, &result);
    finish(r, CTX_IO, 0, NULL, 0);
  }
}
static ctx_status invoke_js(napi_threadsafe_function fn, const uint8_t *input,
                            size_t len, const ctx_call_options *o,
                            uint32_t *kind, ctx_buffer *out) {
  callback *r = calloc(1, sizeof(*r));
  if (!r)
    return CTX_NOMEM;
  if (pthread_mutex_init(&r->mu, NULL)) {
    free(r);
    return CTX_IO;
  }
  if (pthread_cond_init(&r->changed, NULL)) {
    pthread_mutex_destroy(&r->mu);
    free(r);
    return CTX_IO;
  }
  atomic_init(&r->refs, 2);
  r->input = malloc(len ? len : 1);
  if (!r->input) {
    release_callback(r);
    release_callback(r);
    return CTX_NOMEM;
  }
  memcpy(r->input, input, len);
  r->input_len = len;
  r->timeout = o->timeout_ms;
  if (napi_call_threadsafe_function(fn, r, napi_tsfn_nonblocking) != napi_ok) {
    release_callback(r);
    release_callback(r);
    return CTX_CLOSED;
  }
  int64_t end = now() + o->timeout_ms;
  pthread_mutex_lock(&r->mu);
  while (!r->done) {
    if (ctx_cancel_is_signaled(o->cancel) || now() >= end) {
      r->done = 1;
      r->status =
          ctx_cancel_is_signaled(o->cancel) ? CTX_CANCELED : CTX_TIMEOUT;
      break;
    }
    struct timespec t;
    clock_gettime(CLOCK_REALTIME, &t);
    t.tv_nsec += 10000000;
    if (t.tv_nsec >= 1000000000) {
      t.tv_sec++;
      t.tv_nsec -= 1000000000;
    }
    pthread_cond_timedwait(&r->changed, &r->mu, &t);
  }
  ctx_status status = r->status;
  *kind = r->kind;
  out->data = r->output;
  out->len = r->output_len;
  r->output = NULL;
  pthread_mutex_unlock(&r->mu);
  release_callback(r);
  return status;
}
static int32_t allow_placeholder(void *u, const uint8_t *p, size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return 1;
}
static int32_t policy(binding *b, int verify, const ctx_call_options *o,
                      const uint8_t *p, size_t n) {
  ctx_buffer out = {0};
  uint32_t kind;
  ctx_status s =
      invoke_js(verify ? b->verify : b->authorize, p, n, o, &kind, &out);
  ctx_buffer_free(&out);
  return s == CTX_OK ? 0 : 1;
}
static int32_t verify(void *u, const ctx_call_options *o, const uint8_t *p,
                      size_t n) {
  return policy(u, 1, o, p, n);
}
static int32_t authorize(void *u, const ctx_call_options *o, const uint8_t *p,
                         size_t n) {
  return policy(u, 0, o, p, n);
}
static ctx_status handle(void *u, void *call, const uint8_t *p, size_t n,
                         uint32_t ms, ctx_guest_emit emit, void *sink) {
  binding *b = u;
  ctx_call_options o = *(ctx_call_options *)call;
  o.timeout_ms = ms;
  ctx_buffer out = {0};
  uint32_t kind = 0;
  ctx_status s = invoke_js(b->handle, p, n, &o, &kind, &out);
  if (!s)
    s = emit(sink, kind, out.data, out.len);
  ctx_buffer_free(&out);
  return s;
}
static int tsfn(napi_env env, napi_value function,
                napi_threadsafe_function *out) {
  napi_valuetype type;
  if (napi_typeof(env, function, &type) != napi_ok || type != napi_function)
    return 0;
  napi_value name;
  napi_create_string_utf8(env, "ctx-engine-callback", NAPI_AUTO_LENGTH, &name);
  if (napi_create_threadsafe_function(env, function, NULL, name, 0, 1, NULL,
                                      NULL, NULL, callback_js, out) != napi_ok)
    return 0;
  napi_unref_threadsafe_function(env, *out);
  return 1;
}
static void observe_js(napi_env env, napi_value function, void *context,
                       void *data) {
  (void)context;
  char *json = data;
  if (env && function) {
    napi_value arg, receiver, result;
    napi_get_undefined(env, &receiver);
    if (napi_create_string_utf8(env, json, NAPI_AUTO_LENGTH, &arg) == napi_ok &&
        napi_call_function(env, receiver, function, 1, &arg, &result) !=
            napi_ok) {
      bool pending = false;
      napi_is_exception_pending(env, &pending);
      if (pending)
        napi_get_and_clear_last_exception(env, &result);
    }
  }
  free(json);
}
static void observe(void *user, const ctx_call_options *call, const uint8_t *p,
                    size_t n) {
  (void)call;
  binding *b = user;
  if (!b->observe)
    return;
  char *json = malloc(n + 1);
  if (!json)
    return;
  memcpy(json, p, n);
  json[n] = 0;
  if (napi_call_threadsafe_function(b->observe, json, napi_tsfn_nonblocking) !=
      napi_ok)
    free(json);
}
static void cleanup_binding(void *data) {
  binding *b = data;
  b->cleanup_hook = 0;
  ctx_host_close(b->host);
  if (b->verify) {
    napi_release_threadsafe_function(b->verify, napi_tsfn_abort);
    b->verify = NULL;
  }
  if (b->authorize) {
    napi_release_threadsafe_function(b->authorize, napi_tsfn_abort);
    b->authorize = NULL;
  }
  if (b->handle) {
    napi_release_threadsafe_function(b->handle, napi_tsfn_abort);
    b->handle = NULL;
  }
  if (b->observe) {
    napi_release_threadsafe_function(b->observe, napi_tsfn_abort);
    b->observe = NULL;
  }
}
static void dispose_contents(binding *b) {
  if (b->cleanup_hook)
    napi_remove_env_cleanup_hook(b->env, cleanup_binding, b);
  cleanup_binding(b);
  ctx_host_destroy(b->host);
  b->host = NULL;
  ctx_guest_destroy(b->guest);
  b->guest = NULL;
}
static void destroy_binding(napi_env env, void *data, void *hint) {
  (void)env;
  (void)hint;
  binding *b = data;
  dispose_contents(b);
  free(b);
}
static int register_cleanup(napi_env env, binding *b) {
  b->env = env;
  if (napi_add_env_cleanup_hook(env, cleanup_binding, b) != napi_ok)
    return 0;
  b->cleanup_hook = 1;
  return 1;
}
static void free_strings(char **v) {
  if (v) {
    for (size_t i = 0; v[i]; i++)
      free(v[i]);
    free(v);
  }
}
static char **strings(napi_env env, napi_value array, uint32_t *count) {
  if (napi_get_array_length(env, array, count) != napi_ok || *count > 256)
    return NULL;
  char **out = calloc(*count + 1, sizeof(char *));
  if (!out)
    return NULL;
  for (uint32_t i = 0; i < *count; i++) {
    napi_value v;
    size_t n;
    if (napi_get_element(env, array, i, &v) != napi_ok ||
        !(out[i] = string(env, v, &n)) || strlen(out[i]) != n) {
      free_strings(out);
      return NULL;
    }
  }
  return out;
}
static napi_value create_host(napi_env env, napi_callback_info info) {
  size_t count = 7;
  napi_value args[7], external;
  if (napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count != 7)
    return failure(env, "invalid host arguments");
  size_t pn = 0, dn = 0;
  char *path = string(env, args[0], &pn),
       *descriptor = string(env, args[1], &dn);
  uint32_t an = 0, en = 0;
  char **argv = strings(env, args[2], &an), **envp = strings(env, args[3], &en);
  binding *b = calloc(1, sizeof(*b));
  ctx_status status = CTX_INVALID;
  if (!path || strlen(path) != pn || !descriptor || !argv || !envp || !b)
    goto done;
  if (!tsfn(env, args[4], &b->verify) || !tsfn(env, args[5], &b->authorize))
    goto done;
  napi_valuetype observer_type;
  if (napi_typeof(env, args[6], &observer_type) != napi_ok)
    goto done;
  if (observer_type != napi_undefined) {
    napi_value name;
    napi_create_string_utf8(env, "ctx-engine-observer", NAPI_AUTO_LENGTH,
                            &name);
    if (observer_type != napi_function ||
        napi_create_threadsafe_function(env, args[6], NULL, name, 1024, 1, NULL,
                                        NULL, NULL, observe_js,
                                        &b->observe) != napi_ok)
      goto done;
    napi_unref_threadsafe_function(env, b->observe);
  }
  ctx_jsonline_process_config process = {sizeof(process),           path,
                                         (const char *const *)argv, an,
                                         (const char *const *)envp, en};
  ctx_backend_options backend = {sizeof(backend),
                                 CTX_BACKEND_JSONLINE_PROCESS_CONFIG, &process,
                                 sizeof(process)};
  ctx_host_options options = {CTX_HOST_ABI_VERSION,
                              sizeof(options),
                              (uint8_t *)descriptor,
                              dn,
                              allow_placeholder,
                              allow_placeholder,
                              b};
  status = ctx_host_create(&options, &backend, &b->host);
  if (!status) {
    ctx_host_hooks hooks = {sizeof(hooks), b, verify, authorize, observe};
    status = ctx_host_set_hooks(b->host, &hooks);
  }
done:
  free(path);
  free(descriptor);
  free_strings(argv);
  free_strings(envp);
  if (status || !register_cleanup(env, b) ||
      napi_create_external(env, b, destroy_binding, NULL, &external) !=
          napi_ok) {
    if (b)
      destroy_binding(env, b, NULL);
    return failure(env, ctx_host_status_string(status ? status : CTX_IO));
  }
  if (napi_type_tag_object(env, external, &binding_tag) != napi_ok)
    return failure(env, "cannot tag native handle");
  return external;
}
static napi_value create_guest(napi_env env, napi_callback_info info) {
  size_t count = 3;
  napi_value args[3], external;
  if (napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count != 3)
    return failure(env, "invalid guest arguments");
  size_t n = 0;
  char *descriptor = string(env, args[0], &n);
  uint32_t ms = 0;
  binding *b = calloc(1, sizeof(*b));
  ctx_status status = CTX_INVALID;
  if (descriptor && b && napi_get_value_uint32(env, args[1], &ms) == napi_ok &&
      tsfn(env, args[2], &b->handle)) {
    ctx_guest_options options = {CTX_HOST_ABI_VERSION,
                                 sizeof(options),
                                 (uint8_t *)descriptor,
                                 n,
                                 ms,
                                 b,
                                 handle};
    status = ctx_guest_create(&options, &b->guest);
  }
  free(descriptor);
  if (status || !register_cleanup(env, b) ||
      napi_create_external(env, b, destroy_binding, NULL, &external) !=
          napi_ok) {
    if (b)
      destroy_binding(env, b, NULL);
    return failure(env, ctx_host_status_string(status ? status : CTX_IO));
  }
  if (napi_type_tag_object(env, external, &binding_tag) != napi_ok)
    return failure(env, "cannot tag native handle");
  return external;
}
static void destroy_cancel(napi_env env, void *data, void *hint) {
  (void)env;
  (void)hint;
  ctx_cancel_destroy(data);
}
static napi_value new_cancel(napi_env env, napi_callback_info info) {
  (void)info;
  ctx_cancel *c = NULL;
  napi_value out;
  ctx_status s = ctx_cancel_create(&c);
  if (s)
    return failure(env, ctx_host_status_string(s));
  if (napi_create_external(env, c, destroy_cancel, NULL, &out) != napi_ok) {
    ctx_cancel_destroy(c);
    return failure(env, "allocation failed");
  }
  if (napi_type_tag_object(env, out, &cancel_tag) != napi_ok)
    return failure(env, "cannot tag cancellation");
  return out;
}
static napi_value signal_cancel(napi_env env, napi_callback_info info) {
  size_t n = 1;
  napi_value arg;
  void *c = NULL;
  if (napi_get_cb_info(env, info, &n, &arg, NULL, NULL) != napi_ok || n != 1 ||
      !external(env, arg, &cancel_tag, &c))
    return failure(env, "invalid cancellation");
  ctx_cancel_signal(c);
  return undefined(env);
}
static napi_value close_host(napi_env env, napi_callback_info info) {
  size_t n = 1;
  napi_value arg;
  binding *b = NULL;
  if (napi_get_cb_info(env, info, &n, &arg, NULL, NULL) != napi_ok || n != 1 ||
      !external(env, arg, &binding_tag, (void **)&b))
    return failure(env, "invalid host");
  b->disposed = 1;
  ctx_host_close(b->host);
  if (!b->jobs)
    dispose_contents(b);
  return undefined(env);
}
typedef struct {
  napi_async_work work;
  napi_deferred deferred;
  napi_ref owner, signal;
  binding *binding;
  ctx_call_options options;
  char *operation;
  char *root;
  uint8_t *input;
  size_t input_len;
  ctx_buffer output;
  ctx_status status;
} job;
static void execute(napi_env env, void *data) {
  (void)env;
  job *j = data;
  binding *b = j->binding;
  if (j->root) {
    if (!strcmp(j->operation, "verify")) {
      j->status = ctx_package_verify(j->input, j->input_len, j->root);
    } else if (!strcmp(j->operation, "digest")) {
      uint8_t hash[32];
      j->status = ctx_directory_digest(j->root, hash);
      if (!j->status) {
        j->output.data = malloc(67);
        if (!j->output.data)
          j->status = CTX_NOMEM;
        else {
          static const char hex[] = "0123456789abcdef";
          j->output.data[0] = j->output.data[65] = '"';
          for (size_t i = 0; i < 32; i++) {
            j->output.data[1 + i * 2] = hex[hash[i] >> 4];
            j->output.data[2 + i * 2] = hex[hash[i] & 15];
          }
          j->output.data[66] = 0;
          j->output.len = 66;
        }
      }
    } else
      j->status = CTX_INVALID;
  } else if (!strcmp(j->operation, "start") && b->host)
    j->status = ctx_host_start_with_options(b->host, &j->options);
  else if (!strcmp(j->operation, "invoke") && b->host)
    j->status = ctx_host_invoke_with_options(b->host, j->input, j->input_len,
                                             &j->options, &j->output);
  else if (!strcmp(j->operation, "call") && b->host)
    j->status =
        ctx_host_call(b->host, j->input, j->input_len, &j->options, &j->output);
  else if (!strcmp(j->operation, "drain") && b->host)
    j->status = ctx_host_drain_with_options(b->host, &j->options);
  else if (!strcmp(j->operation, "guest.invoke") && b->guest)
    j->status =
        ctx_guest_invoke(b->guest, j->input, j->input_len,
                         j->options.timeout_ms, &j->options, &j->output);
  else if (!strcmp(j->operation, "descriptor") && b->guest)
    j->status = ctx_guest_descriptor(b->guest, &j->output);
  else
    j->status = CTX_INVALID;
  if (ctx_cancel_is_signaled(j->options.cancel))
    j->status = CTX_CANCELED;
}
static void free_job(napi_env env, job *j) {
  if (j->owner)
    napi_delete_reference(env, j->owner);
  if (j->signal)
    napi_delete_reference(env, j->signal);
  if (j->work)
    napi_delete_async_work(env, j->work);
  free(j->operation);
  free(j->root);
  free(j->input);
  ctx_buffer_free(&j->output);
  free(j);
}
static void complete(napi_env env, napi_status code, void *data) {
  job *j = data;
  napi_value value;
  if (code != napi_ok && !j->status)
    j->status = CTX_IO;
  if (j->status) {
    napi_value message, status;
    napi_create_string_utf8(env, ctx_host_status_string(j->status),
                            NAPI_AUTO_LENGTH, &message);
    napi_create_error(env, NULL, message, &value);
    napi_create_int32(env, j->status, &status);
    napi_set_named_property(env, value, "status", status);
    napi_reject_deferred(env, j->deferred, value);
  } else {
    napi_create_string_utf8(env,
                            j->output.data ? (char *)j->output.data : "null",
                            j->output.data ? j->output.len : 4, &value);
    napi_resolve_deferred(env, j->deferred, value);
  }
  if (j->binding) {
    j->binding->jobs--;
    if (j->binding->disposed && !j->binding->jobs)
      dispose_contents(j->binding);
  }
  free_job(env, j);
}
static napi_value request(napi_env env, napi_callback_info info) {
  size_t count = 5;
  napi_value args[5], promise, name;
  if (napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count != 5)
    return failure(env, "invalid call arguments");
  job *j = calloc(1, sizeof(*j));
  if (!j)
    return failure(env, "allocation failed");
  size_t on = 0;
  void *cancel = NULL;
  j->options.struct_size = sizeof(j->options);
  if (!external(env, args[0], &binding_tag, (void **)&j->binding) ||
      !external(env, args[4], &cancel_tag, &cancel) ||
      napi_get_value_uint32(env, args[3], &j->options.timeout_ms) != napi_ok ||
      !j->options.timeout_ms || !(j->operation = string(env, args[1], &on)) ||
      strlen(j->operation) != on ||
      !(j->input = (uint8_t *)string(env, args[2], &j->input_len)))
    goto invalid;
  if (j->binding->disposed)
    goto invalid;
  j->options.cancel = cancel;
  if (napi_create_reference(env, args[0], 1, &j->owner) != napi_ok ||
      napi_create_reference(env, args[4], 1, &j->signal) != napi_ok ||
      napi_create_promise(env, &j->deferred, &promise) != napi_ok)
    goto invalid;
  napi_create_string_utf8(env, "ctx-engine-call", NAPI_AUTO_LENGTH, &name);
  if (napi_create_async_work(env, NULL, name, execute, complete, j, &j->work) !=
          napi_ok ||
      napi_queue_async_work(env, j->work) != napi_ok)
    goto invalid;
  j->binding->jobs++;
  return promise;
invalid:
  free_job(env, j);
  return failure(env, "invalid call or allocation failure");
}
static napi_value service(napi_env env, napi_callback_info info) {
  size_t count = 2;
  napi_value args[2], result;
  if (napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count != 2)
    return failure(env, "invalid service arguments");
  size_t on = 0, n = 0;
  char *op = string(env, args[0], &on), *input = string(env, args[1], &n);
  ctx_buffer out = {0};
  ctx_status status = op && strlen(op) == on && input
                          ? ctx_engine_call(op, (uint8_t *)input, n, &out)
                          : CTX_INVALID;
  free(op);
  free(input);
  if (status) {
    ctx_buffer_free(&out);
    return failure(env, ctx_host_status_string(status));
  }
  napi_status s =
      napi_create_string_utf8(env, (char *)out.data, out.len, &result);
  ctx_buffer_free(&out);
  return s == napi_ok ? result : failure(env, "allocation failed");
}
static napi_value integrity(napi_env env, napi_callback_info info) {
  size_t count = 3, on = 0, rn = 0;
  napi_value args[3], promise, name;
  if (napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count != 3)
    return failure(env, "invalid integrity arguments");
  job *j = calloc(1, sizeof(*j));
  if (!j)
    return failure(env, "allocation failed");
  j->operation = string(env, args[0], &on);
  j->root = string(env, args[1], &rn);
  j->input = (uint8_t *)string(env, args[2], &j->input_len);
  if (!j->operation || strlen(j->operation) != on || !j->root ||
      strlen(j->root) != rn || !rn || !j->input)
    goto invalid;
  if (napi_create_promise(env, &j->deferred, &promise) != napi_ok)
    goto invalid;
  napi_create_string_utf8(env, "ctx-engine-integrity", NAPI_AUTO_LENGTH, &name);
  if (napi_create_async_work(env, NULL, name, execute, complete, j, &j->work) !=
          napi_ok ||
      napi_queue_async_work(env, j->work) != napi_ok)
    goto invalid;
  return promise;
invalid:
  free_job(env, j);
  return failure(env, "invalid integrity arguments or allocation failure");
}
static napi_value init(napi_env env, napi_value exports) {
  const napi_property_descriptor properties[] = {
      {"createHost", NULL, create_host, NULL, NULL, NULL, napi_default, NULL},
      {"createGuest", NULL, create_guest, NULL, NULL, NULL, napi_default, NULL},
      {"newCancel", NULL, new_cancel, NULL, NULL, NULL, napi_default, NULL},
      {"cancel", NULL, signal_cancel, NULL, NULL, NULL, napi_default, NULL},
      {"closeHost", NULL, close_host, NULL, NULL, NULL, napi_default, NULL},
      {"request", NULL, request, NULL, NULL, NULL, napi_default, NULL},
      {"service", NULL, service, NULL, NULL, NULL, napi_default, NULL},
      {"integrity", NULL, integrity, NULL, NULL, NULL, napi_default, NULL}};
  napi_define_properties(
      env, exports, sizeof(properties) / sizeof(properties[0]), properties);
  return exports;
}
NAPI_MODULE(NODE_GYP_MODULE_NAME, init)
