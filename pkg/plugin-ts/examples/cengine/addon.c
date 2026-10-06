/* Minimal asynchronous Node-API integration example, not a published binding.
 * Each batch owns a host on a libuv worker; no JS callbacks on native threads.
 */
#include "ctx_host.h"
#include <node_api.h>
#include <stdlib.h>
#include <string.h>
typedef struct {
  napi_async_work work;
  napi_deferred deferred;
  char *path, *descriptor;
  size_t descriptor_len, count;
  ctx_buffer *requests, *responses;
  ctx_status status;
} job;
static int32_t allow(void *u, const uint8_t *b, size_t n) {
  (void)u;
  (void)b;
  (void)n;
  return 0;
}
static void execute(napi_env env, void *data) {
  (void)env;
  job *j = data;
  ctx_host *h = NULL;
  ctx_jsonline_process_options process = {j->path};
  ctx_backend_options backend = {sizeof(backend), CTX_BACKEND_JSONLINE_PROCESS,
                                 &process, sizeof(process)};
  ctx_host_options o = {CTX_HOST_ABI_VERSION,
                        sizeof(o),
                        (uint8_t *)j->descriptor,
                        j->descriptor_len,
                        allow,
                        allow,
                        NULL};
  j->status = ctx_host_create(&o, &backend, &h);
  if (!j->status)
    j->status = ctx_host_start(h, 3000);
  for (size_t i = 0; !j->status && i < j->count; i++)
    j->status = ctx_host_invoke(h, j->requests[i].data, j->requests[i].len,
                                3000, &j->responses[i]);
  ctx_host_destroy(h);
}
static void release(job *j) {
  if (!j)
    return;
  free(j->path);
  free(j->descriptor);
  for (size_t i = 0; i < j->count; i++) {
    free(j->requests[i].data);
    ctx_buffer_free(&j->responses[i]);
  }
  free(j->requests);
  free(j->responses);
  free(j);
}
static void complete(napi_env env, napi_status status, void *data) {
  job *j = data;
  napi_value result;
  if (status != napi_ok || j->status) {
    napi_value message;
    napi_create_string_utf8(
        env, ctx_host_status_string(j->status ? j->status : CTX_IO),
        NAPI_AUTO_LENGTH, &message);
    napi_create_error(env, NULL, message, &result);
    napi_reject_deferred(env, j->deferred, result);
  } else {
    napi_create_array_with_length(env, j->count, &result);
    for (size_t i = 0; i < j->count; i++) {
      napi_value s;
      napi_create_string_utf8(env, (char *)j->responses[i].data,
                              j->responses[i].len, &s);
      napi_set_element(env, result, (uint32_t)i, s);
    }
    napi_resolve_deferred(env, j->deferred, result);
  }
  napi_delete_async_work(env, j->work);
  release(j);
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
static napi_value run(napi_env env, napi_callback_info info) {
  size_t argc = 3;
  napi_value args[3], promise, name;
  job *j = calloc(1, sizeof(*j));
  if (!j) {
    napi_throw_error(env, NULL, "allocation failed");
    return NULL;
  }
  if (napi_get_cb_info(env, info, &argc, args, NULL, NULL) != napi_ok ||
      argc != 3)
    goto invalid;
  size_t path_len;
  j->path = string(env, args[0], &path_len);
  j->descriptor = string(env, args[1], &j->descriptor_len);
  uint32_t count;
  if (!j->path || strlen(j->path) != path_len || !j->descriptor ||
      napi_get_array_length(env, args[2], &count) != napi_ok || !count ||
      count > 64)
    goto invalid;
  j->requests = calloc(count, sizeof(ctx_buffer));
  j->responses = calloc(count, sizeof(ctx_buffer));
  if (!j->requests || !j->responses)
    goto invalid;
  j->count = count;
  for (uint32_t i = 0; i < count; i++) {
    napi_value v;
    if (napi_get_element(env, args[2], i, &v) != napi_ok)
      goto invalid;
    j->requests[i].data = (uint8_t *)string(env, v, &j->requests[i].len);
    if (!j->requests[i].data)
      goto invalid;
  }
  if (napi_create_promise(env, &j->deferred, &promise) != napi_ok)
    goto invalid;
  if (napi_create_string_utf8(env, "ctx-host", NAPI_AUTO_LENGTH, &name) !=
      napi_ok)
    goto invalid;
  if (napi_create_async_work(env, NULL, name, execute, complete, j, &j->work) !=
      napi_ok)
    goto invalid;
  if (napi_queue_async_work(env, j->work) != napi_ok) {
    napi_delete_async_work(env, j->work);
    goto invalid;
  }
  return promise;
invalid:
  release(j);
  napi_throw_error(env, NULL,
                   "expected absolute guest path, descriptor JSON and 1..64 "
                   "request strings");
  return NULL;
}
static napi_value init(napi_env env, napi_value exports) {
  napi_value fn;
  if (napi_create_function(env, "run", NAPI_AUTO_LENGTH, run, NULL, &fn) !=
      napi_ok)
    return NULL;
  napi_set_named_property(env, exports, "run", fn);
  return exports;
}
NAPI_MODULE(NODE_GYP_MODULE_NAME, init)
