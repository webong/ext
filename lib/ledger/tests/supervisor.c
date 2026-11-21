/* The supervisor, held to the behaviour res/graph/supervisor's tests ask of it.
 * The test binary is its own child: "helper" runs as a supervised process and
 * acts on its EXT_SUPERVISOR_* variables. */
#include "ext_ledger.h"
#include "json.h"
#include "ledger_internal.h"
#include "testutil.h"

#ifdef _WIN32
#define getpid_ GetCurrentProcessId
#else
#include <sys/wait.h>
#define getpid_ getpid
#endif
#if defined(__APPLE__)
#include <TargetConditionals.h>
#endif

static int failures;
#define CHECK(cond, ...)                                   \
  do {                                                     \
    if (!(cond)) {                                         \
      fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); \
      fprintf(stderr, __VA_ARGS__);                        \
      fprintf(stderr, "\n");                               \
      failures++;                                          \
    }                                                      \
  } while (0)

static const char *self;

static int env_is(const char *name, const char *value) {
  const char *v = getenv(name);
  return v && strcmp(v, value) == 0;
}

static void write_text(const char *path, const char *text) {
  FILE *f = fopen(path, "wb");
  if (f) {
    fputs(text, f);
    fclose(f);
  }
}

/* A grandchild for the process-tree test: after a second, it leaves the sentinel. */
static int grandchild(const char *sentinel) {
  ll_sleep_ms(1000);
  write_text(sentinel, "done");
  return 0;
}

static int helper(void) {
  if (env_is("EXT_TREE_HELPER", "1")) {
#ifdef _WIN32
    char cmd[4096];
    snprintf(cmd, sizeof cmd, "\"%s\" grandchild \"%s\"", self, getenv("EXT_TREE_SENTINEL"));
    STARTUPINFOA si;
    memset(&si, 0, sizeof si);
    si.cb = sizeof si;
    PROCESS_INFORMATION pi;
    if (!CreateProcessA(NULL, cmd, NULL, NULL, FALSE, 0, NULL, NULL, &si, &pi)) return 2;
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
#else
    pid_t child = fork();
    if (child < 0) return 2;
    if (child == 0) _exit(grandchild(getenv("EXT_TREE_SENTINEL")));
#endif
    write_text(getenv("EXT_TREE_STARTED"), "started");
    ll_sleep_ms(30000);
    return 0;
  }
  if (env_is("EXT_SUPERVISOR_HOLD", "1")) ll_sleep_ms(30000);
  const char *bytes = getenv("EXT_SUPERVISOR_OUTPUT_BYTES");
  for (int i = 0, n = bytes ? atoi(bytes) : 0; i < n; i++) putchar('x');
  if (env_is("EXT_SUPERVISOR_ECHO_SECRET", "1")) fputs(getenv("EXAMPLE_SECRET") ? getenv("EXAMPLE_SECRET") : "", stdout);
  const char *code = getenv("EXT_SUPERVISOR_EXIT_CODE");
  if (code && atoi(code)) {
    fflush(stdout);
    return atoi(code);
  }
  fputs("runtime-output\n", stdout);
  return 0;
}

/* ---- helpers ---- */

static lj_value *parse(char *text, size_t len) {
  lj_value *v = text ? lj_parse(text, len, NULL) : NULL;
  ext_ledger_free(text);
  return v;
}

static const char *field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : "";
}

static double number(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_NUMBER ? v->u.number : -1;
}

static lj_value *instance(ext_ledger_supervisor *s, const char *id) {
  char *text = NULL;
  size_t len = 0;
  if (ext_ledger_supervisor_instance(s, id, &text, &len)) return NULL;
  return parse(text, len);
}

static lj_value *process_vertex(ext_ledger *store, const char *id) {
  char vid[64], *text = NULL;
  size_t len = 0;
  snprintf(vid, sizeof vid, "process/%s", id);
  if (ext_ledger_get_vertex(store, NULL, "ext.runtime", vid, &text, &len)) return NULL;
  return parse(text, len);
}

/* Polls pred for up to 5 s (10 s for the slow cases). */
typedef int (*pred_fn)(void *arg);
static int wait_for(pred_fn pred, void *arg) {
  for (int64_t until = ll_now_unix_ms() + 10000; ll_now_unix_ms() < until; ll_sleep_ms(10))
    if (pred(arg)) return 1;
  return 0;
}

typedef struct {
  ext_ledger_supervisor *s;
  ext_ledger *store;
  const char *id, *state;
  double crashes;
} watch;

static int instance_is(void *arg) {
  watch *w = arg;
  lj_value *i = instance(w->s, w->id);
  int ok = i && strcmp(field(i, "state"), w->state) == 0 && (w->crashes < 0 || number(i, "crash_count") == w->crashes);
  lj_free(i);
  return ok;
}

static int graph_is(void *arg) {
  watch *w = arg;
  lj_value *v = process_vertex(w->store, w->id);
  int ok = v && strcmp(field(lj_get(v, "attributes"), "state"), w->state) == 0;
  lj_free(v);
  return ok;
}

static char *start(ext_ledger_supervisor *s, const char *spec, const char *secrets, int *rc) {
  char *text = NULL;
  size_t len = 0;
  *rc = ext_ledger_supervisor_start(s, NULL, spec, strlen(spec), secrets, secrets ? strlen(secrets) : 0, &text, &len);
  if (*rc) return NULL;
  lj_value *v = lj_parse(text, len, NULL);
  ext_ledger_free(text);
  char *id = v ? strdup(field(v, "id")) : NULL;
  lj_free(v);
  return id;
}

/* The JSON string of the test binary's path. */
static char *json_self(void) {
  lj_buf b = {0};
  lj_write_string(&b, self, strlen(self));
  size_t n;
  return lj_buf_take(&b, &n);
}

static ext_ledger *memory(void) {
  ext_ledger *store = NULL;
  ext_ledger_open_memory(NULL, &store);
  return store;
}

/* ---- the cases ---- */

static void crash_restart_and_bounded_logs(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor_options o = {0};
  o.log_limit = 128;
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, &o, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec,
           "{\"artifact\":{\"id\":\"crashing\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"],"
           "\"environment\":{\"EXT_SUPERVISOR_EXIT_CODE\":\"7\",\"EXT_SUPERVISOR_OUTPUT_BYTES\":\"4096\"},"
           "\"restart\":{\"max_attempts\":1,\"initial_delay_ms\":1,\"max_delay_ms\":1}}",
           cmd);
  int rc;
  char *id = start(s, spec, NULL, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  if (id) {
    watch w = {s, store, id, "crashed", 2};
    CHECK(wait_for(instance_is, &w), "never crashed twice");
    char *logs = NULL;
    size_t len = 0;
    CHECK(ext_ledger_supervisor_logs(s, id, &logs, &len) == 0 && len > 0 && len <= 128, "log tail %zu", len);
    ext_ledger_free(logs);
    lj_value *v = process_vertex(store, id);
    CHECK(v && number(lj_get(v, "attributes"), "crash_count") == 2, "crash fact missing");
    CHECK(v && strcmp(field(lj_get(v, "attributes"), "last_error"), "process exited unsuccessfully") == 0, "last_error");
    lj_free(v);
    lj_value *i = instance(s, id);
    CHECK(i && number(i, "exit_code") == 7, "exit code");
    lj_free(i);
  }
  free(id);
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static const char secret_value[] = "never-project-this-value";
static void lifecycle_facts_and_secret_references(void) {
  ext_ledger *store = memory();
  size_t blen = 0;
  char *binary = read_file(self, &blen), sum[65];
  CHECK(binary, "read the test binary");
  ll_sha256_hex(binary ? binary : "", blen, sum);
  free(binary);
  ext_ledger_supervisor_options o = {0};
  o.log_limit = 128;
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, &o, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec,
           "{\"artifact\":{\"id\":\"artifact-1\",\"revision\":\"rev-3\",\"checksum\":\"sha256:%s\"},\"command\":%s,\"args\":[\"helper\"],"
           "\"environment\":{\"EXT_SUPERVISOR_ECHO_SECRET\":\"1\"},"
           "\"endpoints\":[{\"id\":\"ipc-1\",\"address\":\"unix:///tmp/example.sock\",\"transport\":\"unix\"},{\"id\":\"ipc-2\",\"address\":\"tcp://127.0.0.1:7000\",\"transport\":\"tcp\"}],"
           "\"connections\":[{\"id\":\"link-1\",\"source_endpoint\":\"ipc-1\",\"target_endpoint\":\"ipc-2\",\"metadata\":{\"channel\":\"control\"}}],"
           "\"dependencies\":[\"database\"]}",
           sum, cmd);
  int rc;
  char secrets[128];
  snprintf(secrets, sizeof secrets, "{\"EXAMPLE_SECRET\":\"%s\"}", secret_value);
  char *id = start(s, spec, secrets, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  CHECK(!start(s, spec, "{\"A=B\":\"x\"}", &rc) && rc == EXT_LEDGER_INVALID, "a secret name with = was accepted");
  CHECK(!start(s, spec, "{\"A\":1}", &rc) && rc == EXT_LEDGER_INVALID, "a secret that is not a string was accepted");
  if (id) {
    watch w = {s, store, id, "stopped", -1};
    CHECK(wait_for(instance_is, &w), "never stopped");
    char *logs = NULL;
    size_t len = 0;
    CHECK(ext_ledger_supervisor_logs(s, id, &logs, &len) == 0 && len == 0, "secret-bearing output must be discarded: %.*s", (int)len, logs ? logs : "");
    ext_ledger_free(logs);
    lj_value *v = process_vertex(store, id);
    CHECK(v && strcmp(field(lj_get(v, "attributes"), "state"), "stopped") == 0, "graph did not receive the stopped fact");
    CHECK(v && !lj_get(lj_get(v, "attributes"), "authorized"), "runtime fact implies domain authority");
    lj_free(v);
    char *snap = NULL;
    size_t slen = 0;
    CHECK(ext_ledger_snapshot(store, NULL, "ext.runtime", &snap, &slen) == 0 && !strstr(snap, secret_value), "secret value was projected");
    CHECK(snap && strstr(snap, "\"connected-to/link-1\"") && strstr(snap, "\"dependency/database\"") && strstr(snap, "\"endpoint/ipc-2\""), "IPC facts missing");
    ext_ledger_free(snap);
    char eid[80], *e = NULL;
    snprintf(eid, sizeof eid, "spawned-by/%s", id);
    CHECK(ext_ledger_get_edge(store, NULL, "ext.runtime", eid, &e, &slen) == 0, "missing spawned-by");
    ext_ledger_free(e);
  }
  free(id);
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static void stop_and_health_projection(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, NULL, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec, "{\"artifact\":{\"id\":\"long\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"],\"environment\":{\"EXT_SUPERVISOR_HOLD\":\"1\"}}", cmd);
  int rc;
  char *id = start(s, spec, NULL, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  if (id) {
    watch w = {s, store, id, "ready", -1};
    CHECK(wait_for(instance_is, &w), "never ready");
    int healthy = 0;
    CHECK(ext_ledger_supervisor_health(s, id, 1, &healthy) == 0 && healthy, "health");
    CHECK(ext_ledger_supervisor_health(s, id, 0, &healthy) == 0 && !healthy, "a failed check is healthy");
    lj_value *v0 = process_vertex(store, id);
    CHECK(v0 && strcmp(field(lj_get(v0, "attributes"), "health"), "unhealthy") == 0, "a failed check was not projected");
    lj_free(v0);
    int64_t began = ll_now_unix_ms();
    CHECK(ext_ledger_supervisor_stop(s, NULL, id) == 0, "stop");
    CHECK(ll_now_unix_ms() - began < 5000, "stop took the force timeout");
    lj_value *i = instance(s, id);
    CHECK(i && strcmp(field(i, "state"), "stopped") == 0, "stop lifecycle");
    lj_free(i);
    lj_value *v = process_vertex(store, id);
    CHECK(v && strcmp(field(lj_get(v, "attributes"), "health"), "unhealthy") == 0, "health fact");
    CHECK(v && !lj_get(lj_get(v, "attributes"), "lease_until"), "a stopped instance holds no lease");
    lj_free(v);
  }
  free(id);
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

/* What Instance reports, the graph has already recorded. */
static void instance_never_leads_graph(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, NULL, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec, "{\"artifact\":{\"id\":\"observed\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"],\"environment\":{\"EXT_SUPERVISOR_HOLD\":\"1\"}}", cmd);
  int rc;
  char *id = start(s, spec, NULL, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  int stopping = 0, done = 0;
  for (int64_t until = ll_now_unix_ms() + 10000; id && !done && ll_now_unix_ms() < until; ll_sleep_ms(1)) {
    lj_value *i = instance(s, id);
    const char *seen = field(i, "state");
    const char *allowed = strcmp(seen, "ready") == 0 ? " ready stopping stopped " : strcmp(seen, "stopping") == 0 ? " stopping stopped " : strcmp(seen, "stopped") == 0 ? " stopped " : NULL;
    if (allowed) {
      lj_value *v = process_vertex(store, id);
      char got[40];
      snprintf(got, sizeof got, " %s ", field(lj_get(v, "attributes"), "state"));
      CHECK(strstr(allowed, got), "Instance reported %s but the graph says%s", seen, got);
      lj_free(v);
    }
    if (strcmp(seen, "ready") == 0 && !stopping) {
      stopping = 1;
      lj_free(i);
      CHECK(ext_ledger_supervisor_stop(s, NULL, id) == 0, "stop");
      continue;
    }
    done = strcmp(seen, "stopped") == 0;
    lj_free(i);
  }
  CHECK(done, "never stopped");
  free(id);
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static void checksum_rejected_before_start(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor_options o = {0};
  o.require_checksum = 1;
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, &o, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  int rc;
  snprintf(spec, sizeof spec, "{\"artifact\":{\"id\":\"verified\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"]}", cmd);
  char *id = start(s, spec, NULL, &rc);
  CHECK(!id && rc == EXT_LEDGER_INVALID, "a required checksum was omitted: %s", ext_ledger_code_name(rc));
  free(id);
  snprintf(spec, sizeof spec, "{\"artifact\":{\"id\":\"verified\",\"revision\":\"1\",\"checksum\":\"sha256:%064d\"},\"command\":%s,\"args\":[\"helper\"]}", 0, cmd);
  id = start(s, spec, NULL, &rc);
  CHECK(!id && rc == EXT_LEDGER_DENIED, "an incorrect checksum was accepted: %s", ext_ledger_code_name(rc));
  free(id);
  char *list = NULL;
  size_t len = 0;
  CHECK(ext_ledger_supervisor_instances(s, &list, &len) == 0 && strcmp(list, "[]") == 0, "a rejected artifact made an instance: %s", list ? list : "");
  ext_ledger_free(list);
  /* Specs it refuses. */
  const char *bad[] = {
      "{\"artifact\":{\"id\":\"a\"},\"command\":\"x\"}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\",\"checksum\":\"md5:00\"},\"command\":\"x\"}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\",\"checksum\":\"sha256:" "zz" "\"},\"command\":\"x\"}",
  };
  for (size_t i = 0; i < sizeof bad / sizeof *bad; i++) {
    id = start(s, bad[i], NULL, &rc);
    CHECK(!id && rc == EXT_LEDGER_INVALID, "accepted %s", bad[i]);
    free(id);
  }
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static void invalid_specs(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, NULL, &s) == 0, "open");
  const char *bad[] = {
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"\"}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"x\",\"secret_references\":{\"K\":{\"scope\":\"s\",\"id\":\"i\"}}}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"x\",\"secret_references\":{\"K\":{\"scope\":\"\",\"id\":\"i\"}}}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"x\",\"endpoints\":[{\"id\":\"e\",\"address\":\"a\",\"transport\":\"t\"},{\"id\":\"e\",\"address\":\"b\",\"transport\":\"t\"}]}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"x\",\"endpoints\":[{\"id\":\"e\",\"address\":\"\",\"transport\":\"t\"}]}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"x\",\"endpoints\":[{\"id\":\"e\",\"address\":\"a\",\"transport\":\"t\"}],\"connections\":[{\"id\":\"c\",\"source_endpoint\":\"e\",\"target_endpoint\":\"f\"}]}",
      "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"x\",\"environment\":{\"A=B\":\"c\"}}",
      "[]",
  };
  for (size_t i = 0; i < sizeof bad / sizeof *bad; i++) {
    int rc;
    char *id = start(s, bad[i], NULL, &rc);
    CHECK(!id && rc == EXT_LEDGER_INVALID, "accepted %s: %s", bad[i], ext_ledger_code_name(rc));
    free(id);
  }
  ext_ledger_supervisor_options o = {0};
  o.orphan_policy = "keep";
  ext_ledger_supervisor *t = NULL;
  CHECK(ext_ledger_supervisor_open(store, &o, &t) == EXT_LEDGER_INVALID, "invalid orphan policy");
  o.orphan_policy = NULL;
  o.lease_ms = 2;
  CHECK(ext_ledger_supervisor_open(store, &o, &t) == EXT_LEDGER_INVALID, "lease under 3 ms");
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static int64_t pid_of(const lj_value *instance) { return (int64_t)number(instance, "pid"); }

static lj_value *await_state(ext_ledger_supervisor *s, const char *id, const char *states, int *rc) {
  ext_ledger_context ctx = {0, ll_now_unix_ms() + 10000};
  char *text = NULL;
  size_t len = 0;
  *rc = ext_ledger_supervisor_await(s, &ctx, id, states, &text, &len);
  return *rc ? NULL : parse(text, len);
}

/* The caller checks endpoints and the handshake, then reports; until then the
 * instance is running, unready and unhealthy. */
static void caller_reports_readiness(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, NULL, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec,
           "{\"artifact\":{\"id\":\"ipc\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"],\"environment\":{\"EXT_SUPERVISOR_HOLD\":\"1\"},"
           "\"await_readiness\":true}",
           cmd);
  int rc;
  char *id = start(s, spec, NULL, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  if (id) {
    lj_value *running = await_state(s, id, "running", &rc);
    CHECK(running && pid_of(running) > 0, "never running: %s", ext_ledger_code_name(rc));
    int64_t pid = running ? pid_of(running) : 0;
    lj_free(running);
    ll_sleep_ms(100);
    lj_value *i = instance(s, id);
    CHECK(i && strcmp(field(i, "state"), "running") == 0, "ready before the caller reported it");
    lj_free(i);
    int healthy = 1;
    CHECK(ext_ledger_supervisor_health(s, id, 1, &healthy) == 0 && !healthy, "an unready process is healthy");
    CHECK(ext_ledger_supervisor_ready(s, id, pid + 1000000, 1) == EXT_LEDGER_CHANGED, "a report for another launch was taken");
    CHECK(ext_ledger_supervisor_ready(s, id, pid, 1) == 0, "report ready");
    lj_value *ready = await_state(s, id, "ready", &rc);
    CHECK(ready, "never ready: %s", ext_ledger_code_name(rc));
    lj_free(ready);
    CHECK(ext_ledger_supervisor_ready(s, id, pid, 1) == EXT_LEDGER_CHANGED, "a second report was taken");
    CHECK(ext_ledger_supervisor_health(s, id, 1, &healthy) == 0 && healthy, "a ready process is unhealthy");
    CHECK(ext_ledger_supervisor_stop(s, NULL, id) == 0, "stop");
    CHECK(ext_ledger_supervisor_health(s, id, 1, &healthy) == 0 && !healthy, "a stopped process is healthy");
    CHECK(await_state(s, id, "ready", &rc) == NULL && rc == EXT_LEDGER_CHANGED, "await on a finished instance: %s", ext_ledger_code_name(rc));
  }
  free(id);
  /* Readiness is only for a spec that awaits it. */
  snprintf(spec, sizeof spec, "{\"artifact\":{\"id\":\"plain\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"],\"environment\":{\"EXT_SUPERVISOR_HOLD\":\"1\"}}", cmd);
  id = start(s, spec, NULL, &rc);
  if (id) {
    lj_value *ready = await_state(s, id, "ready", &rc);
    CHECK(ready, "a spec without await_readiness is ready on start");
    CHECK(ext_ledger_supervisor_ready(s, id, ready ? pid_of(ready) : 0, 1) == EXT_LEDGER_INVALID, "a report for a spec that does not await it");
    lj_free(ready);
  }
  free(id);
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static void failed_readiness_crashes(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, NULL, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec,
           "{\"artifact\":{\"id\":\"unready\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"],\"environment\":{\"EXT_SUPERVISOR_HOLD\":\"1\"},"
           "\"await_readiness\":true}",
           cmd);
  int rc;
  char *id = start(s, spec, NULL, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  if (id) {
    lj_value *running = await_state(s, id, "running", &rc);
    CHECK(running && ext_ledger_supervisor_ready(s, id, pid_of(running), 0) == 0, "report not ready");
    lj_free(running);
    watch w = {s, store, id, "crashed", 1};
    CHECK(wait_for(instance_is, &w), "a failed readiness never crashed");
    lj_value *i = instance(s, id);
    CHECK(i && strcmp(field(i, "last_error"), "readiness or handshake failed") == 0, "last_error");
    lj_free(i);
  }
  free(id);
  /* No report before the timeout crashes it too. */
  snprintf(spec, sizeof spec,
           "{\"artifact\":{\"id\":\"silent\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"],\"environment\":{\"EXT_SUPERVISOR_HOLD\":\"1\"},"
           "\"await_readiness\":true,\"readiness_timeout_ms\":200}",
           cmd);
  id = start(s, spec, NULL, &rc);
  if (id) {
    watch w = {s, store, id, "crashed", 1};
    CHECK(wait_for(instance_is, &w), "an unreported readiness never timed out");
  }
  free(id);
  free(cmd);
  /* A command that is not there crashes as a failed start. */
  id = start(s, "{\"artifact\":{\"id\":\"absent\",\"revision\":\"1\"},\"command\":\"ext-ledger-no-such-command\"}", NULL, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  if (id) {
    watch w = {s, store, id, "crashed", 1};
    CHECK(wait_for(instance_is, &w), "a missing command never crashed");
    lj_value *i = instance(s, id);
    CHECK(i && strcmp(field(i, "last_error"), "process start failed") == 0, "last_error %s", field(i, "last_error"));
    lj_free(i);
  }
  free(id);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static void seed(ext_ledger *store, const char *id, int64_t lease_offset_ms) {
  CHECK(ext_ledger_register(store, NULL, "{\"namespace\":\"ext.runtime\",\"version\":\"1\"}", 41) == 0, "register");
  ll_time t = ll_time_utc(ll_time_now());
  t.sec += lease_offset_ms / 1000;
  lj_buf at = {0};
  ll_time_write(&at, &t);
  char tx[1024];
  int n = snprintf(tx, sizeof tx,
                   "{\"namespace\":\"ext.runtime\",\"vertices\":[{\"id\":\"process/%s\",\"kind\":\"ext.runtime/process-instance\","
                   "\"attributes\":{\"runtime_id\":\"test-runtime\",\"state\":\"running\",\"pid\":%lu,\"lease_until\":%.*s}}]}",
                   id, (unsigned long)getpid_(), (int)at.len, at.ptr);
  lj_buf_free(&at);
  CHECK(ext_ledger_apply(store, NULL, tx, (size_t)n, NULL, NULL) == 0, "seed");
}

static void expired_lease_marks_orphan_and_blocks_start(void) {
  ext_ledger *store = memory();
  seed(store, "old", -60000);
  ext_ledger_supervisor_options o = {0};
  o.runtime_id = "test-runtime";
  o.orphan_policy = "mark";
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, &o, &s) == 0, "open");
  char *text = NULL;
  size_t len = 0;
  CHECK(ext_ledger_supervisor_orphans(s, &text, &len) == 0 && strstr(text, "\"id\":\"old\"") && !strstr(text, "},{"), "recovered orphans: %s", text ? text : "");
  ext_ledger_free(text);
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec, "{\"artifact\":{\"id\":\"new\",\"revision\":\"1\"},\"command\":%s}", cmd);
  int rc;
  char *id = start(s, spec, NULL, &rc);
  CHECK(!id && rc == EXT_LEDGER_BUSY, "a new start bypassed the unresolved orphan: %s", ext_ledger_code_name(rc));
  free(id);
  lj_value *v = process_vertex(store, "old");
  CHECK(v && strcmp(field(lj_get(v, "attributes"), "state"), "orphaned") == 0, "orphan graph state");
  lj_free(v);
  /* This process has no recorded identity, so it is never ended. */
  CHECK(ext_ledger_supervisor_resolve_orphan(s, NULL, "old") == EXT_LEDGER_UNAVAILABLE, "resolved an orphan without an identity");
  CHECK(ext_ledger_supervisor_resolve_orphan(s, NULL, "none") == EXT_LEDGER_NOT_FOUND, "resolved an unknown orphan");
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static void active_lease_rejects_second_supervisor(void) {
  ext_ledger *store = memory();
  seed(store, "live", 60000);
  ext_ledger_supervisor_options o = {0};
  o.runtime_id = "test-runtime";
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, &o, &s) == EXT_LEDGER_BUSY, "an active lease was ignored");
  /* Another runtime is not leased. */
  o.runtime_id = "other";
  CHECK(ext_ledger_supervisor_open(store, &o, &s) == 0, "another runtime");
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static int count_processes(ext_ledger *store) {
  char *snap = NULL;
  size_t len = 0;
  if (ext_ledger_snapshot(store, NULL, "ext.runtime", &snap, &len)) return -1;
  int n = 0;
  for (const char *p = snap; (p = strstr(p, "\"ext.runtime/process-instance\"")); p++) n++;
  ext_ledger_free(snap);
  return n;
}

typedef struct {
  ext_ledger_supervisor *s;
  ext_ledger *store;
} pair;

static int one_kept(void *arg) {
  pair *p = arg;
  char *list = NULL;
  size_t len = 0;
  int one = count_processes(p->store) == 1 && ext_ledger_supervisor_instances(p->s, &list, &len) == 0 && list[0] == '[' && !strstr(list, "},{");
  ext_ledger_free(list);
  return one;
}

static void terminal_instance_retention_bounds_graph(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor_options o = {0};
  o.retain_terminal = 1;
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, &o, &s) == 0, "open");
  char spec[4096], *cmd = json_self();
  snprintf(spec, sizeof spec, "{\"artifact\":{\"id\":\"short\",\"revision\":\"1\"},\"command\":%s,\"args\":[\"helper\"]}", cmd);
  for (int i = 0; i < 3; i++) {
    int rc;
    char *id = start(s, spec, NULL, &rc);
    CHECK(id, "start: %s", ext_ledger_code_name(rc));
    if (id) {
      watch w = {s, store, id, "stopped", -1};
      CHECK(wait_for(graph_is, &w), "never stopped");
    }
    free(id);
  }
  pair p = {s, store};
  CHECK(wait_for(one_kept, &p), "retention: %d process vertices", count_processes(store));
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static int exists(void *arg) {
  FILE *f = fopen(arg, "rb");
  if (f) fclose(f);
  return f != NULL;
}

static void stop_terminates_descendants(void) {
  ext_ledger *store = memory();
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, NULL, &s) == 0, "open");
  char *dir = temp_path("tree"), started[1400], sentinel[1400];
  ll_mkdir(dir);
  snprintf(started, sizeof started, "%s/started", dir);
  snprintf(sentinel, sizeof sentinel, "%s/descendant-survived", dir);
  lj_buf b = {0};
  char *cmd = json_self();
  lj_buf_puts(&b, "{\"artifact\":{\"id\":\"tree\",\"revision\":\"1\"},\"command\":");
  lj_buf_puts(&b, cmd);
  lj_buf_puts(&b, ",\"args\":[\"helper\"],\"environment\":{\"EXT_TREE_HELPER\":\"1\",\"EXT_TREE_STARTED\":");
  lj_write_string(&b, started, strlen(started));
  lj_buf_puts(&b, ",\"EXT_TREE_SENTINEL\":");
  lj_write_string(&b, sentinel, strlen(sentinel));
  lj_buf_puts(&b, "}}");
  size_t n;
  char *spec = lj_buf_take(&b, &n);
  int rc;
  char *id = start(s, spec, NULL, &rc);
  CHECK(id, "start: %s", ext_ledger_code_name(rc));
  if (id) {
    CHECK(wait_for(exists, started), "the tree never started");
    CHECK(ext_ledger_supervisor_stop(s, NULL, id) == 0, "stop");
    ll_sleep_ms(1500);
    CHECK(!exists(sentinel), "a descendant survived the process-tree stop");
  }
  ll_remove(started);
  ll_remove(sentinel);
  ll_remove(dir);
  free(dir);
  free(id);
  free(spec);
  free(cmd);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
}

static void (*const cases[])(void) = {
    crash_restart_and_bounded_logs, lifecycle_facts_and_secret_references, stop_and_health_projection, instance_never_leads_graph,
    checksum_rejected_before_start, invalid_specs, caller_reports_readiness, failed_readiness_crashes,
    expired_lease_marks_orphan_and_blocks_start, active_lease_rejects_second_supervisor, terminal_instance_retention_bounds_graph,
    stop_terminates_descendants,
};

int main(int argc, char **argv) {
  self = argv[0];
  if (argc > 2 && strcmp(argv[1], "grandchild") == 0) return grandchild(argv[2]);
  if (argc > 1 && strcmp(argv[1], "helper") == 0) return helper();
  setvbuf(stdout, NULL, _IONBF, 0);
#if defined(__APPLE__) && TARGET_OS_IPHONE
  /* An iOS application cannot start processes: starting one crashes it as a failed start. */
  ext_ledger *store = memory();
  ext_ledger_supervisor *s = NULL;
  CHECK(ext_ledger_supervisor_open(store, NULL, &s) == 0, "open");
  int rc;
  char *id = start(s, "{\"artifact\":{\"id\":\"a\",\"revision\":\"1\"},\"command\":\"/bin/sh\"}", NULL, &rc);
  watch w = {s, store, id, "crashed", 1};
  CHECK(id && wait_for(instance_is, &w), "an iOS start did not fail");
  free(id);
  ext_ledger_supervisor_close(s);
  ext_ledger_close(store);
  (void)cases;
#else
  for (size_t i = 0; i < sizeof cases / sizeof *cases; i++) cases[i]();
#endif
  if (failures) {
    fprintf(stderr, "%d failure(s)\n", failures);
    return 1;
  }
  printf("supervisor: ok\n");
  return 0;
}
