#ifndef EXT_PLUGIN_AUTHOR_H
#define EXT_PLUGIN_AUTHOR_H
#include "ext_plugin_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ext_plugin_guest ext_plugin_guest;
#define EXT_PLUGIN_GUEST_PAYLOAD 0u
#define EXT_PLUGIN_GUEST_PUBLIC_ERROR 1u
/* Emit exactly one payload JSON value or public RemoteError object. Engine
 * copies bytes immediately. Returning nonzero sanitizes the failure and
 * discards any emitted result. Never retain emit/sink or borrowed input. */
typedef ext_plugin_status (*ext_plugin_guest_emit)(void *sink, uint32_t kind,
                                     const uint8_t *data, size_t len);
typedef struct {
  uint32_t abi_version;
  uint32_t struct_size;
  const uint8_t *descriptor;
  size_t descriptor_len;
  uint32_t max_call_ms; /* zero defaults to 30000 */
  void *user;
  ext_plugin_status (*handle)(void *user, void *call_user, const uint8_t *request,
                       size_t len, uint32_t remaining_ms, ext_plugin_guest_emit emit,
                       void *sink);
} ext_plugin_guest_options;
/* Snapshots descriptor and callbacks. Caller owns user and callback code until
 * destroy; no callback runs during create. Invocation may be concurrent. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_guest_create(const ext_plugin_guest_options *,
                                         ext_plugin_guest **);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_guest_descriptor(ext_plugin_guest *, ext_plugin_buffer *);
/* timeout_ms independently bounds this call. Handler must cooperate; native
 * work cannot be forcibly interrupted. call_user is passed through unchanged
 * for binding-owned context/cancellation/tracing. It is borrowed for this call.
 */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_guest_invoke(ext_plugin_guest *, const uint8_t *, size_t,
                                         uint32_t timeout_ms, void *call_user,
                                         ext_plugin_buffer *);
/* Exactly one owner, only after all invocations have joined. */
EXT_PLUGIN_HOST_API void ext_plugin_guest_destroy(ext_plugin_guest *);
#ifdef __cplusplus
}
#endif
#endif
