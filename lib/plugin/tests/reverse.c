/* Reverse calls: the frame validator, the answer builder, then the host serving a
 * scripted guest that calls it back. The guest is this program started with
 * --guest <mode>; each mode asks for one thing, so every refusal code, every way
 * a handler can answer and every framing violation has a case. The Go
 * differential tests in pkg/plugin-go/cengine run the same behavior through the
 * pure-Go session and compare. */
#define _XOPEN_SOURCE 700 /* realpath, under -std=c11 on glibc */
#include "progress.h"
#include "reverse.h"
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
  "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"reverse/guest\","  \
  "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"reverse.test\","           \
  "\"version\":\"v1\",\"operations\":[{\"name\":\"run\",\"progress\":true}]}]," \
  "\"reverse\":[{\"name\":\"host.svc\",\"version\":\"v1\",\"operations\":["    \
  "{\"name\":\"sign\",\"surface\":\"signing\"},{\"name\":\"secret\"},"        \
  "{\"name\":\"empty\"},{\"name\":\"fail\"},{\"name\":\"twice\"},"            \
  "{\"name\":\"none\"},{\"name\":\"errok\"},{\"name\":\"errcode\"},"          \
  "{\"name\":\"errmsg\"},{\"name\":\"errretry\"},{\"name\":\"errretryok\"},"  \
  "{\"name\":\"badjson\"},{\"name\":\"big\"},{\"name\":\"slow\"},"            \
  "{\"name\":\"slowerr\"}]}]}"
#define CONTRACT "\"contract\":{\"name\":\"host.svc\",\"version\":\"v1\"}"
#define FUTURE "2099-01-01T00:00:00Z"

static char line[1 << 16];
static void put(const char *s) {
  fputs(s, stdout);
  fflush(stdout);
}
static int field(const char *text, const char *key, char *out, size_t size) {
  char pattern[64];
  snprintf(pattern, sizeof(pattern), "\"%s\":\"", key);
  const char *at = strstr(text, pattern);
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
/* Sends one reverse request, reads the host's answer line into reply and strips
 * its newline. An empty reply means the host closed the connection. */
static void reverse(const char *id_field, const char *parent, const char *op,
                    const char *deadline, const char *payload, char *reply,
                    size_t size) {
  char clause[128] = "";
  if (parent)
    snprintf(clause, sizeof(clause), ",\"parent\":\"%s\"", parent);
  printf("{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\","
         "%s%s," CONTRACT ",\"operation\":\"%s\",\"deadline\":%s%s%s}\n",
         id_field, clause, op, deadline, payload ? ",\"payload\":" : "",
         payload ? payload : "");
  fflush(stdout);
  reply[0] = 0;
  if (!fgets(reply, (int)size, stdin))
    reply[0] = 0;
  reply[strcspn(reply, "\r\n")] = 0;
}
static void finish(const char *id, const char *reply) {
  printf("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\",\"payload\":{"
         "\"done\":true,\"reply\":%s}}\n",
         id, reply[0] ? reply : "null");
  fflush(stdout);
}

static int guest(const char *mode) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  char reply[1 << 14], id[64];
  if (!fgets(line, sizeof(line), stdin))
    return 0;
  if (!strcmp(mode, "hello-parent")) {
    reverse("\"id\":\"g1\"", "hello", "sign", "\"" FUTURE "\"", NULL, reply,
            sizeof(reply));
  } else if (!strcmp(mode, "hello-noparent")) {
    reverse("\"id\":\"g1\"", NULL, "sign", "\"" FUTURE "\"", NULL, reply,
            sizeof(reply));
  }
  printf("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"payload\":%s}\n",
         DESCRIPTOR);
  fflush(stdout);
  while (fgets(line, sizeof(line), stdin)) {
    if (!field(line, "id", id, sizeof(id)))
      return 1;
    reply[0] = 0;
    if (!strncmp(mode, "call:", 5)) {
      reverse("\"id\":\"g1\"", id, mode + 5, "\"" FUTURE "\"", "{\"n\":1}",
              reply, sizeof(reply));
    } else if (!strcmp(mode, "bigrequest")) {
      char big[512];
      snprintf(big, sizeof(big), "\"%0100d\"", 0);
      reverse("\"id\":\"g1\"", id, "sign", "\"" FUTURE "\"", big, reply,
              sizeof(reply));
    } else if (!strcmp(mode, "expired")) {
      reverse("\"id\":\"g1\"", id, "sign", "\"2000-01-01T00:00:00Z\"", NULL,
              reply, sizeof(reply));
    } else if (!strcmp(mode, "noparent")) {
      reverse("\"id\":\"g1\"", NULL, "sign", "\"" FUTURE "\"", NULL, reply,
              sizeof(reply));
    } else if (!strcmp(mode, "wrongparent")) {
      reverse("\"id\":\"g1\"", "999", "sign", "\"" FUTURE "\"", NULL, reply,
              sizeof(reply));
    } else if (!strcmp(mode, "twice")) {
      char first[1 << 12];
      reverse("\"id\":\"g1\"", id, "sign", "\"" FUTURE "\"", "1", first,
              sizeof(first));
      reverse("\"id\":\"g2\"", id, "sign", "\"" FUTURE "\"", "2", reply,
              sizeof(reply));
      printf("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"%s\",\"payload\":{"
             "\"done\":true,\"first\":%s,\"reply\":%s}}\n",
             id, first, reply);
      fflush(stdout);
      continue;
    } else if (!strcmp(mode, "interleave")) {
      printf("{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\","
             "\"parent\":\"%s\",\"kind\":\"log\",\"text\":\"one\"}\n",
             id);
      reverse("\"id\":\"g1\"", id, "sign", "\"" FUTURE "\"", NULL, reply,
              sizeof(reply));
      printf("{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\","
             "\"parent\":\"%s\",\"kind\":\"log\",\"text\":\"two\"}\n",
             id);
    } else if (!strncmp(mode, "bad-", 4)) {
      /* A frame that breaks the rules; the host closes the connection. */
      if (!strcmp(mode, "bad-id"))
        reverse("\"id\":\"!!\"", id, "sign", "\"" FUTURE "\"", NULL, reply,
                sizeof(reply));
      else if (!strcmp(mode, "bad-noid"))
        reverse("\"x\":1", id, "sign", "\"" FUTURE "\"", NULL, reply,
                sizeof(reply));
      else if (!strcmp(mode, "bad-deadline"))
        reverse("\"id\":\"g1\"", id, "sign", "\"soon\"", NULL, reply,
                sizeof(reply));
      else if (!strcmp(mode, "bad-nulldeadline"))
        reverse("\"id\":\"g1\"", id, "sign", "null", NULL, reply,
                sizeof(reply));
      else if (!strcmp(mode, "bad-operation"))
        reverse("\"id\":\"g1\"", id, "Sign", "\"" FUTURE "\"", NULL, reply,
                sizeof(reply));
      else if (!strcmp(mode, "bad-extra"))
        reverse("\"id\":\"g1\",\"extra\":1", id, "sign", "\"" FUTURE "\"", NULL,
                reply, sizeof(reply));
      else if (!strcmp(mode, "bad-dup"))
        reverse("\"id\":\"g1\",\"id\":\"g2\"", id, "sign", "\"" FUTURE "\"",
                NULL, reply, sizeof(reply));
      else if (!strcmp(mode, "bad-parentnumber"))
        put("{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\","
            "\"id\":\"g1\",\"parent\":5," CONTRACT ",\"operation\":\"sign\","
            "\"deadline\":\"" FUTURE "\"}\n");
      else if (!strcmp(mode, "bad-version"))
        put("{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v2\","
            "\"id\":\"g1\"," CONTRACT ",\"operation\":\"sign\",\"deadline\":\""
            FUTURE "\"}\n");
      else if (!strcmp(mode, "bad-nocontract"))
        put("{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\","
            "\"id\":\"g1\",\"operation\":\"sign\",\"deadline\":\"" FUTURE
            "\"}\n");
      else if (!strcmp(mode, "bad-response"))
        put("{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\","
            "\"id\":\"g1\",\"payload\":null}\n");
      continue;
    } else {
      return 2;
    }
    finish(id, reply);
  }
  return 0;
}

/* ---- the host side ---- */

static char self[4096];
static int32_t allow(void *u, const uint8_t *p, size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return 0;
}

/* What the hooks saw and what they are told to do. */
static struct {
  int authorized, handled;
  char request[4096];
  uint32_t timeout_ms;
  const void *value;
  const ext_cancel *cancel;
  int sleep_ms;
} seen;
static int token;

static int32_t authorize(void *user, const ext_call_options *o,
                         const uint8_t *json, size_t len) {
  (void)user;
  seen.authorized++;
  assert(len < sizeof(seen.request));
  memcpy(seen.request, json, len);
  seen.request[len] = 0;
  seen.timeout_ms = o->timeout_ms;
  seen.value = o->value;
  seen.cancel = o->cancel;
  return strstr(seen.request, "\"operation\":\"secret\"") ? 1 : 0;
}
static ext_status handle(void *user, const ext_call_options *o,
                         const uint8_t *json, size_t len, ext_reverse_emit emit,
                         void *sink) {
  (void)user;
  (void)o;
  char text[4096];
  assert(len < sizeof(text));
  memcpy(text, json, len);
  text[len] = 0;
  seen.handled++;
  const char *operation = strstr(text, "\"operation\":\"") + 13;
#define IS(name) (!strncmp(operation, name "\"", strlen(name) + 1))
#define PAYLOAD(s) emit(sink, EXT_HOST_REVERSE_PAYLOAD, (const uint8_t *)(s), strlen(s))
#define FAIL(s) emit(sink, EXT_HOST_REVERSE_PUBLIC_ERROR, (const uint8_t *)(s), strlen(s))
  if (IS("sign"))
    return PAYLOAD("\"signed\"");
  if (IS("empty"))
    return emit(sink, EXT_HOST_REVERSE_PAYLOAD, NULL, 0);
  if (IS("fail"))
    return EXT_IO;
  if (IS("none"))
    return EXT_OK;
  if (IS("twice")) {
    (void)PAYLOAD("1");
    return PAYLOAD("2");
  }
  if (IS("errok"))
    return FAIL("{\"code\":\"policy\",\"message\":\"refused for you\","
                "\"retryAfterMilliseconds\":5}");
  if (IS("errcode"))
    return FAIL("{\"code\":\"Policy\",\"message\":\"x\"}");
  if (IS("errretry"))
    return FAIL("{\"code\":\"policy\",\"message\":\"x\","
                "\"retryAfterMilliseconds\":31536000001}");
  if (IS("errretryok"))
    return FAIL("{\"code\":\"policy\",\"message\":\"x\","
                "\"retryAfterMilliseconds\":31536000000}");
  if (IS("errmsg")) {
    static char big[4400];
    int n = snprintf(big, sizeof(big), "{\"code\":\"policy\",\"message\":\"");
    memset(big + n, 'a', 4097);
    strcpy(big + n + 4097, "\"}");
    return FAIL(big);
  }
  if (IS("badjson"))
    return PAYLOAD("{bad");
  if (IS("big"))
    return PAYLOAD("\"0123456789012345678901234567890123456789012345678901234567890123456789\"");
  if (IS("slow") || IS("slowerr")) {
    int64_t until = ext_mono_ns() + (int64_t)seen.sleep_ms * 1000000;
    while (ext_mono_ns() < until) {
    }
    return IS("slow") ? PAYLOAD("\"late\"")
                      : FAIL("{\"code\":\"policy\",\"message\":\"late\"}");
  }
  return EXT_UNSUPPORTED;
}

static ext_status open_host(const char *mode, int services, uint32_t timeout_ms,
                            uint32_t max_payload, ext_host **out) {
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
  if (services) {
    ext_host_services configured = {sizeof(configured),
                                    NULL,
                                    authorize,
                                    handle,
                                    timeout_ms,
                                    max_payload,
                                    0};
    s = ext_host_set_services(*out, &configured);
    assert(s == EXT_OK);
  }
  memset(&seen.request, 0, sizeof(seen.request));
  seen.authorized = seen.handled = 0;
  s = ext_host_start(*out, 5000);
  if (s && strncmp(mode, "hello", 5)) {
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
typedef struct {
  char text[8][64];
  size_t count;
} events;
static void collect(void *user, const uint8_t *json, size_t len) {
  events *e = user;
  assert(e->count < 8 && len < 64);
  memcpy(e->text[e->count], json, len);
  e->text[e->count++][len] = 0;
}
/* Calls "run" once and returns its status; the response body is in body. */
static ext_status call(ext_host *h, char *body, size_t size, events *e) {
  const char *request =
      "{\"contract\":{\"name\":\"reverse.test\",\"version\":\"v1\"},"
      "\"operation\":\"run\",\"payload\":{}}";
  ext_call_options options = {sizeof(options), 5000, NULL, &token};
  ext_progress_options progress = {sizeof(progress), e ? collect : NULL, e, 0,
                                   0};
  ext_buffer out = {0};
  ext_status s = ext_host_call_progress(
      h, (const uint8_t *)request, strlen(request), &options, &progress, &out);
  if (!s && body) {
    assert(out.len < size);
    memcpy(body, out.data, out.len);
    body[out.len] = 0;
  }
  ext_buffer_free(&out);
  return s;
}
/* Runs a scripted guest and returns the call's status and response body. */
static ext_status run(const char *mode, int services, uint32_t timeout_ms,
                      uint32_t max_payload, char *body, size_t size,
                      ext_host_state *state) {
  ext_host *h = NULL;
  ext_status s = open_host(mode, services, timeout_ms, max_payload, &h);
  if (s == EXT_OK)
    s = call(h, body, size, NULL);
  if (state)
    *state = ext_host_get_state(h);
  ext_host_destroy(h);
  return s;
}
static char body[1 << 14];
/* The reverse call is answered and the forward call completes. */
static void answered(const char *mode, int services, const char *want) {
  ext_host_state state;
  ext_status s = run(mode, services, 150, 64, body, sizeof(body), &state);
  if (s != EXT_OK || state != EXT_HOST_READY || !strstr(body, want))
    fprintf(stderr, "%s: status %s, state %d, body %s (want %s)\n", mode,
            ext_host_status_string(s), state, body, want);
  assert(s == EXT_OK && state == EXT_HOST_READY && strstr(body, want));
}
static void violation(const char *mode, ext_status want) {
  ext_host_state state;
  ext_status s = run(mode, 1, 150, 64, body, sizeof(body), &state);
  if (s != want || state != EXT_HOST_FAILED)
    fprintf(stderr, "%s: status %s (want %s), state %d\n", mode,
            ext_host_status_string(s), ext_host_status_string(want), state);
  assert(s == want && state == EXT_HOST_FAILED);
}

/* ---- tests ---- */

static ext_status request_frame(const char *json) {
  yyjson_doc *doc = ext_parse((const uint8_t *)json, strlen(json));
  assert(doc);
  ext_reverse_request request;
  ext_status s = ext_reverse_request_frame(yyjson_doc_get_root(doc), &request);
  if (!s)
    assert(yyjson_is_str(request.id) && request.deadline_ms > 0);
  yyjson_doc_free(doc);
  return s;
}
#define RV "{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\","
static void test_request_frames(void) {
  assert(request_frame(RV "\"id\":\"g1\",\"parent\":\"1\"," CONTRACT
                          ",\"operation\":\"sign\",\"deadline\":\"" FUTURE
                          "\",\"payload\":{\"a\":[1,2]}}") == EXT_OK);
  /* A parent that is absent, null or empty is no parent. */
  yyjson_doc *doc = ext_parse(
      (const uint8_t *)RV "\"id\":\"g1\",\"parent\":\"\"," CONTRACT
                          ",\"operation\":\"sign\",\"deadline\":\"" FUTURE "\"}",
      strlen(RV "\"id\":\"g1\",\"parent\":\"\"," CONTRACT
                ",\"operation\":\"sign\",\"deadline\":\"" FUTURE "\"}"));
  ext_reverse_request r;
  assert(doc && ext_reverse_request_frame(yyjson_doc_get_root(doc), &r) ==
                    EXT_OK &&
         !r.parent && !r.payload);
  yyjson_doc_free(doc);
  assert(request_frame(RV "\"id\":\"g1\",\"parent\":null," CONTRACT
                          ",\"operation\":\"sign\",\"deadline\":\"" FUTURE
                          "\",\"payload\":null}") == EXT_OK);
  assert(request_frame(RV "\"id\":\"g1\"," CONTRACT
                          ",\"operation\":\"a.b/c-d_e\",\"deadline\":"
                          "\"2099-01-01T00:00:00.123456789+01:00\"}") == EXT_OK);
  static const char *const bad[] = {
      "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\"}",
      RV "\"id\":\"\"," CONTRACT ",\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"-a\"," CONTRACT ",\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":7," CONTRACT ",\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\",\"parent\":\"-\"," CONTRACT ",\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\",\"parent\":1," CONTRACT ",\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\",\"contract\":null,\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\",\"contract\":{\"name\":\"a\"},\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\",\"contract\":{\"name\":\"A\",\"version\":\"v1\"},\"operation\":\"o\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"O\",\"deadline\":\"" FUTURE "\"}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"o\"}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"o\",\"deadline\":null}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"o\",\"deadline\":\"0001-01-01T00:00:00Z\"}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"o\",\"deadline\":\"2099-01-01 00:00:00\"}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"o\",\"deadline\":5}",
      RV "\"id\":\"g\"," CONTRACT ",\"operation\":\"o\",\"deadline\":\"" FUTURE "\",\"extra\":1}",
  };
  for (size_t i = 0; i < sizeof(bad) / sizeof(*bad); i++) {
    if (request_frame(bad[i]) != EXT_PROTOCOL)
      fprintf(stderr, "accepted: %s\n", bad[i]);
    assert(request_frame(bad[i]) == EXT_PROTOCOL);
  }
}

/* Parses an answer line: returns the frame's top-level field. */
static yyjson_doc *line_doc(char *text, size_t len) {
  yyjson_doc *doc = ext_parse((const uint8_t *)text, len);
  assert(doc);
  return doc;
}
static void expect_error(char *text, size_t len, const char *code,
                         const char *message, int64_t retry) {
  yyjson_doc *doc = line_doc(text, len);
  yyjson_val *root = yyjson_doc_get_root(doc),
             *error = yyjson_obj_get(root, "error");
  assert(yyjson_equals_str(yyjson_obj_get(root, "frame"), "reverse_response"));
  assert(yyjson_equals_str(yyjson_obj_get(root, "apiVersion"), "ext.plugin/v1"));
  assert(yyjson_equals_str(yyjson_obj_get(root, "id"), "g7"));
  assert(!yyjson_obj_get(root, "payload"));
  if (!yyjson_equals_str(yyjson_obj_get(error, "code"), code) ||
      !yyjson_equals_str(yyjson_obj_get(error, "message"), message))
    fprintf(stderr, "answer: %.*s (want %s: %s)\n", (int)len, text, code,
            message);
  assert(yyjson_equals_str(yyjson_obj_get(error, "code"), code));
  assert(yyjson_equals_str(yyjson_obj_get(error, "message"), message));
  yyjson_val *hint = yyjson_obj_get(error, "retryAfterMilliseconds");
  int64_t milliseconds = 0;
  assert(retry ? hint && ext_int64(hint, &milliseconds) &&
                     milliseconds == retry
               : !hint);
  yyjson_doc_free(doc);
}
static void test_answers(void) {
  size_t len;
  char *line;
  const char *payload = "{\"a\":[1,2.50,\"x\"]}";
  line = ext_reverse_answer("g7", 2, EXT_HOST_REVERSE_PAYLOAD,
                            (const uint8_t *)payload, strlen(payload), 1000,
                            &len);
  assert(line &&
         !strcmp(line, "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext."
                       "plugin/v1\",\"id\":\"g7\",\"payload\":{\"a\":[1,2.50,"
                       "\"x\"]}}"));
  free(line);
  /* An empty payload is null. */
  line = ext_reverse_answer("g7", 2, EXT_HOST_REVERSE_PAYLOAD, NULL, 0, 1000,
                            &len);
  assert(line && strstr(line, "\"payload\":null"));
  free(line);
  /* Over the bound, not a valid value, not a value at all. */
  line = ext_reverse_answer("g7", 2, EXT_HOST_REVERSE_PAYLOAD,
                            (const uint8_t *)payload, strlen(payload), 5,
                            &len);
  expect_error(line, len, "too_large", "reverse call refused", 0);
  free(line);
  line = ext_reverse_answer("g7", 2, EXT_HOST_REVERSE_PAYLOAD,
                            (const uint8_t *)"{\"a\":1,\"a\":2}", 13, 1000,
                            &len);
  expect_error(line, len, "operation_failed", "host service failed", 0);
  free(line);
  line = ext_reverse_answer("g7", 2, 7, (const uint8_t *)"1", 1, 1000, &len);
  expect_error(line, len, "operation_failed", "host service failed", 0);
  free(line);
  /* A public error is passed on when a peer would take it. */
  const char *good = "{\"code\":\"policy\",\"message\":\"no\","
                     "\"retryAfterMilliseconds\":31536000000}";
  line = ext_reverse_answer("g7", 2, EXT_HOST_REVERSE_PUBLIC_ERROR,
                            (const uint8_t *)good, strlen(good), 1000, &len);
  expect_error(line, len, "policy", "no", 31536000000LL);
  free(line);
  static const char *const bad[] = {
      "{\"code\":\"Policy\",\"message\":\"x\"}",
      "{\"code\":\"\",\"message\":\"x\"}",
      "{\"message\":\"x\"}",
      "{\"code\":\"policy\",\"message\":5}",
      "{\"code\":\"policy\",\"message\":\"x\",\"retryAfterMilliseconds\":-1}",
      ("{\"code\":\"policy\",\"message\":\"x\",\"retryAfterMilliseconds\":"
       "31536000001}"),
      "{\"code\":\"policy\",\"message\":\"x\",\"retryAfterMilliseconds\":1.5}",
      "{\"code\":\"policy\",\"message\":\"x\",\"extra\":1}",
      "[1]",
      "7",
  };
  for (size_t i = 0; i < sizeof(bad) / sizeof(*bad); i++) {
    line = ext_reverse_answer("g7", 2, EXT_HOST_REVERSE_PUBLIC_ERROR,
                              (const uint8_t *)bad[i], strlen(bad[i]), 1000,
                              &len);
    expect_error(line, len, "operation_failed", "host service failed", 0);
    free(line);
  }
  /* A missing message is empty; a zero retry hint is left out. */
  const char *bare = "{\"code\":\"policy\",\"retryAfterMilliseconds\":0}";
  line = ext_reverse_answer("g7", 2, EXT_HOST_REVERSE_PUBLIC_ERROR,
                            (const uint8_t *)bare, strlen(bare), 1000, &len);
  expect_error(line, len, "policy", "", 0);
  free(line);
  line = ext_reverse_refusal("g7", 2, EXT_REVERSE_BUSY, &len);
  expect_error(line, len, "busy", "reverse call refused", 0);
  free(line);
}

static void test_calls(void) {
  ext_host_state state;
  /* An answer, with the request the hooks saw. */
  answered("call:sign", 1, "\"reply\":{\"frame\":\"reverse_response\"");
  assert(strstr(body, "\"id\":\"g1\",\"payload\":\"signed\""));
  assert(seen.authorized == 1 && seen.handled == 1);
  assert(strstr(seen.request, "\"apiVersion\":\"ext.plugin/v1\""));
  assert(strstr(seen.request, "\"id\":\"g1\""));
  assert(strstr(seen.request, "\"plugin\":{\"id\":\"reverse/guest\","
                              "\"revision\":\"r1\"}"));
  assert(strstr(seen.request, "\"contract\":{\"name\":\"host.svc\",\"version\":"
                              "\"v1\"}"));
  assert(strstr(seen.request, "\"surface\":\"signing\""));
  assert(strstr(seen.request, "\"deadline\":\"" FUTURE "\""));
  assert(strstr(seen.request, "\"payload\":{\"n\":1}"));
  assert(strstr(seen.request, "\"parent\":\"1\"")); /* the call in flight */
  assert(seen.value == &token && seen.cancel == NULL);
  assert(seen.timeout_ms > 0 && seen.timeout_ms <= 150);
  answered("call:empty", 1, "\"payload\":null");
  assert(!strstr(seen.request, "\"surface\"")); /* the descriptor names none */
  /* Policy: denied before the handler runs. */
  answered("call:secret", 1, "\"code\":\"denied\"");
  assert(seen.authorized == 1 && seen.handled == 0);
  /* How a handler can answer. */
  answered("call:fail", 1, "\"code\":\"operation_failed\"");
  answered("call:none", 1, "\"code\":\"operation_failed\"");
  answered("call:twice", 1, "\"code\":\"operation_failed\"");
  answered("call:errok", 1, "\"code\":\"policy\",\"message\":\"refused for you\","
                            "\"retryAfterMilliseconds\":5");
  answered("call:errretryok", 1, "\"retryAfterMilliseconds\":31536000000");
  answered("call:errcode", 1, "\"code\":\"operation_failed\"");
  answered("call:errmsg", 1, "\"code\":\"operation_failed\"");
  answered("call:errretry", 1, "\"code\":\"operation_failed\"");
  answered("call:badjson", 1, "\"code\":\"operation_failed\"");
  answered("call:big", 1, "\"code\":\"too_large\"");
  /* Refusals the engine decides. */
  answered("call:nothere", 1, "\"code\":\"reverse_undeclared\"");
  assert(seen.authorized == 0);
  answered("expired", 1, "\"code\":\"expired\"");
  assert(seen.authorized == 0);
  answered("bigrequest", 1, "\"code\":\"too_large\"");
  assert(seen.authorized == 0);
  /* A handler that outlives the reverse deadline answers too late to be used,
   * unless what it answers is its own error. */
  seen.sleep_ms = 400;
  answered("call:slow", 1, "\"code\":\"operation_failed\"");
  answered("call:slowerr", 1, "\"code\":\"policy\"");
  seen.sleep_ms = 0;
  /* No services: every reverse call is unavailable, whatever it names. */
  answered("call:sign", 0, "\"code\":\"reverse_unavailable\"");
  answered("call:nothere", 0, "\"code\":\"reverse_unavailable\"");
  answered("wrongparent", 0, "\"code\":\"reverse_unavailable\"");
  answered("noparent", 0, "\"code\":\"reverse_unavailable\"");
  /* Several calls on one connection, and between progress events. */
  answered("twice", 1, "\"first\":{\"frame\":\"reverse_response\"");
  assert(strstr(body, "\"id\":\"g2\",\"payload\":\"signed\""));
  ext_host *h = NULL;
  events e = {0};
  assert(open_host("interleave", 1, 0, 0, &h) == EXT_OK);
  assert(call(h, body, sizeof(body), &e) == EXT_OK && e.count == 2);
  assert(strstr(e.text[0], "one") && strstr(e.text[1], "two"));
  assert(strstr(body, "\"payload\":\"signed\""));
  e.count = 0;
  assert(call(h, body, sizeof(body), &e) == EXT_OK && e.count == 2);
  assert(strstr(seen.request, "\"parent\":\"2\"")); /* the next call's id */
  ext_host_destroy(h);
  /* The call's cancel signal reaches the hooks. */
  ext_cancel *cancel = NULL;
  assert(ext_cancel_create(&cancel) == EXT_OK);
  assert(open_host("call:sign", 1, 0, 0, &h) == EXT_OK);
  const char *request =
      "{\"contract\":{\"name\":\"reverse.test\",\"version\":\"v1\"},"
      "\"operation\":\"run\"}";
  ext_call_options options = {sizeof(options), 5000, cancel, &token};
  ext_buffer out = {0};
  assert(ext_host_call(h, (const uint8_t *)request, strlen(request), &options,
                       &out) == EXT_OK);
  assert(seen.cancel == cancel);
  ext_buffer_free(&out);
  ext_host_destroy(h);
  ext_cancel_destroy(cancel);

  /* Breaking the framing ends the session. */
  violation("wrongparent", EXT_PROTOCOL);
  violation("bad-id", EXT_PROTOCOL);
  violation("bad-noid", EXT_PROTOCOL);
  violation("bad-deadline", EXT_PROTOCOL);
  violation("bad-nulldeadline", EXT_PROTOCOL);
  violation("bad-operation", EXT_PROTOCOL);
  violation("bad-extra", EXT_PROTOCOL);
  violation("bad-dup", EXT_PROTOCOL);
  violation("bad-parentnumber", EXT_PROTOCOL);
  violation("bad-version", EXT_PROTOCOL);
  violation("bad-nocontract", EXT_PROTOCOL);
  violation("bad-response", EXT_PROTOCOL); /* a host never receives these */
  /* A session-scoped call needs the host to read between calls. */
  violation("noparent", EXT_UNSUPPORTED);
  /* Before the handshake there is no call in flight to name. */
  assert(open_host("hello-parent", 1, 0, 0, &h) == EXT_PROTOCOL);
  assert(ext_host_get_state(h) == EXT_HOST_FAILED);
  ext_host_destroy(h);
  assert(open_host("hello-parent", 0, 0, 0, &h) == EXT_OK); /* unavailable */
  ext_host_destroy(h);
  (void)state;
}

static void test_configuration(void) {
  ext_host *h = NULL;
  ext_host_services s = {sizeof(s), NULL, authorize, handle, 0, 0, 0};
  assert(open_host("call:sign", 0, 0, 0, &h) == EXT_OK);
  /* Configuration is for before the host starts. */
  assert(ext_host_set_services(h, &s) == EXT_INVALID);
  ext_host_destroy(h);
  const char *args[] = {"--guest", "x"};
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
  assert(ext_host_create(&options, &backend, &h) == EXT_OK);
  ext_host_services missing = s;
  missing.handle = NULL;
  assert(ext_host_set_services(h, &missing) == EXT_DENIED);
  missing = s;
  missing.authorize = NULL;
  assert(ext_host_set_services(h, &missing) == EXT_DENIED);
  missing = s;
  missing.struct_size = 1;
  assert(ext_host_set_services(h, &missing) == EXT_INVALID);
  missing = s;
  missing.max_payload_bytes = EXT_HOST_MAX_FRAME + 1;
  assert(ext_host_set_services(h, &missing) == EXT_INVALID);
  missing = s;
  missing.max_in_flight = 1025;
  assert(ext_host_set_services(h, &missing) == EXT_INVALID);
  assert(ext_host_set_services(NULL, &s) == EXT_INVALID);
  assert(ext_host_set_services(h, NULL) == EXT_INVALID);
  s.max_payload_bytes = EXT_HOST_MAX_FRAME;
  s.max_in_flight = 1024;
  assert(ext_host_set_services(h, &s) == EXT_OK);
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
  test_request_frames();
  test_answers();
  test_configuration();
  test_calls();
  puts("reverse call checks passed");
  return 0;
}
