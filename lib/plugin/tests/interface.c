/* The library interface: the pure services and the stores behind open, call and close. */
#include "ext_plugin_host.h"
#include "sync.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <direct.h>
#define make_dir(p) _mkdir(p)
#else
#include <sys/stat.h>
#define make_dir(p) mkdir(p, 0700)
#endif

static int failures;
#define CHECK(cond, ...)                                                                           \
  do {                                                                                             \
    if (!(cond)) {                                                                                 \
      fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);                                         \
      fprintf(stderr, __VA_ARGS__);                                                                \
      fprintf(stderr, "\n");                                                                       \
      failures++;                                                                                  \
    }                                                                                              \
  } while (0)

static uint64_t open_json(const char *config, ext_plugin_status want) {
  uint64_t h = 0;
  ext_plugin_status s = ext_plugin_open(config, strlen(config), &h);
  CHECK(s == want, "open %s: %s, want %s", config, ext_plugin_code_name(s), ext_plugin_code_name(want));
  return h;
}

static char *call(uint64_t h, const char *op, const char *in, ext_plugin_status want) {
  char *out = NULL;
  size_t n = 0;
  ext_plugin_status s = ext_plugin_call(h, op, in, in ? strlen(in) : 0, &out, &n);
  CHECK(s == want, "%s %s: %s, want %s", op, in ? in : "", ext_plugin_code_name(s), ext_plugin_code_name(want));
  CHECK(s != EXT_PLUGIN_OK || (out && strlen(out) == n), "%s: OK without output", op);
  CHECK(s == EXT_PLUGIN_OK || !out, "%s: output with an error", op);
  return out;
}

static void expect(uint64_t h, const char *op, const char *in, const char *contains) {
  char *out = call(h, op, in, EXT_PLUGIN_OK);
  CHECK(out && strstr(out, contains), "%s: %s lacks %s", op, out ? out : "(null)", contains);
  ext_plugin_free(out);
}


/* ---- hosts: callbacks as events, answered from another thread ---- */

typedef struct {
  uint64_t host;
  volatile int stop, deny_authorize;
  int verify, authorize, progress, reverse_authorize, reverse;
} answerer;

static uint64_t ticket_after(const char *p) { return strtoull(p, NULL, 10); }

static void *answer_events(void *arg) {
  answerer *a = arg;
  while (!a->stop) {
    char *events = NULL;
    size_t n = 0;
    ext_plugin_status s = ext_plugin_call(a->host, "events", "{\"timeout_ms\":100}", 18, &events, &n);
    if (s) {
      if (s == EXT_PLUGIN_CLOSED || s == EXT_PLUGIN_NOT_FOUND)
        break;
      continue;
    }
    for (const char *p = events; (p = strstr(p, "{\"ticket\":")); p++) {
      uint64_t ticket = ticket_after(p + 10);
      const char *kind = strstr(p, "\"kind\":\"") + 8;
      char reply[256];
      const char *body = NULL;
      if (!strncmp(kind, "verify\"", 7))
        a->verify++, body = "\"allow\":true";
      else if (!strncmp(kind, "authorize\"", 10))
        a->authorize++, body = a->deny_authorize ? "\"allow\":false" : "\"allow\":true";
      else if (!strncmp(kind, "reverse_authorize\"", 18))
        a->reverse_authorize++, body = "\"allow\":true";
      else if (!strncmp(kind, "reverse\"", 8))
        a->reverse++, body = "\"payload\":{\"signed\":true}";
      else if (!strncmp(kind, "progress\"", 9))
        a->progress++;
      if (body) {
        int w = snprintf(reply, sizeof reply, "{\"ticket\":%llu,%s}", (unsigned long long)ticket, body);
        char *out = NULL;
        size_t on = 0;
        CHECK(ext_plugin_call(a->host, "answer", reply, (size_t)w, &out, &on) == EXT_PLUGIN_OK, "answer %s", reply);
        ext_plugin_free(out);
      }
    }
    ext_plugin_free(events);
  }
  return NULL;
}

static const char conformance[] =
    "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"ctx/conformance\",\"revision\":\"fixture-1\"},"
    "\"contracts\":[{\"name\":\"ext.conformance\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"},"
    "{\"name\":\"wait\"},{\"name\":\"private-error\"},{\"name\":\"public-error\"}]}]}";

#define REVERSE_DESCRIPTOR                                                                          \
  "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"reverse/guest\",\"revision\":\"r1\"},"   \
  "\"contracts\":[{\"name\":\"reverse.test\",\"version\":\"v1\",\"operations\":[{\"name\":\"run\","  \
  "\"progress\":true}]}],\"reverse\":[{\"name\":\"host.svc\",\"version\":\"v1\",\"operations\":["    \
  "{\"name\":\"sign\",\"surface\":\"signing\"},{\"name\":\"secret\"},{\"name\":\"empty\"},"         \
  "{\"name\":\"fail\"},{\"name\":\"twice\"},{\"name\":\"none\"},{\"name\":\"errok\"},"              \
  "{\"name\":\"errcode\"},{\"name\":\"errmsg\"},{\"name\":\"errretry\"},{\"name\":\"errretryok\"},"  \
  "{\"name\":\"badjson\"},{\"name\":\"big\"},{\"name\":\"slow\"},{\"name\":\"slowerr\"},"           \
  "{\"name\":\"gate\"},{\"name\":\"hang\"}]}]}"

static void hosts(void) {
  char config[8192];
  /* A shared-library guest whose verify and authorize policies are answered as events. */
  snprintf(config, sizeof config,
           "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"shared_library\",\"path\":\"%s\"},"
           "\"verify\":\"event\",\"authorize\":\"event\",\"event_timeout_ms\":5000}",
           conformance, EXT_TEST_GUEST);
  answerer a = {0};
  a.host = open_json(config, EXT_PLUGIN_OK);
  pthread_t t;
  pthread_create(&t, NULL, answer_events, &a);
  ext_plugin_free(call(a.host, "start", "{\"timeout_ms\":5000}", EXT_PLUGIN_OK));
  expect(a.host, "state", NULL, "\"ready\"");
  expect(a.host, "call",
         "{\"call\":{\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"echo\","
         "\"payload\":7},\"timeout_ms\":5000}",
         "\"echoed\":true");
  a.deny_authorize = 1;
  char *denied = call(a.host, "call",
                      "{\"call\":{\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"echo\","
                      "\"payload\":7},\"timeout_ms\":5000}",
                      EXT_PLUGIN_DENIED);
  ext_plugin_free(denied);
  ext_plugin_free(call(a.host, "answer", "{\"ticket\":999999,\"allow\":true}", EXT_PLUGIN_NOT_FOUND));
  ext_plugin_free(call(a.host, "answer", "{\"ticket\":1}", EXT_PLUGIN_INVALID));
  CHECK(ext_plugin_close(a.host) == EXT_PLUGIN_OK, "close the host");
  a.stop = 1;
  pthread_join(t, NULL);
  CHECK(a.verify == 1 && a.authorize == 2, "policies asked: verify %d, authorize %d", a.verify, a.authorize);

  /* A process guest that reports progress and calls its host back while a call runs. */
  snprintf(config, sizeof config,
           "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"process\",\"executable\":\"%s\","
           "\"arguments\":[\"--guest\",\"interleave\"]},\"verify\":\"allow\",\"authorize\":\"allow\","
           "\"services\":{\"timeout_ms\":5000}}",
           REVERSE_DESCRIPTOR, EXT_REVERSE_GUEST);
  answerer r = {0};
  r.host = open_json(config, EXT_PLUGIN_OK);
  pthread_create(&t, NULL, answer_events, &r);
  ext_plugin_free(call(r.host, "start", "{\"timeout_ms\":5000}", EXT_PLUGIN_OK));
  expect(r.host, "call",
         "{\"call\":{\"contract\":{\"name\":\"reverse.test\",\"version\":\"v1\"},\"operation\":\"run\","
         "\"payload\":{}},\"progress\":{},\"timeout_ms\":5000}",
         "\"signed\":true");
  CHECK(ext_plugin_close(r.host) == EXT_PLUGIN_OK, "close the reverse host");
  r.stop = 1;
  pthread_join(t, NULL);
  CHECK(r.reverse_authorize == 1 && r.reverse == 1, "reverse asked: authorize %d, handle %d", r.reverse_authorize,
        r.reverse);
  CHECK(r.progress == 2, "progress events: %d", r.progress);
  open_json("{\"object\":\"host\",\"descriptor\":{},\"backend\":{\"kind\":\"process\"},\"verify\":\"allow\","
            "\"authorize\":\"allow\"}",
            EXT_PLUGIN_INVALID);
  open_json("{\"object\":\"host\",\"backend\":{\"kind\":\"shared_library\",\"path\":\"/x\"},\"verify\":\"maybe\"}",
            EXT_PLUGIN_INVALID);
}

int main(int argc, char **argv) {
  if (argc < 2) {
    fprintf(stderr, "usage: %s <work-dir>\n", argv[0]);
    return 2;
  }
  const char *work = argv[1];
  make_dir(work);
  char root[1024], user[1024], system_root[1024], trust[1024], config[4096], in[4096];
  snprintf(root, sizeof root, "%s/store", work);
  snprintf(user, sizeof user, "%s/user", work);
  snprintf(system_root, sizeof system_root, "%s/system", work);
  snprintf(trust, sizeof trust, "%s/trust.json", work);
  remove(trust);

  CHECK(ext_plugin_abi_version() == EXT_PLUGIN_ABI_VERSION, "abi version");

  /* Handle 0: a pure service, the digests and the trust records. */
  expect(0, "protocol.negotiate", "{\"preferred\":[\"a\",\"b\"],\"offered\":[\"b\"]}", "\"b\"");
  ext_plugin_free(call(0, "descriptor.match", "{}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(0, "no.such.service", "{}", EXT_PLUGIN_UNSUPPORTED));
  expect(0, "code_name", "{\"code\":10}", "\"not found\"");
  make_dir(root);
  snprintf(in, sizeof in, "{\"root\":\"%s\"}", root);
  char *digest = call(0, "directory_digest", in, EXT_PLUGIN_OK);
  CHECK(digest && strlen(digest) == 66, "a directory digest: %s", digest ? digest : "(null)");
  if (digest) {
    snprintf(in, sizeof in, "{\"path\":\"%s\",\"scope\":\"demo\",\"id\":\"example/demo\",\"digest\":%s}", trust, digest);
    expect(0, "trust_state", in, "\"untrusted\"");
    expect(0, "trust_record", in, "null");
    expect(0, "trust_state", in, "\"trusted\"");
    snprintf(in, sizeof in, "{\"path\":\"%s\",\"scope\":\"demo\",\"id\":\"example/demo\"}", trust);
    expect(0, "trust_revoke", in, "null");
  }
  ext_plugin_free(digest);
  ext_plugin_free(call(0, "trust_state", "{\"path\":\"x\",\"scope\":\"s\",\"id\":\"i\",\"digest\":\"zz\"}", EXT_PLUGIN_INVALID));

  /* A store and a layered view over an empty root. */
  snprintf(config, sizeof config, "{\"object\":\"store\",\"root\":\"%s\"}", root);
  uint64_t store = open_json(config, EXT_PLUGIN_OK);
  expect(store, "ids", NULL, "[]");
  expect(store, "list", NULL, "[]");
  expect(store, "verify", NULL, "[]");
  expect(store, "list_unverified", NULL, "\"packages\"");
  ext_plugin_free(call(store, "load", "{\"id\":\"example/missing\"}", EXT_PLUGIN_NOT_FOUND));
  ext_plugin_free(call(store, "install", "{}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(store, "nothing", "{}", EXT_PLUGIN_UNSUPPORTED));
  ext_plugin_free(call(store, "ids", "[1]", EXT_PLUGIN_INVALID));
  make_dir(user);
  make_dir(system_root);
  snprintf(config, sizeof config, "{\"object\":\"layered\",\"roots\":[\"%s\",\"%s\"]}", user, system_root);
  uint64_t layered = open_json(config, EXT_PLUGIN_OK);
  expect(layered, "list", NULL, "[]");
  ext_plugin_free(call(layered, "load", "{\"id\":\"example/missing\"}", EXT_PLUGIN_NOT_FOUND));
  CHECK(ext_plugin_close(layered) == EXT_PLUGIN_OK, "close the layered view");
  CHECK(ext_plugin_close(store) == EXT_PLUGIN_OK, "close the store");
  CHECK(ext_plugin_close(store) == EXT_PLUGIN_NOT_FOUND, "close a closed handle");
  ext_plugin_free(call(store, "ids", NULL, EXT_PLUGIN_NOT_FOUND));
  open_json("{\"object\":\"store\"}", EXT_PLUGIN_INVALID);
  open_json("{\"object\":\"layered\",\"roots\":[]}", EXT_PLUGIN_INVALID);
  open_json("{\"object\":\"nothing\"}", EXT_PLUGIN_INVALID);
  open_json("not json", EXT_PLUGIN_INVALID);

  hosts();

  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("library interface checks passed\n");
  return 0;
}
