/* What the fixtures cannot hold (README.md of the store contract): live watch
 * delivery, a watch that overflows, concurrent writers in threads and in
 * processes, a closed store, and the file backend's own duties: rewrites,
 * torn writes and refusing a directory it does not own. */
#include "ext_ledger.h"
#include "ledger_internal.h"
#include "testutil.h"

#ifndef _WIN32
#include <pthread.h>
#include <sys/stat.h>
#include <sys/wait.h>
#endif

#define NS "behaviour.example"

static int failures;
#define CHECK(cond, ...)                                     \
  do {                                                       \
    if (!(cond)) {                                           \
      fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);   \
      fprintf(stderr, __VA_ARGS__);                          \
      fprintf(stderr, "\n");                                 \
      failures++;                                            \
    }                                                        \
  } while (0)

static void reg(ext_ledger *s) {
  const char *schema = "{\"namespace\":\"" NS "\",\"version\":\"1\"}";
  int rc = ext_ledger_register(s, NULL, schema, strlen(schema));
  CHECK(rc == 0, "register: %s", ext_ledger_code_name(rc));
}

static int put(ext_ledger *s, const char *id) {
  char tx[512];
  snprintf(tx, sizeof tx, "{\"namespace\":\"" NS "\",\"vertices\":[{\"id\":\"%s\",\"kind\":\"" NS "/node\"}]}", id);
  return ext_ledger_apply(s, NULL, tx, strlen(tx), NULL, NULL);
}

static uint64_t cursor_of(const char *json) {
  const char *p = strstr(json, "\"cursor\":");
  return p ? strtoull(p + 9, NULL, 10) : 0;
}

static uint64_t current_cursor(ext_ledger *s) {
  char *snap;
  size_t n;
  if (ext_ledger_snapshot(s, NULL, NS, &snap, &n)) return 0;
  uint64_t c = cursor_of(snap);
  ext_ledger_free(snap);
  return c;
}

static ext_ledger *open_file(const char *dir, const ext_ledger_options *o) {
  ext_ledger *s = NULL;
  int rc = ext_ledger_open(NULL, dir, o, &s);
  CHECK(rc == 0, "open %s: %s", dir, ext_ledger_code_name(rc));
  return s;
}

static void watch_streams(ext_ledger *s) {
  ext_ledger_watch *w;
  int rc = ext_ledger_watch_open(s, NULL, NS, 0, 16, &w);
  CHECK(rc == 0, "watch: %s", ext_ledger_code_name(rc));
  if (rc) return;
  for (int i = 0; i < 3; i++) {
    char id[16];
    snprintf(id, sizeof id, "v%d", i);
    CHECK(put(s, id) == 0, "apply %s", id);
  }
  for (uint64_t want = 1; want <= 3; want++) {
    char *c;
    size_t n;
    rc = ext_ledger_watch_next(w, 3000, &c, &n);
    CHECK(rc == 0 && cursor_of(c) == want, "watch change %llu: %s", (unsigned long long)want, ext_ledger_code_name(rc));
    if (!rc) ext_ledger_free(c);
  }
  char *c;
  size_t n;
  rc = ext_ledger_watch_next(w, 50, &c, &n);
  CHECK(rc == EXT_LEDGER_TIMEOUT, "an idle watch times out: %s", ext_ledger_code_name(rc));
  ext_ledger_watch_close(w);
}

static void full_watch_ends(ext_ledger *s) {
  ext_ledger_watch *w;
  int rc = ext_ledger_watch_open(s, NULL, NS, current_cursor(s), 1, &w);
  CHECK(rc == 0, "watch: %s", ext_ledger_code_name(rc));
  if (rc) return;
  for (int i = 0; i < 4; i++) {
    char id[16];
    snprintf(id, sizeof id, "f%d", i);
    put(s, id);
  }
  char *c;
  size_t n;
  int delivered = 0;
  while ((rc = ext_ledger_watch_next(w, 3000, &c, &n)) == 0) {
    delivered++;
    ext_ledger_free(c);
  }
  CHECK(rc == EXT_LEDGER_UNAVAILABLE && delivered <= 1, "an overflowing watch ends after its buffer: %s after %d", ext_ledger_code_name(rc),
        delivered);
  ext_ledger_watch_close(w);
}

typedef struct {
  ext_ledger *s;
  int index, rc;
} writer;

#ifdef _WIN32
static DWORD WINAPI write_one(LPVOID arg) {
#else
static void *write_one(void *arg) {
#endif
  writer *w = arg;
  char id[32];
  snprintf(id, sizeof id, "w%d", w->index);
  w->rc = put(w->s, id);
  return 0;
}

static void concurrent_writers(ext_ledger *s) {
  enum { N = 8 };
  writer ws[N];
#ifdef _WIN32
  HANDLE th[N];
  for (int i = 0; i < N; i++) {
    ws[i] = (writer){s, i, -1};
    th[i] = CreateThread(NULL, 0, write_one, &ws[i], 0, NULL);
  }
  WaitForMultipleObjects(N, th, TRUE, INFINITE);
  for (int i = 0; i < N; i++) CloseHandle(th[i]);
#else
  pthread_t th[N];
  for (int i = 0; i < N; i++) {
    ws[i] = (writer){s, i, -1};
    pthread_create(&th[i], NULL, write_one, &ws[i]);
  }
  for (int i = 0; i < N; i++) pthread_join(th[i], NULL);
#endif
  for (int i = 0; i < N; i++) CHECK(ws[i].rc == 0, "writer %d: %s", i, ext_ledger_code_name(ws[i].rc));
}

static void closed_store(ext_ledger *s) {
  ext_ledger_watch *w;
  int rc = ext_ledger_watch_open(s, NULL, "", current_cursor(s), 4, &w);
  CHECK(rc == 0, "watch: %s", ext_ledger_code_name(rc));
  ext_ledger_close(s);
  if (!rc) {
    char *c;
    size_t n;
    while ((rc = ext_ledger_watch_next(w, 1000, &c, &n)) == 0) ext_ledger_free(c);
    CHECK(rc == EXT_LEDGER_UNAVAILABLE, "close ends the watch: %s", ext_ledger_code_name(rc));
    CHECK(put(s, "late") == EXT_LEDGER_UNAVAILABLE, "apply after close is unavailable");
    ext_ledger_watch_close(w);
  }
}

static void run_backend(int file) {
  ext_ledger_options o = {0};
  char *dir = temp_path("behaviour");
  ext_ledger *s;
  if (file) s = open_file(dir, &o);
  else ext_ledger_open_memory(&o, &s);
  if (!s) return;
  reg(s);
  watch_streams(s);
  full_watch_ends(s);
  concurrent_writers(s);
  char *snap;
  size_t n;
  int rc = ext_ledger_snapshot(s, NULL, NS, &snap, &n);
  CHECK(rc == 0 && cursor_of(snap) == 15, "snapshot after 15 commits: %s %s", ext_ledger_code_name(rc), rc ? "" : snap);
  if (!rc) ext_ledger_free(snap);
  closed_store(s);
  remove_store(dir);
  free(dir);
}

/* ---- the file backend ---- */

static int count_lines(const char *path) {
  size_t len;
  char *data = read_file(path, &len);
  if (!data) return -1;
  int n = 0;
  for (size_t i = 0; i < len; i++) n += data[i] == '\n';
  free(data);
  return n;
}

static void rewrites_keep_everything(void) {
  ext_ledger_options o = {0};
  o.max_changes = 8;
  char *dir = temp_path("rewrite");
  ext_ledger *s = open_file(dir, &o);
  if (!s) return;
  reg(s);
  const char *keyed = "{\"namespace\":\"" NS "\",\"idempotency_key\":\"k\",\"vertices\":[{\"id\":\"keyed\",\"kind\":\"" NS "/node\"}]}";
  for (int i = 0; i < 300; i++) {
    char id[16];
    snprintf(id, sizeof id, "r%03d", i);
    int rc = i == 295 ? ext_ledger_apply(s, NULL, keyed, strlen(keyed), NULL, NULL) : put(s, id);
    CHECK(rc == 0, "commit %d: %s", i, ext_ledger_code_name(rc));
  }
  ext_ledger_close(s);
  char path[1300];
  snprintf(path, sizeof path, "%s/checkpoint", dir);
  CHECK(count_lines(path) > 0, "a rewrite wrote a checkpoint");
  snprintf(path, sizeof path, "%s/log", dir);
  int lines = count_lines(path);
  CHECK(lines > 0 && lines < 140, "the log was rewritten: %d lines", lines);

  s = open_file(dir, &o);
  if (!s) return;
  char *out;
  size_t n;
  int rc = ext_ledger_query_vertices(s, NULL, "{\"namespace\":\"" NS "\",\"limit\":1000}", strlen("{\"namespace\":\"" NS "\",\"limit\":1000}"), &out, &n);
  int count = 0;
  for (const char *p = out; !rc && (p = strstr(p, "\"kind\"")); p++) count++;
  CHECK(rc == 0 && count == 300, "every vertex survives the rewrites: %d", count);
  if (!rc) ext_ledger_free(out);
  rc = ext_ledger_snapshot(s, NULL, NS, &out, &n);
  CHECK(rc == 0 && cursor_of(out) == 300 && strstr(out, "\"history_floor\":292"), "cursor and floor survive: %s", rc ? "" : out);
  if (!rc) ext_ledger_free(out);
  rc = ext_ledger_changes(s, NULL, NS, 292, 100, &out, &n);
  int changes = 0;
  for (const char *p = out; !rc && (p = strstr(p, "\"committed_at\"")); p++) changes++;
  CHECK(rc == 0 && changes == 8, "the retained history survives: %d", changes);
  if (!rc) ext_ledger_free(out);
  char *commit;
  rc = ext_ledger_apply(s, NULL, keyed, strlen(keyed), &commit, &n);
  CHECK(rc == 0 && strstr(commit, "\"replayed\":true") && cursor_of(commit) == 296, "a retained idempotency key replays: %s", rc ? "" : commit);
  if (!rc) ext_ledger_free(commit);
  ext_ledger_close(s);
  remove_store(dir);
  free(dir);
}

static void torn_tail_is_repaired(void) {
  ext_ledger_options o = {0};
  char *dir = temp_path("torn");
  ext_ledger *s = open_file(dir, &o);
  if (!s) return;
  reg(s);
  put(s, "a");
  ext_ledger_close(s);
  char path[1300];
  snprintf(path, sizeof path, "%s/log", dir);
  FILE *f = fopen(path, "ab");
  fputs("0badc0de {\"record\":\"commit\",\"chan", f); /* a writer that stopped mid-line */
  fclose(f);
  s = open_file(dir, &o);
  if (!s) return;
  CHECK(put(s, "b") == 0, "a writer appends after a torn line");
  ext_ledger_close(s);
  s = open_file(dir, &o);
  if (!s) return;
  char *out;
  size_t n;
  int rc = ext_ledger_get_vertex(s, NULL, NS, "b", &out, &n);
  CHECK(rc == 0, "the commit after the repair is read back: %s", ext_ledger_code_name(rc));
  if (!rc) ext_ledger_free(out);
  ext_ledger_close(s);
  remove_store(dir);
  free(dir);
}

static void refuses_what_it_does_not_own(void) {
  ext_ledger_options o = {0};
  char *dir = temp_path("foreign");
  ll_mkdir(dir);
  char path[1300];
  snprintf(path, sizeof path, "%s/graph.db", dir);
  FILE *f = fopen(path, "wb");
  fclose(f);
  ext_ledger *s = NULL;
  int rc = ext_ledger_open(NULL, dir, &o, &s);
  CHECK(rc == EXT_LEDGER_UNSUPPORTED, "a directory with other files: %s", ext_ledger_code_name(rc));
  rc = ext_ledger_open(NULL, path, &o, &s);
  CHECK(rc == EXT_LEDGER_UNSUPPORTED, "a path that is a file: %s", ext_ledger_code_name(rc));
  remove_store(dir);
  free(dir);

  dir = temp_path("layout");
  s = open_file(dir, &o);
  if (!s) return;
  ext_ledger_close(s);
  snprintf(path, sizeof path, "%s/log", dir);
  const char *line = "{\"generation\":0,\"layout\":\"ext.graph.ledger/v0\",\"record\":\"log\"}";
  f = fopen(path, "wb");
  fprintf(f, "%08x %s\n", (unsigned)ll_crc32(line, strlen(line)), line);
  fclose(f);
  rc = ext_ledger_open(NULL, dir, &o, &s);
  CHECK(rc == EXT_LEDGER_UNSUPPORTED, "another layout: %s", ext_ledger_code_name(rc));
  remove_store(dir);
  free(dir);
}

/* A reader follows a writer without locking or writing, also in a directory it may not write. */
static void reader_follows(void) {
  ext_ledger_options o = {0};
  char *dir = temp_path("reader");
  ext_ledger *r = NULL;
  int rc = ext_ledger_open_reader(NULL, dir, &o, &r);
  CHECK(rc == EXT_LEDGER_NOT_FOUND, "a reader of no store: %s", ext_ledger_code_name(rc));
  ext_ledger *w = open_file(dir, &o);
  if (!w) return;
  reg(w);
  CHECK(put(w, "a") == 0, "write a");
  rc = ext_ledger_open_reader(NULL, dir, &o, &r);
  CHECK(rc == 0, "open reader: %s", ext_ledger_code_name(rc));
  if (rc) return;
  CHECK(put(r, "x") == EXT_LEDGER_UNSUPPORTED, "a reader refuses writes");
  CHECK(put(w, "b") == 0, "write b");
  char *out;
  size_t n;
  rc = ext_ledger_get_vertex(r, NULL, NS, "b", &out, &n);
  CHECK(rc == 0, "the reader sees a later commit: %s", ext_ledger_code_name(rc));
  if (!rc) ext_ledger_free(out);
  ext_ledger_watch *watch;
  rc = ext_ledger_watch_open(r, NULL, NS, 2, 4, &watch);
  CHECK(rc == 0, "reader watch: %s", ext_ledger_code_name(rc));
  CHECK(put(w, "c") == 0, "write c");
  if (!rc) {
    rc = ext_ledger_watch_next(watch, 5000, &out, &n);
    CHECK(rc == 0 && cursor_of(out) == 3, "the reader's watch delivers the writer's commit: %s", ext_ledger_code_name(rc));
    if (!rc) ext_ledger_free(out);
    ext_ledger_watch_close(watch);
  }
  ext_ledger_close(r);
  ext_ledger_close(w);
#ifndef _WIN32
  if (geteuid() != 0) { /* root writes anywhere */
    chmod(dir, 0500);
    rc = ext_ledger_open_reader(NULL, dir, &o, &r);
    CHECK(rc == 0, "a reader of a read-only directory: %s", ext_ledger_code_name(rc));
    if (!rc) {
      rc = ext_ledger_get_vertex(r, NULL, NS, "c", &out, &n);
      CHECK(rc == 0, "read from a read-only directory: %s", ext_ledger_code_name(rc));
      if (!rc) ext_ledger_free(out);
      ext_ledger_close(r);
    }
    chmod(dir, 0700);
  }
#endif
  remove_store(dir);
  free(dir);
}

/* Handles in one process share writes and watches through the files. */
static void handles_share(void) {
  ext_ledger_options o = {0};
  char *dir = temp_path("handles");
  ext_ledger *a = open_file(dir, &o), *b = open_file(dir, &o);
  if (!a || !b) return;
  reg(a);
  reg(b);
  ext_ledger_watch *w;
  int rc = ext_ledger_watch_open(a, NULL, NS, 0, 16, &w);
  CHECK(rc == 0, "watch: %s", ext_ledger_code_name(rc));
  concurrent_writers(b);
  for (uint64_t want = 1; !rc && want <= 8; want++) {
    char *c;
    size_t n;
    int got = ext_ledger_watch_next(w, 5000, &c, &n);
    CHECK(got == 0 && cursor_of(c) == want, "the other handle's commit %llu: %s", (unsigned long long)want, ext_ledger_code_name(got));
    if (got) break;
    ext_ledger_free(c);
  }
  if (!rc) ext_ledger_watch_close(w);
  ext_ledger_close(a);
  ext_ledger_close(b);
  remove_store(dir);
  free(dir);
}

#ifndef _WIN32
/* Processes share writes and cursors through the lock and the log. */
static void processes_share(void) {
  ext_ledger_options o = {0};
  char *dir = temp_path("processes");
  ext_ledger *parent = open_file(dir, &o);
  if (!parent) return;
  reg(parent);
  enum { N = 4, EACH = 25 };
  pid_t pids[N];
  for (int i = 0; i < N; i++) {
    pids[i] = fork();
    if (pids[i] == 0) {
      ext_ledger *s;
      if (ext_ledger_open(NULL, dir, &o, &s)) _exit(1);
      for (int k = 0; k < EACH; k++) {
        char id[32];
        snprintf(id, sizeof id, "p%d-%d", i, k);
        if (put(s, id)) _exit(2);
      }
      ext_ledger_close(s);
      _exit(0);
    }
  }
  for (int i = 0; i < N; i++) {
    int status;
    waitpid(pids[i], &status, 0);
    CHECK(WIFEXITED(status) && WEXITSTATUS(status) == 0, "writer process %d: status %d", i, status);
  }
  char *out;
  size_t n;
  int rc = ext_ledger_snapshot(parent, NULL, NS, &out, &n);
  CHECK(rc == 0 && cursor_of(out) == N * EACH, "no commit is lost across processes: %s", rc ? "" : out);
  if (!rc) ext_ledger_free(out);
  ext_ledger_close(parent);
  remove_store(dir);
  free(dir);
}
#endif

int main(void) {
  run_backend(0);
  run_backend(1);
  rewrites_keep_everything();
  torn_tail_is_repaired();
  refuses_what_it_does_not_own();
  handles_share();
  reader_follows();
#ifndef _WIN32
  processes_share();
#endif
  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("ok: behaviour\n");
  return 0;
}
