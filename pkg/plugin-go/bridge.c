// go:build ctx_cengine && cgo && (darwin || linux)
#include "ctx_host.h"
extern int32_t ctxGoPolicy(uintptr_t, int, uint8_t *, size_t);
static int32_t verify(void *p, const uint8_t *b, size_t n) {
  return ctxGoPolicy((uintptr_t)p, 0, (uint8_t *)b, n);
}
static int32_t authorize(void *p, const uint8_t *b, size_t n) {
  return ctxGoPolicy((uintptr_t)p, 1, (uint8_t *)b, n);
}
ctx_status ctx_go_create(const char *path, const uint8_t *d, size_t n,
                         uintptr_t handle, ctx_host **out) {
  ctx_jsonline_process_options process = {path};
  ctx_backend_options backend = {sizeof(backend), CTX_BACKEND_JSONLINE_PROCESS,
                                 &process, sizeof(process)};
  ctx_host_options o = {
      CTX_HOST_ABI_VERSION, sizeof(o), d, n, verify, authorize, (void *)handle};
  return ctx_host_create(&o, &backend, out);
}

extern int32_t ctxGoConnect(uintptr_t, uint32_t, uintptr_t, ctx_emit, void *);
extern int32_t ctxGoInvoke(uintptr_t, uint8_t *, size_t, uint32_t, uintptr_t,
                           ctx_emit, void *);
extern void ctxGoCloseBackend(uintptr_t);
extern void ctxGoReleaseBackend(uintptr_t);
static ctx_status connect_backend(void *user, const ctx_call_options *o,
                                  ctx_emit emit, void *context) {
  return ctxGoConnect((uintptr_t)user, o->timeout_ms, (uintptr_t)o->value, emit,
                      context);
}
static ctx_status invoke_backend(void *user, const uint8_t *data, size_t len,
                                 const ctx_call_options *o, ctx_emit emit,
                                 void *context) {
  return ctxGoInvoke((uintptr_t)user, (uint8_t *)data, len, o->timeout_ms,
                     (uintptr_t)o->value, emit, context);
}
static void close_backend(void *user) { ctxGoCloseBackend((uintptr_t)user); }
static void release_backend(void *user) {
  ctxGoReleaseBackend((uintptr_t)user);
}
ctx_status ctx_go_create_backend(const uint8_t *data, size_t len,
                                 uintptr_t policy, uintptr_t runtime,
                                 uint32_t flags, ctx_host **out) {
  ctx_backend_extension_context extension = {
      sizeof(extension), flags,         (void *)runtime, connect_backend,
      invoke_backend,    close_backend, release_backend};
  ctx_backend_options backend = {sizeof(backend), CTX_BACKEND_EXTENSION_CONTEXT,
                                 &extension, sizeof(extension)};
  ctx_host_options options = {
      CTX_HOST_ABI_VERSION, sizeof(options), data, len, verify, authorize,
      (void *)policy};
  return ctx_host_create(&options, &backend, out);
}
ctx_status ctx_go_emit(ctx_emit emit, void *context, uint8_t *data,
                       size_t len) {
  return emit(context, data, len);
}

void ctx_go_call_init(ctx_call_options *o, uint32_t timeout,
                      const ctx_cancel *cancel, uintptr_t value) {
  *o = (ctx_call_options){sizeof(*o), timeout, cancel, (void *)value};
}
extern int32_t ctxGoContextPolicy(uintptr_t, int, uintptr_t, uint8_t *, size_t);
extern void ctxGoObserver(uintptr_t, uintptr_t, uint8_t *, size_t);
static int32_t verify_context(void *user, const ctx_call_options *o,
                              const uint8_t *data, size_t len) {
  return ctxGoContextPolicy((uintptr_t)user, 0, (uintptr_t)o->value,
                            (uint8_t *)data, len);
}
static int32_t authorize_context(void *user, const ctx_call_options *o,
                                 const uint8_t *data, size_t len) {
  return ctxGoContextPolicy((uintptr_t)user, 1, (uintptr_t)o->value,
                            (uint8_t *)data, len);
}
static void observe_context(void *user, const ctx_call_options *o,
                            const uint8_t *data, size_t len) {
  ctxGoObserver((uintptr_t)user, (uintptr_t)o->value, (uint8_t *)data, len);
}
ctx_status ctx_go_set_hooks(ctx_host *host, uintptr_t user) {
  ctx_host_hooks hooks = {sizeof(hooks), (void *)user, verify_context,
                          authorize_context, observe_context};
  return ctx_host_set_hooks(host, &hooks);
}
ctx_status ctx_go_create_process(const char *path,const char *const *args,size_t argc,const char *const *env,size_t envc,const uint8_t *d,size_t n,uintptr_t handle,ctx_host **out){
 ctx_jsonline_process_config process={sizeof(process),path,args,argc,env,envc};
 ctx_backend_options backend={sizeof(backend),CTX_BACKEND_JSONLINE_PROCESS_CONFIG,&process,sizeof(process)};
 ctx_host_options o={CTX_HOST_ABI_VERSION,sizeof(o),d,n,verify,authorize,(void *)handle};return ctx_host_create(&o,&backend,out);
}
