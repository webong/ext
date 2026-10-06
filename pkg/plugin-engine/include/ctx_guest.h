#ifndef CTX_GUEST_H
#define CTX_GUEST_H
#include "ctx_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ctx_guest ctx_guest;
#define CTX_GUEST_PAYLOAD 0u
#define CTX_GUEST_PUBLIC_ERROR 1u
/* Emit exactly one payload JSON value or public RemoteError object. Engine
 * copies bytes immediately. Returning nonzero sanitizes the failure and
 * discards any emitted result. Never retain emit/sink or borrowed input. */
typedef ctx_status (*ctx_guest_emit)(void *sink, uint32_t kind,
                                     const uint8_t *data, size_t len);
typedef struct {
  uint32_t abi_version;
  uint32_t struct_size;
  const uint8_t *descriptor;
  size_t descriptor_len;
  uint32_t max_call_ms; /* zero defaults to 30000 */
  void *user;
  ctx_status (*handle)(void *user, void *call_user, const uint8_t *request,
                       size_t len, uint32_t remaining_ms, ctx_guest_emit emit,
                       void *sink);
} ctx_guest_options;
/* Snapshots descriptor and callbacks. Caller owns user and callback code until
 * destroy; no callback runs during create. Invocation may be concurrent. */
CTX_HOST_API ctx_status ctx_guest_create(const ctx_guest_options *,
                                         ctx_guest **);
CTX_HOST_API ctx_status ctx_guest_descriptor(ctx_guest *, ctx_buffer *);
/* timeout_ms independently bounds this call. Handler must cooperate; native
 * work cannot be forcibly interrupted. call_user is passed through unchanged
 * for binding-owned context/cancellation/tracing. It is borrowed for this call.
 */
CTX_HOST_API ctx_status ctx_guest_invoke(ctx_guest *, const uint8_t *, size_t,
                                         uint32_t timeout_ms, void *call_user,
                                         ctx_buffer *);
/* Exactly one owner, only after all invocations have joined. */
CTX_HOST_API void ctx_guest_destroy(ctx_guest *);
#ifdef __cplusplus
}
#endif
#endif
