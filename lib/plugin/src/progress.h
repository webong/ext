#ifndef EXT_PROGRESS_H
#define EXT_PROGRESS_H
#include "wire.h"

/* Progress events of a call, shared by every side that carries them: the frame
 * validator, the token-bucket rate limit and the host-side sink. The reference
 * is plugin.Progress, plugin.Limiter and the host's callback sink in
 * pkg/plugin-go/contract (reverse.go). */

#define EXT_PROGRESS_TEXT_MAX 2048u
#define EXT_PROGRESS_DEFAULT_EVENTS 50
#define EXT_PROGRESS_DEFAULT_BYTES (64 * 1024)

int64_t ext_mono_ns(void);

/* A token bucket counted in events and bytes, with a burst of twice the
 * per-second rate. A rate that is not positive uses the default. It counts what
 * it drops. Not thread safe; one call owns one limiter. */
typedef struct {
  double events, bytes, event_rate, byte_rate;
  int64_t last_ns;
  uint64_t dropped;
} ext_limiter;
void ext_limiter_init(ext_limiter *, int64_t events_per_second,
                      int64_t bytes_per_second, int64_t now_ns);
/* Returns nonzero when an event whose text is bytes long may pass, and then
 * stores the number of events dropped since the previous passing one in
 * *dropped. Otherwise it counts the drop and returns zero. */
int ext_limiter_admit(ext_limiter *, size_t bytes, int64_t now_ns,
                      uint64_t *dropped);
/* Adds drops a peer already counted, so they are reported by the next event. */
void ext_limiter_carry(ext_limiter *, uint64_t);
/* Returns the drops not yet reported and resets the count. */
uint64_t ext_limiter_pending(ext_limiter *);

/* One validated event. text points into the frame it was read from. */
typedef struct {
  int log;    /* kind is "log", otherwise "progress" */
  int stream; /* 0 none, 1 out, 2 err */
  const char *text;
  size_t text_len;
  uint64_t completed, total, dropped;
} ext_progress_event;

/* Validates a progress frame and extracts the event, parent id included
 * (borrowed from frame). EXT_PROTOCOL for anything plugin.ProgressFrame would
 * not decode or validate. */
ext_status ext_progress_frame(yyjson_val *frame, ext_progress_event *event,
                              yyjson_val **parent);

/* The host's side of one call: applies the limits as a backstop to what the
 * guest sent and hands what passes to fn as a JSON Progress object. fn may be
 * NULL, in which case events are still counted and limited. */
typedef struct {
  ext_limiter limiter;
  ext_progress_fn fn;
  void *user;
} ext_progress_sink;
void ext_progress_sink_init(ext_progress_sink *, ext_progress_fn fn, void *user,
                            int64_t events_per_second,
                            int64_t bytes_per_second);
/* Passes one event on. A marker (an empty log event that only carries a count)
 * is folded into the count the next event or the final marker reports. Events
 * over the limit are dropped and counted: that is not an error. */
ext_status ext_progress_sink_deliver(ext_progress_sink *,
                                     const ext_progress_event *);
/* Delivers a final marker for events never reported. Call it once, when the
 * call ends, whatever its outcome. */
void ext_progress_sink_finish(ext_progress_sink *);
#endif
