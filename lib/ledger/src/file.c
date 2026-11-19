/* The file backend: res/graph/testdata/store/v1/ledger.json. A directory holds
 * an append-only log and a checkpoint; a writer holds an advisory lock on the
 * lock file, readers never lock. Every handle keeps the whole state in memory
 * and catches up by reading what other handles appended. */
#include "ledger_internal.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define LOCK_WAIT_MS 5000    /* how long a writer waits for another writer's lock before busy */
#define REWRITE_MIN_DEAD 64  /* rewrite.when in ledger.json */
#define RELOAD_ATTEMPTS 50

enum { P_LOCK, P_LOG, P_CHECKPOINT, P_LOG_TMP, P_CHECKPOINT_TMP, P_COUNT };
static const char *const file_names[P_COUNT] = {"lock", "log", "checkpoint", "log.tmp", "checkpoint.tmp"};

typedef struct {
  ll_backend backend;
  char *paths[P_COUNT];
  char *dir;
  ll_file *lock;
  ll_file *log;
  ll_fid log_id;
  uint64_t generation; /* of the log held */
  uint64_t base;       /* the checkpoint's base cursor */
  uint64_t offset;     /* the end of the last whole line read from the log */
  uint64_t commits;    /* commit lines in the log */
  int locked, stale, read_only;
} file_backend;

/* ---- lines ---- */

static void frame(lj_buf *out, const lj_buf *json) {
  char crc[10];
  snprintf(crc, sizeof crc, "%08x ", (unsigned)ll_crc32(json->ptr, json->len));
  lj_buf_put(out, crc, 9);
  lj_buf_put(out, json->ptr, json->len);
  lj_buf_putc(out, '\n');
}

static int hexval(char c) {
  if (c >= '0' && c <= '9') return c - '0';
  if (c >= 'a' && c <= 'f') return c - 'a' + 10;
  return -1;
}

/* Decodes one line (without its newline). NULL when its frame or CRC is wrong. */
static lj_value *unframe(const char *line, size_t n, int *oom) {
  *oom = 0;
  if (n < 10 || line[8] != ' ') return NULL;
  uint32_t want = 0;
  for (int i = 0; i < 8; i++) {
    int h = hexval(line[i]);
    if (h < 0) return NULL;
    want = want << 4 | (uint32_t)h;
  }
  if (ll_crc32(line + 9, n - 9) != want) return NULL;
  lj_value *v = lj_parse(line + 9, n - 9, oom);
  if (v && v->type != LJ_OBJECT) {
    lj_free(v);
    return NULL;
  }
  return v;
}

typedef int (*line_fn)(void *arg, const lj_value *record, uint64_t offset);

/* Reads the whole lines of f from offset on, calling each with every record.
 * Stops before a torn last line; *end is the offset after the last whole line. */
static int scan(ll_file *f, uint64_t offset, line_fn each, void *arg, uint64_t *end) {
  *end = offset;
  uint64_t size;
  int rc = ll_file_size(f, &size);
  if (rc) return rc;
  if (size <= offset) return 0;
  size_t n = (size_t)(size - offset);
  if ((uint64_t)n != size - offset) return EXT_LEDGER_TOO_LARGE;
  char *data = malloc(n);
  if (!data) return EXT_LEDGER_IO;
  size_t got;
  if ((rc = ll_file_read_at(f, offset, data, n, &got))) {
    free(data);
    return rc;
  }
  size_t at = 0;
  while (!rc && at < got) {
    char *nl = memchr(data + at, '\n', got - at);
    if (!nl) break; /* torn, or still being written */
    size_t len = (size_t)(nl - (data + at));
    int oom;
    lj_value *record = unframe(data + at, len, &oom);
    if (!record) {
      if (oom) rc = EXT_LEDGER_IO;
      else if (memchr(nl + 1, '\n', got - (size_t)(nl + 1 - data))) rc = EXT_LEDGER_IO; /* a bad line that is not the last */
      break;                                                                     /* a torn last line */
    }
    rc = each(arg, record, offset + at);
    lj_free(record);
    if (!rc) {
      at += len + 1;
      *end = offset + at;
    }
  }
  free(data);
  return rc;
}

static const char *record_kind(const lj_value *r) {
  const lj_value *k = lj_get(r, "record");
  return k && k->type == LJ_STRING ? k->u.string.ptr : "";
}

static int get_u64(const lj_value *r, const char *key, uint64_t *out) {
  const lj_value *v = lj_get(r, key);
  if (!v || v->type != LJ_NUMBER || v->u.number < 0 || v->u.number != (double)(uint64_t)v->u.number) return EXT_LEDGER_IO;
  *out = (uint64_t)v->u.number;
  return 0;
}

static int check_layout(const lj_value *r) {
  const lj_value *l = lj_get(r, "layout");
  if (!l || l->type != LJ_STRING || strcmp(l->u.string.ptr, EXT_LEDGER_LAYOUT) != 0) return EXT_LEDGER_UNSUPPORTED;
  return 0;
}

/* ---- the checkpoint ---- */

typedef struct {
  ll_state *state;
  uint64_t generation, base, floor, count;
  int header, ended;
} ck_reader;

static int checkpoint_line(void *arg, const lj_value *r, uint64_t offset) {
  ck_reader *ck = arg;
  const char *kind = record_kind(r);
  int rc;
  if (offset == 0) {
    if (strcmp(kind, "checkpoint") != 0) return EXT_LEDGER_UNSUPPORTED;
    if ((rc = check_layout(r))) return rc;
    if (get_u64(r, "generation", &ck->generation) || get_u64(r, "base", &ck->base) || get_u64(r, "floor", &ck->floor)) return EXT_LEDGER_IO;
    ck->header = 1;
    return 0;
  }
  if (!ck->header || ck->ended) return EXT_LEDGER_IO;
  if (strcmp(kind, "namespace") == 0) {
    ll_schema schema;
    uint64_t revision;
    if (get_u64(r, "revision", &revision) || ll_schema_decode(lj_get(r, "schema"), &schema)) return EXT_LEDGER_IO;
    rc = ll_state_set_schema(ck->state, &schema);
    if (!rc) ll_state_ns(ck->state, LL_S(schema.ns), schema.ns.n)->revision = revision;
    ll_schema_free(&schema);
    ck->count++;
    return rc;
  }
  if (strcmp(kind, "vertex") == 0 || strcmp(kind, "edge") == 0) {
    int edge = kind[0] == 'e';
    ll_record *rec;
    if (ll_record_decode(lj_get(r, edge ? "edge" : "vertex"), edge, &rec)) return EXT_LEDGER_IO;
    rc = ll_state_put(ck->state, rec);
    ll_record_unref(rec);
    ck->count++;
    return rc;
  }
  if (strcmp(kind, "end") == 0) {
    uint64_t count;
    if (get_u64(r, "count", &count) || count != ck->count) return EXT_LEDGER_IO;
    ck->ended = 1;
    return 0;
  }
  return EXT_LEDGER_IO;
}

static int load_checkpoint(file_backend *fb, ll_state *state, ck_reader *ck) {
  memset(ck, 0, sizeof *ck);
  ck->state = state;
  ll_file *f;
  int rc = ll_file_open(fb->paths[P_CHECKPOINT], LL_READ, &f);
  if (rc == EXT_LEDGER_NOT_FOUND) return 0;
  if (rc) return rc;
  uint64_t end;
  rc = scan(f, 0, checkpoint_line, ck, &end);
  ll_file_close(f);
  if (!rc && !ck->ended) rc = EXT_LEDGER_IO;
  if (!rc) {
    state->cursor = ck->base;
    state->floor = ck->floor;
  }
  return rc;
}

/* ---- the log ---- */

typedef struct {
  file_backend *fb;
  ll_state *state;
  uint64_t generation;
} log_reader;

static int replay_commit(file_backend *fb, ll_state *st, const lj_value *r) {
  ll_change *c;
  if (ll_change_decode(lj_get(r, "change"), &c)) return EXT_LEDGER_IO;
  fb->commits++;
  int rc = 0;
  int graph = c->cursor > fb->base;
  if (graph && c->cursor != st->cursor + 1) rc = EXT_LEDGER_IO; /* cursors increase by one */
  if (!rc && c->cursor > st->floor) {
    ll_s key = {0}, fp = {0};
    const lj_value *k = lj_get(r, "idempotency_key"), *f = lj_get(r, "fingerprint");
    if (k && k->type == LJ_STRING) key = (ll_s){k->u.string.ptr, k->u.string.len};
    if (f && f->type == LJ_STRING) fp = (ll_s){f->u.string.ptr, f->u.string.len};
    rc = ll_state_apply(st, c, &key, &fp, graph);
  }
  ll_change_unref(c);
  return rc;
}

static int log_line(void *arg, const lj_value *r, uint64_t offset) {
  log_reader *lr = arg;
  const char *kind = record_kind(r);
  if (offset == 0) {
    if (strcmp(kind, "log") != 0) return EXT_LEDGER_UNSUPPORTED;
    int rc = check_layout(r);
    if (rc) return rc;
    return get_u64(r, "generation", &lr->generation);
  }
  if (strcmp(kind, "schema") == 0) {
    ll_schema schema;
    if (ll_schema_decode(lj_get(r, "schema"), &schema)) return EXT_LEDGER_IO;
    int rc = ll_state_set_schema(lr->state, &schema);
    ll_schema_free(&schema);
    return rc;
  }
  if (strcmp(kind, "commit") == 0) return replay_commit(lr->fb, lr->state, r);
  if (strcmp(kind, "floor") == 0) {
    uint64_t floor;
    if (get_u64(r, "floor", &floor)) return EXT_LEDGER_IO;
    ll_state_set_floor(lr->state, floor);
    return 0;
  }
  return EXT_LEDGER_IO;
}

/* Reads the checkpoint and the log from the start into a fresh state. */
static int reload(file_backend *fb, ll_state *state) {
  for (int attempt = 0; attempt < RELOAD_ATTEMPTS; attempt++) {
    ll_state fresh;
    ll_state_init(&fresh);
    ck_reader ck;
    int rc = load_checkpoint(fb, &fresh, &ck);
    ll_file *log = NULL;
    if (!rc) rc = ll_file_open(fb->paths[P_LOG], fb->read_only ? LL_READ : LL_READ | LL_WRITE, &log);
    if (rc == EXT_LEDGER_NOT_FOUND) rc = EXT_LEDGER_IO;
    log_reader lr = {fb, &fresh, 0};
    uint64_t end = 0, commits = fb->commits, base = fb->base;
    fb->commits = 0;
    fb->base = ck.base;
    if (!rc) rc = scan(log, 0, log_line, &lr, &end);
    if (!rc && end == 0) rc = EXT_LEDGER_IO; /* no header */
    if (!rc && lr.generation > ck.generation) { /* a rewrite ran between the two reads */
      rc = -1;
    }
    ll_fid id;
    if (!rc) rc = ll_file_identity(log, &id);
    if (rc) {
      ll_state_free(&fresh);
      ll_file_close(log);
      fb->commits = commits;
      fb->base = base;
      if (rc == -1) {
        ll_sleep_ms(2);
        continue;
      }
      return rc;
    }
    ll_state_free(state);
    *state = fresh;
    ll_file_close(fb->log);
    fb->log = log;
    fb->log_id = id;
    fb->generation = lr.generation;
    fb->offset = end;
    fb->stale = 0;
    return 0;
  }
  return EXT_LEDGER_BUSY;
}

static int fb_refresh(ll_backend *b, ll_state *state) {
  file_backend *fb = (file_backend *)b;
  if (fb->stale || !fb->log) return reload(fb, state);
  ll_fid id;
  int rc = ll_path_identity(fb->paths[P_LOG], &id);
  if (rc == EXT_LEDGER_NOT_FOUND) return EXT_LEDGER_IO;
  if (rc) return rc;
  if (id.a != fb->log_id.a || id.b != fb->log_id.b) return reload(fb, state);
  log_reader lr = {fb, state, fb->generation};
  uint64_t end;
  rc = scan(fb->log, fb->offset, log_line, &lr, &end);
  if (rc) {
    fb->stale = 1;
    return rc;
  }
  fb->offset = end;
  return 0;
}

/* ---- writing ---- */

static int take_lock(file_backend *fb, const ext_ledger_context *ctx) {
  int64_t give_up = ll_now_unix_ms() + LOCK_WAIT_MS;
  for (;;) {
    int rc = ll_file_try_lock(fb->lock);
    if (rc == 0) {
      fb->locked = 1;
      return 0;
    }
    if (rc != EXT_LEDGER_BUSY) return rc;
    if ((rc = ll_ctx_check(ctx))) return rc;
    if (ll_now_unix_ms() >= give_up) return EXT_LEDGER_BUSY;
    ll_sleep_ms(2);
  }
}

static void fb_end_write(ll_backend *b) {
  file_backend *fb = (file_backend *)b;
  if (fb->locked) ll_file_unlock(fb->lock);
  fb->locked = 0;
}

static int fb_begin_write(ll_backend *b, const ext_ledger_context *ctx, ll_state *state) {
  file_backend *fb = (file_backend *)b;
  if (fb->read_only) return EXT_LEDGER_UNSUPPORTED;
  int rc = take_lock(fb, ctx);
  if (rc) return rc;
  if (!(rc = fb_refresh(b, state))) {
    uint64_t size;
    rc = ll_file_size(fb->log, &size);
    if (!rc && size > fb->offset) rc = ll_file_truncate(fb->log, fb->offset); /* a writer stopped mid-line */
    ll_remove(fb->paths[P_LOG_TMP]);
    ll_remove(fb->paths[P_CHECKPOINT_TMP]);
  }
  if (rc) fb_end_write(b);
  return rc;
}

static int append(file_backend *fb, const lj_buf *lines) {
  if (lines->oom) return EXT_LEDGER_IO;
  int rc = ll_file_write_at(fb->log, fb->offset, lines->ptr, lines->len);
  if (!rc) rc = ll_file_sync(fb->log);
  if (rc) {
    fb->stale = 1;
    return rc;
  }
  fb->offset += lines->len;
  return 0;
}

static void schema_line(lj_buf *out, const ll_schema *schema) {
  lj_buf json = {0};
  lj_buf_puts(&json, "{\"record\":\"schema\",\"schema\":");
  ll_schema_write(&json, schema);
  lj_buf_putc(&json, '}');
  if (json.oom) out->oom = 1;
  else frame(out, &json);
  lj_buf_free(&json);
}

static void commit_line(lj_buf *out, const ll_retained *r) {
  lj_buf json = {0};
  lj_buf_puts(&json, "{\"change\":");
  ll_change_write(&json, r->change);
  if (r->key.n) {
    lj_buf_puts(&json, ",\"fingerprint\":");
    lj_write_string(&json, LL_S(r->fingerprint), r->fingerprint.n);
    lj_buf_puts(&json, ",\"idempotency_key\":");
    lj_write_string(&json, LL_S(r->key), r->key.n);
  }
  lj_buf_puts(&json, ",\"record\":\"commit\"}");
  if (json.oom) out->oom = 1;
  else frame(out, &json);
  lj_buf_free(&json);
}

static void header_line(lj_buf *out, const char *kind, uint64_t generation, int checkpoint, uint64_t base, uint64_t floor) {
  lj_buf json = {0};
  lj_buf_putc(&json, '{');
  if (checkpoint) {
    lj_buf_puts(&json, "\"base\":");
    lj_write_uint(&json, base);
    lj_buf_puts(&json, ",\"floor\":");
    lj_write_uint(&json, floor);
    lj_buf_putc(&json, ',');
  }
  lj_buf_puts(&json, "\"generation\":");
  lj_write_uint(&json, generation);
  lj_buf_puts(&json, ",\"layout\":\"" EXT_LEDGER_LAYOUT "\",\"record\":\"");
  lj_buf_puts(&json, kind);
  lj_buf_puts(&json, "\"}");
  if (json.oom) out->oom = 1;
  else frame(out, &json);
  lj_buf_free(&json);
}

static int fb_write_schema(ll_backend *b, ll_state *state, const ll_schema *schema) {
  (void)state;
  lj_buf lines = {0};
  schema_line(&lines, schema);
  int rc = append((file_backend *)b, &lines);
  lj_buf_free(&lines);
  return rc;
}

static int fb_write_commit(ll_backend *b, ll_state *state, const ll_retained *r, uint64_t floor, int moved) {
  (void)state;
  file_backend *fb = (file_backend *)b;
  lj_buf lines = {0};
  commit_line(&lines, r);
  if (moved) {
    lj_buf json = {0};
    lj_buf_puts(&json, "{\"floor\":");
    lj_write_uint(&json, floor);
    lj_buf_puts(&json, ",\"record\":\"floor\"}");
    if (json.oom) lines.oom = 1;
    else frame(&lines, &json);
    lj_buf_free(&json);
  }
  int rc = append(fb, &lines);
  lj_buf_free(&lines);
  if (!rc) fb->commits++;
  return rc;
}

/* ---- rewriting ---- */

static int ns_cmp(const void *a, const void *b) {
  const ll_namespace *x = *(ll_namespace *const *)a, *y = *(ll_namespace *const *)b;
  return ll_s_cmp(&x->schema.ns, &y->schema.ns);
}

static int write_file(const char *tmp, const char *path, const lj_buf *content) {
  if (content->oom) return EXT_LEDGER_IO;
  ll_file *f;
  int rc = ll_file_open(tmp, LL_READ | LL_WRITE | LL_CREATE | LL_TRUNC, &f);
  if (rc) return rc;
  rc = ll_file_write_at(f, 0, content->ptr, content->len);
  if (!rc) rc = ll_file_sync(f);
  ll_file_close(f);
  if (!rc) rc = ll_rename(tmp, path);
  return rc;
}

static void record_lines(lj_buf *out, ll_namespace **spaces, size_t n, int edges, uint64_t *count) {
  for (size_t i = 0; i < n; i++) {
    ll_map *m = edges ? &spaces[i]->edges : &spaces[i]->vertices;
    ll_record **records = m->len ? malloc(m->len * sizeof *records) : NULL;
    if (m->len && !records) {
      out->oom = 1;
      return;
    }
    size_t k = 0;
    LL_MAP_EACH(m, slot) records[k++] = slot->value;
    if (k) qsort(records, k, sizeof *records, ll_record_id_cmp);
    for (size_t j = 0; j < k; j++) {
      lj_buf json = {0};
      lj_buf_puts(&json, edges ? "{\"edge\":" : "{\"record\":\"vertex\",\"vertex\":");
      ll_record_write(&json, records[j]);
      lj_buf_puts(&json, edges ? ",\"record\":\"edge\"}" : "}");
      if (json.oom) out->oom = 1;
      else frame(out, &json);
      lj_buf_free(&json);
      (*count)++;
    }
    free(records);
  }
}

static int rewrite(file_backend *fb, ll_state *state) {
  uint64_t generation = fb->generation + 1;
  size_t n = state->namespaces.len, k = 0;
  ll_namespace **spaces = n ? malloc(n * sizeof *spaces) : NULL;
  if (n && !spaces) return EXT_LEDGER_IO;
  LL_MAP_EACH(&state->namespaces, slot) spaces[k++] = slot->value;
  if (k) qsort(spaces, k, sizeof *spaces, ns_cmp);
  lj_buf ck = {0};
  uint64_t count = 0;
  header_line(&ck, "checkpoint", generation, 1, state->cursor, state->floor);
  for (size_t i = 0; i < k; i++) {
    lj_buf json = {0};
    lj_buf_puts(&json, "{\"record\":\"namespace\",\"revision\":");
    lj_write_uint(&json, spaces[i]->revision);
    lj_buf_puts(&json, ",\"schema\":");
    ll_schema_write(&json, &spaces[i]->schema);
    lj_buf_putc(&json, '}');
    if (json.oom) ck.oom = 1;
    else frame(&ck, &json);
    lj_buf_free(&json);
    count++;
  }
  record_lines(&ck, spaces, k, 0, &count);
  record_lines(&ck, spaces, k, 1, &count);
  free(spaces);
  lj_buf end = {0};
  lj_buf_puts(&end, "{\"count\":");
  lj_write_uint(&end, count);
  lj_buf_puts(&end, ",\"record\":\"end\"}");
  if (end.oom) ck.oom = 1;
  else frame(&ck, &end);
  lj_buf_free(&end);
  int rc = write_file(fb->paths[P_CHECKPOINT_TMP], fb->paths[P_CHECKPOINT], &ck);
  lj_buf_free(&ck);
  if (rc) return rc;

  lj_buf log = {0};
  header_line(&log, "log", generation, 0, 0, 0);
  size_t retained = ll_state_retained(state);
  for (size_t i = 0; i < retained; i++) commit_line(&log, ll_state_retained_at(state, i));
  rc = write_file(fb->paths[P_LOG_TMP], fb->paths[P_LOG], &log);
  size_t log_len = log.len;
  lj_buf_free(&log);
  if (rc) return rc;
  ll_sync_dir(fb->dir);

  ll_file *f;
  if ((rc = ll_file_open(fb->paths[P_LOG], LL_READ | LL_WRITE, &f))) return rc;
  ll_fid id;
  if ((rc = ll_file_identity(f, &id))) {
    ll_file_close(f);
    return rc;
  }
  ll_file_close(fb->log);
  fb->log = f;
  fb->log_id = id;
  fb->generation = generation;
  fb->base = state->cursor;
  fb->offset = log_len;
  fb->commits = retained;
  return 0;
}

static void fb_maintain(ll_backend *b, ll_state *state) {
  file_backend *fb = (file_backend *)b;
  uint64_t live = ll_state_retained(state);
  uint64_t dead = fb->commits > live ? fb->commits - live : 0;
  if (dead < REWRITE_MIN_DEAD || dead < live) return;
  if (rewrite(fb, state)) fb->stale = 1; /* the files on disk are still valid; read them again next time */
}

/* ---- opening ---- */

static void fb_close(ll_backend *b) {
  file_backend *fb = (file_backend *)b;
  fb_end_write(b);
  ll_file_close(fb->log);
  ll_file_close(fb->lock);
  for (int i = 0; i < P_COUNT; i++) free(fb->paths[i]);
  free(fb->dir);
  free(fb);
}

static const ll_backend_ops file_ops = {fb_refresh, fb_begin_write, fb_end_write, fb_write_schema, fb_write_commit, fb_maintain, fb_close};

static int allowed_entry(void *arg, const char *name) {
  (void)arg;
  if (name[0] == '.') return 0;
  for (int i = 0; i < P_COUNT; i++)
    if (strcmp(name, file_names[i]) == 0) return 0;
  return EXT_LEDGER_UNSUPPORTED;
}

static char *join(const char *dir, const char *name) {
  size_t a = strlen(dir), b = strlen(name);
  while (a > 1 && (dir[a - 1] == '/' || dir[a - 1] == '\\')) a--;
  char *p = malloc(a + b + 2);
  if (!p) return NULL;
  memcpy(p, dir, a);
  p[a] = '/';
  memcpy(p + a + 1, name, b + 1);
  return p;
}

/* Creates the log of a new store, atomically, so a reader never sees one without its header. */
static int create_log(file_backend *fb, const ext_ledger_context *ctx) {
  int rc = take_lock(fb, ctx);
  if (rc) return rc;
  if (ll_path_kind(fb->paths[P_LOG]) == 0) {
    ll_state scratch;
    ll_state_init(&scratch);
    ck_reader ck;
    rc = load_checkpoint(fb, &scratch, &ck);
    ll_state_free(&scratch);
    lj_buf log = {0};
    if (!rc) header_line(&log, "log", ck.generation, 0, 0, 0);
    if (!rc) rc = write_file(fb->paths[P_LOG_TMP], fb->paths[P_LOG], &log);
    lj_buf_free(&log);
    if (!rc) ll_sync_dir(fb->dir);
  }
  fb_end_write(&fb->backend);
  return rc;
}

int ll_dir_open(const ext_ledger_context *ctx, const char *dir, int read_only, ll_state *state, ll_backend **out) {
  int kind = ll_path_kind(dir);
  if (kind < 0) return EXT_LEDGER_IO;
  if (kind == 2) return EXT_LEDGER_UNSUPPORTED;
  if (kind == 0 && read_only) return EXT_LEDGER_NOT_FOUND;
  if (kind == 0 && ll_mkdir(dir)) return EXT_LEDGER_IO;
  int rc = ll_list_dir(dir, allowed_entry, NULL);
  if (rc) return rc;
  file_backend *fb = calloc(1, sizeof *fb);
  if (!fb) return EXT_LEDGER_IO;
  fb->backend.ops = &file_ops;
  fb->read_only = read_only;
  fb->dir = join(dir, ".");
  if (fb->dir) fb->dir[strlen(fb->dir) - 2] = 0; /* dir without a trailing separator */
  for (int i = 0; i < P_COUNT; i++)
    if (!(fb->paths[i] = join(dir, file_names[i]))) rc = EXT_LEDGER_IO;
  if (!fb->dir) rc = EXT_LEDGER_IO;
  if (!rc && read_only && ll_path_kind(fb->paths[P_LOG]) == 0) rc = EXT_LEDGER_NOT_FOUND; /* no store here yet */
  if (!rc && !read_only) rc = ll_file_open(fb->paths[P_LOCK], LL_READ | LL_WRITE | LL_CREATE, &fb->lock);
  if (!rc && !read_only) rc = create_log(fb, ctx);
  if (!rc) rc = reload(fb, state);
  if (rc) {
    fb_close(&fb->backend);
    return rc;
  }
  *out = &fb->backend;
  return 0;
}
