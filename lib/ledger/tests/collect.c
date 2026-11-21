/* The collectors on the machine the test runs on: the process collection finds
 * this process, and a machine recorder records it. */
#include "ext_ledger.h"
#include "ledger_internal.h"
#include "testutil.h"

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
  int rc = ext_ledger_machine_environment(&out, &len);
  CHECK(rc == 0, "machine environment: %s", ext_ledger_code_name(rc));
  lj_value *env = rc ? NULL : lj_parse(out, len, NULL);
  if (!rc) ext_ledger_free(out);
  CHECK(env && hex24(field(env, "host_environment")) && *field(env, "os") && *field(env, "architecture") && *field(env, "now"),
        "the environment names the host, OS, architecture and time");
  printf("environment: os %s, architecture %s\n", field(env, "os"), field(env, "architecture"));
  lj_free(env);

  rc = ext_ledger_collect(NULL, "processes", &out, &len);
  if (rc == EXT_LEDGER_UNSUPPORTED || rc == EXT_LEDGER_DENIED || rc == EXT_LEDGER_UNAVAILABLE) {
    printf("skip: this OS refuses or has no process collection (%s)\n", ext_ledger_code_name(rc));
    return 0;
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

  /* A machine recorder records the scan. */
  ext_ledger *store;
  ext_ledger_open_memory(NULL, &store);
  ext_ledger_recorder *r;
  rc = ext_ledger_recorder_open_machine(store, NULL, &r);
  CHECK(rc == 0, "open a machine recorder: %s", ext_ledger_code_name(rc));
  if (!rc) {
    rc = ext_ledger_scan(r, NULL, "processes");
    CHECK(rc == 0, "scan processes: %s", ext_ledger_code_name(rc));
    const char *q = "{\"namespace\":\"ext.system\",\"kind\":\"ext.system/process\",\"limit\":65536}";
    rc = ext_ledger_query_vertices(store, NULL, q, strlen(q), &out, &len);
    lj_value *found = rc ? NULL : lj_parse(out, len, NULL);
    if (!rc) ext_ledger_free(out);
    int recorded = 0;
    for (size_t i = 0; found && i < found->u.array.len; i++)
      recorded |= (long)number(lj_get(found->u.array.items[i], "attributes"), "pid") == (long)getpid_();
    CHECK(recorded, "the scan recorded this process");
    lj_free(found);
    rc = ext_ledger_scan(r, NULL, "processes");
    CHECK(rc == 0, "a second scan reconciles: %s", ext_ledger_code_name(rc));
    rc = ext_ledger_scan(r, NULL, "host");
    CHECK(rc == 0, "scan the host: %s", ext_ledger_code_name(rc));
    rc = ext_ledger_get_vertex(store, NULL, "ext.system", "host-inventory/filesystems", &out, &len);
    CHECK(rc == 0, "the scan recorded the filesystem inventory: %s", ext_ledger_code_name(rc));
    if (!rc) ext_ledger_free(out);
    ext_ledger_recorder_close(r);
  }
  ext_ledger_close(store);
  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("ok: collectors\n");
  return 0;
}
