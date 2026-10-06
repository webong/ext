// go:build ctx_cengine && cgo && (darwin || linux)
#include "ctx_guest.h"
extern int32_t ctxGoGuestHandler(uintptr_t, uintptr_t, uint8_t *, size_t,
                                 uint32_t, ctx_guest_emit, void *);
static ctx_status handle(void *user, void *call_user, const uint8_t *data,
                         size_t len, uint32_t timeout, ctx_guest_emit emit,
                         void *sink) {
  return ctxGoGuestHandler((uintptr_t)user, (uintptr_t)call_user,
                           (uint8_t *)data, len, timeout, emit, sink);
}
ctx_status ctx_go_guest_create(const uint8_t *data, size_t len,
                               uint32_t timeout, uintptr_t user,
                               ctx_guest **out) {
  ctx_guest_options options = {
      CTX_HOST_ABI_VERSION, sizeof(options), data, len, timeout,
      (void *)user,         handle};
  return ctx_guest_create(&options, out);
}
ctx_status ctx_go_guest_emit(ctx_guest_emit emit, void *sink, uint32_t kind,
                             uint8_t *data, size_t len) {
  return emit(sink, kind, data, len);
}
ctx_status ctx_go_guest_invoke(ctx_guest *guest, const uint8_t *data,
                               size_t len, uint32_t timeout,
                               uintptr_t call_user, ctx_buffer *out) {
  return ctx_guest_invoke(guest, data, len, timeout, (void *)call_user, out);
}
