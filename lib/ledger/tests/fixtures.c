/* Runs the res/graph store contract (cases.json and fingerprints.json) against
 * the memory and file backends, as res/graph/store/storetest does in Go. */
#include "ext_ledger.h"
#include "ledger_internal.h"
#include "testutil.h"

static const char *fixtures;
static int failures;

static lj_value *load(const char *name) {
  char path[2048];
  snprintf(path, sizeof path, "%s/%s", fixtures, name);
  size_t len;
  char *text = read_file(path, &len);
  if (!text) {
    fprintf(stderr, "cannot read %s\n", path);
    exit(2);
  }
  lj_value *v = lj_parse(text, len, NULL);
  free(text);
  if (!v) {
    fprintf(stderr, "%s is not JSON\n", path);
    exit(2);
  }
  return v;
}

static char *to_text(const lj_value *v, size_t *len) {
  lj_buf b = {0};
  if (v) lj_write(&b, v);
  else lj_buf_puts(&b, "null");
  return lj_buf_take(&b, len);
}

static const char *str_field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : "";
}

static double num_field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_NUMBER ? v->u.number : 0;
}

static ext_ledger_options options_of(const lj_value *c) {
  const lj_value *o = lj_get(c, "options");
  ext_ledger_options opts = {0};
  opts.max_changes = (int64_t)num_field(o, "max_changes");
  opts.max_change_bytes = (int64_t)num_field(o, "max_change_bytes");
  opts.max_transaction_records = (int64_t)num_field(o, "max_transaction_records");
  opts.max_record_bytes = (int64_t)num_field(o, "max_record_bytes");
  opts.max_results = (int64_t)num_field(o, "max_results");
  opts.max_snapshot_records = (int64_t)num_field(o, "max_snapshot_records");
  return opts;
}

typedef enum { B_MEMORY, B_FILE, B_SQLITE } backend;
static const char *const backend_names[] = {"memory", "file", "sqlite"};

static int open_store(backend b, const char *dir, const ext_ledger_options *o, ext_ledger **out) {
  if (b == B_MEMORY) return ext_ledger_open_memory(o, out);
  if (b == B_SQLITE) {
    char path[1300];
    snprintf(path, sizeof path, "%s/graph.db", dir);
    return ext_ledger_open_sqlite(NULL, path, o, out);
  }
  return ext_ledger_open(NULL, dir, o, out);
}

static int run_watch(ext_ledger *s, const lj_value *st, char **out, size_t *out_len) {
  const lj_value *expect = lj_get(st, "expect");
  size_t want = expect && expect->type == LJ_ARRAY ? expect->u.array.len : 0;
  ext_ledger_watch *w;
  int rc = ext_ledger_watch_open(s, NULL, str_field(st, "namespace"), (uint64_t)num_field(st, "after"), (int64_t)num_field(st, "buffer"), &w);
  if (rc) return rc;
  lj_buf b = {0};
  lj_buf_putc(&b, '[');
  for (size_t i = 0; i < want; i++) {
    char *change;
    size_t len;
    if (ext_ledger_watch_next(w, 2000, &change, &len)) break;
    if (i) lj_buf_putc(&b, ',');
    lj_buf_put(&b, change, len);
    ext_ledger_free(change);
  }
  lj_buf_putc(&b, ']');
  ext_ledger_watch_close(w);
  *out = lj_buf_take(&b, out_len);
  return 0;
}

static int run_step(ext_ledger *s, const lj_value *st, char **out, size_t *out_len) {
  const char *op = str_field(st, "op");
  *out = NULL;
  size_t len;
  int rc;
  if (strcmp(op, "register") == 0) {
    char *text = to_text(lj_get(st, "schema"), &len);
    rc = ext_ledger_register(s, NULL, text, len);
    free(text);
    return rc;
  }
  if (strcmp(op, "apply") == 0) {
    char *text = to_text(lj_get(st, "transaction"), &len);
    rc = ext_ledger_apply(s, NULL, text, len, out, out_len);
    free(text);
    return rc;
  }
  if (strcmp(op, "get_vertex") == 0) return ext_ledger_get_vertex(s, NULL, str_field(st, "namespace"), str_field(st, "id"), out, out_len);
  if (strcmp(op, "get_edge") == 0) return ext_ledger_get_edge(s, NULL, str_field(st, "namespace"), str_field(st, "id"), out, out_len);
  if (strcmp(op, "query_vertices") == 0 || strcmp(op, "query_edges") == 0) {
    char *text = to_text(lj_get(st, "query"), &len);
    rc = op[6] == 'v' ? ext_ledger_query_vertices(s, NULL, text, len, out, out_len) : ext_ledger_query_edges(s, NULL, text, len, out, out_len);
    free(text);
    return rc;
  }
  if (strcmp(op, "traverse") == 0)
    return ext_ledger_traverse(s, NULL, str_field(st, "namespace"), str_field(st, "start"), (int64_t)num_field(st, "max_depth"),
                               (int64_t)num_field(st, "max_vertices"), out, out_len);
  if (strcmp(op, "snapshot") == 0) return ext_ledger_snapshot(s, NULL, str_field(st, "namespace"), out, out_len);
  if (strcmp(op, "changes") == 0)
    return ext_ledger_changes(s, NULL, str_field(st, "namespace"), (uint64_t)num_field(st, "after"), (int64_t)num_field(st, "limit"), out, out_len);
  if (strcmp(op, "watch") == 0) return run_watch(s, st, out, out_len);
  fprintf(stderr, "unknown op %s\n", op);
  exit(2);
}

static void run_case(const lj_value *c, backend b) {
  const char *name = str_field(c, "name");
  const lj_value *durable = lj_get(c, "durable");
  if (b == B_MEMORY && durable && durable->type == LJ_BOOL && durable->u.boolean) return;
  ext_ledger_options o = options_of(c);
  char *dir = temp_path("case");
  ext_ledger *s;
  int rc = open_store(b, dir, &o, &s);
  if (rc) {
    fprintf(stderr, "FAIL %s [%s]: open: %s\n", name, backend_names[b], ext_ledger_code_name(rc));
    failures++;
    remove_store(dir);
  free(dir);
    return;
  }
  const lj_value *steps = lj_get(c, "steps");
  for (size_t i = 0; i < steps->u.array.len; i++) {
    const lj_value *st = steps->u.array.items[i];
    if (strcmp(str_field(st, "op"), "reopen") == 0) {
      ext_ledger_close(s);
      if ((rc = open_store(b, dir, &o, &s))) {
        fprintf(stderr, "FAIL %s [%s]: step %zu: reopen: %s\n", name, backend_names[b], i, ext_ledger_code_name(rc));
        failures++;
        remove_store(dir);
  free(dir);
        return;
      }
      continue;
    }
    char *out;
    size_t len;
    rc = run_step(s, st, &out, &len);
    const char *want_code = str_field(st, "error");
    if (strcmp(ext_ledger_code_name(rc), want_code) != 0) {
      fprintf(stderr, "FAIL %s [%s]: step %zu (%s): code \"%s\", want \"%s\"\n", name, backend_names[b], i, str_field(st, "op"),
              ext_ledger_code_name(rc), want_code);
      failures++;
      ext_ledger_free(out);
      break;
    }
    const lj_value *expect = lj_get(st, "expect");
    if (!rc && expect) {
      lj_value *actual = out ? lj_parse(out, len, NULL) : NULL;
      if (!match(expect, actual)) {
        size_t wl;
        char *w = to_text(expect, &wl);
        fprintf(stderr, "FAIL %s [%s]: step %zu (%s):\n  got  %s\n  want %s\n", name, backend_names[b], i, str_field(st, "op"),
                out ? out : "null", w);
        free(w);
        failures++;
        lj_free(actual);
        ext_ledger_free(out);
        break;
      }
      lj_free(actual);
    }
    ext_ledger_free(out);
  }
  ext_ledger_close(s);
  remove_store(dir);
  free(dir);
}

static void run_fingerprints(void) {
  lj_value *file = load("fingerprints.json");
  const lj_value *cases = lj_get(file, "cases");
  for (size_t i = 0; i < cases->u.array.len; i++) {
    const lj_value *c = cases->u.array.items[i];
    size_t len, clen;
    char *text = to_text(lj_get(c, "transaction"), &len);
    char *canonical, fp[65];
    int rc = ext_ledger_canonical_transaction(text, len, &canonical, &clen, fp);
    free(text);
    if (rc || strcmp(canonical, str_field(c, "canonical")) != 0 || strcmp(fp, str_field(c, "sha256")) != 0) {
      fprintf(stderr, "FAIL fingerprint %s:\n  got  %s %s\n  want %s %s\n", str_field(c, "name"), rc ? ext_ledger_code_name(rc) : canonical,
              rc ? "" : fp, str_field(c, "canonical"), str_field(c, "sha256"));
      failures++;
    }
    if (!rc) ext_ledger_free(canonical);
  }
  lj_free(file);
}

/* The SQLite backend's tables are those of tables.json, written as the Go store writes them. */
static void quote_all(lj_buf *b, const lj_value *names) {
  for (size_t i = 0; i < names->u.array.len; i++) {
    if (i) lj_buf_puts(b, ", ");
    lj_buf_putc(b, '"');
    lj_buf_puts(b, names->u.array.items[i]->u.string.ptr);
    lj_buf_putc(b, '"');
  }
}

static void run_layout(void) {
  const char *const *have = ll_sqlite_layout();
  if (!have) return;
  lj_value *file = load("tables.json");
  size_t k = 0;
  const lj_value *tables = lj_get(file, "tables"), *indexes = lj_get(file, "indexes");
  for (size_t i = 0; i < tables->u.array.len + indexes->u.array.len; i++, k++) {
    lj_buf b = {0};
    if (i < tables->u.array.len) {
      const lj_value *t = tables->u.array.items[i];
      lj_buf_puts(&b, "CREATE TABLE \"");
      lj_buf_puts(&b, str_field(t, "name"));
      lj_buf_puts(&b, "\" (\n  ");
      const lj_value *cols = lj_get(t, "columns");
      int parts = 0;
      for (size_t c = 0; c < cols->u.array.len; c++, parts++) {
        const lj_value *col = cols->u.array.items[c];
        if (parts) lj_buf_puts(&b, ",\n  ");
        lj_buf_putc(&b, '"');
        lj_buf_puts(&b, str_field(col, "name"));
        lj_buf_puts(&b, "\" ");
        lj_buf_puts(&b, str_field(col, "type"));
        const lj_value *nn = lj_get(col, "not_null");
        if (nn && nn->u.boolean) lj_buf_puts(&b, " NOT NULL");
        if (*str_field(col, "check")) {
          lj_buf_puts(&b, " CHECK (");
          lj_buf_puts(&b, str_field(col, "check"));
          lj_buf_putc(&b, ')');
        }
      }
      const lj_value *pk = lj_get(t, "primary_key");
      if (pk) {
        lj_buf_puts(&b, ",\n  PRIMARY KEY (");
        quote_all(&b, pk);
        lj_buf_putc(&b, ')');
      }
      const lj_value *fks = lj_get(t, "foreign_keys");
      for (size_t f = 0; fks && f < fks->u.array.len; f++) {
        const lj_value *fk = fks->u.array.items[f];
        lj_buf_puts(&b, ",\n  FOREIGN KEY (");
        quote_all(&b, lj_get(fk, "columns"));
        lj_buf_puts(&b, ") REFERENCES \"");
        lj_buf_puts(&b, str_field(fk, "references"));
        lj_buf_puts(&b, "\" (");
        quote_all(&b, lj_get(fk, "to"));
        lj_buf_putc(&b, ')');
      }
      lj_buf_puts(&b, "\n)");
      const lj_value *wr = lj_get(t, "without_rowid");
      if (wr && wr->u.boolean) lj_buf_puts(&b, " WITHOUT ROWID");
    } else {
      const lj_value *ix = indexes->u.array.items[i - tables->u.array.len];
      lj_buf_puts(&b, "CREATE INDEX \"");
      lj_buf_puts(&b, str_field(ix, "name"));
      lj_buf_puts(&b, "\" ON \"");
      lj_buf_puts(&b, str_field(ix, "table"));
      lj_buf_puts(&b, "\" (");
      quote_all(&b, lj_get(ix, "columns"));
      lj_buf_putc(&b, ')');
    }
    if (!have[k] || strcmp(have[k], b.ptr) != 0) {
      fprintf(stderr, "FAIL sqlite layout statement %zu:\n  got  %s\n  want %s\n", k, have[k] ? have[k] : "(none)", b.ptr);
      failures++;
    }
    lj_buf_free(&b);
  }
  if (have[k]) {
    fprintf(stderr, "FAIL sqlite layout: an extra statement %s\n", have[k]);
    failures++;
  }
  lj_free(file);
}

int main(int argc, char **argv) {
  if (argc != 2) {
    fprintf(stderr, "usage: ledger_fixtures <lib/ledger/testdata/store/v1>\n");
    return 2;
  }
  fixtures = argv[1];
  run_fingerprints();
  run_layout();
  int sqlite = ll_sqlite_layout() != NULL; /* built with the SQLite backend */
  lj_value *file = load("cases.json");
  const lj_value *cases = lj_get(file, "cases");
  size_t n = cases->u.array.len;
  for (size_t i = 0; i < n; i++) {
    run_case(cases->u.array.items[i], B_MEMORY);
    run_case(cases->u.array.items[i], B_FILE);
    if (sqlite) run_case(cases->u.array.items[i], B_SQLITE);
  }
  lj_free(file);
  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("ok: %zu cases on memory, file%s; fingerprints\n", n, sqlite ? ", sqlite; the sqlite layout" : "");
  return 0;
}
