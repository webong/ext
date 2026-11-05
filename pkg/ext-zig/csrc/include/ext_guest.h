#ifndef EXT_GUEST_H
#define EXT_GUEST_H
#include "ext_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ext_guest ext_guest;
#define EXT_GUEST_PAYLOAD 0u
#define EXT_GUEST_PUBLIC_ERROR 1u
/* Emit exactly one payload JSON value or public RemoteError object. Engine
 * copies bytes immediately. Returning nonzero sanitizes the failure and
 * discards any emitted result. Never retain emit/sink or borrowed input. */
typedef ext_status (*ext_guest_emit)(void *sink, uint32_t kind,
                                     const uint8_t *data, size_t len);
typedef struct {
  uint32_t abi_version;
  uint32_t struct_size;
  const uint8_t *descriptor;
  size_t descriptor_len;
  uint32_t max_call_ms; /* zero defaults to 30000 */
  void *user;
  ext_status (*handle)(void *user, void *call_user, const uint8_t *request,
                       size_t len, uint32_t remaining_ms, ext_guest_emit emit,
                       void *sink);
} ext_guest_options;
/* Snapshots descriptor and callbacks. Caller owns user and callback code until
 * destroy; no callback runs during create. Invocation may be concurrent. */
EXT_HOST_API ext_status ext_guest_create(const ext_guest_options *,
                                         ext_guest **);
EXT_HOST_API ext_status ext_guest_descriptor(ext_guest *, ext_buffer *);
/* timeout_ms independently bounds this call. Handler must cooperate; native
 * work cannot be forcibly interrupted. call_user is passed through unchanged
 * for binding-owned context/cancellation/tracing. It is borrowed for this call.
 */
EXT_HOST_API ext_status ext_guest_invoke(ext_guest *, const uint8_t *, size_t,
                                         uint32_t timeout_ms, void *call_user,
                                         ext_buffer *);
/* Exactly one owner, only after all invocations have joined. */
EXT_HOST_API void ext_guest_destroy(ext_guest *);
#ifdef __cplusplus
}
#endif
#endif
