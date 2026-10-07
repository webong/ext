#ifndef CTX_PLUGIN_H
#define CTX_PLUGIN_H
#include <stdint.h>

#ifdef _WIN32
#define CTX_PLUGIN_EXPORT __declspec(dllexport)
#define CTX_PLUGIN_CALL __cdecl
#else
#define CTX_PLUGIN_EXPORT __attribute__((visibility("default")))
#define CTX_PLUGIN_CALL
#endif

#ifdef __cplusplus
extern "C" {
#endif

#define CTX_PLUGIN_ABI_VERSION 1u
#define CTX_PLUGIN_MAX_FRAME_BYTES (24u * 1024u * 1024u)
#define CTX_PLUGIN_HANDSHAKE 1u
#define CTX_PLUGIN_INVOKE 2u
#define CTX_PLUGIN_OK 0u
#define CTX_PLUGIN_INVALID 1u
#define CTX_PLUGIN_CLOSED 2u
#define CTX_PLUGIN_FAILED 3u

/* open creates an independent session; zero means failure. All functions use
 * the C calling convention. No function may let an exception cross this ABI.
 * Handshake must precede invoke. Requests/responses are UTF-8 JSON without a
 * trailing NUL or newline. Handshake input is {"deadline":"RFC3339 timestamp"};
 * output is a ext.plugin/v1 Descriptor. Invoke uses its Request and Response.
 * The caller owns all buffers, which are valid only for the duration of call.
 * The guest may neither retain nor free pointers, nor write beyond capacity.
 * Set *response_len to zero on failure; only OK permits reading the response.
 * The host supplies MAX_FRAME_BYTES capacity. Do not retry calls to resize a
 * buffer: an operation can already have performed effects. Domain errors use
 * Response.error with status OK; nonzero statuses are transport failures.
 * Each handle is serialized by the CTX host; different handles may run at once.
 * close releases session resources after outstanding calls return. Loaded code
 * stays resident. Cancellation does not interrupt arbitrary native execution.
 */
CTX_PLUGIN_EXPORT uint32_t CTX_PLUGIN_CALL ctx_plugin_abi_version(void);
CTX_PLUGIN_EXPORT uint64_t CTX_PLUGIN_CALL ctx_plugin_open(void);
CTX_PLUGIN_EXPORT uint32_t CTX_PLUGIN_CALL ctx_plugin_call(
    uint64_t handle, uint32_t operation,
    const uint8_t *request, uint32_t request_len,
    uint8_t *response, uint32_t response_capacity, uint32_t *response_len);
CTX_PLUGIN_EXPORT void CTX_PLUGIN_CALL ctx_plugin_close(uint64_t handle);

#ifdef __cplusplus
}
#endif
#endif
