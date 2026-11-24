/* Host sessions behind the library interface. A host is opened from JSON, and every callback the
 * typed API takes (the verify and authorize policies, the observer, progress, and the reverse-call
 * hooks) is the interface's own: it turns the callback into an event in the host's queue. The
 * binding takes events with the "events" operation and answers those that ask with "answer", from
 * another thread than the one waiting in "start" or "call", so a binding needs no way to be called
 * from C (purego has none it can rely on). An event that asks and is not answered in time is a
 * denial; progress and observer events never wait, and are dropped (and counted) when the queue is
 * full. Every queued event carries seq, counting from 1, and "sequence" is the seq of the last one
 * queued, so a binding that has handled every event up to it has seen all a finished call caused. */
#include "interface_host.h"
#include "progress.h"
#include "sync.h"
#include "wire.h"
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct event {
  struct event *next;
  char *json; /* {"seq","ticket","kind","data"} */
  size_t len;
} event;

typedef struct waiter {
  struct waiter *next;
  uint64_t ticket;
  int done, allow, is_error;
  char *payload; /* the answer's payload or error, JSON */
  size_t payload_len;
} waiter;

struct ext_interface_host {
  ext_plugin_host *host;
  pthread_mutex_t m;
  pthread_cond_t c;
  event *head, *tail;
  size_t queued, dropped;
  waiter *waiters;
  uint64_t next_ticket, seq; /* seq: the last event queued */
  int verify_event, authorize_event, closing;
  uint32_t event_timeout_ms;
};

enum { MAX_QUEUE = 1024 };

static int64_t mono_ms(void) { return ext_mono_ns() / 1000000; }

/* wait waits on the host's condition for at most ms; the caller rechecks its own deadline. */
static void wait_for(ext_interface_host *h, int64_t ms) {
  if (ms <= 0)
    return;
  struct timespec t;
#ifdef __APPLE__
  t.tv_sec = (time_t)(ms / 1000);
  t.tv_nsec = (long)(ms % 1000) * 1000000;
  pthread_cond_timedwait_relative_np(&h->c, &h->m, &t);
#else
  clock_gettime(CLOCK_REALTIME, &t);
  int64_t ns = (int64_t)t.tv_nsec + (ms % 1000) * 1000000;
  t.tv_sec += (time_t)(ms / 1000 + ns / 1000000000);
  t.tv_nsec = (long)(ns % 1000000000);
  pthread_cond_timedwait(&h->c, &h->m, &t);
#endif
}

/* push queues an event; ticket 0 is one that asks nothing and may be dropped. tag, when not NULL,
 * names the call the event belongs to (checked by tag_ok). Called locked. */
static int push(ext_interface_host *h, uint64_t ticket, const char *kind, const char *tag, const uint8_t *data,
                size_t len) {
  if (h->queued >= MAX_QUEUE) {
    h->dropped++;
    return 0;
  }
  size_t n = len + strlen(kind) + (tag ? strlen(tag) + 10 : 0) + 64;
  event *e = malloc(sizeof *e);
  char *json = malloc(n);
  if (!e || !json) {
    free(e);
    free(json);
    h->dropped++;
    return 0;
  }
  int w = snprintf(json, n, "{\"seq\":%llu,\"ticket\":%llu,\"kind\":\"%s\"%s%s%s,\"data\":%.*s}",
                   (unsigned long long)(h->seq + 1), (unsigned long long)ticket, kind, tag ? ",\"tag\":\"" : "",
                   tag ? tag : "", tag ? "\"" : "", len ? (int)len : 4, len ? (const char *)data : "null");
  h->seq++;
  e->json = json, e->len = (size_t)w, e->next = NULL;
  if (h->tail)
    h->tail->next = e;
  else
    h->head = e;
  h->tail = e;
  h->queued++;
  pthread_cond_broadcast(&h->c);
  return 1;
}

static void notify_tagged(ext_interface_host *h, const char *kind, const char *tag, const uint8_t *data,
                          size_t len) {
  pthread_mutex_lock(&h->m);
  if (!h->closing)
    push(h, 0, kind, tag, data, len);
  pthread_mutex_unlock(&h->m);
}
static void notify(ext_interface_host *h, const char *kind, const uint8_t *data, size_t len) {
  notify_tagged(h, kind, NULL, data, len);
}

/* A call's tag is at most 64 of [A-Za-z0-9._:-], so it goes into an event as it is. */
static int tag_ok(const char *t) {
  size_t n = strlen(t);
  if (!n || n > 64)
    return 0;
  for (size_t i = 0; i < n; i++)
    if (!((t[i] >= 'a' && t[i] <= 'z') || (t[i] >= 'A' && t[i] <= 'Z') || (t[i] >= '0' && t[i] <= '9') ||
          t[i] == '.' || t[i] == '_' || t[i] == ':' || t[i] == '-'))
      return 0;
  return 1;
}

/* ask queues an event and waits for its answer, at most timeout_ms (0: the host's event timeout).
 * It returns 1 when answered, with the waiter filled in; 0 when not answered in time or closing. */
static int ask(ext_interface_host *h, const char *kind, const uint8_t *data, size_t len, uint32_t timeout_ms,
               waiter *out) {
  int64_t limit = h->event_timeout_ms;
  if (timeout_ms && timeout_ms < limit)
    limit = timeout_ms;
  int64_t deadline = mono_ms() + limit;
  memset(out, 0, sizeof *out);
  pthread_mutex_lock(&h->m);
  if (h->closing) {
    pthread_mutex_unlock(&h->m);
    return 0;
  }
  out->ticket = ++h->next_ticket;
  if (!push(h, out->ticket, kind, NULL, data, len)) {
    pthread_mutex_unlock(&h->m);
    return 0;
  }
  out->next = h->waiters;
  h->waiters = out;
  while (!out->done && !h->closing) {
    int64_t left = deadline - mono_ms();
    if (left <= 0)
      break;
    wait_for(h, left > 50 ? 50 : left);
  }
  for (waiter **p = &h->waiters; *p; p = &(*p)->next)
    if (*p == out) {
      *p = out->next;
      break;
    }
  int answered = out->done;
  pthread_mutex_unlock(&h->m);
  return answered;
}

/* ---- the callbacks the engine calls ---- */

static int32_t policy(ext_interface_host *h, int as_event, const char *kind, const uint8_t *json, size_t len) {
  if (!as_event)
    return 0;
  waiter w;
  int answered = ask(h, kind, json, len, 0, &w);
  free(w.payload);
  return answered && w.allow ? 0 : 1;
}
static int32_t verify_cb(void *user, const uint8_t *json, size_t len) {
  ext_interface_host *h = user;
  return policy(h, h->verify_event, "verify", json, len);
}
static int32_t authorize_cb(void *user, const uint8_t *json, size_t len) {
  ext_interface_host *h = user;
  return policy(h, h->authorize_event, "authorize", json, len);
}
static void observe_cb(void *user, const ext_plugin_call_options *o, const uint8_t *json, size_t len) {
  (void)o;
  notify(user, "observe", json, len);
}
/* The progress of one call, with the call's tag. */
typedef struct {
  ext_interface_host *h;
  const char *tag;
} call_progress;
static void progress_cb(void *user, const uint8_t *json, size_t len) {
  call_progress *c = user;
  notify_tagged(c->h, "progress", c->tag, json, len);
}
static int32_t reverse_authorize_cb(void *user, const ext_plugin_call_options *o, const uint8_t *json, size_t len) {
  waiter w;
  int answered = ask(user, "reverse_authorize", json, len, o ? o->timeout_ms : 0, &w);
  free(w.payload);
  return answered && w.allow ? 0 : 1;
}
static ext_plugin_status reverse_cb(void *user, const ext_plugin_call_options *o, const uint8_t *request,
                                    size_t len, ext_plugin_reverse_emit emit, void *sink) {
  waiter w;
  if (!ask(user, "reverse", request, len, o ? o->timeout_ms : 0, &w)) {
    free(w.payload);
    return EXT_PLUGIN_TIMEOUT;
  }
  ext_plugin_status s =
      emit(sink, w.is_error ? EXT_PLUGIN_HOST_REVERSE_PUBLIC_ERROR : EXT_PLUGIN_HOST_REVERSE_PAYLOAD,
           (const uint8_t *)w.payload, w.payload_len);
  free(w.payload);
  return s;
}

/* ---- opening ---- */

static char *written(yyjson_val *v, size_t *n) { return v ? yyjson_val_write(v, 0, n) : NULL; }

static int strings_of(yyjson_val *array, const char ***out, size_t *count) {
  *out = NULL, *count = 0;
  if (!array)
    return 1;
  if (!yyjson_is_arr(array))
    return 0;
  size_t n = yyjson_arr_size(array);
  if (!n)
    return 1;
  const char **list = calloc(n, sizeof *list);
  if (!list)
    return 0;
  for (size_t i = 0; i < n; i++)
    if (!(list[i] = yyjson_get_str(yyjson_arr_get(array, i)))) {
      free(list);
      return 0;
    }
  *out = list, *count = n;
  return 1;
}

static int mode_of(yyjson_val *config, const char *key, int *as_event) {
  const char *m = yyjson_get_str(yyjson_obj_get(config, key));
  if (!m)
    return 0;
  if (!strcmp(m, "allow"))
    *as_event = 0;
  else if (!strcmp(m, "event"))
    *as_event = 1;
  else
    return 0;
  return 1;
}

static int uint32_of(yyjson_val *o, const char *key, uint32_t *out) {
  yyjson_val *v = yyjson_obj_get(o, key);
  if (!v)
    return 1;
  const char *p = yyjson_get_raw(v);
  size_t n = yyjson_get_len(v);
  uint64_t x = 0;
  if (!yyjson_is_raw(v) || !p || !n)
    return 0;
  for (size_t i = 0; i < n; i++) {
    if (p[i] < '0' || p[i] > '9' || x > 0xffffffffu)
      return 0;
    x = x * 10 + (uint64_t)(p[i] - '0');
  }
  if (x > 0xffffffffu)
    return 0;
  *out = (uint32_t)x;
  return 1;
}

ext_plugin_status ext_interface_host_open(yyjson_val *config, ext_interface_host **out) {
  *out = NULL;
  ext_interface_host *h = calloc(1, sizeof *h);
  if (!h)
    return EXT_PLUGIN_NOMEM;
  h->event_timeout_ms = 30000;
  pthread_mutex_init(&h->m, NULL);
  pthread_cond_init(&h->c, NULL);
  ext_plugin_status s = EXT_PLUGIN_INVALID;
  char *descriptor = NULL;
  size_t descriptor_len = 0;
  const char **arguments = NULL, **environment = NULL;
  size_t argument_count = 0, environment_count = 0;
  yyjson_val *backend = yyjson_obj_get(config, "backend");
  const char *kind = yyjson_get_str(yyjson_obj_get(backend, "kind"));
  if (!mode_of(config, "verify", &h->verify_event) || !mode_of(config, "authorize", &h->authorize_event) ||
      !uint32_of(config, "event_timeout_ms", &h->event_timeout_ms) || !kind ||
      !(descriptor = written(yyjson_obj_get(config, "descriptor"), &descriptor_len)))
    goto fail;
  if (!h->event_timeout_ms)
    h->event_timeout_ms = 30000;
  yyjson_val *requirements = yyjson_obj_get(config, "requirements");
  if (requirements) {
    size_t rn = 0;
    char *r = written(requirements, &rn);
    char *check = r ? malloc(descriptor_len + rn + 48) : NULL;
    if (!check) {
      free(r);
      s = EXT_PLUGIN_NOMEM;
      goto fail;
    }
    int w = snprintf(check, descriptor_len + rn + 48, "{\"descriptor\":%.*s,\"requirements\":%.*s}",
                     (int)descriptor_len, descriptor, (int)rn, r);
    ext_plugin_buffer b = {0};
    s = ext_plugin_services_call("descriptor.requirements", (const uint8_t *)check, (size_t)w, &b);
    ext_plugin_buffer_free(&b);
    free(check);
    free(r);
    if (s)
      goto fail;
    s = EXT_PLUGIN_INVALID;
  }
  ext_plugin_host_options options = {EXT_PLUGIN_HOST_ABI_VERSION, sizeof options, (const uint8_t *)descriptor,
                                     descriptor_len, verify_cb, authorize_cb, h};
  if (!strcmp(kind, "process")) {
    ext_plugin_jsonline_process_launch launch = {sizeof launch, yyjson_get_str(yyjson_obj_get(backend, "executable")),
                                                 NULL, 0, NULL, 0,
                                                 yyjson_get_str(yyjson_obj_get(backend, "working_directory")), 0};
    if (!launch.executable || !strings_of(yyjson_obj_get(backend, "arguments"), &arguments, &argument_count) ||
        !strings_of(yyjson_obj_get(backend, "environment"), &environment, &environment_count) ||
        !uint32_of(backend, "stderr_bytes", &launch.stderr_bytes))
      goto fail;
    launch.arguments = arguments, launch.argument_count = argument_count;
    launch.environment = environment, launch.environment_count = environment_count;
    ext_plugin_backend_options b = {sizeof b, EXT_PLUGIN_BACKEND_JSONLINE_PROCESS_LAUNCH, &launch, sizeof launch};
    s = ext_plugin_host_create(&options, &b, &h->host);
  } else if (!strcmp(kind, "shared_library")) {
    ext_plugin_shared_library_options lo = {sizeof lo, yyjson_get_str(yyjson_obj_get(backend, "path"))};
    ext_plugin_backend_extension_context ext;
    if (!lo.path)
      goto fail;
    if ((s = ext_plugin_shared_library_backend_create(&lo, &ext)))
      goto fail;
    ext_plugin_backend_options b = {sizeof b, EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT, &ext, sizeof ext};
    if ((s = ext_plugin_host_create(&options, &b, &h->host)))
      ext.release(ext.user);
  } else {
    goto fail;
  }
  if (s)
    goto fail;
  if (yyjson_get_bool(yyjson_obj_get(config, "observe"))) {
    ext_plugin_host_hooks hooks = {sizeof hooks, h, NULL, NULL, observe_cb};
    if ((s = ext_plugin_host_set_hooks(h->host, &hooks)))
      goto fail;
  }
  yyjson_val *services = yyjson_obj_get(config, "services");
  if (services) {
    ext_plugin_host_services sv = {sizeof sv, h, reverse_authorize_cb, reverse_cb, 0, 0, 0, NULL};
    if (!yyjson_is_obj(services) || !uint32_of(services, "timeout_ms", &sv.timeout_ms) ||
        !uint32_of(services, "max_payload_bytes", &sv.max_payload_bytes) ||
        !uint32_of(services, "max_in_flight", &sv.max_in_flight)) {
      s = EXT_PLUGIN_INVALID;
      goto fail;
    }
    if ((s = ext_plugin_host_set_services(h->host, &sv)))
      goto fail;
  }
  free(descriptor);
  free(arguments);
  free(environment);
  *out = h;
  return EXT_PLUGIN_OK;
fail:
  free(descriptor);
  free(arguments);
  free(environment);
  if (h->host)
    ext_plugin_host_destroy(h->host);
  pthread_cond_destroy(&h->c);
  pthread_mutex_destroy(&h->m);
  free(h);
  return s;
}

void ext_interface_host_shutdown(ext_interface_host *h) {
  pthread_mutex_lock(&h->m);
  h->closing = 1;
  pthread_cond_broadcast(&h->c);
  pthread_mutex_unlock(&h->m);
  ext_plugin_host_close(h->host);
}

void ext_interface_host_free(ext_interface_host *h) {
  ext_plugin_host_destroy(h->host);
  for (event *e = h->head, *next; e; e = next) {
    next = e->next;
    free(e->json);
    free(e);
  }
  pthread_cond_destroy(&h->c);
  pthread_mutex_destroy(&h->m);
  free(h);
}

/* ---- calling ---- */

static ext_plugin_status hand_out(ext_plugin_buffer *b, char **out, size_t *out_len) {
  char *copy = malloc(b->len + 1);
  if (!copy) {
    ext_plugin_buffer_free(b);
    return EXT_PLUGIN_NOMEM;
  }
  if (b->len)
    memcpy(copy, b->data, b->len);
  copy[b->len] = 0;
  *out = copy, *out_len = b->len;
  ext_plugin_buffer_free(b);
  return EXT_PLUGIN_OK;
}

static ext_plugin_status literal(const char *s, char **out, size_t *out_len) {
  size_t n = strlen(s);
  if (!(*out = malloc(n + 1)))
    return EXT_PLUGIN_NOMEM;
  memcpy(*out, s, n + 1);
  *out_len = n;
  return EXT_PLUGIN_OK;
}

static ext_plugin_status take_events(ext_interface_host *h, yyjson_val *in, char **out, size_t *out_len) {
  uint32_t timeout = 0, max = 64;
  if (!uint32_of(in, "timeout_ms", &timeout) || !uint32_of(in, "max", &max) || !max)
    return EXT_PLUGIN_INVALID;
  int64_t deadline = mono_ms() + timeout;
  pthread_mutex_lock(&h->m);
  while (!h->head && !h->dropped && !h->closing) {
    int64_t left = deadline - mono_ms();
    if (left <= 0)
      break;
    wait_for(h, left);
  }
  if (!h->head && !h->dropped && h->closing) {
    pthread_mutex_unlock(&h->m);
    return EXT_PLUGIN_CLOSED;
  }
  size_t n = 2, taken = 0;
  for (event *e = h->head; e && taken < max; e = e->next, taken++)
    n += e->len + 1;
  n += 64;
  char *json = malloc(n);
  if (!json) {
    pthread_mutex_unlock(&h->m);
    return EXT_PLUGIN_NOMEM;
  }
  size_t at = 0;
  json[at++] = '[';
  if (h->dropped) {
    at += (size_t)snprintf(json + at, n - at, "{\"seq\":0,\"ticket\":0,\"kind\":\"dropped\",\"data\":%zu}", h->dropped);
    h->dropped = 0;
  }
  for (size_t i = 0; i < taken; i++) {
    event *e = h->head;
    if (at > 1)
      json[at++] = ',';
    memcpy(json + at, e->json, e->len);
    at += e->len;
    h->head = e->next;
    if (!h->head)
      h->tail = NULL;
    h->queued--;
    free(e->json);
    free(e);
  }
  json[at++] = ']';
  json[at] = 0;
  pthread_mutex_unlock(&h->m);
  *out = json, *out_len = at;
  return EXT_PLUGIN_OK;
}

static ext_plugin_status answer(ext_interface_host *h, yyjson_val *in, char **out, size_t *out_len) {
  yyjson_val *ticket = yyjson_obj_get(in, "ticket"), *allow = yyjson_obj_get(in, "allow"),
             *payload = yyjson_obj_get(in, "payload"), *error = yyjson_obj_get(in, "error");
  uint64_t id = 0;
  const char *p = yyjson_get_raw(ticket);
  size_t n = yyjson_get_len(ticket);
  if (!yyjson_is_raw(ticket) || !p || !n)
    return EXT_PLUGIN_INVALID;
  for (size_t i = 0; i < n; i++) {
    if (p[i] < '0' || p[i] > '9')
      return EXT_PLUGIN_INVALID;
    id = id * 10 + (uint64_t)(p[i] - '0');
  }
  if (!!allow + !!payload + !!error != 1 || (allow && !yyjson_is_bool(allow)))
    return EXT_PLUGIN_INVALID;
  size_t len = 0;
  char *text = payload ? written(payload, &len) : error ? written(error, &len) : NULL;
  if ((payload || error) && !text)
    return EXT_PLUGIN_NOMEM;
  pthread_mutex_lock(&h->m);
  waiter *w = h->waiters;
  while (w && w->ticket != id)
    w = w->next;
  if (!w || w->done) {
    pthread_mutex_unlock(&h->m);
    free(text);
    return EXT_PLUGIN_NOT_FOUND;
  }
  w->done = 1;
  w->allow = allow ? yyjson_get_bool(allow) : 0;
  w->is_error = error != NULL;
  w->payload = text, w->payload_len = len;
  pthread_cond_broadcast(&h->c);
  pthread_mutex_unlock(&h->m);
  return literal("null", out, out_len);
}

ext_plugin_status ext_interface_host_call(ext_interface_host *h, const char *op, yyjson_val *in, char **out,
                                          size_t *out_len) {
  uint32_t timeout = 30000;
  if (!uint32_of(in, "timeout_ms", &timeout))
    return EXT_PLUGIN_INVALID;
  if (!timeout)
    timeout = 1;
  ext_plugin_call_options o = {sizeof o, timeout, NULL, NULL};
  if (!strcmp(op, "events"))
    return take_events(h, in, out, out_len);
  if (!strcmp(op, "answer"))
    return answer(h, in, out, out_len);
  if (!strcmp(op, "sequence")) {
    char json[32];
    pthread_mutex_lock(&h->m);
    snprintf(json, sizeof json, "%llu", (unsigned long long)h->seq);
    pthread_mutex_unlock(&h->m);
    return literal(json, out, out_len);
  }
  if (!strcmp(op, "start") || !strcmp(op, "drain")) {
    ext_plugin_status s = op[0] == 's' ? ext_plugin_host_start_with_options(h->host, &o)
                                       : ext_plugin_host_drain_with_options(h->host, &o);
    return s ? s : literal("null", out, out_len);
  }
  if (!strcmp(op, "state")) {
    static const char *names[] = {"\"created\"", "\"ready\"", "\"draining\"", "\"closed\"", "\"failed\""};
    ext_plugin_host_state st = ext_plugin_host_get_state(h->host);
    return literal((size_t)st < 5 ? names[st] : "\"unknown\"", out, out_len);
  }
  if (!strcmp(op, "stderr")) {
    uint8_t buffer[65536];
    size_t n = 0;
    ext_plugin_status s = ext_plugin_host_process_stderr(h->host, buffer, sizeof buffer, &n);
    if (s)
      return s;
    yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
    if (!doc)
      return EXT_PLUGIN_NOMEM;
    yyjson_mut_doc_set_root(doc, yyjson_mut_strncpy(doc, (const char *)buffer, n));
    size_t len = 0;
    char *json = yyjson_mut_write(doc, 0, &len);
    yyjson_mut_doc_free(doc);
    if (!json)
      return EXT_PLUGIN_NOMEM;
    *out = json, *out_len = len;
    return EXT_PLUGIN_OK;
  }
  if (!strcmp(op, "invoke") || !strcmp(op, "call")) {
    size_t n = 0;
    char *body = written(yyjson_obj_get(in, op[0] == 'i' ? "request" : "call"), &n);
    if (!body)
      return EXT_PLUGIN_INVALID;
    ext_plugin_buffer b = {0};
    ext_plugin_status s;
    yyjson_val *progress = yyjson_obj_get(in, "progress");
    if (op[0] == 'i') {
      s = ext_plugin_host_invoke_with_options(h->host, (const uint8_t *)body, n, &o, &b);
    } else if (progress) {
      yyjson_val *tag = yyjson_obj_get(in, "tag");
      call_progress cp = {h, yyjson_get_str(tag)};
      ext_plugin_progress_options po = {sizeof po, progress_cb, &cp, 0, 0};
      if ((tag && (!cp.tag || !tag_ok(cp.tag))) || !uint32_of(progress, "events_per_second", &po.events_per_second) ||
          !uint32_of(progress, "bytes_per_second", &po.bytes_per_second))
        s = EXT_PLUGIN_INVALID;
      else
        s = ext_plugin_host_call_progress(h->host, (const uint8_t *)body, n, &o, &po, &b);
    } else {
      s = ext_plugin_host_call(h->host, (const uint8_t *)body, n, &o, &b);
    }
    free(body);
    if (s) {
      ext_plugin_buffer_free(&b);
      return s;
    }
    return hand_out(&b, out, out_len);
  }
  return EXT_PLUGIN_UNSUPPORTED;
}
