#ifndef EXT_STREAM_H
#define EXT_STREAM_H
#include "ext_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ext_streams ext_streams;
typedef struct {
  uint32_t struct_size, capacity, max_age_ms;
  void *user;
  /* open gets a separate lifetime signal. Call cancellation applies during
   * open only; a successful stream outlives that call. Value ownership
   * transfers even on failure. Read emits exactly one {items:[...],done:bool}
   * JSON value. close must unblock read and may run concurrently with it.
   * release runs after close and all reads have joined. No callback may unwind
   * or reenter. */
  ext_status (*open)(void *, const ext_call_options *, const ext_cancel *,
                     const uint8_t *, size_t, void **);
  ext_status (*read)(void *, void *, const ext_call_options *,
                     const ext_cancel *, uint32_t limit, ext_emit, void *);
  ext_status (*close)(void *, void *);
  void (*release)(void *, void *);
} ext_stream_options;
EXT_HOST_API ext_status ext_streams_create(const ext_stream_options *,
                                           ext_streams **);
/* Scope is an authenticated subject supplied by the application, 1..256 bytes.
 * Never pass a caller-controlled claim without authorization. IDs are 24 random
 * bytes encoded as 48 lowercase hex characters. Open output is a JSON string.
 */
EXT_HOST_API ext_status ext_streams_open(ext_streams *, const uint8_t *scope,
                                         size_t, const uint8_t *parameters,
                                         size_t, const ext_call_options *,
                                         ext_buffer *id);
EXT_HOST_API ext_status ext_streams_read(ext_streams *, const uint8_t *scope,
                                         size_t, const char *id,
                                         uint64_t sequence, uint32_t limit,
                                         const ext_call_options *,
                                         ext_buffer *);
EXT_HOST_API ext_status ext_streams_remove(ext_streams *, const uint8_t *scope,
                                           size_t, const char *id);
EXT_HOST_API ext_status ext_streams_close(ext_streams *);
/* Requires close, all calls joined, no retained readers. EXT_DRAINING means
 * asynchronous expiry cleanup is still in progress; retry after it completes.
 */
EXT_HOST_API ext_status ext_streams_destroy(ext_streams *);
#ifdef __cplusplus
}
#endif
#endif
