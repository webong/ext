// go:build ext_cengine && cgo && (darwin || linux)
#include "ext_guest.h"
extern int32_t ctxGoGuestHandler(uintptr_t, uintptr_t, uint8_t *, size_t,
                                 uint32_t, ext_guest_emit, void *);
static ext_status handle(void *user, void *call_user, const uint8_t *data,
                         size_t len, uint32_t timeout, ext_guest_emit emit,
                         void *sink) {
  return ctxGoGuestHandler((uintptr_t)user, (uintptr_t)call_user,
                           (uint8_t *)data, len, timeout, emit, sink);
}
ext_status ext_go_guest_create(const uint8_t *data, size_t len,
                               uint32_t timeout, uintptr_t user,
                               ext_guest **out) {
  ext_guest_options options = {
      EXT_HOST_ABI_VERSION, sizeof(options), data, len, timeout,
      (void *)user,         handle};
  return ext_guest_create(&options, out);
}
ext_status ext_go_guest_emit(ext_guest_emit emit, void *sink, uint32_t kind,
                             uint8_t *data, size_t len) {
  return emit(sink, kind, data, len);
}
ext_status ext_go_guest_invoke(ext_guest *guest, const uint8_t *data,
                               size_t len, uint32_t timeout,
                               uintptr_t call_user, ext_buffer *out) {
  return ext_guest_invoke(guest, data, len, timeout, (void *)call_user, out);
}
