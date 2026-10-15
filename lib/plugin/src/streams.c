#define _POSIX_C_SOURCE 200809L
#include "ext_stream.h"
#include "wire.h"
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

typedef struct stream_entry stream_entry;
struct stream_entry {
  stream_entry *next, *work_next;
  char id[49];
  uint8_t scope[256];
  size_t scope_len;
  int64_t expires;
  uint64_t sequence;
  ext_cancel *life;
  void *value;
  unsigned users;
  int opening, live, counted, closing, closed, busy, releasing;
  ext_status close_status;
};
struct ext_streams {
  pthread_mutex_t mu;
  pthread_cond_t changed;
  pthread_t timer, workers[4];
  size_t worker_count;
  stream_entry *work_head, *work_tail;
  ext_stream_options options;
  stream_entry *entries;
  size_t occupied;
  int closed;
  ext_status cleanup;
};
static int64_t now(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (int64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}
static int valid(const ext_call_options *o) {
  return o && o->struct_size == sizeof(*o) && o->timeout_ms;
}
static ext_status stopped(const ext_call_options *o, int64_t deadline) {
  return ext_cancel_is_signaled(o->cancel) ? EXT_CANCELED
         : now() >= deadline               ? EXT_TIMEOUT
                                           : EXT_OK;
}
static void wait_slice(ext_streams *s) {
  struct timespec t;
  clock_gettime(CLOCK_REALTIME, &t);
  t.tv_nsec += 10000000;
  if (t.tv_nsec >= 1000000000) {
    t.tv_sec++;
    t.tv_nsec -= 1000000000;
  }
  pthread_cond_timedwait(&s->changed, &s->mu, &t);
}
static int scope_valid(const uint8_t *p, size_t n) {
  return p && n && n <= 256;
}
static int id_valid(const char *id) { return id && *id && strlen(id) <= 64; }
static stream_entry *find(ext_streams *s, const char *id) {
  for (stream_entry *e = s->entries; e; e = e->next)
    if (e->live && !strcmp(e->id, id))
      return e;
  return NULL;
}
static int scoped(stream_entry *e, const uint8_t *p, size_t n) {
  return e && e->scope_len == n && !memcmp(e->scope, p, n);
}
/* Drop a use under lock. Release callbacks run outside the lock, with a
 * temporary entry use so destroy cannot race foreign reader destruction. */
static void drop(ext_streams *s, stream_entry *e) {
  if (--e->users || !e->closed)
    return;
  /* Keep the entry linked until release finishes: destroy remains busy. */
  e->users = 1;
  e->releasing = 1;
  pthread_mutex_unlock(&s->mu);
  if (e->value)
    s->options.release(s->options.user, e->value);
  pthread_mutex_lock(&s->mu);
  stream_entry **p = &s->entries;
  while (*p != e)
    p = &(*p)->next;
  *p = e->next;
  ext_cancel_destroy(e->life);
  free(e);
  pthread_cond_broadcast(&s->changed);
}
/* Retire immediately, independently of slow close/read callbacks. */
static int retire(ext_streams *s, stream_entry *e) {
  e->live = 0;
  if (e->counted) {
    e->counted = 0;
    s->occupied--;
  }
  ext_cancel_signal(e->life);
  if (!e->opening && !e->closing && !e->closed) {
    e->closing = 1;
    e->users++;
    return 1;
  }
  return 0;
}
static void close_entry(ext_streams *s, stream_entry *e) {
  ext_status status =
      e->value ? s->options.close(s->options.user, e->value) : EXT_OK;
  pthread_mutex_lock(&s->mu);
  e->close_status = status;
  e->closed = 1;
  e->closing = 0;
  if (!s->cleanup && status)
    s->cleanup = status;
  pthread_cond_broadcast(&s->changed);
  drop(s, e);
  pthread_mutex_unlock(&s->mu);
}
static void *cleanup_worker(void *arg) {
  ext_streams *s = arg;
  pthread_mutex_lock(&s->mu);
  for (;;) {
    while (!s->work_head && !s->closed)
      pthread_cond_wait(&s->changed, &s->mu);
    stream_entry *e = s->work_head;
    if (!e)
      break;
    s->work_head = e->work_next;
    if (!s->work_head)
      s->work_tail = NULL;
    pthread_mutex_unlock(&s->mu);
    close_entry(s, e);
    pthread_mutex_lock(&s->mu);
  }
  pthread_mutex_unlock(&s->mu);
  return NULL;
}
static void *expiry(void *arg) {
  ext_streams *s = arg;
  pthread_mutex_lock(&s->mu);
  while (!s->closed) {
    int64_t at = now();
    int queued = 0;
    for (stream_entry *e = s->entries; e; e = e->next) {
      if (e->counted && at >= e->expires && retire(s, e)) {
        if (s->work_tail)
          s->work_tail->work_next = e;
        else
          s->work_head = e;
        s->work_tail = e;
        queued = 1;
      }
    }
    if (queued)
      pthread_cond_broadcast(&s->changed);
    wait_slice(s);
  }
  pthread_mutex_unlock(&s->mu);
  return NULL;
}
ext_status ext_streams_create(const ext_stream_options *o, ext_streams **out) {
  if (!out)
    return EXT_INVALID;
  *out = NULL;
  if (!o || o->struct_size != sizeof(*o) || !o->capacity ||
      o->capacity > 1024 || !o->max_age_ms || o->max_age_ms > 86400000 ||
      !o->open || !o->read || !o->close || !o->release)
    return EXT_INVALID;
  ext_streams *s = calloc(1, sizeof(*s));
  if (!s)
    return EXT_NOMEM;
  s->options = *o;
  if (pthread_mutex_init(&s->mu, NULL)) {
    free(s);
    return EXT_IO;
  }
  if (pthread_cond_init(&s->changed, NULL)) {
    pthread_mutex_destroy(&s->mu);
    free(s);
    return EXT_IO;
  }
  int error = 0;
  for (size_t i = 0; i < 4; i++) {
    error = pthread_create(&s->workers[i], NULL, cleanup_worker, s);
    if (error)
      break;
    s->worker_count++;
  }
  if (!error)
    error = pthread_create(&s->timer, NULL, expiry, s);
  if (error) {
    pthread_mutex_lock(&s->mu);
    s->closed = 1;
    pthread_cond_broadcast(&s->changed);
    pthread_mutex_unlock(&s->mu);
    for (size_t i = 0; i < s->worker_count; i++)
      pthread_join(s->workers[i], NULL);
    pthread_cond_destroy(&s->changed);
    pthread_mutex_destroy(&s->mu);
    free(s);
    return EXT_IO;
  }
  *out = s;
  return EXT_OK;
}
static ext_status random_id(char out[49]) {
  int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
  if (fd < 0)
    return EXT_IO;
  uint8_t bytes[24];
  size_t used = 0;
  ext_status s = EXT_OK;
  while (used < sizeof(bytes)) {
    ssize_t n = read(fd, bytes + used, sizeof(bytes) - used);
    if (n > 0)
      used += (size_t)n;
    else if (!n || errno != EINTR) {
      s = EXT_IO;
      break;
    }
  }
  close(fd);
  if (s)
    return s;
  const char *hex = "0123456789abcdef";
  for (size_t i = 0; i < 24; i++) {
    out[2 * i] = hex[bytes[i] >> 4];
    out[2 * i + 1] = hex[bytes[i] & 15];
  }
  out[48] = 0;
  return EXT_OK;
}
ext_status ext_streams_open(ext_streams *s, const uint8_t *scope,
                            size_t scope_len, const uint8_t *params, size_t len,
                            const ext_call_options *o, ext_buffer *out) {
  if (!out)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  if (!s || !valid(o))
    return EXT_INVALID;
  if (!scope_valid(scope, scope_len))
    return EXT_DENIED;
  int64_t end = now() + o->timeout_ms;
  ext_status status = stopped(o, end);
  if (status)
    return status;
  yyjson_doc *doc = ext_parse(params, len);
  if (!doc)
    return EXT_INVALID;
  yyjson_doc_free(doc);
  stream_entry *e = calloc(1, sizeof(*e));
  if (!e)
    return EXT_NOMEM;
  status = ext_cancel_create(&e->life);
  if (status) {
    free(e);
    return status;
  }
  e->users = 1;
  e->opening = 1;
  e->counted = 1;
  e->expires = now() + s->options.max_age_ms;
  e->scope_len = scope_len;
  memcpy(e->scope, scope, scope_len);
  pthread_mutex_lock(&s->mu);
  if (s->closed)
    status = EXT_CLOSED;
  else if (s->occupied >= s->options.capacity)
    status = EXT_CAPACITY;
  if (status) {
    pthread_mutex_unlock(&s->mu);
    ext_cancel_destroy(e->life);
    free(e);
    return status;
  }
  s->occupied++;
  e->next = s->entries;
  s->entries = e;
  pthread_mutex_unlock(&s->mu);
  status = s->options.open(s->options.user, o, e->life, params, len, &e->value);
  if (!status)
    status = stopped(o, end);
  if (!status && ext_cancel_is_signaled(e->life))
    status = EXT_CANCELED;
  if (!status && now() >= e->expires)
    status = EXT_TIMEOUT;
  if (!status && !e->value)
    status = EXT_INVALID;
  if (!status)
    status = random_id(e->id);
  pthread_mutex_lock(&s->mu);
  e->opening = 0;
  if (!status && s->closed)
    status = EXT_CLOSED;
  if (!status && ext_cancel_is_signaled(e->life))
    status = EXT_CANCELED;
  if (!status) {
    /* Defend even against a random collision: do not replace an existing
     * stream. */
    if (find(s, e->id))
      status = EXT_AMBIGUOUS;
    else {
      out->data = malloc(51);
      if (!out->data)
        status = EXT_NOMEM;
      else {
        out->data[0] = '"';
        memcpy(out->data + 1, e->id, 48);
        out->data[49] = '"';
        out->data[50] = 0;
        out->len = 50;
        e->live = 1;
      }
    }
  }
  int close_now = status ? retire(s, e) : 0;
  pthread_mutex_unlock(&s->mu);
  if (close_now)
    close_entry(s, e);
  pthread_mutex_lock(&s->mu);
  drop(s, e);
  pthread_mutex_unlock(&s->mu);
  return status;
}
typedef struct {
  ext_buffer data;
  int emitted;
} batch_sink;
static ext_status emit(void *arg, const uint8_t *p, size_t n) {
  batch_sink *b = arg;
  if (b->emitted++ || !p || !n || n > EXT_HOST_MAX_FRAME)
    return EXT_INVALID;
  b->data.data = malloc(n);
  if (!b->data.data)
    return EXT_NOMEM;
  memcpy(b->data.data, p, n);
  b->data.len = n;
  return EXT_OK;
}
static ext_status batch(ext_buffer *raw, uint32_t limit, int *done) {
  yyjson_doc *d = ext_parse(raw->data, raw->len);
  if (!d)
    return EXT_INVALID;
  yyjson_val *v = yyjson_doc_get_root(d), *items = yyjson_obj_get(v, "items"),
             *end = yyjson_obj_get(v, "done");
  ext_status status = EXT_OK;
  if (!EXT_FIELDS(v, "items", "done") ||
      (!yyjson_is_arr(items) && items && !yyjson_is_null(items)) ||
      (end && !yyjson_is_bool(end) && !yyjson_is_null(end)) ||
      yyjson_arr_size(items) > limit)
    status = EXT_INVALID;
  if (!status) {
    size_t i, n, total = 0;
    yyjson_val *item;
    yyjson_arr_foreach(items, i, n, item) {
      size_t len = 0;
      char *json = yyjson_val_write(item, 0, &len);
      if (!json) {
        status = EXT_NOMEM;
        break;
      }
      free(json);
      total += len;
      if (total > 1048576) {
        status = EXT_INVALID;
        break;
      }
    }
  }
  if (!status) {
    *done = yyjson_get_bool(end);
    yyjson_mut_doc *out = yyjson_mut_doc_new(NULL);
    if (!out)
      status = EXT_NOMEM;
    else {
      yyjson_mut_val *root = yyjson_mut_obj(out),
                     *array = yyjson_is_arr(items)
                                  ? yyjson_val_mut_copy(out, items)
                                  : yyjson_mut_arr(out);
      yyjson_mut_doc_set_root(out, root);
      if (!root || !array ||
          !yyjson_mut_obj_add_val(out, root, "items", array) ||
          !yyjson_mut_obj_add_bool(out, root, "done", *done))
        status = EXT_NOMEM;
      else {
        ext_buffer_free(raw);
        raw->data = (uint8_t *)yyjson_mut_write(out, 0, &raw->len);
        if (!raw->data)
          status = EXT_NOMEM;
      }
      yyjson_mut_doc_free(out);
    }
  }
  yyjson_doc_free(d);
  return status;
}
ext_status ext_streams_read(ext_streams *s, const uint8_t *scope,
                            size_t scope_len, const char *id, uint64_t sequence,
                            uint32_t limit, const ext_call_options *o,
                            ext_buffer *out) {
  if (!out)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  if (!s || !valid(o) || !id_valid(id) || !sequence || !limit || limit > 256)
    return EXT_INVALID;
  if (!scope_valid(scope, scope_len))
    return EXT_DENIED;
  int64_t end = now() + o->timeout_ms;
  pthread_mutex_lock(&s->mu);
  stream_entry *e = find(s, id);
  if (!scoped(e, scope, scope_len)) {
    pthread_mutex_unlock(&s->mu);
    return EXT_DENIED;
  }
  e->users++;
  ext_status status = EXT_OK;
  while (e->busy && !ext_cancel_is_signaled(e->life) &&
         !(status = stopped(o, end)))
    wait_slice(s);
  if (!status)
    status = stopped(o, end);
  if (!status && ext_cancel_is_signaled(e->life))
    status = EXT_CANCELED;
  if (!status && (e->sequence == UINT64_MAX || sequence != e->sequence + 1))
    status = EXT_SEQUENCE;
  if (status) {
    drop(s, e);
    pthread_mutex_unlock(&s->mu);
    return status;
  }
  e->busy = 1;
  pthread_mutex_unlock(&s->mu);
  batch_sink sink = {0};
  ext_call_options bounded = *o;
  bounded.timeout_ms = (uint32_t)(end - now());
  if (!(status = stopped(o, end)))
    status = s->options.read(s->options.user, e->value, &bounded, e->life,
                             limit, emit, &sink);
  if (!status && sink.emitted != 1)
    status = EXT_INVALID;
  if (!status)
    status = stopped(o, end);
  if (!status && ext_cancel_is_signaled(e->life))
    status = EXT_CANCELED;
  int done = 0;
  if (!status)
    status = batch(&sink.data, limit, &done);
  pthread_mutex_lock(&s->mu);
  e->busy = 0;
  pthread_cond_broadcast(&s->changed);
  if (!status)
    e->sequence++;
  int close_now = (status || done) ? retire(s, e) : 0;
  pthread_mutex_unlock(&s->mu);
  if (close_now)
    close_entry(s, e);
  pthread_mutex_lock(&s->mu);
  if (done && !status) {
    while (e->closing)
      pthread_cond_wait(&s->changed, &s->mu);
    status = e->close_status;
  }
  drop(s, e);
  pthread_mutex_unlock(&s->mu);
  if (status)
    ext_buffer_free(&sink.data);
  else
    *out = sink.data;
  return status;
}
ext_status ext_streams_remove(ext_streams *s, const uint8_t *scope,
                              size_t scope_len, const char *id) {
  if (!s || !id_valid(id))
    return EXT_INVALID;
  if (!scope_valid(scope, scope_len))
    return EXT_DENIED;
  pthread_mutex_lock(&s->mu);
  stream_entry *e = find(s, id);
  if (!e) {
    pthread_mutex_unlock(&s->mu);
    return EXT_OK;
  }
  if (!scoped(e, scope, scope_len)) {
    pthread_mutex_unlock(&s->mu);
    return EXT_DENIED;
  }
  e->users++;
  int close_now = retire(s, e);
  pthread_mutex_unlock(&s->mu);
  if (close_now)
    close_entry(s, e);
  pthread_mutex_lock(&s->mu);
  while (e->closing)
    pthread_cond_wait(&s->changed, &s->mu);
  ext_status status = e->close_status;
  drop(s, e);
  pthread_mutex_unlock(&s->mu);
  return status;
}
ext_status ext_streams_close(ext_streams *s) {
  if (!s)
    return EXT_INVALID;
  pthread_mutex_lock(&s->mu);
  s->closed = 1;
  for (stream_entry *e = s->entries; e; e = e->next) {
    e->live = 0;
    if (e->counted) {
      e->counted = 0;
      s->occupied--;
    }
    ext_cancel_signal(e->life);
  }
  pthread_cond_broadcast(&s->changed);
  for (;;) {
    stream_entry *e;
    for (e = s->entries; e; e = e->next)
      if (!e->opening && !e->closing && !e->closed)
        break;
    if (e) {
      retire(s, e);
      pthread_mutex_unlock(&s->mu);
      close_entry(s, e);
      pthread_mutex_lock(&s->mu);
      continue;
    }
    int waiting = 0;
    for (e = s->entries; e; e = e->next)
      if (e->closing || e->releasing) {
        waiting = 1;
        break;
      }
    if (!waiting)
      break;
    pthread_cond_wait(&s->changed, &s->mu);
  }
  ext_status status = s->cleanup;
  pthread_mutex_unlock(&s->mu);
  return status;
}
ext_status ext_streams_destroy(ext_streams *s) {
  if (!s)
    return EXT_INVALID;
  pthread_mutex_lock(&s->mu);
  int busy = !s->closed || s->entries;
  pthread_mutex_unlock(&s->mu);
  if (busy)
    return EXT_DRAINING;
  pthread_join(s->timer, NULL);
  for (size_t i = 0; i < s->worker_count; i++)
    pthread_join(s->workers[i], NULL);
  pthread_cond_destroy(&s->changed);
  pthread_mutex_destroy(&s->mu);
  free(s);
  return EXT_OK;
}
