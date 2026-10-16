#ifndef EXT_GO_BRIDGE_H
#define EXT_GO_BRIDGE_H
/* Glue between the C engine and the Go binding (pkg/plugin-go): C callbacks that
 * call the Go functions the binding exports with //export, and helpers cgo can
 * call. Everything is static inline so the Go package needs no .c file and no
 * build constraint on one; each cgo preamble includes this header. The extern
 * declarations name the Go functions defined by the binding. */
#include "ext_guest.h"
#include "ext_host.h"
#include "ext_instance.h"
#include "ext_stream.h"
#include <stddef.h>
#include <stdint.h>

/* --- host: policies, process and backend creation, hooks --- */
extern int32_t ctxGoPolicy(uintptr_t, int, uint8_t *, size_t);
extern int32_t ctxGoConnect(uintptr_t, uint32_t, uintptr_t, ext_emit, void *);
extern int32_t ctxGoInvoke(uintptr_t, uint8_t *, size_t, uint32_t, uintptr_t,
                           ext_emit, void *);
extern void ctxGoCloseBackend(uintptr_t);
extern void ctxGoReleaseBackend(uintptr_t);
extern int32_t ctxGoContextPolicy(uintptr_t, int, uintptr_t, uint8_t *, size_t);
extern void ctxGoObserver(uintptr_t, uintptr_t, uint8_t *, size_t);

static inline int32_t ext_go_verify(void *p, const uint8_t *b, size_t n) {
  return ctxGoPolicy((uintptr_t)p, 0, (uint8_t *)b, n);
}
static inline int32_t ext_go_authorize(void *p, const uint8_t *b, size_t n) {
  return ctxGoPolicy((uintptr_t)p, 1, (uint8_t *)b, n);
}
static inline ext_status ext_go_create(const char *path, const uint8_t *d,
                                       size_t n, uintptr_t handle,
                                       ext_host **out) {
  ext_jsonline_process_options process = {path};
  ext_backend_options backend = {sizeof(backend), EXT_BACKEND_JSONLINE_PROCESS,
                                 &process, sizeof(process)};
  ext_host_options o = {EXT_HOST_ABI_VERSION,
                        sizeof(o),
                        d,
                        n,
                        ext_go_verify,
                        ext_go_authorize,
                        (void *)handle};
  return ext_host_create(&o, &backend, out);
}
static inline ext_status ext_go_connect_backend(void *user,
                                                const ext_call_options *o,
                                                ext_emit emit, void *context) {
  return ctxGoConnect((uintptr_t)user, o->timeout_ms, (uintptr_t)o->value, emit,
                      context);
}
static inline ext_status ext_go_invoke_backend(void *user, const uint8_t *data,
                                               size_t len,
                                               const ext_call_options *o,
                                               ext_emit emit, void *context) {
  return ctxGoInvoke((uintptr_t)user, (uint8_t *)data, len, o->timeout_ms,
                     (uintptr_t)o->value, emit, context);
}
static inline void ext_go_close_backend(void *user) {
  ctxGoCloseBackend((uintptr_t)user);
}
static inline void ext_go_release_backend(void *user) {
  ctxGoReleaseBackend((uintptr_t)user);
}
static inline ext_status ext_go_create_backend(const uint8_t *data, size_t len,
                                               uintptr_t policy,
                                               uintptr_t runtime,
                                               uint32_t flags,
                                               ext_host **out) {
  ext_backend_extension_context extension = {
      sizeof(extension),       flags,
      (void *)runtime,         ext_go_connect_backend,
      ext_go_invoke_backend,   ext_go_close_backend,
      ext_go_release_backend};
  ext_backend_options backend = {sizeof(backend), EXT_BACKEND_EXTENSION_CONTEXT,
                                 &extension, sizeof(extension)};
  ext_host_options options = {EXT_HOST_ABI_VERSION,
                              sizeof(options),
                              data,
                              len,
                              ext_go_verify,
                              ext_go_authorize,
                              (void *)policy};
  return ext_host_create(&options, &backend, out);
}
static inline ext_status ext_go_emit(ext_emit emit, void *context, uint8_t *data,
                                     size_t len) {
  return emit(context, data, len);
}
static inline void ext_go_call_init(ext_call_options *o, uint32_t timeout,
                                    const ext_cancel *cancel, uintptr_t value) {
  *o = (ext_call_options){sizeof(*o), timeout, cancel, (void *)value};
}
static inline ext_status ext_go_create_launch(
    const char *path, const char *const *args, size_t argc,
    const char *const *env, size_t envc, const char *dir, uint32_t stderr_bytes,
    const uint8_t *d, size_t n, uintptr_t handle, ext_host **out) {
  ext_jsonline_process_launch launch = {sizeof(launch), path, args, argc,
                                        env,            envc, dir,  stderr_bytes};
  ext_backend_options backend = {sizeof(backend),
                                 EXT_BACKEND_JSONLINE_PROCESS_LAUNCH, &launch,
                                 sizeof(launch)};
  ext_host_options o = {EXT_HOST_ABI_VERSION,
                        sizeof(o),
                        d,
                        n,
                        ext_go_verify,
                        ext_go_authorize,
                        (void *)handle};
  return ext_host_create(&o, &backend, out);
}
static inline int32_t ext_go_verify_context(void *user,
                                            const ext_call_options *o,
                                            const uint8_t *data, size_t len) {
  return ctxGoContextPolicy((uintptr_t)user, 0, (uintptr_t)o->value,
                            (uint8_t *)data, len);
}
static inline int32_t ext_go_authorize_context(void *user,
                                               const ext_call_options *o,
                                               const uint8_t *data,
                                               size_t len) {
  return ctxGoContextPolicy((uintptr_t)user, 1, (uintptr_t)o->value,
                            (uint8_t *)data, len);
}
static inline void ext_go_observe_context(void *user, const ext_call_options *o,
                                          const uint8_t *data, size_t len) {
  ctxGoObserver((uintptr_t)user, (uintptr_t)o->value, (uint8_t *)data, len);
}
static inline ext_status ext_go_set_hooks(ext_host *host, uintptr_t user) {
  ext_host_hooks hooks = {sizeof(hooks),         (void *)user,
                          ext_go_verify_context, ext_go_authorize_context,
                          ext_go_observe_context};
  return ext_host_set_hooks(host, &hooks);
}
static inline ext_status ext_go_create_process(
    const char *path, const char *const *args, size_t argc,
    const char *const *env, size_t envc, const uint8_t *d, size_t n,
    uintptr_t handle, ext_host **out) {
  ext_jsonline_process_config process = {sizeof(process), path, args,
                                         argc,            env,  envc};
  ext_backend_options backend = {sizeof(backend),
                                 EXT_BACKEND_JSONLINE_PROCESS_CONFIG, &process,
                                 sizeof(process)};
  ext_host_options o = {EXT_HOST_ABI_VERSION,
                        sizeof(o),
                        d,
                        n,
                        ext_go_verify,
                        ext_go_authorize,
                        (void *)handle};
  return ext_host_create(&o, &backend, out);
}

/* --- guest --- */
extern int32_t ctxGoGuestHandler(uintptr_t, uintptr_t, uint8_t *, size_t,
                                 uint32_t, ext_guest_emit, void *);
static inline ext_status ext_go_guest_handle(void *user, void *call_user,
                                             const uint8_t *data, size_t len,
                                             uint32_t timeout,
                                             ext_guest_emit emit, void *sink) {
  return ctxGoGuestHandler((uintptr_t)user, (uintptr_t)call_user,
                           (uint8_t *)data, len, timeout, emit, sink);
}
static inline ext_status ext_go_guest_create(const uint8_t *data, size_t len,
                                             uint32_t timeout, uintptr_t user,
                                             ext_guest **out) {
  ext_guest_options options = {EXT_HOST_ABI_VERSION,
                               sizeof(options),
                               data,
                               len,
                               timeout,
                               (void *)user,
                               ext_go_guest_handle};
  return ext_guest_create(&options, out);
}
static inline ext_status ext_go_guest_emit(ext_guest_emit emit, void *sink,
                                           uint32_t kind, uint8_t *data,
                                           size_t len) {
  return emit(sink, kind, data, len);
}
static inline ext_status ext_go_guest_invoke(ext_guest *guest,
                                             const uint8_t *data, size_t len,
                                             uint32_t timeout,
                                             uintptr_t call_user,
                                             ext_buffer *out) {
  return ext_guest_invoke(guest, data, len, timeout, (void *)call_user, out);
}

/* --- instances --- */
extern int32_t ctxGoInstanceCreate(uintptr_t, uintptr_t, uint32_t, uint8_t *,
                                   size_t, uint8_t *, size_t, uintptr_t *);
extern int32_t ctxGoInstanceDispose(uintptr_t, uintptr_t);
extern void ctxGoInstanceObserve(uintptr_t, uint8_t *, size_t, uint8_t *, size_t,
                                 char *);
/* Go performs application validation before entering C admission so its local
 * error is retained. C has already checked the JSON before this hook. */
static inline ext_status ext_go_instance_validate(void *u, const uint8_t *p,
                                                  size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return EXT_OK;
}
static inline ext_status ext_go_instance_create_value(
    void *u, const ext_call_options *o, const ext_cancel *life,
    const uint8_t *k, size_t kn, const uint8_t *c, size_t cn, void **value) {
  (void)life;
  uintptr_t v = 0;
  ext_status s = ctxGoInstanceCreate((uintptr_t)u, (uintptr_t)o->value,
                                     o->timeout_ms, (uint8_t *)k, kn,
                                     (uint8_t *)c, cn, &v);
  *value = (void *)v;
  return s;
}
static inline ext_status ext_go_instance_dispose(void *u, void *v) {
  return ctxGoInstanceDispose((uintptr_t)u, (uintptr_t)v);
}
static inline void ext_go_instance_observe(void *u, const uint8_t *k, size_t kn,
                                           const uint8_t *r, size_t rn,
                                           const char *s) {
  ctxGoInstanceObserve((uintptr_t)u, (uint8_t *)k, kn, (uint8_t *)r, rn,
                       (char *)s);
}
static inline ext_status ext_go_instances_create(uintptr_t user,
                                                 uint32_t capacity,
                                                 ext_instances **out) {
  ext_instance_options o = {sizeof(o),
                            capacity,
                            (void *)user,
                            ext_go_instance_validate,
                            ext_go_instance_create_value,
                            ext_go_instance_dispose,
                            ext_go_instance_observe};
  return ext_instances_create(&o, out);
}
static inline uintptr_t ext_go_lease_value(ext_lease *l) {
  return (uintptr_t)ext_lease_value(l);
}

/* --- streams --- */
extern int32_t ctxGoStreamOpen(uintptr_t, uintptr_t, uint8_t *, size_t,
                               uintptr_t *);
extern int32_t ctxGoStreamRead(uintptr_t, uintptr_t, uint32_t, uint32_t,
                               ext_emit, void *);
extern int32_t ctxGoStreamClose(uintptr_t, uintptr_t);
extern void ctxGoStreamRelease(uintptr_t);
static inline ext_status ext_go_stream_open(void *u, const ext_call_options *o,
                                            const ext_cancel *life,
                                            const uint8_t *data, size_t n,
                                            void **value) {
  (void)life;
  uintptr_t v = 0;
  ext_status s =
      ctxGoStreamOpen((uintptr_t)u, (uintptr_t)o->value, (uint8_t *)data, n, &v);
  *value = (void *)v;
  return s;
}
static inline ext_status ext_go_stream_read(void *u, void *v,
                                            const ext_call_options *o,
                                            const ext_cancel *life,
                                            uint32_t limit, ext_emit emit,
                                            void *sink) {
  (void)u;
  (void)life;
  return ctxGoStreamRead((uintptr_t)v, (uintptr_t)o->value, o->timeout_ms,
                         limit, emit, sink);
}
static inline ext_status ext_go_stream_close(void *u, void *v) {
  return ctxGoStreamClose((uintptr_t)u, (uintptr_t)v);
}
static inline void ext_go_stream_release(void *u, void *v) {
  (void)u;
  ctxGoStreamRelease((uintptr_t)v);
}
static inline ext_status ext_go_streams_create(uintptr_t user, uint32_t capacity,
                                               uint32_t max_age,
                                               ext_streams **out) {
  ext_stream_options o = {sizeof(o),
                          capacity,
                          max_age,
                          (void *)user,
                          ext_go_stream_open,
                          ext_go_stream_read,
                          ext_go_stream_close,
                          ext_go_stream_release};
  return ext_streams_create(&o, out);
}
#endif
