#ifndef EXT_REVERSE_H
#define EXT_REVERSE_H
#include "wire.h"

/* Reverse calls: a plugin calling an operation its host offers. The reference is
 * ReverseRequestFrame, ReverseResponseFrame and Session.runReverse in
 * pkg/ext-go/engine (reverse.go, reverse_session.go). */

#define EXT_REVERSE_DEFAULT_PAYLOAD (1u << 20)
#define EXT_REVERSE_DEFAULT_IN_FLIGHT 4u
#define EXT_REVERSE_MAX_IN_FLIGHT 1024u
#define EXT_REVERSE_DEFAULT_TIMEOUT_MS 30000u

/* A validated reverse_request frame. The values point into the frame. parent
 * and payload are NULL when absent; an absent or empty parent means the call is
 * session scoped. */
typedef struct {
  yyjson_val *id, *parent, *contract, *operation, *payload, *deadline;
  int64_t deadline_ms;
} ext_reverse_request;

/* Validates a reverse_request frame exactly as ReverseRequestFrame decodes and
 * validates it. EXT_PROTOCOL for anything else. */
ext_status ext_reverse_request_frame(yyjson_val *frame,
                                     ext_reverse_request *request);

/* The stable codes a reverse call is refused with. A refusal is an answer, never
 * the end of the session. */
#define EXT_REVERSE_UNDECLARED "reverse_undeclared"
#define EXT_REVERSE_UNAVAILABLE "reverse_unavailable"
#define EXT_REVERSE_DENIED "denied"
#define EXT_REVERSE_TOO_LARGE "too_large"
#define EXT_REVERSE_BUSY "busy"
#define EXT_REVERSE_EXPIRED "expired"

/* What a reverse handler produced: kind is EXT_HOST_REVERSE_PAYLOAD or
 * EXT_HOST_REVERSE_PUBLIC_ERROR (ext_host.h) and data its JSON. The functions
 * below return the reverse_response line (without the newline) in memory the
 * caller frees, or NULL when memory ran out. id is the request's id. */
char *ext_reverse_refusal(const char *id, size_t id_len, const char *code,
                          size_t *len);
/* The generic failure of a handler that failed, or whose answer could not be
 * used. */
char *ext_reverse_failure(const char *id, size_t id_len, size_t *len);
/* The answer to a handler that returned: its payload (empty means null), or its
 * public error if a peer would accept it. An answer the peer cannot take
 * becomes a failure, a payload over max_payload or a frame over the frame limit
 * becomes a too_large refusal. */
char *ext_reverse_answer(const char *id, size_t id_len, uint32_t kind,
                         const uint8_t *data, size_t n, size_t max_payload,
                         size_t *len);
/* Makes c read as signaled whenever parent is, too. Call it before anything polls
 * c; parent must outlive that use. */
void ext_cancel_link(ext_cancel *c, const ext_cancel *parent);
#endif
