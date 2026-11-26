/* The collectors on the machine the test runs on: the process collection finds
 * this process, and a machine recorder records it. */
#include "ext_ledger.h"
#include "ledger_internal.h"
#include "collect.h"
#include "testutil.h"
#if defined(__linux__) || defined(__APPLE__)
#include <sys/socket.h>
#endif
#ifdef __APPLE__
#include <arpa/inet.h>
#include <netinet/in.h>
#endif

#ifdef _WIN32
#define getpid_ GetCurrentProcessId
#else
#define getpid_ getpid
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

static int hex24(const char *s) {
  if (strlen(s) != 24) return 0;
  for (const char *p = s; *p; p++)
    if (!((*p >= '0' && *p <= '9') || (*p >= 'a' && *p <= 'f'))) return 0;
  return 1;
}

static const char *field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : "";
}

static double number(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_NUMBER ? v->u.number : -1;
}

int main(void) {
  char *out;
  size_t len;
  const char *invalid_search[] = {
    "{\"what\":\"shells\",\"search_path\":null}",
    "{\"what\":\"shells\",\"search_path\":42}",
    "{\"what\":\"shells\",\"search_path\":\"x\\u0000y\"}",
    "{\"what\":\"filesystems\",\"search_path\":\"\"}"
  };
  for (size_t i = 0; i < sizeof invalid_search / sizeof *invalid_search; i++) {
    int result = ext_ledger_call(0, "collect", invalid_search[i], strlen(invalid_search[i]), &out, &len);
    CHECK(result == EXT_LEDGER_INVALID, "invalid shell search path rejected: %s", invalid_search[i]);
    if (out) ext_ledger_free(out);
  }
  int rc = ext_ledger_collect_environment(&out, &len);
  CHECK(rc == 0, "machine environment: %s", ext_ledger_code_name(rc));
  lj_value *env = rc ? NULL : lj_parse(out, len, NULL);
  if (!rc) ext_ledger_free(out);
  CHECK(env && hex24(field(env, "host_environment")) && *field(env, "os") && *field(env, "architecture") && *field(env, "now"),
        "the environment names the host, OS, architecture and time");
  printf("environment: os %s, architecture %s\n", field(env, "os"), field(env, "architecture"));
  lj_free(env);

  /* The described interface applies the requested process bound before the
   * ledger returns the snapshot. Some OSes deny a process-table scan. */
  const char *bounded = "{\"what\":\"processes\",\"limit\":1}";
  rc = ext_ledger_call(0, "collect", bounded, strlen(bounded), &out, &len);
  if (rc == EXT_LEDGER_OK) {
    lj_value *one = lj_parse(out, len, NULL);
    const lj_value *items = lj_get(one, "processes");
    CHECK(items && items->type == LJ_ARRAY && items->u.array.len <= 1, "process limit bounds the snapshot");
    CHECK(hex24(field(one, "host_id")) && *field(one, "observed_at"), "bounded snapshot has host identity and time");
    lj_free(one);
    ext_ledger_free(out);
  } else {
    CHECK(rc == EXT_LEDGER_UNSUPPORTED || rc == EXT_LEDGER_DENIED || rc == EXT_LEDGER_UNAVAILABLE,
          "bounded process collection: %s", ext_ledger_code_name(rc));
  }

  /* Targeted native inspection works without the process-table scan. The
   * public snapshot also needs a readable boot identity on this host. */
#if defined(__linux__) || defined(__APPLE__)
  int pair[2] = {-1, -1};
  CHECK(socketpair(AF_UNIX, SOCK_STREAM, 0, pair) == 0, "create a local socket for process inspection");
#endif
#ifdef __APPLE__
  int listener = socket(AF_INET, SOCK_STREAM, 0);
  struct sockaddr_in bound = {0};
  bound.sin_len = sizeof bound;
  bound.sin_family = AF_INET;
  bound.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  if (listener >= 0 && bind(listener, (struct sockaddr *)&bound, sizeof bound) != 0) {
    close(listener); /* a restricted test runner may deny even a loopback bind */
    listener = -1;
  }
  socklen_t bound_len = sizeof bound;
  if (listener >= 0) getsockname(listener, (struct sockaddr *)&bound, &bound_len);
#endif
  lj_value *target = lj_new(LJ_ARRAY);
  rc = ll_process_one(NULL, (int64_t)getpid_(), target);
  CHECK(rc == 0 && target->u.array.len == 1, "inspect this process identity: %s", ext_ledger_code_name(rc));
  if (!rc && target->u.array.len == 1) {
    lj_value *p = target->u.array.items[0];
    CHECK(*field(p, "start_id"), "targeted identity has a start ID");
    rc = ll_process_resources(NULL, (int64_t)getpid_(), 16, p);
    CHECK(rc == 0, "inspect this process resources: %s", ext_ledger_code_name(rc));
    if (!rc) {
      const lj_value *coverage = lj_get(p, "coverage");
      static const char *kinds[] = {"usage", "file", "mapping", "socket", "ipc"};
      for (size_t i = 0; i < sizeof kinds / sizeof *kinds; i++)
        CHECK(*field(lj_get(coverage, kinds[i]), "state"), "targeted %s coverage is explicit", kinds[i]);
#if defined(__linux__) || defined(__APPLE__)
      int resolved = 0;
      const lj_value *resources = lj_get(p, "resources");
      for (size_t i = 0; resources && i < resources->u.array.len; i++)
        if (!strcmp(field(resources->u.array.items[i], "protocol"), "unix")) resolved++;
      CHECK(resolved >= 2, "both local socket descriptors resolve through the kernel table");
#endif
#ifdef __APPLE__
      char expected[64];
      snprintf(expected, sizeof expected, "127.0.0.1:%u", (unsigned)ntohs(bound.sin_port));
      int matched = 0;
      for (size_t i = 0; resources && i < resources->u.array.len; i++)
        if (!strcmp(field(resources->u.array.items[i], "protocol"), "tcp") &&
            !strcmp(field(resources->u.array.items[i], "local_address"), expected)) matched++;
      if (listener >= 0) CHECK(matched >= 1, "native TCP socket endpoint matches its bound address");
#endif
    }
  }
  lj_free(target);
#if defined(__linux__) || defined(__APPLE__)
  if (pair[0] >= 0) close(pair[0]);
  if (pair[1] >= 0) close(pair[1]);
#endif
#ifdef __APPLE__
  if (listener >= 0) close(listener);
#endif

  rc = ext_ledger_collect_process(NULL, (int64_t)getpid_(), 16, &out, &len);
  if (rc == 0) {
    lj_value *one = lj_parse(out, len, NULL);
    const lj_value *items = lj_get(one, "processes");
    CHECK(!strcmp(field(one, "scope"), "process") && items && items->type == LJ_ARRAY && items->u.array.len == 1,
          "targeted public snapshot has one process");
    lj_free(one);
    ext_ledger_free(out);
  } else CHECK(rc == EXT_LEDGER_DENIED || rc == EXT_LEDGER_UNAVAILABLE || rc == EXT_LEDGER_UNSUPPORTED,
               "targeted process collection: %s", ext_ledger_code_name(rc));

  rc = ext_ledger_collect(NULL, "processes", &out, &len);
  if (rc == EXT_LEDGER_UNSUPPORTED || rc == EXT_LEDGER_DENIED || rc == EXT_LEDGER_UNAVAILABLE) {
    printf("skip: this OS refuses or has no process collection (%s)\n", ext_ledger_code_name(rc));
    return failures ? 1 : 0;
  }
  CHECK(rc == 0, "collect processes: %s", ext_ledger_code_name(rc));
  if (rc) return 1;
  lj_value *snap = lj_parse(out, len, NULL);
  ext_ledger_free(out);
  const lj_value *processes = lj_get(snap, "processes");
  CHECK(hex24(field(snap, "host_id")) && !strcmp(field(snap, "scope"), "all") && processes && processes->u.array.len > 1,
        "a snapshot of all processes with a host identity");
  CHECK(!strcmp(field(lj_get(snap, "enumeration"), "state"), "complete") && *field(lj_get(snap, "enumeration"), "observed_at"),
        "the enumeration is complete and dated");
  const lj_value *self = NULL;
  double last = 0;
  int sorted = 1;
  for (size_t i = 0; processes && i < processes->u.array.len; i++) {
    const lj_value *p = processes->u.array.items[i];
    if (number(p, "pid") < last) sorted = 0;
    last = number(p, "pid");
    if ((long)number(p, "pid") == (long)getpid_()) self = p;
  }
  CHECK(sorted, "processes are in PID order");
  CHECK(self != NULL, "this process is in the snapshot");
  if (self) {
    const lj_value *identity = lj_get(lj_get(self, "coverage"), "identity");
    CHECK(*field(self, "start_id") && !strcmp(field(identity, "state"), "complete") && *field(identity, "observed_at"),
          "this process has a start identity");
    CHECK(*field(self, "executable") && *field(self, "name"), "this process has an executable and a name: %s %s", field(self, "executable"),
          field(self, "name"));
#ifndef _WIN32
    CHECK((long)number(self, "parent_pid") == (long)getppid(), "this process's parent is right");
#endif
    printf("self: pid %.0f start %s executable %s\n", number(self, "pid"), field(self, "start_id"), field(self, "executable"));
  }
  lj_free(snap);

  /* The host inventory: shells and filesystems, the root among them. */
  rc = ext_ledger_collect(NULL, "host", &out, &len);
  CHECK(rc == 0, "collect the host: %s", ext_ledger_code_name(rc));
  lj_value *host = rc ? NULL : lj_parse(out, len, NULL);
  if (!rc) ext_ledger_free(out);
  const lj_value *shells = lj_get(host, "shells"), *filesystems = lj_get(host, "filesystems");
  CHECK(shells && shells->type == LJ_ARRAY, "the host has a shell list");
  int root = 0;
  for (size_t i = 0; filesystems && filesystems->type == LJ_ARRAY && i < filesystems->u.array.len; i++) {
    const char *mount = field(filesystems->u.array.items[i], "mount_point");
#ifdef _WIN32
    root |= strlen(mount) == 3 && mount[1] == ':' && mount[2] == '\\';
#else
    root |= strcmp(mount, "/") == 0;
#endif
  }
  CHECK(root, "the root filesystem is among the host's filesystems");
  printf("host: %zu shells, %zu filesystems\n", shells ? shells->u.array.len : 0, filesystems ? filesystems->u.array.len : 0);
  lj_free(host);

  /* Resource capacity and the current app-visible storage volume come from C. */
  rc = ext_ledger_collect(NULL, "resources", &out, &len);
  CHECK(rc == 0, "collect resources: %s", ext_ledger_code_name(rc));
  lj_value *machine = rc ? NULL : lj_parse(out, len, NULL);
  if (!rc) ext_ledger_free(out);
  CHECK(machine && number(machine, "logical_cpus") >= 1, "resources include logical CPU capacity");
  CHECK(machine && number(machine, "memory_bytes") > 0, "resources include physical memory");
#if defined(__APPLE__) || defined(_WIN32) || defined(__ANDROID__)
  const lj_value *volumes = machine ? lj_get(machine, "storage") : NULL;
  CHECK(volumes && volumes->type == LJ_ARRAY && volumes->u.array.len > 0,
        "resources include an app-visible storage volume");
#endif
  lj_free(machine);

  /* A machine recorder records the scan. */
  ext_ledger_store *store;
  ext_ledger_store_open_memory(NULL, &store);
  ext_ledger_recorder *r;
  rc = ext_ledger_recorder_open_machine(store, NULL, &r);
  CHECK(rc == 0, "open a machine recorder: %s", ext_ledger_code_name(rc));
  if (!rc) {
    rc = ext_ledger_recorder_scan(r, NULL, "processes");
    CHECK(rc == 0, "scan processes: %s", ext_ledger_code_name(rc));
    const char *q = "{\"namespace\":\"ext.system\",\"kind\":\"ext.system/process\",\"limit\":65536}";
    rc = ext_ledger_store_query_vertices(store, NULL, q, strlen(q), &out, &len);
    lj_value *found = rc ? NULL : lj_parse(out, len, NULL);
    if (!rc) ext_ledger_free(out);
    int recorded = 0;
    for (size_t i = 0; found && i < found->u.array.len; i++)
      recorded |= (long)number(lj_get(found->u.array.items[i], "attributes"), "pid") == (long)getpid_();
    CHECK(recorded, "the scan recorded this process");
    lj_free(found);
    rc = ext_ledger_recorder_scan_process(r, NULL, (int64_t)getpid_(), 16);
    CHECK(rc == 0, "targeted scan records process resources: %s", ext_ledger_code_name(rc));
    if (!rc) {
      const char *resources_query = "{\"namespace\":\"ext.system\",\"kind\":\"ext.system/process-resource\",\"limit\":65536}";
      rc = ext_ledger_store_query_vertices(store, NULL, resources_query, strlen(resources_query), &out, &len);
      lj_value *resources = rc ? NULL : lj_parse(out, len, NULL);
      if (!rc) ext_ledger_free(out);
      CHECK(rc == 0 && resources && resources->type == LJ_ARRAY && resources->u.array.len > 0,
            "targeted scan recorded resources");
      lj_free(resources);
    }
    rc = ext_ledger_recorder_scan(r, NULL, "processes");
    CHECK(rc == 0, "a second scan reconciles: %s", ext_ledger_code_name(rc));
    rc = ext_ledger_recorder_scan(r, NULL, "host");
    CHECK(rc == 0, "scan the host: %s", ext_ledger_code_name(rc));
    rc = ext_ledger_store_get_vertex(store, NULL, "ext.system", "host-inventory/filesystems", &out, &len);
    CHECK(rc == 0, "the scan recorded the filesystem inventory: %s", ext_ledger_code_name(rc));
    if (!rc) ext_ledger_free(out);
    ext_ledger_recorder_close(r);
  }
  ext_ledger_store_close(store);
  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("ok: collectors\n");
  return 0;
}
