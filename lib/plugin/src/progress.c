#define _POSIX_C_SOURCE 200809L
#include "progress.h"
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include "sync.h"

int64_t ext_mono_ns(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (int64_t)t.tv_sec * 1000000000 + t.tv_nsec;
}

static double smaller(double a, double b) { return a < b ? a : b; }
void ext_limiter_init(ext_limiter *l, int64_t events_per_second,
                      int64_t bytes_per_second, int64_t now_ns) {
  double events = events_per_second > 0 ? (double)events_per_second
                                        : EXT_PROGRESS_DEFAULT_EVENTS,
         bytes = bytes_per_second > 0 ? (double)bytes_per_second
                                      : EXT_PROGRESS_DEFAULT_BYTES;
  *l = (ext_limiter){2 * events, 2 * bytes, events, bytes, now_ns, 0};
}
int ext_limiter_admit(ext_limiter *l, size_t bytes, int64_t now_ns,
                      uint64_t *dropped) {
  double elapsed = (double)(now_ns - l->last_ns) / 1e9;
  if (elapsed > 0) {
    l->events = smaller(l->events + elapsed * l->event_rate, 2 * l->event_rate);
    l->bytes = smaller(l->bytes + elapsed * l->byte_rate, 2 * l->byte_rate);
    l->last_ns = now_ns;
  }
  if (l->events < 1 || l->bytes < (double)bytes) {
    l->dropped++;
    *dropped = 0;
    return 0;
  }
  l->events--;
  l->bytes -= (double)bytes;
  *dropped = l->dropped;
  l->dropped = 0;
  return 1;
}
void ext_limiter_carry(ext_limiter *l, uint64_t n) { l->dropped += n; }
uint64_t ext_limiter_pending(ext_limiter *l) {
  uint64_t n = l->dropped;
  l->dropped = 0;
  return n;
}

/* A field Go decodes into a string: absent and null are empty, anything else
 * that is not a string is a decoding error. */
static int string_field(yyjson_val *v, const char **s, size_t *n) {
  *s = "";
  *n = 0;
  if (!v || yyjson_is_null(v))
    return 1;
  if (!yyjson_is_str(v))
    return 0;
  *s = yyjson_get_str(v);
  *n = yyjson_get_len(v);
  return 1;
}
/* A field Go decodes into a uint64: absent and null are zero, otherwise the
 * literal must be plain digits that fit (no sign, fraction or exponent). The
 * parser keeps numbers as raw text. */
static int uint64_field(yyjson_val *v, uint64_t *out) {
  *out = 0;
  if (!v || yyjson_is_null(v))
    return 1;
  if (!yyjson_is_raw(v))
    return 0;
  const char *s = yyjson_get_raw(v);
  size_t n = yyjson_get_len(v);
  uint64_t x = 0;
  if (!s || !n)
    return 0;
  for (size_t i = 0; i < n; i++) {
    uint64_t digit = (uint64_t)(s[i] - '0');
    if (s[i] < '0' || s[i] > '9' || x > (UINT64_MAX - digit) / 10)
      return 0;
    x = x * 10 + digit;
  }
  *out = x;
  return 1;
}

ext_status ext_progress_frame(yyjson_val *frame, ext_progress_event *event,
                              yyjson_val **parent) {
  const char *kind, *stream;
  size_t kind_len, stream_len;
  yyjson_val *id = yyjson_is_obj(frame) ? yyjson_obj_get(frame, "parent") : NULL;
  if (!EXT_FIELDS(frame, "frame", "apiVersion", "parent", "kind", "stream",
                  "text", "completed", "total", "dropped") ||
      !yyjson_equals_str(yyjson_obj_get(frame, "frame"), "progress") ||
      !yyjson_equals_str(yyjson_obj_get(frame, "apiVersion"), "ext.plugin/v1") ||
      !ext_text(id, 1, 0) ||
      !string_field(yyjson_obj_get(frame, "kind"), &kind, &kind_len) ||
      !string_field(yyjson_obj_get(frame, "stream"), &stream, &stream_len) ||
      !string_field(yyjson_obj_get(frame, "text"), &event->text,
                    &event->text_len) ||
      !uint64_field(yyjson_obj_get(frame, "completed"), &event->completed) ||
      !uint64_field(yyjson_obj_get(frame, "total"), &event->total) ||
      !uint64_field(yyjson_obj_get(frame, "dropped"), &event->dropped))
    return EXT_PROTOCOL;
  int is_log = kind_len == 3 && !memcmp(kind, "log", 3),
      is_progress = kind_len == 8 && !memcmp(kind, "progress", 8);
  event->log = is_log;
  event->stream = !stream_len                              ? 0
                  : stream_len == 3 && !memcmp(stream, "out", 3) ? 1
                  : stream_len == 3 && !memcmp(stream, "err", 3) ? 2
                                                                 : -1;
  if ((!is_log && !is_progress) || event->stream < 0 ||
      (event->stream && !is_log) || event->text_len > EXT_PROGRESS_TEXT_MAX ||
      (event->total && event->completed > event->total))
    return EXT_PROTOCOL;
  *parent = id;
  return EXT_OK;
}

void ext_progress_sink_init(ext_progress_sink *s, ext_progress_fn fn,
                            void *user, int64_t events_per_second,
                            int64_t bytes_per_second) {
  ext_limiter_init(&s->limiter, events_per_second, bytes_per_second,
                   ext_mono_ns());
  s->fn = fn;
  s->user = user;
}

/* Hands one event to fn as the JSON of plugin.Progress: zero fields are left
 * out, as Go's omitempty does. */
static ext_status emit_event(ext_progress_sink *s, const ext_progress_event *e,
                             uint64_t dropped) {
  if (!s->fn)
    return EXT_OK;
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  if (!doc)
    return EXT_NOMEM;
  yyjson_mut_val *root = yyjson_mut_obj(doc);
  yyjson_mut_doc_set_root(doc, root);
  int ok = root && yyjson_mut_obj_add_str(doc, root, "kind",
                                          e->log ? "log" : "progress");
  if (ok && e->stream)
    ok = yyjson_mut_obj_add_str(doc, root, "stream",
                                e->stream == 1 ? "out" : "err");
  if (ok && e->text_len)
    ok = yyjson_mut_obj_add_strncpy(doc, root, "text", e->text, e->text_len);
  if (ok && e->completed)
    ok = yyjson_mut_obj_add_uint(doc, root, "completed", e->completed);
  if (ok && e->total)
    ok = yyjson_mut_obj_add_uint(doc, root, "total", e->total);
  if (ok && dropped)
    ok = yyjson_mut_obj_add_uint(doc, root, "dropped", dropped);
  size_t len = 0;
  char *json = ok ? yyjson_mut_write(doc, 0, &len) : NULL;
  yyjson_mut_doc_free(doc);
  if (!json)
    return EXT_NOMEM;
  s->fn(s->user, (const uint8_t *)json, len);
  free(json);
  return EXT_OK;
}

ext_status ext_progress_sink_deliver(ext_progress_sink *s,
                                     const ext_progress_event *e) {
  /* A marker carries only a count. It is folded into the total reported by the
   * next event or the final marker, and is not itself rate-limited. */
  if (e->log && !e->text_len && !e->stream && e->dropped) {
    ext_limiter_carry(&s->limiter, e->dropped);
    return EXT_OK;
  }
  uint64_t dropped = 0;
  if (!ext_limiter_admit(&s->limiter, e->text_len, ext_mono_ns(), &dropped)) {
    /* The dropped event's own count must not vanish with it. */
    ext_limiter_carry(&s->limiter, e->dropped);
    return EXT_OK;
  }
  return emit_event(s, e, e->dropped + dropped);
}

void ext_progress_sink_finish(ext_progress_sink *s) {
  uint64_t n = ext_limiter_pending(&s->limiter);
  if (!n)
    return;
  ext_progress_event marker = {1, 0, "", 0, 0, 0, 0};
  (void)emit_event(s, &marker, n);
}
