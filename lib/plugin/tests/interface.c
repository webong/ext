/* The library interface: the pure services and the stores behind open, call and close. */
#include "ext_plugin_host.h"
#include "sync.h"
#include <stdatomic.h>
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
  atomic_int hold;     /* events are not taken while set */
  atomic_int progress; /* also read by the thread waiting for a call's events */
  int verify, authorize, reverse_authorize, reverse, tagged, connects, invokes, dropped;
  const char *descriptor;
} answerer;

static uint64_t ticket_after(const char *p) { return strtoull(p, NULL, 10); }

static void *answer_events(void *arg) {
  answerer *a = arg;
  while (!a->stop) {
    if (atomic_load(&a->hold)) {
      struct timespec pause = {0, 1000000};
      nanosleep(&pause, NULL);
      continue;
    }
    char *events = NULL;
    size_t n = 0;
    ext_plugin_status s = ext_plugin_call(a->host, "events", "{\"timeout_ms\":100}", 18, &events, &n);
    if (s) {
      if (s == EXT_PLUGIN_CLOSED || s == EXT_PLUGIN_NOT_FOUND)
        break;
      continue;
    }
    if (strstr(events, "\"kind\":\"dropped\""))
      a->dropped++;
    for (const char *p = events; (p = strstr(p, "\"ticket\":")); p++) {
      uint64_t ticket = ticket_after(p + 9);
      const char *kind = strstr(p, "\"kind\":\"") + 8;
      char reply[8192], transport[4096];
      const char *body = NULL;
      if (!strncmp(kind,"backend_connect\"",16)) {
        a->connects++;
        snprintf(transport,sizeof transport,"\"payload\":%s",a->descriptor);body=transport;
      } else if (!strncmp(kind,"backend_invoke\"",15)) {
        a->invokes++;
        body="\"payload\":{\"apiVersion\":\"ext.io.pc/v1\",\"id\":\"1\",\"payload\":\"transport-answer\"}";
      } else if (!strncmp(kind, "verify\"", 7))
        a->verify++, body = "\"allow\":true";
      else if (!strncmp(kind, "authorize\"", 10))
        a->authorize++, body = a->deny_authorize ? "\"allow\":false" : "\"allow\":true";
      else if (!strncmp(kind, "reverse_authorize\"", 18))
        a->reverse_authorize++, body = "\"allow\":true";
      else if (!strncmp(kind, "reverse\"", 8))
        a->reverse++, body = "\"payload\":{\"signed\":true}";
      else if (!strncmp(kind, "progress\"", 9)) {
        a->progress++;
        const char *end = strstr(p + 1, "{\"seq\":");
        const char *tag = strstr(p, "\"tag\":\"call-1\"");
        if (tag && (!end || tag < end))
          a->tagged++;
      }
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

struct release_args {
  answerer *a;
  struct timespec after;
};
static void *release_after(void *arg) {
  struct release_args *r = arg;
  nanosleep(&r->after, NULL);
  atomic_store(&r->a->hold, 0);
  return NULL;
}

static const char conformance[] =
    "{\"apiVersion\":\"ext.io.pc/v1\",\"identity\":{\"id\":\"ctx/conformance\",\"revision\":\"fixture-1\"},"
    "\"contracts\":[{\"name\":\"ext.conformance\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"},"
    "{\"name\":\"wait\"},{\"name\":\"private-error\"},{\"name\":\"public-error\"}]}]}";

static void event_backend(void) {
  char config[4096], input[256];
  snprintf(config,sizeof config,"{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"events\"},\"verify\":\"event\",\"authorize\":\"event\"}",conformance);
  answerer a={0};a.descriptor=conformance;a.host=open_json(config,EXT_PLUGIN_OK);
  pthread_t thread;pthread_create(&thread,NULL,answer_events,&a);
  ext_plugin_free(call(a.host,"start",NULL,EXT_PLUGIN_OK));
  expect(a.host,"call","{\"call\":{\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"echo\"}}","transport-answer");
  uint64_t registry=open_json("{\"object\":\"registry\"}",EXT_PLUGIN_OK);
  snprintf(input,sizeof input,"{\"name\":\"ext.conformance\",\"version\":\"v1\",\"target\":%llu}",(unsigned long long)a.host);
  ext_plugin_free(call(registry,"register",input,EXT_PLUGIN_OK));
  ext_plugin_free(call(registry,"register",input,EXT_PLUGIN_EXISTS));
  ext_plugin_free(call(registry,"unregister",input,EXT_PLUGIN_OK));
  ext_plugin_free(call(registry,"unregister",input,EXT_PLUGIN_NOT_FOUND));
  CHECK(ext_plugin_close(registry)==EXT_PLUGIN_OK,"close event registry");
  CHECK(ext_plugin_close(a.host)==EXT_PLUGIN_OK,"close event host");
  pthread_join(thread,NULL);
  CHECK(a.connects==1 && a.invokes==1 && a.verify==1 && a.authorize==1,"event backend lifecycle");
}

#define REVERSE_DESCRIPTOR                                                                          \
  "{\"apiVersion\":\"ext.io.pc/v1\",\"identity\":{\"id\":\"reverse/guest\",\"revision\":\"r1\"},"   \
  "\"contracts\":[{\"name\":\"reverse.test\",\"version\":\"v1\",\"operations\":[{\"name\":\"run\","  \
  "\"progress\":true}]}],\"reverse\":[{\"name\":\"host.svc\",\"version\":\"v1\",\"operations\":["    \
  "{\"name\":\"sign\",\"surface\":\"signing\"},{\"name\":\"secret\"},{\"name\":\"empty\"},"         \
  "{\"name\":\"fail\"},{\"name\":\"twice\"},{\"name\":\"none\"},{\"name\":\"errok\"},"              \
  "{\"name\":\"errcode\"},{\"name\":\"errmsg\"},{\"name\":\"errretry\"},{\"name\":\"errretryok\"},"  \
  "{\"name\":\"badjson\"},{\"name\":\"big\"},{\"name\":\"slow\"},{\"name\":\"slowerr\"},"           \
  "{\"name\":\"gate\"},{\"name\":\"hang\"}]}]}"

/* Route handles pin target storage, but never keep a closed provider available. */
static void routing_lifetime(void) {
  char config[8192], descriptor[4096], routes[1024];
  snprintf(config, sizeof config,
    "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"shared_library\",\"path\":\"%s\"},\"verify\":\"allow\",\"authorize\":\"allow\"}", conformance, EXT_TEST_GUEST);
  uint64_t target = open_json(config, EXT_PLUGIN_OK);
  ext_plugin_free(call(target, "start", "{\"timeout_ms\":5000}", EXT_PLUGIN_OK));
  uint64_t second = open_json(config, EXT_PLUGIN_OK);
  ext_plugin_free(call(second, "start", "{\"timeout_ms\":5000}", EXT_PLUGIN_OK));
  snprintf(descriptor, sizeof descriptor,
    "%.*s,\"reverse\":[{\"name\":\"ext.conformance\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"},{\"name\":\"wait\"}]}]}",
    (int)strlen(conformance)-1, conformance);
  snprintf(routes, sizeof routes,
    "{\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"echo\",\"target\":%llu}",
    (unsigned long long)target);
  snprintf(config, sizeof config,
    "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"process\",\"executable\":\"%s\"},\"verify\":\"allow\",\"authorize\":\"allow\",\"routes\":[%s,%s]}", descriptor, EXT_REVERSE_GUEST, routes, routes);
  open_json(config, EXT_PLUGIN_EXISTS);
  snprintf(config, sizeof config,
    "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"process\",\"executable\":\"%s\"},\"verify\":\"allow\",\"authorize\":\"allow\",\"routes\":[%s]}", conformance, EXT_REVERSE_GUEST, routes);
  open_json(config, EXT_PLUGIN_UNSUPPORTED); /* source did not declare reverse */
  snprintf(config, sizeof config,
    "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"process\",\"executable\":\"%s\"},\"verify\":\"allow\",\"authorize\":\"allow\",\"routes\":[%s]}", descriptor, EXT_REVERSE_GUEST, routes);
  uint64_t source = open_json(config, EXT_PLUGIN_OK);
  CHECK(ext_plugin_close(source) == EXT_PLUGIN_OK, "close initial source");
  char single_config[8192];
  memcpy(single_config, config, sizeof single_config);
  snprintf(config, sizeof config,
    "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"process\",\"executable\":\"%s\"},\"verify\":\"allow\",\"authorize\":\"allow\",\"routes\":[%s,{\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"wait\",\"target\":%llu}]}", descriptor, EXT_REVERSE_GUEST, routes, (unsigned long long)second);
  source = open_json(config, EXT_PLUGIN_OK);
  CHECK(ext_plugin_close(source) == EXT_PLUGIN_OK, "close source with two targets of one contract");
  CHECK(ext_plugin_close(second) == EXT_PLUGIN_OK, "close second operation provider");
  memcpy(config, single_config, sizeof config);
  source = open_json(config, EXT_PLUGIN_OK);
  CHECK(ext_plugin_close(source) == EXT_PLUGIN_OK, "close source before target");
  expect(target, "state", NULL, "\"ready\"");
  source = open_json(config, EXT_PLUGIN_OK);
  CHECK(ext_plugin_close(target) == EXT_PLUGIN_OK, "close a pinned target");
  expect(source, "state", NULL, "\"created\"");
  open_json(config, EXT_PLUGIN_NOT_FOUND);
  CHECK(ext_plugin_close(source) == EXT_PLUGIN_OK, "release closed target after source");
}

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
         "\"payload\":{}},\"tag\":\"call-1\",\"progress\":{},\"timeout_ms\":5000}",
         "\"signed\":true");
  ext_plugin_free(call(r.host, "call",
                       "{\"call\":{\"contract\":{\"name\":\"reverse.test\",\"version\":\"v1\"},\"operation\":\"run\","
                       "\"payload\":{}},\"tag\":\"not a tag\",\"progress\":{},\"timeout_ms\":5000}",
                       EXT_PLUGIN_INVALID));
  /* The call queued two progress events and two reverse events, each with its own seq. */
  char *seq = call(r.host, "sequence", NULL, EXT_PLUGIN_OK);
  CHECK(seq && strtoull(seq, NULL, 10) >= 4, "sequence after the call: %s", seq ? seq : "(null)");
  ext_plugin_free(seq);
  CHECK(ext_plugin_close(r.host) == EXT_PLUGIN_OK, "close the reverse host");
  r.stop = 1;
  pthread_join(t, NULL);
  CHECK(r.reverse_authorize == 1 && r.reverse == 1, "reverse asked: authorize %d, handle %d", r.reverse_authorize,
        r.reverse);
  CHECK(atomic_load(&r.progress) == 2 && r.tagged == 2, "progress events: %d, tagged %d", atomic_load(&r.progress), r.tagged);
  /* A call's progress is not dropped when the binding falls behind: with the reader held back
   * while the guest sends more than the queue holds, every event still arrives, in order. */
  snprintf(config, sizeof config,
           "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"process\",\"executable\":\"%s\","
           "\"arguments\":[\"--guest\",\"flood\"]},\"verify\":\"allow\",\"authorize\":\"allow\"}",
           REVERSE_DESCRIPTOR, EXT_REVERSE_GUEST);
  answerer f = {0};
  f.host = open_json(config, EXT_PLUGIN_OK);
  atomic_store(&f.hold, 1);
  pthread_create(&t, NULL, answer_events, &f);
  ext_plugin_free(call(f.host, "start", "{\"timeout_ms\":5000}", EXT_PLUGIN_OK));
  struct timespec held = {0, 300000000};
  pthread_t release;
  pthread_create(&release, NULL, release_after, &(struct release_args){&f, held});
  expect(f.host, "call",
         "{\"call\":{\"contract\":{\"name\":\"reverse.test\",\"version\":\"v1\"},\"operation\":\"run\","
         "\"payload\":{}},\"tag\":\"call-1\",\"progress\":{\"events_per_second\":100000,"
         "\"bytes_per_second\":16777216},\"timeout_ms\":10000}",
         "\"done\":true");
  pthread_join(release, NULL);
  /* Every event is queued once the call returns; give the reader up to 5 s to take them. */
  for (int waited = 0; atomic_load(&f.progress) < 3000 && waited < 5000; waited++) {
    struct timespec pause = {0, 1000000};
    nanosleep(&pause, NULL);
  }
  CHECK(ext_plugin_close(f.host) == EXT_PLUGIN_OK, "close the flood host");
  f.stop = 1;
  pthread_join(t, NULL);
  CHECK(atomic_load(&f.progress) == 3000 && f.tagged == 3000 && f.dropped == 0, "flood: %d progress, %d tagged, %d drops",
        atomic_load(&f.progress), f.tagged, f.dropped);
  /* A descriptor that does not meet the host's requirements is refused before anything starts. */
  snprintf(config, sizeof config,
           "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"shared_library\",\"path\":\"%s\"},"
           "\"requirements\":[{\"contract\":{\"name\":\"other.contract\",\"version\":\"v1\"},\"operation\":\"echo\"}],"
           "\"verify\":\"allow\",\"authorize\":\"allow\"}",
           conformance, EXT_TEST_GUEST);
  open_json(config, EXT_PLUGIN_NOT_FOUND);
  snprintf(config, sizeof config,
           "{\"object\":\"host\",\"descriptor\":%s,\"backend\":{\"kind\":\"shared_library\",\"path\":\"%s\"},"
           "\"requirements\":[{\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"echo\"}],"
           "\"verify\":\"allow\",\"authorize\":\"allow\"}",
           conformance, EXT_TEST_GUEST);
  CHECK(ext_plugin_close(open_json(config, EXT_PLUGIN_OK)) == EXT_PLUGIN_OK, "close a host that meets its requirements");
  open_json("{\"object\":\"host\",\"descriptor\":{},\"backend\":{\"kind\":\"process\"},\"verify\":\"allow\","
            "\"authorize\":\"allow\"}",
            EXT_PLUGIN_INVALID);
  open_json("{\"object\":\"host\",\"backend\":{\"kind\":\"shared_library\",\"path\":\"/x\"},\"verify\":\"maybe\"}",
            EXT_PLUGIN_INVALID);
}

static void contributed_graph(void) {
  const char *descriptor="{\"apiVersion\":\"ext.io.pc/v1\",\"identity\":{\"id\":\"peer/provider\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"example\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}]}";
  const char *config="{\"object\":\"execution_graph\",\"os\":\"linux\",\"arch\":\"amd64\","
    "\"root\":{\"identity\":{\"id\":\"peer/provider\",\"revision\":\"r1\"},\"contract\":{\"name\":\"example\",\"version\":\"v1\"}},"
    "\"packages\":[{\"binding\":{\"host\":\"peer-host\",\"digest\":\"1111111111111111111111111111111111111111111111111111111111111111\"},"
    "\"manifest\":{\"apiVersion\":\"ext.io/v1\",\"kind\":\"package\",\"identity\":{\"id\":\"peer/provider\",\"revision\":\"r1\"},"
    "\"contracts\":[{\"name\":\"example\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}]}}]}";
  uint64_t g=open_json(config,EXT_PLUGIN_OK);
  ext_plugin_free(call(g,"start","{\"reviews\":[{\"identity\":{\"id\":\"peer/provider\",\"revision\":\"r1\"},\"digest\":\"2222222222222222222222222222222222222222222222222222222222222222\"}]}",EXT_PLUGIN_MISMATCH));
  CHECK(ext_plugin_close(g)==EXT_PLUGIN_OK,"close mismatched contribution");
  answerer a={0};a.host=open_json(config,EXT_PLUGIN_OK);a.descriptor=descriptor;
  pthread_t thread;pthread_create(&thread,NULL,answer_events,&a);
  ext_plugin_free(call(a.host,"start","{\"reviews\":[{\"identity\":{\"id\":\"peer/provider\",\"revision\":\"r1\"},\"digest\":\"1111111111111111111111111111111111111111111111111111111111111111\"}]}",EXT_PLUGIN_OK));
  expect(a.host,"call","{\"call\":{\"contract\":{\"name\":\"example\",\"version\":\"v1\"},\"operation\":\"read\"}}","transport-answer");
  expect(a.host,"observe","{}","\"pid\":null");
  expect(a.host,"snapshot","{\"namespace\":\"example.graph\",\"processes\":[null]}","peer-host");
  CHECK(ext_plugin_close(a.host)==EXT_PLUGIN_OK,"close contributed graph");
  pthread_join(thread,NULL);
  CHECK(a.connects==1 && a.invokes==1,"directory-free contribution executed through host events");
}

static void execution_graph(void) {
  const char *config =
    "{\"object\":\"execution_graph\",\"temp_root\":\".\",\"os\":\"linux\",\"arch\":\"amd64\","
    "\"root\":{\"identity\":{\"id\":\"graph/root\",\"revision\":\"r1\"},\"contract\":{\"name\":\"example\",\"version\":\"v1\"}},"
    "\"packages\":[{\"directory\":\"missing\",\"manifest\":{\"apiVersion\":\"ext.io/v1\",\"kind\":\"package\","
    "\"identity\":{\"id\":\"graph/root\",\"revision\":\"r1\"},"
    "\"contracts\":[{\"name\":\"example\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}],"
    "\"artifacts\":[{\"name\":\"guest\",\"path\":\"guest\",\"sha256\":\"0000000000000000000000000000000000000000000000000000000000000000\"}]}}]}";
  uint64_t g = open_json(config, EXT_PLUGIN_OK);
  expect(g, "plan", "{}", "\"order\":[0]");
  ext_plugin_free(call(g, "call", "{\"call\":{}}", EXT_PLUGIN_CLOSED));
  ext_plugin_free(call(g, "start", "{\"reviews\":[],\"timeout_ms\":0}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(g, "start", "{\"reviews\":[]}", EXT_PLUGIN_DENIED));
  ext_plugin_free(call(g, "start", "{\"reviews\":[]}", EXT_PLUGIN_CLOSED));
  CHECK(ext_plugin_close(g) == EXT_PLUGIN_OK, "close denied graph");
  g = open_json(config, EXT_PLUGIN_OK);
  CHECK(ext_plugin_close(g) == EXT_PLUGIN_OK, "close graph before start");
  ext_plugin_free(call(g, "plan", "{}", EXT_PLUGIN_NOT_FOUND));
  open_json("{\"object\":\"execution_graph\"}", EXT_PLUGIN_INVALID);
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

  CHECK(open_json("{\"object\":\"package_writer\",\"root\":\"bad\\u0000path\"}", EXT_PLUGIN_INVALID) == 0,
        "writer accepted a truncated root");
  /* A described writer contends with existing CLI directory locks. */
  snprintf(config, sizeof config, "{\"object\":\"package_writer\",\"root\":\"%s\"}", user);
  ext_plugin_store_writer *direct = NULL;
  CHECK(ext_plugin_store_writer_acquire(NULL, &direct) == EXT_PLUGIN_INVALID, "null writer home");
  CHECK(ext_plugin_store_writer_acquire(user, NULL) == EXT_PLUGIN_INVALID, "null writer output");
  CHECK(ext_plugin_store_writer_acquire(user, &direct) == EXT_PLUGIN_OK, "direct writer acquire");
  CHECK(open_json(config, EXT_PLUGIN_EXISTS) == 0, "described writer bypassed direct writer");
  CHECK(ext_plugin_store_writer_release(direct) == EXT_PLUGIN_OK, "direct writer release");
  CHECK(ext_plugin_store_writer_release(direct) == EXT_PLUGIN_OK, "direct release twice");
  ext_plugin_store_writer_free(direct);
  uint64_t writer = open_json(config, EXT_PLUGIN_OK);
  direct = NULL;
  CHECK(ext_plugin_store_writer_acquire(user, &direct) == EXT_PLUGIN_EXISTS && direct == NULL,
        "direct writer bypassed described writer");
  CHECK(open_json(config, EXT_PLUGIN_EXISTS) == 0, "contended writer returned a handle");
  ext_plugin_free(call(writer, "unknown", "{}", EXT_PLUGIN_UNSUPPORTED));
  CHECK(ext_plugin_close(writer) == EXT_PLUGIN_OK, "close writer");
  writer = open_json(config, EXT_PLUGIN_OK);
  CHECK(ext_plugin_close(writer) == EXT_PLUGIN_OK, "reacquire writer");

  /* Handle 0: a pure service, the digests and the trust records. */
  expect(0, "protocol.negotiate", "{\"preferred\":[\"a\",\"b\"],\"offered\":[\"b\"]}", "\"b\"");
  ext_plugin_free(call(0, "descriptor.match", "{}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(0, "no.such.service", "{}", EXT_PLUGIN_UNSUPPORTED));
  expect(0, "code_name", "{\"code\":10}", "\"not_found\"");
  /* The public description accepts manifest arrays, including an empty plan. */
  expect(0, "package.resolve", "[]", "\"order\":null");
  ext_plugin_free(call(0, "package.resolve", "{}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(0, "package.resolve", "[{}]", EXT_PLUGIN_INVALID));
  /* Previous wire versions and nested descriptors are not compatibility inputs. */
  ext_plugin_free(call(0,"package.validate","{\"apiVersion\":\"ext.package/v1\",\"kind\":\"package\",\"identity\":{\"id\":\"example/provider\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"example\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}]}",EXT_PLUGIN_INVALID));
  ext_plugin_free(call(0,"package.validate","{\"apiVersion\":\"ext.io/v1\",\"kind\":\"other\",\"identity\":{\"id\":\"example/provider\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"example\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}]}",EXT_PLUGIN_INVALID));
  ext_plugin_free(call(0,"package.validate","{\"apiVersion\":\"ext.io/v1\",\"kind\":\"package\",\"identity\":{\"id\":\"example/provider\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"example\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}],\"descriptor\":{}}",EXT_PLUGIN_INVALID));
  ext_plugin_free(call(0,"descriptor.validate","{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"example/provider\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"example\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}]}",EXT_PLUGIN_INVALID));
  /* describe serves interface.json as it is. */
  FILE *f = fopen(EXT_INTERFACE_JSON, "rb");
  CHECK(f != NULL, "open %s", EXT_INTERFACE_JSON);
  if (f) {
    static char file[1 << 17];
    size_t fn = fread(file, 1, sizeof file, f);
    fclose(f);
    char *described = NULL;
    size_t dn = 0;
    CHECK(ext_plugin_call(0, "describe", NULL, 0, &described, &dn) == EXT_PLUGIN_OK, "describe");
    CHECK(described && dn == fn && !memcmp(described, file, fn), "describe is not interface.json (%zu and %zu bytes)", dn, fn);
    ext_plugin_free(described);
  }
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
  open_json("{\"object\":\"store\",\"root\":\"root\",\"layout\":{\"manifest_path\":\"../peer.json\"}}", EXT_PLUGIN_INVALID);
  open_json("{\"object\":\"store\",\"root\":\"root\",\"layout\":{\"manifest_path\":\"peer.json\\u0000ignored\"}}", EXT_PLUGIN_INVALID);
  snprintf(config, sizeof config,
           "{\"object\":\"store\",\"root\":\"%s\",\"layout\":{\"manifest_path\":\"config/peer.json\"}}", root);
  uint64_t custom = open_json(config, EXT_PLUGIN_OK);
  expect(custom, "ids", NULL, "[]");
  CHECK(ext_plugin_close(custom) == EXT_PLUGIN_OK, "close the custom layout store");
  snprintf(config, sizeof config, "{\"object\":\"store\",\"root\":\"%s\"}", root);
  uint64_t store = open_json(config, EXT_PLUGIN_OK);
  expect(store, "ids", NULL, "[]");
  expect(store, "list", NULL, "[]");
  expect(store, "verify", NULL, "[]");
  expect(store, "list_unverified", NULL, "\"packages\"");
  ext_plugin_free(call(store, "load", "{\"id\":\"example/missing\"}", EXT_PLUGIN_NOT_FOUND));
  ext_plugin_free(call(store, "install", "{}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(store, "snapshot", "{}", EXT_PLUGIN_INVALID));
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
  event_backend();
  routing_lifetime();
  execution_graph();
  contributed_graph();

  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("library interface checks passed\n");
  return 0;
}
