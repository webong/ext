/* Progress frames: the rate limiter, the frame validator and the sink, then the
 * host's frame loop against a scripted guest. The guest is this program started
 * with --guest <mode>; each mode misbehaves in one way, so every protocol
 * violation the host must refuse has a case. The Go differential tests in
 * pkg/ext-go/cgoengine compare the same behavior with the pure-Go session. */
#define _XOPEN_SOURCE 700 /* realpath, under -std=c11 on glibc */
#include "progress.h"
#include "wire.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#include <windows.h>
#endif

/* ---- the scripted guest ---- */

#define DESCRIPTOR                                                             \
  "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"frames/guest\","  \
  "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"frames.test\","            \
  "\"version\":\"v1\",\"operations\":[{\"name\":\"plain\"},{\"name\":"        \
  "\"stream\",\"progress\":true}]}]}"
#define PREFIX "{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\","

static void put(const char *s) {
  fputs(s, stdout);
  fflush(stdout);
}
static void put_frame(const char *parent, const char *fields) {
  printf(PREFIX "\"parent\":\"%s\",%s}\n", parent, fields);
  fflush(stdout);
}
static void respond(const char *id) {
  printf("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\",\"payload\":"
         "{\"done\":true}}\n",
         id);
  fflush(stdout);
}
/* Copies the string value of "key" in a request line. */
static int field(const char *line, const char *key, char *out, size_t size) {
  char pattern[64];
  snprintf(pattern, sizeof(pattern), "\"%s\":\"", key);
  const char *at = strstr(line, pattern);
  if (!at)
    return 0;
  at += strlen(pattern);
  const char *end = strchr(at, '"');
  if (!end || (size_t)(end - at) >= size)
    return 0;
  memcpy(out, at, (size_t)(end - at));
  out[end - at] = 0;
  return 1;
}

static int guest(const char *mode) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  static char line[1 << 16];
  if (!fgets(line, sizeof(line), stdin))
    return 0;
  if (!strcmp(mode, "hellofirst"))
    put_frame("hello", "\"kind\":\"log\",\"text\":\"early\"");
  printf("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"payload\":%s}\n",
         DESCRIPTOR);
  fflush(stdout);
  while (fgets(line, sizeof(line), stdin)) {
    char id[64], op[64];
    if (!field(line, "id", id, sizeof(id)) ||
        !field(line, "operation", op, sizeof(op)))
      return 1;
    int stream = !strcmp(op, "stream");
    if (!strcmp(mode, "ok")) {
      if (stream) {
        put_frame(id, "\"kind\":\"progress\",\"completed\":1,\"total\":3");
        put_frame(id, "\"kind\":\"log\",\"stream\":\"out\",\"text\":\"hello\"");
        put_frame(id, "\"kind\":\"log\",\"stream\":\"err\",\"text\":\"oops\"");
      }
      respond(id);
    } else if (!strcmp(mode, "batch")) {
      /* Three frames and the response arrive in one read. */
      char all[1024];
      int n = 0;
      for (int i = 0; i < 3; i++)
        n += snprintf(all + n, sizeof(all) - (size_t)n,
                      PREFIX "\"parent\":\"%s\",\"kind\":\"log\",\"text\":"
                             "\"b%d\"}\n",
                      id, i);
      n += snprintf(all + n, sizeof(all) - (size_t)n,
                    "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\","
                    "\"payload\":{\"done\":true}}\n",
                    id);
      fwrite(all, 1, (size_t)n, stdout);
      fflush(stdout);
    } else if (!strcmp(mode, "split")) {
      /* One frame, a byte at a time, then the response. */
      char frame[512];
      snprintf(frame, sizeof(frame),
               PREFIX "\"parent\":\"%s\",\"kind\":\"log\",\"text\":\"slow\"}\n",
               id);
      for (const char *c = frame; *c; c++) {
        fputc(*c, stdout);
        fflush(stdout);
      }
      respond(id);
    } else if (!strcmp(mode, "flood")) {
      for (int i = 0; i < 300; i++)
        put_frame(id, "\"kind\":\"log\",\"text\":\"x\"");
      respond(id);
    } else if (!strcmp(mode, "marker")) {
      put_frame(id, "\"kind\":\"log\",\"text\":\"a\"");
      put_frame(id, "\"kind\":\"log\",\"dropped\":7");
      put_frame(id, "\"kind\":\"log\",\"text\":\"b\"");
      respond(id);
    } else if (!strcmp(mode, "undeclared")) {
      put_frame(id, "\"kind\":\"log\",\"text\":\"x\"");
      respond(id);
    } else if (!strcmp(mode, "wrongparent")) {
      put_frame("999", "\"kind\":\"log\",\"text\":\"x\"");
      respond(id);
    } else if (!strcmp(mode, "badkind")) {
      put_frame(id, "\"kind\":\"debug\"");
      respond(id);
    } else if (!strcmp(mode, "streamprogress")) {
      put_frame(id, "\"kind\":\"progress\",\"stream\":\"out\"");
      respond(id);
    } else if (!strcmp(mode, "overtotal")) {
      put_frame(id, "\"kind\":\"progress\",\"completed\":5,\"total\":2");
      respond(id);
    } else if (!strcmp(mode, "negative")) {
      put_frame(id, "\"kind\":\"progress\",\"completed\":-1");
      respond(id);
    } else if (!strcmp(mode, "bigtext")) {
      char big[2200];
      int n = snprintf(big, sizeof(big), "\"kind\":\"log\",\"text\":\"");
      memset(big + n, 'x', 2049);
      strcpy(big + n + 2049, "\"");
      put_frame(id, big);
      respond(id);
    } else if (!strcmp(mode, "dupkey")) {
      put_frame(id, "\"kind\":\"log\",\"kind\":\"log\"");
      respond(id);
    } else if (!strcmp(mode, "unknown")) {
      put("{\"frame\":\"mystery\"}\n");
      respond(id);
    } else if (!strcmp(mode, "notobject")) {
      put("[1,2]\n");
      respond(id);
    } else if (!strcmp(mode, "nullroot")) {
      put("null\n");
    } else if (!strcmp(mode, "garbage")) {
      put("not json\n");
    } else if (!strcmp(mode, "wrongid")) {
      put("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"other\",\"payload\":"
          "null}\n");
    } else if (!strcmp(mode, "wrongversion")) {
      printf("{\"apiVersion\":\"ext.plugin/v2\",\"id\":\"%s\",\"payload\":"
             "null}\n",
             id);
      fflush(stdout);
    } else if (!strcmp(mode, "extrafield")) {
      printf("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\",\"payload\":"
             "null,\"extra\":1}\n",
             id);
      fflush(stdout);
    } else if (!strcmp(mode, "dupresponse")) {
      printf("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\",\"id\":\"%s\","
             "\"payload\":null}\n",
             id, id);
      fflush(stdout);
    } else if (!strcmp(mode, "reverse")) {
      put("{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\","
          "\"id\":\"g1\",\"contract\":{\"name\":\"h\",\"version\":\"v1\"},"
          "\"operation\":\"o\",\"deadline\":\"2099-01-01T00:00:00Z\"}\n");
      respond(id);
    } else if (!strcmp(mode, "reverseresponse")) {
      put("{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\","
          "\"id\":\"g1\",\"payload\":null}\n");
      respond(id);
    } else if (!strcmp(mode, "after")) {
      /* The response, then more: the response must be the last thing sent. */
      char all[1024];
      int n = snprintf(all, sizeof(all),
                       "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\","
                       "\"payload\":{\"done\":true}}\n",
                       id);
      n += snprintf(all + n, sizeof(all) - (size_t)n,
                    PREFIX "\"parent\":\"%s\",\"kind\":\"log\",\"text\":"
                           "\"late\"}\n",
                    id);
      fwrite(all, 1, (size_t)n, stdout);
      fflush(stdout);
    } else if (!strcmp(mode, "stall")) {
      put_frame(id, "\"kind\":\"log\",\"text\":\"before\"");
      for (;;) {
        /* Never answers; the host's deadline ends the call. */
        if (!fgets(line, sizeof(line), stdin))
          return 0;
      }
    } else {
      return 2;
    }
  }
  return 0;
}

/* ---- the host side ---- */

typedef struct {
  char *items[512];
  size_t count;
} events;
static void collect(void *user, const uint8_t *json, size_t len) {
  events *e = user;
  assert(e->count < sizeof(e->items) / sizeof(*e->items));
  e->items[e->count] = malloc(len + 1);
  assert(e->items[e->count]);
  memcpy(e->items[e->count], json, len);
  e->items[e->count][len] = 0;
  e->count++;
}
static void clear(events *e) {
  for (size_t i = 0; i < e->count; i++)
    free(e->items[i]);
  e->count = 0;
}
static int32_t allow(void *u, const uint8_t *p, size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return 0;
}
static char self[4096];

/* Starts the scripted guest through the launch backend, which gives the child a
 * scrubbed environment that still holds PATH (and SystemRoot on Windows): a child
 * with no environment at all cannot find the runtime libraries it was linked
 * with on every toolchain. A failure says why, since a CI log is all there is. */
static ext_status open_host(const char *mode, ext_host **out) {
  const char *args[] = {"--guest", mode};
  ext_jsonline_process_launch launch = {sizeof(launch), self, args, 2,
                                        NULL,           0,    NULL, 0};
  ext_backend_options backend = {sizeof(backend),
                                 EXT_BACKEND_JSONLINE_PROCESS_LAUNCH, &launch,
                                 sizeof(launch)};
  ext_host_options options = {EXT_HOST_ABI_VERSION,
                              sizeof(options),
                              (const uint8_t *)DESCRIPTOR,
                              strlen(DESCRIPTOR),
                              allow,
                              allow,
                              NULL};
  ext_status s = ext_host_create(&options, &backend, out);
  if (s) {
    fprintf(stderr, "create %s: %s (executable %s)\n", mode,
            ext_host_status_string(s), self);
    return s;
  }
  s = ext_host_start(*out, 5000);
  if (s && strcmp(mode, "hellofirst")) {
    uint8_t text[512];
    size_t n = 0;
    if (ext_host_process_stderr(*out, text, sizeof(text) - 1, &n) != EXT_OK)
      n = 0;
    text[n] = 0;
    fprintf(stderr, "start %s: %s (executable %s); child stderr: %s\n", mode,
            ext_host_status_string(s), self, (const char *)text);
  }
  return s;
}
static ext_status call(ext_host *h, const char *op, events *e,
                       uint32_t timeout_ms, uint32_t events_per_second) {
  char request[256];
  snprintf(request, sizeof(request),
           "{\"contract\":{\"name\":\"frames.test\",\"version\":\"v1\"},"
           "\"operation\":\"%s\",\"payload\":{}}",
           op);
  ext_call_options options = {sizeof(options), timeout_ms, NULL, NULL};
  ext_progress_options progress = {sizeof(progress), collect, e,
                                   events_per_second, 0};
  ext_buffer out = {0};
  ext_status s = ext_host_call_progress(h, (const uint8_t *)request,
                                        strlen(request), &options, &progress,
                                        &out);
  if (!s)
    assert(strstr((const char *)out.data, "\"done\":true"));
  ext_buffer_free(&out);
  return s;
}
/* Opens a guest in mode, calls op once and returns the call's status. */
static ext_status run(const char *mode, const char *op, events *e,
                      ext_host_state *state) {
  ext_host *h = NULL;
  ext_status s = open_host(mode, &h);
  assert(s == EXT_OK);
  s = call(h, op, e, 5000, 0);
  if (state)
    *state = ext_host_get_state(h);
  ext_host_destroy(h);
  return s;
}
static void expect_violation(const char *mode, const char *op,
                             ext_status want) {
  events e = {0};
  ext_host_state state;
  ext_status s = run(mode, op, &e, &state);
  if (s != want || state != EXT_HOST_FAILED)
    fprintf(stderr, "%s on %s: status %s (want %s), state %d\n", mode, op,
            ext_host_status_string(s), ext_host_status_string(want), state);
  assert(s == want);
  assert(state == EXT_HOST_FAILED); /* a violation ends the session */
  clear(&e);
}

/* ---- tests ---- */

static void test_limiter(void) {
  ext_limiter l;
  uint64_t dropped;
  const int64_t second = 1000000000;
  /* Defaults: a burst of twice 50 events. */
  ext_limiter_init(&l, 0, 0, 0);
  for (int i = 0; i < 100; i++)
    assert(ext_limiter_admit(&l, 1, 0, &dropped) && dropped == 0);
  assert(!ext_limiter_admit(&l, 1, 0, &dropped));
  assert(!ext_limiter_admit(&l, 1, 0, &dropped));
  /* The next event that passes reports the two that did not. */
  assert(ext_limiter_admit(&l, 1, second, &dropped) && dropped == 2);
  assert(ext_limiter_pending(&l) == 0);
  /* A second later the bucket holds the rate again, never more than the burst. */
  ext_limiter_init(&l, 10, 1000000, 0);
  for (int i = 0; i < 20; i++)
    assert(ext_limiter_admit(&l, 0, 0, &dropped));
  assert(!ext_limiter_admit(&l, 0, 0, &dropped));
  assert(ext_limiter_admit(&l, 0, 100 * second, &dropped) && dropped == 1);
  for (int i = 0; i < 19; i++)
    assert(ext_limiter_admit(&l, 0, 100 * second, &dropped));
  assert(!ext_limiter_admit(&l, 0, 100 * second, &dropped));
  /* Bytes are counted separately: 100 bytes a second is a burst of 200. */
  ext_limiter_init(&l, 1000, 100, 0);
  assert(ext_limiter_admit(&l, 100, 0, &dropped));
  assert(ext_limiter_admit(&l, 100, 0, &dropped));
  assert(!ext_limiter_admit(&l, 1, 0, &dropped));
  /* An event larger than the burst can never pass. */
  assert(!ext_limiter_admit(&l, 201, 1000 * second, &dropped));
  /* Counts carried from a peer are reported with the pending ones. */
  ext_limiter_carry(&l, 5);
  assert(ext_limiter_pending(&l) == 5 + 2);
  assert(ext_limiter_pending(&l) == 0);
  /* Rates that are not positive use the defaults. */
  ext_limiter_init(&l, -3, 0, 0);
  for (int i = 0; i < 100; i++)
    assert(ext_limiter_admit(&l, 0, 0, &dropped));
  assert(!ext_limiter_admit(&l, 0, 0, &dropped));
}

static ext_status frame(const char *json, ext_progress_event *e) {
  yyjson_doc *doc = ext_parse((const uint8_t *)json, strlen(json));
  assert(doc);
  yyjson_val *parent;
  ext_status s = ext_progress_frame(yyjson_doc_get_root(doc), e, &parent);
  if (!s)
    assert(yyjson_is_str(parent));
  yyjson_doc_free(doc);
  return s;
}
static void test_frame_validation(void) {
  ext_progress_event e;
  assert(frame(PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":1,"
                      "\"total\":2}",
               &e) == EXT_OK);
  assert(!e.log && !e.stream && e.completed == 1 && e.total == 2);
  assert(frame(PREFIX "\"parent\":\"a.b\",\"kind\":\"log\",\"stream\":\"err\","
                      "\"text\":\"t\",\"dropped\":3}",
               &e) == EXT_OK);
  assert(e.log && e.stream == 2 && e.text_len == 1 && e.dropped == 3);
  /* null decodes to the zero value, as in Go. */
  assert(frame(PREFIX "\"parent\":\"1\",\"kind\":\"log\",\"stream\":null,"
                      "\"text\":null,\"completed\":null,\"total\":null,"
                      "\"dropped\":null}",
               &e) == EXT_OK);
  assert(frame(PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":5}",
               &e) == EXT_OK); /* no total: completed is not bounded */
  assert(frame(PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":"
                      "18446744073709551615}",
               &e) == EXT_OK);
  static const char *const bad[] = {
      "{\"frame\":\"log\",\"apiVersion\":\"ext.plugin/v1\",\"parent\":\"1\","
      "\"kind\":\"log\"}",
      "{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v2\",\"parent\":"
      "\"1\",\"kind\":\"log\"}",
      PREFIX "\"kind\":\"log\"}",
      PREFIX "\"parent\":\"\",\"kind\":\"log\"}",
      PREFIX "\"parent\":\"-x\",\"kind\":\"log\"}",
      PREFIX "\"parent\":1,\"kind\":\"log\"}",
      PREFIX "\"parent\":\"1\"}",
      PREFIX "\"parent\":\"1\",\"kind\":\"\"}",
      PREFIX "\"parent\":\"1\",\"kind\":\"LOG\"}",
      PREFIX "\"parent\":\"1\",\"kind\":7}",
      PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"stream\":\"out\"}",
      PREFIX "\"parent\":\"1\",\"kind\":\"log\",\"stream\":\"stdout\"}",
      PREFIX "\"parent\":\"1\",\"kind\":\"log\",\"text\":5}",
      PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":3,"
             "\"total\":2}",
      PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":-1}",
      PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":1.5}",
      PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":1e2}",
      PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":\"1\"}",
      PREFIX "\"parent\":\"1\",\"kind\":\"progress\",\"completed\":"
             "18446744073709551616}",
      PREFIX "\"parent\":\"1\",\"kind\":\"log\",\"extra\":1}",
  };
  for (size_t i = 0; i < sizeof(bad) / sizeof(*bad); i++) {
    if (frame(bad[i], &e) != EXT_PROTOCOL)
      fprintf(stderr, "accepted: %s\n", bad[i]);
    assert(frame(bad[i], &e) == EXT_PROTOCOL);
  }
  /* The text bound is in bytes of the decoded string. */
  char big[2400];
  int n = snprintf(big, sizeof(big),
                   PREFIX "\"parent\":\"1\",\"kind\":\"log\",\"text\":\"");
  memset(big + n, 'x', 2048);
  strcpy(big + n + 2048, "\"}");
  assert(frame(big, &e) == EXT_OK && e.text_len == 2048);
  memset(big + n, 'x', 2049);
  strcpy(big + n + 2049, "\"}");
  assert(frame(big, &e) == EXT_PROTOCOL);
  /* Two bytes of text written as one escape and one character. */
  assert(frame(PREFIX "\"parent\":\"1\",\"kind\":\"log\",\"text\":\"\\u00e9\"}",
               &e) == EXT_OK &&
         e.text_len == 2);
}

static void test_sink(void) {
  events e = {0};
  ext_progress_sink sink;
  ext_progress_event event = {1, 0, "x", 1, 0, 0, 0};
  ext_progress_sink_init(&sink, collect, &e, 0, 0);
  /* The burst passes, the rest is dropped and counted, never an error. */
  for (int i = 0; i < 300; i++)
    assert(ext_progress_sink_deliver(&sink, &event) == EXT_OK);
  size_t delivered = e.count;
  assert(delivered >= 100 && delivered <= 110);
  ext_progress_sink_finish(&sink);
  assert(e.count == delivered + 1);
  assert(strstr(e.items[e.count - 1], "\"kind\":\"log\""));
  assert(!strstr(e.items[e.count - 1], "\"text\""));
  /* Delivered events plus every reported drop are the 300 events sent. */
  unsigned long long lost = 0;
  for (size_t i = 0; i < e.count; i++) {
    const char *at = strstr(e.items[i], "\"dropped\":");
    if (at)
      lost += strtoull(at + 10, NULL, 10);
  }
  assert(delivered + lost == 300);
  ext_progress_sink_finish(&sink); /* nothing left: no second marker */
  assert(e.count == delivered + 1);
  clear(&e);

  /* A marker carries a count and nothing else: it is folded into the next
   * event, not delivered. */
  ext_progress_sink_init(&sink, collect, &e, 0, 0);
  ext_progress_event marker = {1, 0, "", 0, 0, 0, 7};
  assert(ext_progress_sink_deliver(&sink, &marker) == EXT_OK);
  assert(e.count == 0);
  assert(ext_progress_sink_deliver(&sink, &event) == EXT_OK);
  assert(e.count == 1 && strstr(e.items[0], "\"dropped\":7"));
  assert(strstr(e.items[0], "\"text\":\"x\""));
  /* A marker that only counts is not rate limited, and a final one reports it. */
  assert(ext_progress_sink_deliver(&sink, &marker) == EXT_OK);
  ext_progress_sink_finish(&sink);
  assert(e.count == 2 && strstr(e.items[1], "\"dropped\":7"));
  clear(&e);

  /* Zero fields are left out of the JSON, and a missing callback only counts. */
  ext_progress_sink_init(&sink, collect, &e, 0, 0);
  ext_progress_event full = {1, 1, "out", 3, 4, 9, 2};
  assert(ext_progress_sink_deliver(&sink, &full) == EXT_OK);
  assert(!strcmp(e.items[0], "{\"kind\":\"log\",\"stream\":\"out\",\"text\":"
                             "\"out\",\"completed\":4,\"total\":9,\"dropped\":"
                             "2}"));
  clear(&e);
  ext_progress_sink_init(&sink, NULL, NULL, 0, 0);
  assert(ext_progress_sink_deliver(&sink, &event) == EXT_OK);
  ext_progress_sink_finish(&sink);
}

static void test_calls(void) {
  events e = {0};
  ext_host_state state;
  /* Events arrive in order, validated, before the response. */
  assert(run("ok", "stream", &e, &state) == EXT_OK && state == EXT_HOST_READY);
  assert(e.count == 3);
  assert(!strcmp(e.items[0], "{\"kind\":\"progress\",\"completed\":1,"
                             "\"total\":3}"));
  assert(!strcmp(e.items[1], "{\"kind\":\"log\",\"stream\":\"out\",\"text\":"
                             "\"hello\"}"));
  assert(!strcmp(e.items[2], "{\"kind\":\"log\",\"stream\":\"err\",\"text\":"
                             "\"oops\"}"));
  clear(&e);
  /* An operation that declared no progress sends none, and none is delivered. */
  assert(run("ok", "plain", &e, &state) == EXT_OK && e.count == 0);
  /* Several frames in one read, and one frame over many reads. */
  assert(run("batch", "stream", &e, &state) == EXT_OK && e.count == 3);
  assert(strstr(e.items[2], "\"text\":\"b2\""));
  clear(&e);
  assert(run("split", "stream", &e, &state) == EXT_OK && e.count == 1);
  assert(strstr(e.items[0], "\"text\":\"slow\""));
  clear(&e);
  /* A flood is limited, and what was lost is counted and reported. */
  assert(run("flood", "stream", &e, &state) == EXT_OK &&
         state == EXT_HOST_READY);
  unsigned long long lost = 0;
  size_t texts = 0;
  for (size_t i = 0; i < e.count; i++) {
    const char *at = strstr(e.items[i], "\"dropped\":");
    if (at)
      lost += strtoull(at + 10, NULL, 10);
    texts += strstr(e.items[i], "\"text\"") != NULL;
  }
  assert(texts >= 100 && texts <= 140 && texts + lost == 300);
  assert(!strstr(e.items[e.count - 1], "\"text\""));
  clear(&e);
  /* A marker the guest sent is folded into the next event. */
  assert(run("marker", "stream", &e, &state) == EXT_OK && e.count == 2);
  assert(strstr(e.items[0], "\"text\":\"a\"") && !strstr(e.items[0], "dropped"));
  assert(strstr(e.items[1], "\"text\":\"b\"") &&
         strstr(e.items[1], "\"dropped\":7"));
  clear(&e);

  /* The caller's rate travels in the request and the host applies it. */
  ext_host *h = NULL;
  assert(open_host("flood", &h) == EXT_OK);
  assert(call(h, "stream", &e, 5000, 10) == EXT_OK); /* burst of 20 events */
  texts = 0;
  for (size_t i = 0; i < e.count; i++)
    texts += strstr(e.items[i], "\"text\"") != NULL;
  assert(texts >= 20 && texts <= 40);
  clear(&e);
  /* Rates beyond the bounds are refused before anything is sent. */
  ext_call_options options = {sizeof(options), 5000, NULL, NULL};
  ext_progress_options big = {sizeof(big), collect, &e, (1u << 20) + 1, 0};
  const char *request =
      "{\"contract\":{\"name\":\"frames.test\",\"version\":\"v1\"},"
      "\"operation\":\"stream\"}";
  ext_buffer out = {0};
  assert(ext_host_call_progress(h, (const uint8_t *)request, strlen(request),
                                &options, &big, &out) == EXT_INVALID);
  big.events_per_second = 0;
  big.bytes_per_second = (1u << 30) + 1;
  assert(ext_host_call_progress(h, (const uint8_t *)request, strlen(request),
                                &options, &big, &out) == EXT_INVALID);
  big.bytes_per_second = 0;
  big.struct_size = 1;
  assert(ext_host_call_progress(h, (const uint8_t *)request, strlen(request),
                                &options, &big, &out) == EXT_INVALID);
  assert(ext_host_get_state(h) == EXT_HOST_READY);
  /* No progress options is plain ext_host_call: events are limited, discarded. */
  assert(ext_host_call_progress(h, (const uint8_t *)request, strlen(request),
                                &options, NULL, &out) == EXT_OK);
  ext_buffer_free(&out);
  ext_host_destroy(h);

  /* Every violation fails the session with a protocol error. */
  expect_violation("undeclared", "plain", EXT_PROTOCOL);
  expect_violation("wrongparent", "stream", EXT_PROTOCOL);
  expect_violation("badkind", "stream", EXT_PROTOCOL);
  expect_violation("streamprogress", "stream", EXT_PROTOCOL);
  expect_violation("overtotal", "stream", EXT_PROTOCOL);
  expect_violation("negative", "stream", EXT_PROTOCOL);
  expect_violation("bigtext", "stream", EXT_PROTOCOL);
  expect_violation("dupkey", "stream", EXT_PROTOCOL);
  expect_violation("unknown", "stream", EXT_PROTOCOL);
  expect_violation("notobject", "stream", EXT_PROTOCOL);
  expect_violation("reverseresponse", "stream", EXT_PROTOCOL);
  expect_violation("nullroot", "stream", EXT_PROTOCOL);
  expect_violation("garbage", "stream", EXT_PROTOCOL);
  expect_violation("wrongid", "stream", EXT_PROTOCOL);
  /* A response for this call that cannot be accepted keeps its own class. */
  expect_violation("wrongversion", "stream", EXT_MISMATCH);
  expect_violation("extrafield", "stream", EXT_INVALID);
  expect_violation("dupresponse", "stream", EXT_INVALID);
  expect_violation("after", "stream", EXT_INVALID);
  /* A reverse request to a host with no services is refused as unavailable, and
   * the call goes on (tests/reverse.c covers the rest). */
  assert(run("reverse", "stream", &e, &state) == EXT_OK &&
         state == EXT_HOST_READY);

  /* A frame before the handshake answers is a violation of the handshake. */
  assert(open_host("hellofirst", &h) == EXT_PROTOCOL);
  ext_host_destroy(h);

  /* A guest that never answers is ended by the deadline, after its events. */
  assert(open_host("stall", &h) == EXT_OK);
  assert(call(h, "stream", &e, 300, 0) == EXT_TIMEOUT);
  assert(e.count == 1 && strstr(e.items[0], "\"text\":\"before\""));
  assert(ext_host_get_state(h) == EXT_HOST_FAILED);
  clear(&e);
  ext_host_destroy(h);
}

static ext_status extension_connect(void *user, uint32_t timeout_ms,
                                    ext_emit emit, void *context) {
  (void)user;
  (void)timeout_ms;
  (void)emit;
  (void)context;
  return EXT_UNSUPPORTED;
}
static ext_status extension_invoke(void *user, const uint8_t *request,
                                   size_t len, uint32_t timeout_ms,
                                   ext_emit emit, void *context) {
  (void)user;
  (void)request;
  (void)len;
  (void)timeout_ms;
  (void)emit;
  (void)context;
  return EXT_UNSUPPORTED;
}
static void extension_close(void *user) { (void)user; }
static void test_descriptors(void) {
  /* Progress and reverse calls are carried by a host that owns the child's
   * pipes; an extension backend hands the engine one response at a time, so it
   * refuses the plugin rather than run it without them. */
  ext_host *h = NULL;
  ext_host_options options = {EXT_HOST_ABI_VERSION,
                              sizeof(options),
                              (const uint8_t *)DESCRIPTOR,
                              strlen(DESCRIPTOR),
                              allow,
                              allow,
                              NULL};
  const char *none[] = {NULL};
  ext_jsonline_process_config process = {sizeof(process), self, none, 0, NULL,
                                         0};
  ext_backend_options backend = {sizeof(backend),
                                 EXT_BACKEND_JSONLINE_PROCESS_CONFIG, &process,
                                 sizeof(process)};
  assert(ext_host_create(&options, &backend, &h) == EXT_OK);
  ext_host_destroy(h);
  const char *reverse =
      "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"a\","
      "\"revision\":\"r\"},\"contracts\":[{\"name\":\"c\",\"version\":\"v1\","
      "\"operations\":[{\"name\":\"o\"}]}],\"reverse\":[{\"name\":\"h\","
      "\"version\":\"v1\",\"operations\":[{\"name\":\"s\"}]}]}";
  options.descriptor = (const uint8_t *)reverse;
  options.descriptor_len = strlen(reverse);
  assert(ext_host_create(&options, &backend, &h) == EXT_OK);
  ext_host_destroy(h);
  /* An extension backend carries neither: it is refused for a descriptor that
   * declares them, and runs one that does not. */
  ext_backend_extension extension = {sizeof(extension), 0, NULL, extension_connect,
                                     extension_invoke, extension_close,
                                     extension_close};
  ext_backend_options extended = {sizeof(extended), EXT_BACKEND_EXTENSION,
                                  &extension, sizeof(extension)};
  options.descriptor = (const uint8_t *)DESCRIPTOR;
  options.descriptor_len = strlen(DESCRIPTOR);
  assert(ext_host_create(&options, &extended, &h) == EXT_UNSUPPORTED);
  options.descriptor = (const uint8_t *)reverse;
  options.descriptor_len = strlen(reverse);
  assert(ext_host_create(&options, &extended, &h) == EXT_UNSUPPORTED);
  const char *plain =
      "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"a\","
      "\"revision\":\"r\"},\"contracts\":[{\"name\":\"c\",\"version\":\"v1\","
      "\"operations\":[{\"name\":\"o\"}]}]}";
  options.descriptor = (const uint8_t *)plain;
  options.descriptor_len = strlen(plain);
  assert(ext_host_create(&options, &extended, &h) == EXT_OK);
  ext_host_destroy(h);
}

#ifdef _WIN32
static void find_self(const char *argv0) {
  (void)argv0;
  assert(GetModuleFileNameA(NULL, self, sizeof(self)) > 0);
}
#else
static void find_self(const char *argv0) {
  assert(realpath(argv0, self));
}
#endif

int main(int argc, char **argv) {
  if (argc == 3 && !strcmp(argv[1], "--guest"))
    return guest(argv[2]);
  assert(argc == 1);
  find_self(argv[0]);
  test_limiter();
  test_frame_validation();
  test_sink();
  test_descriptors();
  test_calls();
  puts("progress frame checks passed");
  return 0;
}
