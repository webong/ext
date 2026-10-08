#define _POSIX_C_SOURCE 200809L
#include "ext_guest.h"
#include "ext_host.h"
#include "ext_instance.h"
#include "ext_stream.h"
#include <node_api.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

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
  ext_host *host;
  ext_guest *guest;
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
  ext_status status;
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
      *len > EXT_HOST_MAX_FRAME)
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
static void finish(callback *r, ext_status status, uint32_t kind,
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
  int32_t status = EXT_IO;
  uint32_t kind = 0;
  size_t len = 0;
  uint8_t *bytes = NULL;
  if (count != 3 || napi_get_value_int32(env, args[0], &status) != napi_ok ||
      napi_get_value_uint32(env, args[1], &kind) != napi_ok)
    status = EXT_IO;
  else if (status == EXT_OK) {
    bytes = (uint8_t *)string(env, args[2], &len);
    if (!bytes)
      status = EXT_INVALID;
  }
  finish(r, status, kind, bytes, len);
  return undefined(env);
}
static void callback_js(napi_env env, napi_value function, void *context,
                        void *data) {
  (void)context;
  callback *r = data;
  if (!env || !function) {
    finish(r, EXT_CLOSED, 0, NULL, 0);
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
    finish(r, EXT_IO, 0, NULL, 0);
    release_callback(r);
    return;
  }
  /* The reply function owns the queued reference. A promise may retain it after
   * a timed-out native call. No stack or engine handle is retained in r. */
  if (napi_add_finalizer(env, args[2], r, reply_finalizer, NULL, NULL) !=
      napi_ok) {
    finish(r, EXT_IO, 0, NULL, 0);
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
    finish(r, EXT_IO, 0, NULL, 0);
  }
}
static ext_status invoke_js(napi_threadsafe_function fn, const uint8_t *input,
                            size_t len, const ext_call_options *o,
                            const ext_cancel *life, uint32_t *kind,
                            ext_buffer *out) {
  callback *r = calloc(1, sizeof(*r));
  if (!r)
    return EXT_NOMEM;
  if (pthread_mutex_init(&r->mu, NULL)) {
    free(r);
    return EXT_IO;
  }
  if (pthread_cond_init(&r->changed, NULL)) {
    pthread_mutex_destroy(&r->mu);
    free(r);
    return EXT_IO;
  }
  atomic_init(&r->refs, 2);
  r->input = malloc(len ? len : 1);
  if (!r->input) {
    release_callback(r);
    release_callback(r);
    return EXT_NOMEM;
  }
  memcpy(r->input, input, len);
  r->input_len = len;
  r->timeout = o->timeout_ms;
  if (napi_call_threadsafe_function(fn, r, napi_tsfn_nonblocking) != napi_ok) {
    release_callback(r);
    release_callback(r);
    return EXT_CLOSED;
  }
  int64_t end = now() + o->timeout_ms;
  pthread_mutex_lock(&r->mu);
  while (!r->done) {
    if (ext_cancel_is_signaled(o->cancel) || ext_cancel_is_signaled(life) ||
        now() >= end) {
      r->done = 1;
      r->status = ext_cancel_is_signaled(o->cancel) ||
                          ext_cancel_is_signaled(life)
                      ? EXT_CANCELED
                      : EXT_TIMEOUT;
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
  ext_status status = r->status;
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
static int32_t policy(binding *b, int verify, const ext_call_options *o,
                      const uint8_t *p, size_t n) {
  ext_buffer out = {0};
  uint32_t kind;
  ext_status s =
      invoke_js(verify ? b->verify : b->authorize, p, n, o, NULL, &kind, &out);
  ext_buffer_free(&out);
  return s == EXT_OK ? 0 : 1;
}
static int32_t verify(void *u, const ext_call_options *o, const uint8_t *p,
                      size_t n) {
  return policy(u, 1, o, p, n);
}
static int32_t authorize(void *u, const ext_call_options *o, const uint8_t *p,
                         size_t n) {
  return policy(u, 0, o, p, n);
}
static ext_status handle(void *u, void *call, const uint8_t *p, size_t n,
                         uint32_t ms, ext_guest_emit emit, void *sink) {
  binding *b = u;
  ext_call_options o = *(ext_call_options *)call;
  o.timeout_ms = ms;
  ext_buffer out = {0};
  uint32_t kind = 0;
  ext_status s = invoke_js(b->handle, p, n, &o, NULL, &kind, &out);
  if (!s)
    s = emit(sink, kind, out.data, out.len);
  ext_buffer_free(&out);
  return s;
}
static int tsfn(napi_env env, napi_value function,
                napi_threadsafe_function *out) {
  napi_valuetype type;
  if (napi_typeof(env, function, &type) != napi_ok || type != napi_function)
    return 0;
  napi_value name;
  napi_create_string_utf8(env, "ext-engine-callback", NAPI_AUTO_LENGTH, &name);
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
static void observe(void *user, const ext_call_options *call, const uint8_t *p,
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
  ext_host_close(b->host);
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
  ext_host_destroy(b->host);
  b->host = NULL;
  ext_guest_destroy(b->guest);
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
  ext_status status = EXT_INVALID;
  if (!path || strlen(path) != pn || !descriptor || !argv || !envp || !b)
    goto done;
  if (!tsfn(env, args[4], &b->verify) || !tsfn(env, args[5], &b->authorize))
    goto done;
  napi_valuetype observer_type;
  if (napi_typeof(env, args[6], &observer_type) != napi_ok)
    goto done;
  if (observer_type != napi_undefined) {
    napi_value name;
    napi_create_string_utf8(env, "ext-engine-observer", NAPI_AUTO_LENGTH,
                            &name);
    if (observer_type != napi_function ||
        napi_create_threadsafe_function(env, args[6], NULL, name, 1024, 1, NULL,
                                        NULL, NULL, observe_js,
                                        &b->observe) != napi_ok)
      goto done;
    napi_unref_threadsafe_function(env, b->observe);
  }
  ext_jsonline_process_config process = {sizeof(process),           path,
                                         (const char *const *)argv, an,
                                         (const char *const *)envp, en};
  ext_backend_options backend = {sizeof(backend),
                                 EXT_BACKEND_JSONLINE_PROCESS_CONFIG, &process,
                                 sizeof(process)};
  ext_host_options options = {EXT_HOST_ABI_VERSION,
                              sizeof(options),
                              (uint8_t *)descriptor,
                              dn,
                              allow_placeholder,
                              allow_placeholder,
                              b};
  status = ext_host_create(&options, &backend, &b->host);
  if (!status) {
    ext_host_hooks hooks = {sizeof(hooks), b, verify, authorize, observe};
    status = ext_host_set_hooks(b->host, &hooks);
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
    return failure(env, ext_host_status_string(status ? status : EXT_IO));
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
  ext_status status = EXT_INVALID;
  if (descriptor && b && napi_get_value_uint32(env, args[1], &ms) == napi_ok &&
      tsfn(env, args[2], &b->handle)) {
    ext_guest_options options = {EXT_HOST_ABI_VERSION,
                                 sizeof(options),
                                 (uint8_t *)descriptor,
                                 n,
                                 ms,
                                 b,
                                 handle};
    status = ext_guest_create(&options, &b->guest);
  }
  free(descriptor);
  if (status || !register_cleanup(env, b) ||
      napi_create_external(env, b, destroy_binding, NULL, &external) !=
          napi_ok) {
    if (b)
      destroy_binding(env, b, NULL);
    return failure(env, ext_host_status_string(status ? status : EXT_IO));
  }
  if (napi_type_tag_object(env, external, &binding_tag) != napi_ok)
    return failure(env, "cannot tag native handle");
  return external;
}
static void destroy_cancel(napi_env env, void *data, void *hint) {
  (void)env;
  (void)hint;
  ext_cancel_destroy(data);
}
static napi_value new_cancel(napi_env env, napi_callback_info info) {
  (void)info;
  ext_cancel *c = NULL;
  napi_value out;
  ext_status s = ext_cancel_create(&c);
  if (s)
    return failure(env, ext_host_status_string(s));
  if (napi_create_external(env, c, destroy_cancel, NULL, &out) != napi_ok) {
    ext_cancel_destroy(c);
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
  ext_cancel_signal(c);
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
  ext_host_close(b->host);
  if (!b->jobs)
    dispose_contents(b);
  return undefined(env);
}
typedef struct {
  napi_async_work work;
  napi_deferred deferred;
  napi_ref owner, signal;
  binding *binding;
  ext_call_options options;
  char *operation;
  char *root;
  uint8_t *input;
  size_t input_len;
  ext_buffer output;
  ext_status status;
} job;
static void execute(napi_env env, void *data) {
  (void)env;
  job *j = data;
  binding *b = j->binding;
  if (j->root) {
    if (!strcmp(j->operation, "verify")) {
      j->status = ext_package_verify(j->input, j->input_len, j->root);
    } else if (!strcmp(j->operation, "digest")) {
      uint8_t hash[32];
      j->status = ext_directory_digest(j->root, hash);
      if (!j->status) {
        j->output.data = malloc(67);
        if (!j->output.data)
          j->status = EXT_NOMEM;
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
      j->status = EXT_INVALID;
  } else if (!strcmp(j->operation, "start") && b->host)
    j->status = ext_host_start_with_options(b->host, &j->options);
  else if (!strcmp(j->operation, "invoke") && b->host)
    j->status = ext_host_invoke_with_options(b->host, j->input, j->input_len,
                                             &j->options, &j->output);
  else if (!strcmp(j->operation, "call") && b->host)
    j->status =
        ext_host_call(b->host, j->input, j->input_len, &j->options, &j->output);
  else if (!strcmp(j->operation, "drain") && b->host)
    j->status = ext_host_drain_with_options(b->host, &j->options);
  else if (!strcmp(j->operation, "guest.invoke") && b->guest)
    j->status =
        ext_guest_invoke(b->guest, j->input, j->input_len,
                         j->options.timeout_ms, &j->options, &j->output);
  else if (!strcmp(j->operation, "descriptor") && b->guest)
    j->status = ext_guest_descriptor(b->guest, &j->output);
  else
    j->status = EXT_INVALID;
  if (ext_cancel_is_signaled(j->options.cancel))
    j->status = EXT_CANCELED;
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
  ext_buffer_free(&j->output);
  free(j);
}
static void complete(napi_env env, napi_status code, void *data) {
  job *j = data;
  napi_value value;
  if (code != napi_ok && !j->status)
    j->status = EXT_IO;
  if (j->status) {
    napi_value message, status;
    napi_create_string_utf8(env, ext_host_status_string(j->status),
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
  napi_create_string_utf8(env, "ext-engine-call", NAPI_AUTO_LENGTH, &name);
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
  ext_buffer out = {0};
  ext_status status = op && strlen(op) == on && input
                          ? ext_engine_call(op, (uint8_t *)input, n, &out)
                          : EXT_INVALID;
  free(op);
  free(input);
  if (status) {
    ext_buffer_free(&out);
    return failure(env, ext_host_status_string(status));
  }
  napi_status s =
      napi_create_string_utf8(env, (char *)out.data, out.len, &result);
  ext_buffer_free(&out);
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
  napi_create_string_utf8(env, "ext-engine-integrity", NAPI_AUTO_LENGTH, &name);
  if (napi_create_async_work(env, NULL, name, execute, complete, j, &j->work) !=
          napi_ok ||
      napi_queue_async_work(env, j->work) != napi_ok)
    goto invalid;
  return promise;
invalid:
  free_job(env, j);
  return failure(env, "invalid integrity arguments or allocation failure");
}

/* Instance and stream managers. Their C callbacks run on engine threads and
 * block on one JS function per manager. Fields are NUL separated after the
 * operation name; keys and configuration therefore cannot contain NUL. Native
 * calls run on libuv workers so a callback can never wait on the JS thread. */
static const napi_type_tag manager_tag = {0x4e3f1a8c52d70b19ULL,
                                          0x91c0e5b7a6d24f38ULL};
static const napi_type_tag lease_tag = {0x7a21c4d90be35f68ULL,
                                        0x2bd8f6e1479a0c53ULL};
typedef struct {
  ext_instances *instances;
  ext_streams *streams;
  napi_threadsafe_function fn;
  napi_env env;
  int cleanup_hook, disposed;
  size_t jobs;
} manager;
typedef struct {
  ext_lease *lease;
} lease_box;
typedef struct {
  const uint8_t *p;
  size_t n;
} field;
static ext_status manager_call(manager *m, const ext_call_options *call,
                               const ext_cancel *life, ext_buffer *out,
                               const char *op, const field *fields,
                               size_t count) {
  ext_call_options fallback = {sizeof(fallback), 30000, NULL, NULL};
  if (!call)
    call = &fallback;
  size_t total = strlen(op) + 1;
  for (size_t i = 0; i < count; i++) {
    if (memchr(fields[i].p, 0, fields[i].n))
      return EXT_INVALID;
    total += fields[i].n + (i + 1 < count ? 1 : 0);
  }
  uint8_t *frame = malloc(total ? total : 1), *at = frame;
  if (!frame)
    return EXT_NOMEM;
  size_t n = strlen(op);
  memcpy(at, op, n);
  at += n;
  for (size_t i = 0; i < count; i++) {
    *at++ = 0;
    if (fields[i].n)
      memcpy(at, fields[i].p, fields[i].n);
    at += fields[i].n;
  }
  uint32_t kind = 0;
  ext_buffer scratch = {0};
  ext_status s = invoke_js(m->fn, frame, (size_t)(at - frame), call, life,
                           &kind, out ? out : &scratch);
  free(frame);
  ext_buffer_free(&scratch);
  return s;
}
static void *handle_id(ext_buffer *out, ext_status *status) {
  char *end = NULL;
  unsigned long long id = 0;
  if (*status || !out->data || !out->len)
    goto bad;
  char *text = malloc(out->len + 1);
  if (!text)
    goto bad;
  memcpy(text, out->data, out->len);
  text[out->len] = 0;
  id = strtoull(text, &end, 10);
  int valid = id && end && !*end;
  free(text);
  if (valid)
    return (void *)(uintptr_t)id;
bad:
  if (!*status)
    *status = EXT_INVALID;
  return NULL;
}
static ext_status instance_validate(void *u, const uint8_t *p, size_t n) {
  field f[] = {{p, n}};
  return manager_call(u, NULL, NULL, NULL, "validate", f, 1);
}
static ext_status instance_create(void *u, const ext_call_options *call,
                                  const ext_cancel *life, const uint8_t *key,
                                  size_t kn, const uint8_t *config, size_t cn,
                                  void **value) {
  ext_buffer out = {0};
  field f[] = {{key, kn}, {config, cn}};
  ext_status s = manager_call(u, call, life, &out, "create", f, 2);
  *value = handle_id(&out, &s);
  ext_buffer_free(&out);
  return s;
}
static ext_status instance_dispose(void *u, void *value) {
  char id[32];
  snprintf(id, sizeof(id), "%llu", (unsigned long long)(uintptr_t)value);
  field f[] = {{(uint8_t *)id, strlen(id)}};
  return manager_call(u, NULL, NULL, NULL, "dispose", f, 1);
}
static ext_status stream_open(void *u, const ext_call_options *call,
                              const ext_cancel *life, const uint8_t *p,
                              size_t n, void **value) {
  ext_buffer out = {0};
  field f[] = {{p, n}};
  ext_status s = manager_call(u, call, life, &out, "open", f, 1);
  *value = handle_id(&out, &s);
  ext_buffer_free(&out);
  return s;
}
static ext_status stream_read(void *u, void *value, const ext_call_options *call,
                              const ext_cancel *life, uint32_t limit,
                              ext_emit emit, void *sink) {
  char id[32], count[16];
  snprintf(id, sizeof(id), "%llu", (unsigned long long)(uintptr_t)value);
  snprintf(count, sizeof(count), "%u", limit);
  field f[] = {{(uint8_t *)id, strlen(id)}, {(uint8_t *)count, strlen(count)}};
  ext_buffer out = {0};
  ext_status s = manager_call(u, call, life, &out, "read", f, 2);
  if (!s)
    s = emit(sink, out.data, out.len);
  ext_buffer_free(&out);
  return s;
}
static ext_status stream_close(void *u, void *value) {
  char id[32];
  snprintf(id, sizeof(id), "%llu", (unsigned long long)(uintptr_t)value);
  field f[] = {{(uint8_t *)id, strlen(id)}};
  return manager_call(u, NULL, NULL, NULL, "close", f, 1);
}
static void stream_release(void *u, void *value) {
  char id[32];
  snprintf(id, sizeof(id), "%llu", (unsigned long long)(uintptr_t)value);
  field f[] = {{(uint8_t *)id, strlen(id)}};
  (void)manager_call(u, NULL, NULL, NULL, "release", f, 1);
}
static void release_manager_function(manager *m) {
  if (m->fn) {
    napi_release_threadsafe_function(m->fn, napi_tsfn_abort);
    m->fn = NULL;
  }
}
static void cleanup_manager(void *data) {
  manager *m = data;
  m->cleanup_hook = 0;
  /* Environment teardown cannot wait for JS. The C manager is left for process
   * exit unless the application closed it explicitly. */
  release_manager_function(m);
}
static void finish_manager(manager *m) {
  if (m->cleanup_hook)
    napi_remove_env_cleanup_hook(m->env, cleanup_manager, m);
  m->cleanup_hook = 0;
  release_manager_function(m);
  m->disposed = 1;
}
static void destroy_manager_external(napi_env env, void *data, void *hint) {
  (void)env;
  (void)hint;
  manager *m = data;
  if (m->cleanup_hook)
    napi_remove_env_cleanup_hook(m->env, cleanup_manager, m);
  release_manager_function(m);
  /* Leak a still-live C manager rather than block the JS finalizer. */
  if (!m->instances && !m->streams && !m->jobs)
    free(m);
}
static napi_value wrap_manager(napi_env env, manager *m, ext_status status) {
  napi_value out;
  m->env = env;
  if (status || napi_add_env_cleanup_hook(env, cleanup_manager, m) != napi_ok)
    goto fail;
  m->cleanup_hook = 1;
  if (napi_create_external(env, m, destroy_manager_external, NULL, &out) !=
          napi_ok ||
      napi_type_tag_object(env, out, &manager_tag) != napi_ok)
    goto fail;
  return out;
fail:
  release_manager_function(m);
  if (m->instances)
    ext_instances_destroy(m->instances);
  if (m->streams)
    ext_streams_destroy(m->streams);
  if (m->cleanup_hook)
    napi_remove_env_cleanup_hook(env, cleanup_manager, m);
  free(m);
  return failure(env, ext_host_status_string(status ? status : EXT_IO));
}
static napi_value create_instances(napi_env env, napi_callback_info info) {
  size_t count = 2;
  napi_value args[2];
  uint32_t capacity = 0;
  manager *m = calloc(1, sizeof(*m));
  if (!m || napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count != 2 || napi_get_value_uint32(env, args[0], &capacity) != napi_ok ||
      !tsfn(env, args[1], &m->fn)) {
    if (m) {
      release_manager_function(m);
      free(m);
    }
    return failure(env, "invalid instance manager arguments");
  }
  ext_instance_options options = {sizeof(options),    capacity,
                                  m,                  instance_validate,
                                  instance_create,    instance_dispose,
                                  NULL};
  ext_status status = ext_instances_create(&options, &m->instances);
  return wrap_manager(env, m, status);
}
static napi_value create_streams(napi_env env, napi_callback_info info) {
  size_t count = 3;
  napi_value args[3];
  uint32_t capacity = 0, max_age = 0;
  manager *m = calloc(1, sizeof(*m));
  if (!m || napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count != 3 || napi_get_value_uint32(env, args[0], &capacity) != napi_ok ||
      napi_get_value_uint32(env, args[1], &max_age) != napi_ok ||
      !tsfn(env, args[2], &m->fn)) {
    if (m) {
      release_manager_function(m);
      free(m);
    }
    return failure(env, "invalid stream manager arguments");
  }
  ext_stream_options options = {sizeof(options), capacity,      max_age, m,
                                stream_open,     stream_read,   stream_close,
                                stream_release};
  ext_status status = ext_streams_create(&options, &m->streams);
  return wrap_manager(env, m, status);
}
typedef struct {
  napi_async_work work;
  napi_deferred deferred;
  napi_ref owner, signal, lease_ref;
  manager *manager;
  lease_box *box;
  ext_lease *lease;
  ext_call_options options;
  char *operation;
  char *field[3];
  size_t length[3];
  ext_buffer output;
  ext_status status;
} manager_job;
static int is_op(const manager_job *j, const char *name) {
  return !strcmp(j->operation, name);
}
static void manager_execute(napi_env env, void *data) {
  (void)env;
  manager_job *j = data;
  manager *m = j->manager;
  const uint8_t *a = (uint8_t *)j->field[0], *b = (uint8_t *)j->field[1],
                *c = (uint8_t *)j->field[2];
  size_t an = j->length[0], bn = j->length[1], cn = j->length[2];
  if (m->instances) {
    if (is_op(j, "configure"))
      j->status =
          ext_instances_configure(m->instances, a, an, b, bn, c, cn, &j->options);
    else if (is_op(j, "acquire"))
      j->status = ext_instances_acquire(m->instances, a, an, &j->lease);
    else if (is_op(j, "release"))
      j->status = ext_lease_release(j->lease);
    else if (is_op(j, "remove"))
      j->status = ext_instances_remove(m->instances, a, an);
    else if (is_op(j, "close"))
      j->status = ext_instances_close(m->instances, &j->options);
    else if (is_op(j, "destroy")) {
      int64_t end = now() + j->options.timeout_ms;
      do {
        j->status = ext_instances_destroy(m->instances);
        if (j->status == EXT_DRAINING)
          nanosleep(&(struct timespec){0, 10000000}, NULL);
      } while (j->status == EXT_DRAINING &&
               !ext_cancel_is_signaled(j->options.cancel) && now() < end);
    } else
      j->status = EXT_INVALID;
  } else if (m->streams) {
    if (is_op(j, "open"))
      j->status =
          ext_streams_open(m->streams, a, an, b, bn, &j->options, &j->output);
    else if (is_op(j, "read")) {
      char *end = NULL, *limit_end = NULL;
      unsigned long long sequence = strtoull(j->field[2], &end, 10);
      unsigned long limit = end && *end == ' ' ? strtoul(end + 1, &limit_end, 10)
                                              : 0;
      if (!limit_end || *limit_end || !limit || limit > 0xffffffffUL)
        j->status = EXT_INVALID;
      else
        j->status = ext_streams_read(m->streams, a, an, j->field[1], sequence,
                                     (uint32_t)limit, &j->options, &j->output);
    } else if (is_op(j, "remove"))
      j->status = ext_streams_remove(m->streams, a, an, j->field[1]);
    else if (is_op(j, "close"))
      j->status = ext_streams_close(m->streams);
    else if (is_op(j, "destroy")) {
      int64_t end = now() + j->options.timeout_ms;
      do {
        j->status = ext_streams_destroy(m->streams);
        if (j->status == EXT_DRAINING)
          nanosleep(&(struct timespec){0, 10000000}, NULL);
      } while (j->status == EXT_DRAINING &&
               !ext_cancel_is_signaled(j->options.cancel) && now() < end);
    } else
      j->status = EXT_INVALID;
  } else
    j->status = EXT_CLOSED;
}
static void free_manager_job(napi_env env, manager_job *j) {
  if (j->owner)
    napi_delete_reference(env, j->owner);
  if (j->signal)
    napi_delete_reference(env, j->signal);
  if (j->lease_ref)
    napi_delete_reference(env, j->lease_ref);
  if (j->work)
    napi_delete_async_work(env, j->work);
  free(j->operation);
  for (int i = 0; i < 3; i++)
    free(j->field[i]);
  ext_buffer_free(&j->output);
  free(j);
}
static void free_lease_box(napi_env env, void *data, void *hint) {
  (void)env;
  (void)hint;
  /* An unreleased lease is intentionally leaked: releasing here could run a
   * disposer on the JS thread and wait on JS. Applications must release. */
  free(data);
}
static void manager_complete(napi_env env, napi_status code, void *data) {
  manager_job *j = data;
  manager *m = j->manager;
  napi_value value;
  if (code != napi_ok && !j->status)
    j->status = EXT_IO;
  if (j->status) {
    napi_value message, status;
    napi_create_string_utf8(env, ext_host_status_string(j->status),
                            NAPI_AUTO_LENGTH, &message);
    napi_create_error(env, NULL, message, &value);
    napi_create_int32(env, j->status, &status);
    napi_set_named_property(env, value, "status", status);
    napi_reject_deferred(env, j->deferred, value);
  } else if (is_op(j, "acquire") && j->lease) {
    lease_box *box = malloc(sizeof(*box));
    if (box && napi_create_external(env, box, free_lease_box, NULL, &value) == napi_ok &&
        napi_type_tag_object(env, value, &lease_tag) == napi_ok) {
      box->lease = j->lease;
      napi_resolve_deferred(env, j->deferred, value);
    } else {
      free(box);
      ext_lease_release(j->lease);
      napi_value message;
      napi_create_string_utf8(env, "allocation failed", NAPI_AUTO_LENGTH,
                              &message);
      napi_create_error(env, NULL, message, &value);
      napi_reject_deferred(env, j->deferred, value);
    }
  } else {
    napi_create_string_utf8(env,
                            j->output.data ? (char *)j->output.data : "null",
                            j->output.data ? j->output.len : 4, &value);
    napi_resolve_deferred(env, j->deferred, value);
  }
  if (!j->status && is_op(j, "destroy")) {
    m->instances = NULL;
    m->streams = NULL;
    finish_manager(m);
  }
  m->jobs--;
  free_manager_job(env, j);
}
/* request(manager, operation, a, b, c, timeout, cancel[, lease]) */
static napi_value manager_request(napi_env env, napi_callback_info info) {
  size_t count = 8;
  napi_value args[8], promise, name;
  if (napi_get_cb_info(env, info, &count, args, NULL, NULL) != napi_ok ||
      count < 7)
    return failure(env, "invalid manager call arguments");
  manager_job *j = calloc(1, sizeof(*j));
  if (!j)
    return failure(env, "allocation failed");
  size_t on = 0;
  void *cancel = NULL;
  j->options.struct_size = sizeof(j->options);
  if (!external(env, args[0], &manager_tag, (void **)&j->manager) ||
      !external(env, args[6], &cancel_tag, &cancel) ||
      napi_get_value_uint32(env, args[5], &j->options.timeout_ms) != napi_ok ||
      !j->options.timeout_ms || !(j->operation = string(env, args[1], &on)) ||
      strlen(j->operation) != on)
    goto invalid;
  for (int i = 0; i < 3; i++)
    if (!(j->field[i] = string(env, args[2 + i], &j->length[i])))
      goto invalid;
  if (j->manager->disposed)
    goto invalid;
  if (count == 8) {
    lease_box *box = NULL;
    if (!external(env, args[7], &lease_tag, (void **)&box) || !box->lease)
      goto invalid;
    j->box = box;
    j->lease = box->lease;
    box->lease = NULL; /* exactly one release per lease */
    if (napi_create_reference(env, args[7], 1, &j->lease_ref) != napi_ok)
      goto invalid;
  }
  j->options.cancel = cancel;
  if (napi_create_reference(env, args[0], 1, &j->owner) != napi_ok ||
      napi_create_reference(env, args[6], 1, &j->signal) != napi_ok ||
      napi_create_promise(env, &j->deferred, &promise) != napi_ok)
    goto invalid;
  napi_create_string_utf8(env, "ext-engine-manager", NAPI_AUTO_LENGTH, &name);
  if (napi_create_async_work(env, NULL, name, manager_execute,
                             manager_complete, j, &j->work) != napi_ok ||
      napi_queue_async_work(env, j->work) != napi_ok)
    goto invalid;
  j->manager->jobs++;
  return promise;
invalid:
  if (j->box && !j->box->lease)
    j->box->lease = j->lease;
  free_manager_job(env, j);
  return failure(env, "invalid manager call or allocation failure");
}
static napi_value lease_info(napi_env env, napi_callback_info info) {
  size_t count = 1;
  napi_value arg, out, revision, id;
  lease_box *box = NULL;
  if (napi_get_cb_info(env, info, &count, &arg, NULL, NULL) != napi_ok ||
      count != 1 || !external(env, arg, &lease_tag, (void **)&box) ||
      !box->lease)
    return failure(env, "invalid lease");
  size_t n = 0;
  const uint8_t *rev = ext_lease_revision(box->lease, &n);
  if (napi_create_object(env, &out) != napi_ok ||
      napi_create_string_utf8(env, (const char *)rev, n, &revision) !=
          napi_ok ||
      napi_create_double(env, (double)(uintptr_t)ext_lease_value(box->lease),
                         &id) != napi_ok)
    return failure(env, "allocation failed");
  napi_set_named_property(env, out, "revision", revision);
  napi_set_named_property(env, out, "id", id);
  return out;
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
      {"integrity", NULL, integrity, NULL, NULL, NULL, napi_default, NULL},
      {"createInstances", NULL, create_instances, NULL, NULL, NULL,
       napi_default, NULL},
      {"createStreams", NULL, create_streams, NULL, NULL, NULL, napi_default,
       NULL},
      {"managerRequest", NULL, manager_request, NULL, NULL, NULL, napi_default,
       NULL},
      {"leaseInfo", NULL, lease_info, NULL, NULL, NULL, napi_default, NULL}};
  napi_define_properties(
      env, exports, sizeof(properties) / sizeof(properties[0]), properties);
  return exports;
}
NAPI_MODULE(NODE_GYP_MODULE_NAME, init)
