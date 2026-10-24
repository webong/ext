/* Progress and reverse calls over an extension backend. The backend here plays a
 * plugin: it is handed each request, and hands the engine the tagged frames the
 * plugin would send (ext_host_backend_progress, ext_host_backend_reverse_admit and
 * _run), from its own threads where a transport would. Each mode of the backend
 * asks for one thing, so every refusal, every way a hook can answer and every
 * framing violation has a case. The Go differential tests in pkg/plugin-go/cgoengine
 * run the same scenarios through the pure-Go session and compare. */
#include "progress.h"
#include "reverse.h"
#include "sync.h"
#include "wire.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define DESCRIPTOR                                                             \
  "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"backend/test\","   \
  "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"backend.test\","            \
  "\"version\":\"v1\",\"operations\":[{\"name\":\"run\",\"progress\":true},"   \
  "{\"name\":\"quiet\"}]}],\"reverse\":[{\"name\":\"host.svc\",\"version\":"   \
  "\"v1\",\"operations\":[{\"name\":\"sign\",\"surface\":\"signing\"},"        \
  "{\"name\":\"secret\"},{\"name\":\"empty\"},{\"name\":\"fail\"},"            \
  "{\"name\":\"gate\"},{\"name\":\"hang\"}]}]}"
#define PLAIN                                                                  \
  "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"backend/test\","   \
  "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"backend.test\","            \
  "\"version\":\"v1\",\"operations\":[{\"name\":\"quiet\"}]}]}"
#define CONTRACT "\"contract\":{\"name\":\"host.svc\",\"version\":\"v1\"}"
#define FUTURE "2099-01-01T00:00:00Z"

/* ---- what the hooks saw and are told to do ---- */

static pthread_mutex_t seen_mu = PTHREAD_MUTEX_INITIALIZER;
static struct {
  int authorized, handled, canceled_in_hook, gate_open;
  char request[4096];
  const void *value;
  uint32_t timeout_ms;
} seen;
static int token, session_token;

static int count(int *counter) {
  pthread_mutex_lock(&seen_mu);
  int n = *counter;
  pthread_mutex_unlock(&seen_mu);
  return n;
}
static void pause_ms(int ms) {
  int64_t until = ext_mono_ns() + (int64_t)ms * 1000000;
  while (ext_mono_ns() < until) {
  }
}
static int wait_for(int *counter, int want) {
  int64_t until = ext_mono_ns() + 3000000000LL;
  while (ext_mono_ns() < until && count(counter) < want)
    pause_ms(1);
  return count(counter) >= want;
}
static void spin_until(const ext_call_options *o, int64_t ns, int *flag) {
  int64_t until = ext_mono_ns() + ns;
  while (ext_mono_ns() < until && !ext_cancel_is_signaled(o->cancel) &&
         !(flag && count(flag)))
    ;
}
static int32_t allow(void *u, const uint8_t *p, size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return 0;
}
static int32_t authorize(void *user, const ext_call_options *o,
                         const uint8_t *json, size_t len) {
  (void)user;
  pthread_mutex_lock(&seen_mu);
  seen.authorized++;
  assert(len < sizeof(seen.request));
  memcpy(seen.request, json, len);
  seen.request[len] = 0;
  seen.value = o->value;
  seen.timeout_ms = o->timeout_ms;
  int deny = strstr(seen.request, "\"operation\":\"secret\"") != NULL;
  pthread_mutex_unlock(&seen_mu);
  return deny;
}
static ext_status handle(void *user, const ext_call_options *o,
                         const uint8_t *json, size_t len, ext_reverse_emit emit,
                         void *sink) {
  (void)user;
  char text[4096];
  assert(len < sizeof(text));
  memcpy(text, json, len);
  text[len] = 0;
  pthread_mutex_lock(&seen_mu);
  seen.handled++;
  pthread_mutex_unlock(&seen_mu);
  const char *operation = strstr(text, "\"operation\":\"") + 13;
#define IS(name) (!strncmp(operation, name "\"", strlen(name) + 1))
#define PAYLOAD(s) emit(sink, EXT_HOST_REVERSE_PAYLOAD, (const uint8_t *)(s), strlen(s))
  if (IS("sign"))
    return PAYLOAD("\"signed\"");
  if (IS("empty"))
    return emit(sink, EXT_HOST_REVERSE_PAYLOAD, NULL, 0);
  if (IS("fail"))
    return EXT_IO;
  if (IS("gate")) {
    spin_until(o, 3000000000LL, &seen.gate_open);
    return PAYLOAD("\"gated\"");
  }
  if (IS("hang")) {
    spin_until(o, 5000000000LL, NULL);
    pthread_mutex_lock(&seen_mu);
    seen.canceled_in_hook = ext_cancel_is_signaled(o->cancel);
    pthread_mutex_unlock(&seen_mu);
    return PAYLOAD("\"too late\"");
  }
  return EXT_UNSUPPORTED;
}

/* ---- the backend ---- */

typedef struct {
  const char *mode;
  ext_host *host;
  /* Threads the backend started to run admitted requests, as a transport would. */
  pthread_t threads[8];
  int running;
  char reply[8][512];
  ext_status status[8];
  /* The last violation the engine reported to a frame, for the test to read. */
  ext_status last;
  /* Events the rate limit passed on and dropped, and the caller's own signal. */
  int passed, dropped;
  ext_cancel *caller;
} backend;

typedef struct {
  backend *b;
  ext_reverse_ticket *ticket;
  int slot;
  const ext_cancel *caller;
} run_args;

static ext_status connect_cb(void *user, uint32_t timeout_ms, ext_emit emit,
                             void *context) {
  (void)user;
  (void)timeout_ms;
  const char *d = DESCRIPTOR;
  return emit(context, (const uint8_t *)d, strlen(d));
}
static ext_status connect_plain(void *user, uint32_t timeout_ms, ext_emit emit,
                                void *context) {
  (void)user;
  (void)timeout_ms;
  const char *d = PLAIN;
  return emit(context, (const uint8_t *)d, strlen(d));
}
static void close_cb(void *user) { (void)user; }
static void release_cb(void *user) { (void)user; }

static void request_id(const uint8_t *request, size_t len, char *id,
                       size_t size) {
  yyjson_doc *doc = ext_parse(request, len);
  assert(doc);
  const char *s = yyjson_get_str(yyjson_obj_get(yyjson_doc_get_root(doc), "id"));
  assert(s && strlen(s) < size);
  strcpy(id, s);
  yyjson_doc_free(doc);
}
static ext_status frame_progress(backend *b, const char *parent,
                                 const char *rest) {
  /* The frame's own text, a parent of up to 63 characters and a rest of up to 1023. */
  char frame[1280];
  snprintf(frame, sizeof(frame),
           "{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\","
           "\"parent\":\"%s\",%s}",
           parent, rest);
  return ext_host_backend_progress(b->host, (const uint8_t *)frame,
                                   strlen(frame), NULL);
}
static void progress(backend *b, const char *parent, const char *rest) {
  b->last = frame_progress(b, parent, rest);
}
/* One reverse request: the frame, as the plugin would write it. */
static void request_frame(char *frame, size_t size, const char *id,
                          const char *parent, const char *op) {
  char clause[96] = "";
  if (parent)
    snprintf(clause, sizeof(clause), ",\"parent\":\"%s\"", parent);
  snprintf(frame, size,
           "{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\","
           "\"id\":\"%s\"%s," CONTRACT ",\"operation\":\"%s\",\"deadline\":\""
           FUTURE "\",\"payload\":{\"n\":1}}",
           id, clause, op);
}
/* Admits a request and, when admitted, returns the ticket; the refusal, or the
 * answer once run, is copied to reply. */
static ext_reverse_ticket *admit(backend *b, const char *frame, char *reply,
                                 size_t size) {
  ext_reverse_ticket *ticket = NULL;
  ext_buffer answer = {0};
  b->last = ext_host_backend_reverse_admit(b->host, (const uint8_t *)frame,
                                           strlen(frame), &ticket, &answer);
  reply[0] = 0;
  if (!b->last && answer.len) {
    assert(!ticket && answer.len < size);
    memcpy(reply, answer.data, answer.len);
    reply[answer.len] = 0;
  }
  ext_buffer_free(&answer);
  return ticket;
}
static void run_ticket(backend *b, ext_reverse_ticket *ticket,
                       const ext_cancel *caller, char *reply, size_t size,
                       ext_status *status) {
  ext_buffer answer = {0};
  *status = ext_host_backend_reverse_run(b->host, ticket, caller, &answer);
  reply[0] = 0;
  if (!*status) {
    assert(answer.len < size);
    memcpy(reply, answer.data, answer.len);
    reply[answer.len] = 0;
  }
  ext_buffer_free(&answer);
}
static void *run_main(void *arg) {
  run_args *a = arg;
  run_ticket(a->b, a->ticket, a->caller, a->b->reply[a->slot],
             sizeof(a->b->reply[0]), &a->b->status[a->slot]);
  free(a);
  return NULL;
}
/* Runs a ticket on a thread of the backend's, as a transport would. */
static void run_async(backend *b, ext_reverse_ticket *ticket,
                      const ext_cancel *caller) {
  run_args *a = malloc(sizeof(*a));
  assert(a && b->running < 8);
  *a = (run_args){b, ticket, b->running, caller};
  assert(!pthread_create(&b->threads[b->running++], NULL, run_main, a));
}
static void join_all(backend *b) {
  for (int i = 0; i < b->running; i++)
    pthread_join(b->threads[i], NULL);
  b->running = 0;
}

static void respond(const char *id, const char *payload, ext_emit emit,
                    void *context) {
  char body[2048];
  int n = snprintf(body, sizeof(body),
                   "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\","
                   "\"payload\":%s}",
                   id, payload);
  assert(n > 0 && (size_t)n < sizeof(body));
  assert(emit(context, (const uint8_t *)body, (size_t)n) == EXT_OK);
}

static ext_status invoke_cb(void *user, const uint8_t *request, size_t len,
                            uint32_t timeout_ms, ext_emit emit, void *context) {
  (void)timeout_ms;
  backend *b = user;
  char id[64], reply[512], frame[1024], escaped[1100];
  request_id(request, len, id, sizeof(id));
  const char *mode = b->mode;
  if (!strcmp(mode, "progress")) {
    progress(b, id, "\"kind\":\"progress\",\"completed\":1,\"total\":2");
    assert(b->last == EXT_OK);
    snprintf(frame, sizeof(frame), "\"kind\":\"log\",\"text\":\"%s\"", id);
    progress(b, id, frame);
    assert(b->last == EXT_OK);
    respond(id, "{\"done\":true}", emit, context);
    return EXT_OK;
  }
  if (!strcmp(mode, "progress-slow")) {
    /* Calls overlap: each call's events go to its own sink. */
    for (int i = 0; i < 3; i++) {
      snprintf(frame, sizeof(frame), "\"kind\":\"log\",\"text\":\"%s\"", id);
      /* Calls overlap, so the backend's own state is not touched. */
      ext_status s = frame_progress(b, id, frame);
      assert(s == EXT_OK);
      pause_ms(10);
    }
    respond(id, "{\"done\":true}", emit, context);
    return EXT_OK;
  }
  if (!strcmp(mode, "progress-burst")) {
    for (int i = 0; i < 400; i++) {
      char frame_text[256];
      snprintf(frame_text, sizeof(frame_text),
               "{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\","
               "\"parent\":\"%s\",\"kind\":\"log\",\"text\":\"x\"}",
               id);
      int delivered = -1;
      ext_status s = ext_host_backend_progress(
          b->host, (const uint8_t *)frame_text, strlen(frame_text), &delivered);
      assert(s == EXT_OK); /* a limit drops events, it breaks no rule */
      assert(delivered == 0 || delivered == 1);
      if (delivered)
        b->passed++;
      else
        b->dropped++;
    }
    respond(id, "{\"done\":true}", emit, context);
    return EXT_OK;
  }
  if (!strncmp(mode, "progress-bad", 12)) {
    const char *parent = !strcmp(mode, "progress-bad-parent") ? "999" : id;
    const char *rest =
        !strcmp(mode, "progress-bad-kind")     ? "\"kind\":\"debug\""
        : !strcmp(mode, "progress-bad-extra")  ? "\"kind\":\"log\",\"x\":1"
        : !strcmp(mode, "progress-bad-total")  ? "\"kind\":\"progress\","
                                                 "\"completed\":3,\"total\":2"
        : !strcmp(mode, "progress-bad-stream") ? "\"kind\":\"progress\","
                                                 "\"stream\":\"out\""
                                               : "\"kind\":\"log\"";
    progress(b, parent, rest);
    return b->last; /* the connection is failed */
  }
  if (!strcmp(mode, "progress-garbage")) {
    b->last = ext_host_backend_progress(b->host, (const uint8_t *)"{nope", 5, NULL);
    return b->last;
  }
  if (!strcmp(mode, "progress-undeclared")) {
    /* "quiet" did not declare progress; the call is in flight all the same. */
    progress(b, id, "\"kind\":\"log\",\"text\":\"x\"");
    return b->last;
  }
  if (!strncmp(mode, "call:", 5)) {
    request_frame(frame, sizeof(frame), "g1", id, mode + 5);
    ext_reverse_ticket *ticket = admit(b, frame, reply, sizeof(reply));
    if (ticket) {
      ext_status s;
      run_ticket(b, ticket, NULL, reply, sizeof(reply), &s);
      assert(s == EXT_OK);
    }
    if (b->last)
      return b->last;
    size_t j = 0;
    for (size_t i = 0; reply[i] && j + 2 < sizeof(escaped); i++) {
      if (reply[i] == '"' || reply[i] == '\\')
        escaped[j++] = '\\';
      escaped[j++] = reply[i];
    }
    escaped[j] = 0;
    char payload[1400];
    snprintf(payload, sizeof(payload), "{\"done\":true,\"reply\":\"%s\"}", escaped);
    respond(id, payload, emit, context);
    return EXT_OK;
  }
  if (!strcmp(mode, "burst")) {
    /* Six requests at once; four are served, two are busy. The ones admitted run
     * on threads of the backend's, and the call returns once they are answered. */
    int refused = 0;
    for (int i = 1; i <= 6; i++) {
      char name[8];
      snprintf(name, sizeof(name), "g%d", i);
      request_frame(frame, sizeof(frame), name, id, "gate");
      ext_reverse_ticket *ticket = admit(b, frame, reply, sizeof(reply));
      assert(!b->last);
      if (ticket)
        run_async(b, ticket, NULL);
      else if (strstr(reply, "\"code\":\"busy\""))
        refused++;
    }
    assert(refused == 2 && wait_for(&seen.handled, 4));
    pthread_mutex_lock(&seen_mu);
    seen.gate_open = 1;
    pthread_mutex_unlock(&seen_mu);
    join_all(b);
    respond(id, "{\"done\":true}", emit, context);
    return EXT_OK;
  }
  if (!strcmp(mode, "hang-call")) {
    /* Admitted, running, and never waited for: the call ends first. */
    request_frame(frame, sizeof(frame), "g1", id, "hang");
    ext_reverse_ticket *ticket = admit(b, frame, reply, sizeof(reply));
    assert(ticket && !b->last);
    run_async(b, ticket, NULL);
    assert(wait_for(&seen.handled, 1));
    respond(id, "{\"done\":true}", emit, context);
    return EXT_OK;
  }
  if (!strcmp(mode, "caller-cancel")) {
    /* The backend's own signal ends a running request: here a plugin in the host's
     * process whose context expires while the host's handler runs. */
    request_frame(frame, sizeof(frame), "g1", id, "hang");
    ext_reverse_ticket *ticket = admit(b, frame, reply, sizeof(reply));
    assert(ticket && !b->last);
    run_async(b, ticket, b->caller);
    assert(wait_for(&seen.handled, 1));
    ext_cancel_signal(b->caller);
    join_all(b);
    respond(id, "{\"done\":true}", emit, context);
    return EXT_OK;
  }
  if (!strcmp(mode, "bad-reverse")) {
    request_frame(frame, sizeof(frame), "!!", id, "sign");
    admit(b, frame, reply, sizeof(reply));
    return b->last;
  }
  if (!strcmp(mode, "wrong-parent")) {
    request_frame(frame, sizeof(frame), "g1", "999", "sign");
    admit(b, frame, reply, sizeof(reply));
    return b->last;
  }
  if (!strcmp(mode, "garbage-reverse")) {
    ext_reverse_ticket *ticket = NULL;
    ext_buffer answer = {0};
    b->last = ext_host_backend_reverse_admit(b->host, (const uint8_t *)"[1]", 3,
                                             &ticket, &answer);
    assert(!ticket && !answer.len);
    return b->last;
  }
  if (!strcmp(mode, "session")) {
    /* No parent: served with the session's value, from another thread. */
    request_frame(frame, sizeof(frame), "s1", NULL, "sign");
    ext_reverse_ticket *ticket = admit(b, frame, reply, sizeof(reply));
    assert(ticket && !b->last);
    run_async(b, ticket, NULL);
    join_all(b);
    snprintf(b->reply[7], sizeof(b->reply[7]), "%s", b->reply[0]);
    respond(id, "{\"done\":true}", emit, context);
    return EXT_OK;
  }
  respond(id, "{\"done\":true}", emit, context);
  return EXT_OK;
}

/* ---- the host ---- */

typedef struct {
  char text[16][96];
  char last[96]; /* the latest event, whatever the count */
  size_t count;
} events;
static pthread_mutex_t events_mu = PTHREAD_MUTEX_INITIALIZER;
static void collect(void *user, const uint8_t *json, size_t len) {
  events *e = user;
  pthread_mutex_lock(&events_mu);
  if (e->count < 16 && len < 96) {
    memcpy(e->text[e->count], json, len);
    e->text[e->count][len] = 0;
  }
  if (len < sizeof(e->last)) {
    memcpy(e->last, json, len);
    e->last[len] = 0;
  }
  e->count++;
  pthread_mutex_unlock(&events_mu);
}

/* An observer a test installs before the next host starts, then clears. */
static void (*pending_observer)(void *, const ext_call_options *,
                                const uint8_t *, size_t);
static void *pending_observer_user;
static ext_status open_host(backend *b, uint32_t flags, int services,
                            uint32_t max_in_flight, ext_host **out) {
  ext_backend_extension extension = {
      sizeof(extension), flags, b, connect_cb, invoke_cb, close_cb, release_cb};
  ext_backend_options backend_options = {sizeof(backend_options),
                                         EXT_BACKEND_EXTENSION, &extension,
                                         sizeof(extension)};
  ext_host_options options = {EXT_HOST_ABI_VERSION,
                              sizeof(options),
                              (const uint8_t *)DESCRIPTOR,
                              strlen(DESCRIPTOR),
                              allow,
                              allow,
                              NULL};
  ext_status s = ext_host_create(&options, &backend_options, out);
  if (s)
    return s;
  b->host = *out;
  if (pending_observer) {
    ext_host_hooks hooks = {sizeof(hooks), pending_observer_user, NULL, NULL,
                            pending_observer};
    s = ext_host_set_hooks(*out, &hooks);
    if (s)
      return s;
  }
  if (services) {
    ext_host_services configured = {sizeof(configured), NULL, authorize, handle,
                                    0,                  0,    max_in_flight,
                                    &session_token};
    s = ext_host_set_services(*out, &configured);
    if (s)
      return s;
  }
  pthread_mutex_lock(&seen_mu);
  memset(&seen, 0, sizeof(seen));
  pthread_mutex_unlock(&seen_mu);
  return ext_host_start(*out, 5000);
}

static const char *RUN =
    "{\"contract\":{\"name\":\"backend.test\",\"version\":\"v1\"},"
    "\"operation\":\"run\",\"payload\":{}}";
static const char *QUIET =
    "{\"contract\":{\"name\":\"backend.test\",\"version\":\"v1\"},"
    "\"operation\":\"quiet\",\"payload\":{}}";
static char body[1 << 12];
static ext_status call_into(ext_host *h, const char *request, events *e,
                            ext_cancel *cancel, char *body, size_t size) {
  ext_call_options options = {sizeof(options), 5000, cancel, &token};
  ext_progress_options progress = {sizeof(progress), e ? collect : NULL, e, 0,
                                   0};
  ext_buffer out = {0};
  ext_status s = ext_host_call_progress(
      h, (const uint8_t *)request, strlen(request), &options, &progress, &out);
  body[0] = 0;
  if (!s) {
    assert(out.len < size);
    memcpy(body, out.data, out.len);
    body[out.len] = 0;
  }
  ext_buffer_free(&out);
  return s;
}
static ext_status call(ext_host *h, const char *request, events *e,
                       ext_cancel *cancel) {
  return call_into(h, request, e, cancel, body, sizeof(body));
}
/* One call of a mode on a fresh host; the status, with the backend left for the
 * test to read. */
static ext_status once(backend *b, const char *mode, uint32_t flags, int services,
                       const char *request, events *e, ext_host_state *state) {
  *b = (backend){0};
  b->mode = mode;
  ext_host *h = NULL;
  ext_status s = open_host(b, flags, services, 0, &h);
  assert(s == EXT_OK);
  s = call(h, request, e, NULL);
  if (state)
    *state = ext_host_get_state(h);
  join_all(b);
  ext_host_destroy(h);
  return s;
}
static void answered(const char *mode, int services, const char *want) {
  backend b;
  ext_host_state state;
  ext_status s = once(&b, mode, EXT_BACKEND_FRAMES, services, RUN, NULL, &state);
  if (s != EXT_OK || state != EXT_HOST_READY || !strstr(body, want))
    fprintf(stderr, "%s: status %s, state %d, body %s (want %s)\n", mode,
            ext_host_status_string(s), state, body, want);
  assert(s == EXT_OK && state == EXT_HOST_READY && strstr(body, want));
}
static void violation(const char *mode, const char *request) {
  backend b;
  ext_host_state state;
  ext_status s = once(&b, mode, EXT_BACKEND_FRAMES, 1, request, NULL, &state);
  if (s != EXT_PROTOCOL || state != EXT_HOST_FAILED || b.last != EXT_PROTOCOL)
    fprintf(stderr, "%s: status %s (state %d), frame status %s\n", mode,
            ext_host_status_string(s), state, ext_host_status_string(b.last));
  assert(s == EXT_PROTOCOL && state == EXT_HOST_FAILED && b.last == EXT_PROTOCOL);
}

/* ---- tests ---- */

static void test_configuration(void) {
  backend b = {0};
  ext_host *h = NULL;
  ext_backend_extension extension = {sizeof(extension), 0, &b, connect_cb,
                                     invoke_cb,         close_cb, release_cb};
  ext_backend_options backend_options = {sizeof(backend_options),
                                         EXT_BACKEND_EXTENSION, &extension,
                                         sizeof(extension)};
  ext_host_options options = {EXT_HOST_ABI_VERSION,
                              sizeof(options),
                              (const uint8_t *)DESCRIPTOR,
                              strlen(DESCRIPTOR),
                              allow,
                              allow,
                              NULL};
  /* A backend that does not hand over its frames cannot serve a plugin that
   * declares them. */
  assert(ext_host_create(&options, &backend_options, &h) == EXT_UNSUPPORTED);
  /* Unknown flags are refused. */
  extension.flags = 8;
  assert(ext_host_create(&options, &backend_options, &h) == EXT_INVALID);
  /* One that declares nothing needs no flag, and then has no frames to give. */
  extension.flags = 0;
  extension.connect = connect_plain;
  options.descriptor = (const uint8_t *)PLAIN;
  options.descriptor_len = strlen(PLAIN);
  assert(ext_host_create(&options, &backend_options, &h) == EXT_OK);
  b.host = h;
  ext_host_services services = {sizeof(services), NULL, authorize, handle, 0, 0,
                                0, NULL};
  assert(ext_host_set_services(h, &services) == EXT_INVALID);
  const char *frame = "{\"frame\":\"progress\"}";
  ext_reverse_ticket *ticket = NULL;
  ext_buffer answer = {0};
  assert(ext_host_backend_progress(h, (const uint8_t *)frame, strlen(frame),
                                   NULL) == EXT_INVALID);
  assert(ext_host_backend_reverse_admit(h, (const uint8_t *)frame, strlen(frame),
                                        &ticket, &answer) == EXT_INVALID);
  assert(ext_host_backend_reverse_run(h, NULL, NULL, &answer) == EXT_INVALID);
  ext_host_destroy(h);
  /* With the flag, the services can be set and the calls take frames. */
  extension.flags = EXT_BACKEND_FRAMES;
  assert(ext_host_create(&options, &backend_options, &h) == EXT_OK);
  assert(ext_host_set_services(h, &services) == EXT_OK);
  assert(ext_host_backend_progress(NULL, (const uint8_t *)frame, strlen(frame),
                                   NULL) == EXT_INVALID);
  assert(ext_host_backend_reverse_admit(h, NULL, 0, &ticket, &answer) ==
         EXT_INVALID);
  assert(ext_host_backend_reverse_admit(h, (const uint8_t *)frame, 1, NULL,
                                        &answer) == EXT_INVALID);
  ext_host_destroy(h);
}

static void test_progress(void) {
  backend b;
  events e = {0};
  ext_host_state state;
  assert(once(&b, "progress", EXT_BACKEND_FRAMES, 0, RUN, &e, &state) == EXT_OK);
  assert(state == EXT_HOST_READY && e.count == 2);
  assert(strstr(e.text[0], "\"kind\":\"progress\"") &&
         strstr(e.text[0], "\"completed\":1") && strstr(e.text[0], "\"total\":2"));
  assert(strstr(e.text[1], "\"kind\":\"log\"") && strstr(e.text[1], "\"text\":\"1\""));
  assert(strstr(body, "\"done\":true"));
  /* The rate limit is a backstop: events over it are dropped and reported, and
   * the plugin has broken no rule. */
  e = (events){0};
  assert(once(&b, "progress-burst", EXT_BACKEND_FRAMES, 0, RUN, &e, &state) ==
         EXT_OK);
  assert(state == EXT_HOST_READY && e.count > 50 && e.count < 400);
  /* The backend is told which events were passed on: the rest were dropped. */
  assert(b.passed + b.dropped == 400 && b.dropped > 0 &&
         (size_t)b.passed + 1 == e.count); /* and the final marker */
  /* The last event is the marker that says how many were lost. */
  assert(strstr(e.last, "\"kind\":\"log\"") && strstr(e.last, "\"dropped\":") &&
         !strstr(e.last, "\"text\""));
  /* Without a sink events are limited and discarded. */
  assert(once(&b, "progress", EXT_BACKEND_FRAMES, 0, RUN, NULL, &state) == EXT_OK);
  /* Frames the call does not allow end the session. */
  violation("progress-bad-parent", RUN);
  violation("progress-bad-kind", RUN);
  violation("progress-bad-extra", RUN);
  violation("progress-bad-total", RUN);
  violation("progress-bad-stream", RUN);
  violation("progress-garbage", RUN);
  violation("progress-undeclared", QUIET);
  /* A call that has returned is no longer in flight. */
  backend c = {0};
  c.mode = "progress";
  ext_host *h = NULL;
  assert(open_host(&c, EXT_BACKEND_FRAMES, 0, 0, &h) == EXT_OK);
  assert(call(h, RUN, NULL, NULL) == EXT_OK);
  progress(&c, "1", "\"kind\":\"log\",\"text\":\"late\"");
  assert(c.last == EXT_PROTOCOL);
  ext_host_destroy(h);
}

/* Calls that overlap each get only their own events. */
typedef struct {
  ext_host *host;
  events e;
  ext_status status;
} caller;
static void *caller_main(void *arg) {
  caller *c = arg;
  char own[256];
  c->status = call_into(c->host, RUN, &c->e, NULL, own, sizeof(own));
  return NULL;
}
static void test_concurrent_progress(void) {
  backend b = {0};
  b.mode = "progress-slow";
  ext_host *h = NULL;
  assert(open_host(&b, EXT_BACKEND_FRAMES | EXT_BACKEND_CONCURRENT, 0, 0, &h) ==
         EXT_OK);
  caller callers[4] = {{0}};
  pthread_t threads[4];
  for (int i = 0; i < 4; i++) {
    callers[i].host = h;
    assert(!pthread_create(&threads[i], NULL, caller_main, &callers[i]));
  }
  for (int i = 0; i < 4; i++)
    pthread_join(threads[i], NULL);
  for (int i = 0; i < 4; i++) {
    assert(callers[i].status == EXT_OK && callers[i].e.count == 3);
    for (size_t k = 1; k < 3; k++)
      assert(!strcmp(callers[i].e.text[k], callers[i].e.text[0]));
    for (int j = 0; j < i; j++)
      assert(strcmp(callers[i].e.text[0], callers[j].e.text[0]));
  }
  assert(ext_host_get_state(h) == EXT_HOST_READY);
  ext_host_destroy(h);
}

static void test_reverse(void) {
  /* An answer, with the request the hooks saw. */
  answered("call:sign", 1, "\\\"id\\\":\\\"g1\\\",\\\"payload\\\":\\\"signed\\\"");
  assert(seen.authorized == 1 && seen.handled == 1);
  assert(strstr(seen.request, "\"apiVersion\":\"ext.plugin/v1\""));
  assert(strstr(seen.request, "\"plugin\":{\"id\":\"backend/test\","
                              "\"revision\":\"r1\"}"));
  assert(strstr(seen.request, "\"surface\":\"signing\""));
  assert(strstr(seen.request, "\"payload\":{\"n\":1}"));
  assert(strstr(seen.request, "\"parent\":\"1\""));
  /* The value of the call in flight, so a binding can derive the handler's
   * context from the caller's. */
  assert(seen.value == &token && seen.timeout_ms > 0);
  answered("call:empty", 1, "\\\"payload\\\":null");
  answered("call:secret", 1, "\\\"code\\\":\\\"denied\\\"");
  assert(seen.handled == 0);
  answered("call:fail", 1, "\\\"code\\\":\\\"operation_failed\\\"");
  answered("call:nothere", 1, "\\\"code\\\":\\\"reverse_undeclared\\\"");
  assert(seen.authorized == 0);
  /* No services: every reverse call is unavailable. */
  answered("call:sign", 0, "\\\"code\\\":\\\"reverse_unavailable\\\"");
  answered("call:nothere", 0, "\\\"code\\\":\\\"reverse_unavailable\\\"");
  /* A request with no parent is session scoped, served with the session's value
   * from a thread of the backend's. */
  backend b;
  ext_host_state state;
  assert(once(&b, "session", EXT_BACKEND_FRAMES, 1, RUN, NULL, &state) == EXT_OK);
  assert(state == EXT_HOST_READY && seen.value == &session_token);
  assert(strstr(b.reply[7], "\"payload\":\"signed\""));
  assert(!strstr(seen.request, "\"parent\""));

  /* Admission is bounded: of six requests at once, four run and two are busy. */
  assert(once(&b, "burst", EXT_BACKEND_FRAMES, 1, RUN, NULL, &state) == EXT_OK);
  assert(state == EXT_HOST_READY && seen.handled == 4);
  /* (the backend asserted the two refusals itself) */

  /* A request still running when its call ends is told to stop, and the call
   * waits for it. */
  assert(once(&b, "hang-call", EXT_BACKEND_FRAMES, 1, RUN, NULL, &state) ==
         EXT_OK);
  assert(seen.canceled_in_hook == 1);

  /* The backend's own signal reaches the hook, and the call does not wait for
   * the hook to give up on its own. */
  backend c = {0};
  c.mode = "caller-cancel";
  assert(ext_cancel_create(&c.caller) == EXT_OK);
  ext_host *h = NULL;
  assert(open_host(&c, EXT_BACKEND_FRAMES, 1, 0, &h) == EXT_OK);
  int64_t started = ext_mono_ns();
  assert(call(h, RUN, NULL, NULL) == EXT_OK);
  assert(ext_mono_ns() - started < 2000000000LL && seen.canceled_in_hook == 1);
  ext_host_destroy(h);
  ext_cancel_destroy(c.caller);

  /* Breaking the framing ends the session. */
  violation("bad-reverse", RUN);
  violation("wrong-parent", RUN);
  violation("garbage-reverse", RUN);
}

/* Closing the host tells the hooks of session-scoped requests to stop, and destroy
 * waits for them. */
static void test_close(void) {
  backend b = {0};
  b.mode = "none";
  ext_host *h = NULL;
  assert(open_host(&b, EXT_BACKEND_FRAMES, 1, 0, &h) == EXT_OK);
  char frame[512], reply[512];
  request_frame(frame, sizeof(frame), "s1", NULL, "hang");
  ext_reverse_ticket *ticket = admit(&b, frame, reply, sizeof(reply));
  assert(ticket && !b.last);
  run_async(&b, ticket, NULL);
  assert(wait_for(&seen.handled, 1));
  ext_host_close(h);
  join_all(&b);
  assert(seen.canceled_in_hook == 1 && b.status[0] == EXT_OK);
  ext_host_destroy(h);
  /* A ticket belongs to one host. */
  backend c = {0};
  c.mode = "none";
  ext_host *other = NULL, *g = NULL;
  assert(open_host(&c, EXT_BACKEND_FRAMES, 1, 0, &other) == EXT_OK);
  assert(open_host(&b, EXT_BACKEND_FRAMES, 1, 0, &g) == EXT_OK);
  request_frame(frame, sizeof(frame), "s1", NULL, "sign");
  ticket = admit(&b, frame, reply, sizeof(reply));
  assert(ticket);
  ext_buffer answer = {0};
  assert(ext_host_backend_reverse_run(other, ticket, NULL, &answer) == EXT_INVALID);
  assert(ext_host_backend_reverse_run(g, ticket, NULL, &answer) == EXT_OK);
  ext_buffer_free(&answer);
  ext_host_destroy(g);
  ext_host_destroy(other);
}

typedef struct {
  char last[512]; /* the latest observer event */
  size_t count;
} observed;
static void observe_cb(void *user, const ext_call_options *o,
                       const uint8_t *json, size_t len) {
  (void)o;
  observed *e = user;
  pthread_mutex_lock(&events_mu);
  assert(len < sizeof(e->last));
  memcpy(e->last, json, len);
  e->last[len] = 0;
  e->count++;
  pthread_mutex_unlock(&events_mu);
}
/* A call refused before it has a request is observed without a request id, as
 * the Go session observes it: an operation the descriptor does not list, a
 * caller that is already canceled, and a session that is closed. */
static void test_refused_calls_are_observed(void) {
  backend b = {0};
  b.mode = "quiet";
  observed e = {0};
  pending_observer = observe_cb;
  pending_observer_user = &e;
  ext_host *h = NULL;
  assert(open_host(&b, EXT_BACKEND_FRAMES, 0, 0, &h) == EXT_OK);
  pending_observer = NULL;
  size_t before = e.count; /* the host's own stages: verify, connect, handshake */
  assert(before >= 1);

  assert(call(h,
              "{\"contract\":{\"name\":\"backend.test\",\"version\":\"v1\"},"
              "\"operation\":\"nope\",\"payload\":{}}",
              NULL, NULL) == EXT_UNSUPPORTED);
  assert(e.count == before + 1);
  assert(strstr(e.last, "\"stage\":\"invoke\"") &&
         strstr(e.last, "\"code\":\"unsupported\"") &&
         strstr(e.last, "\"operation\":\"nope\"") &&
         !strstr(e.last, "requestID"));

  ext_cancel *cancel = NULL;
  assert(ext_cancel_create(&cancel) == EXT_OK);
  ext_cancel_signal(cancel);
  assert(call(h, RUN, NULL, cancel) == EXT_CANCELED);
  assert(e.count == before + 2);
  assert(strstr(e.last, "\"code\":\"canceled\"") &&
         !strstr(e.last, "requestID"));
  ext_cancel_destroy(cancel);

  /* A call that is served has its id. */
  assert(call(h, QUIET, NULL, NULL) == EXT_OK);
  assert(strstr(e.last, "\"code\":\"ok\"") &&
         strstr(e.last, "\"requestID\":\"1\""));

  ext_host_close(h);
  assert(call(h, RUN, NULL, NULL) == EXT_CLOSED);
  assert(strstr(e.last, "\"code\":\"closed\"") &&
         !strstr(e.last, "requestID"));
  join_all(&b);
  ext_host_destroy(h);
}

int main(void) {
  test_refused_calls_are_observed();
  test_configuration();
  test_progress();
  test_concurrent_progress();
  test_reverse();
  test_close();
  puts("extension backend checks passed");
  return 0;
}
