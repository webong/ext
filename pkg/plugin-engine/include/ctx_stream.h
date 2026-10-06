#ifndef CTX_STREAM_H
#define CTX_STREAM_H
#include "ctx_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ctx_streams ctx_streams;
typedef struct {
  uint32_t struct_size, capacity, max_age_ms;
  void *user;
  /* open gets a separate lifetime signal. Call cancellation applies during
   * open only; a successful stream outlives that call. Value ownership
   * transfers even on failure. Read emits exactly one {items:[...],done:bool}
   * JSON value. close must unblock read and may run concurrently with it.
   * release runs after close and all reads have joined. No callback may unwind
   * or reenter. */
  ctx_status (*open)(void *, const ctx_call_options *, const ctx_cancel *,
                     const uint8_t *, size_t, void **);
  ctx_status (*read)(void *, void *, const ctx_call_options *,
                     const ctx_cancel *, uint32_t limit, ctx_emit, void *);
  ctx_status (*close)(void *, void *);
  void (*release)(void *, void *);
} ctx_stream_options;
CTX_HOST_API ctx_status ctx_streams_create(const ctx_stream_options *,
                                           ctx_streams **);
/* Scope is an authenticated subject supplied by the application, 1..256 bytes.
 * Never pass a caller-controlled claim without authorization. IDs are 24 random
 * bytes encoded as 48 lowercase hex characters. Open output is a JSON string.
 */
CTX_HOST_API ctx_status ctx_streams_open(ctx_streams *, const uint8_t *scope,
                                         size_t, const uint8_t *parameters,
                                         size_t, const ctx_call_options *,
                                         ctx_buffer *id);
CTX_HOST_API ctx_status ctx_streams_read(ctx_streams *, const uint8_t *scope,
                                         size_t, const char *id,
                                         uint64_t sequence, uint32_t limit,
                                         const ctx_call_options *,
                                         ctx_buffer *);
CTX_HOST_API ctx_status ctx_streams_remove(ctx_streams *, const uint8_t *scope,
                                           size_t, const char *id);
CTX_HOST_API ctx_status ctx_streams_close(ctx_streams *);
/* Requires close, all calls joined, no retained readers. CTX_DRAINING means
 * asynchronous expiry cleanup is still in progress; retry after it completes.
 */
CTX_HOST_API ctx_status ctx_streams_destroy(ctx_streams *);
#ifdef __cplusplus
}
#endif
#endif
