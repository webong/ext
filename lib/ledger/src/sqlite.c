/* The optional SQLite backend: one database laid out as
 * lib/ledger/testdata/store/v1/tables.json says, the layout the Go store in
 * pkg/ext-go/engine/ledger/recorder/sqlite writes, so either opens the other's database. Like
 * every backend it keeps the state in memory; it catches up with other
 * connections when SQLite's data_version moves. Built with EXT_LEDGER_SQLITE. */
#include "ledger_internal.h"

#include <sqlite3.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define SQLITE_LAYOUT "ext.graph.store/v1"

/* The tables and indexes of tables.json, written as the Go store writes them;
 * tests/fixtures.c checks them against the fixture. */
static const char *const layout_statements[] = {
    "CREATE TABLE \"store\" (\n  \"id\" INTEGER NOT NULL CHECK (id = 1),\n  \"layout\" TEXT NOT NULL,\n  \"cursor\" INTEGER NOT NULL,\n  "
    "\"history_floor\" INTEGER NOT NULL,\n  PRIMARY KEY (\"id\")\n)",
    "CREATE TABLE \"namespaces\" (\n  \"namespace\" TEXT NOT NULL,\n  \"schema_version\" TEXT NOT NULL,\n  \"schema\" TEXT NOT NULL,\n  "
    "\"revision\" INTEGER NOT NULL,\n  PRIMARY KEY (\"namespace\")\n) WITHOUT ROWID",
    "CREATE TABLE \"vertices\" (\n  \"namespace\" TEXT NOT NULL,\n  \"id\" TEXT NOT NULL,\n  \"kind\" TEXT NOT NULL,\n  \"revision\" INTEGER NOT "
    "NULL,\n  \"created_at\" TEXT NOT NULL,\n  \"updated_at\" TEXT NOT NULL,\n  \"labels\" TEXT,\n  \"attributes\" TEXT,\n  \"provenance\" TEXT,\n  "
    "PRIMARY KEY (\"namespace\", \"id\"),\n  FOREIGN KEY (\"namespace\") REFERENCES \"namespaces\" (\"namespace\")\n) WITHOUT ROWID",
    "CREATE TABLE \"edges\" (\n  \"namespace\" TEXT NOT NULL,\n  \"id\" TEXT NOT NULL,\n  \"type\" TEXT NOT NULL,\n  \"from_id\" TEXT NOT NULL,\n  "
    "\"to_id\" TEXT NOT NULL,\n  \"revision\" INTEGER NOT NULL,\n  \"created_at\" TEXT NOT NULL,\n  \"updated_at\" TEXT NOT NULL,\n  \"labels\" "
    "TEXT,\n  \"attributes\" TEXT,\n  \"provenance\" TEXT,\n  PRIMARY KEY (\"namespace\", \"id\"),\n  FOREIGN KEY (\"namespace\", \"from_id\") "
    "REFERENCES \"vertices\" (\"namespace\", \"id\"),\n  FOREIGN KEY (\"namespace\", \"to_id\") REFERENCES \"vertices\" (\"namespace\", \"id\")\n) "
    "WITHOUT ROWID",
    "CREATE TABLE \"changes\" (\n  \"cursor\" INTEGER NOT NULL,\n  \"namespace\" TEXT NOT NULL,\n  \"revision\" INTEGER NOT NULL,\n  "
    "\"committed_at\" TEXT NOT NULL,\n  \"size\" INTEGER NOT NULL,\n  \"body\" TEXT NOT NULL,\n  PRIMARY KEY (\"cursor\")\n)",
    "CREATE TABLE \"idempotency\" (\n  \"namespace\" TEXT NOT NULL,\n  \"key\" TEXT NOT NULL,\n  \"fingerprint\" TEXT NOT NULL,\n  \"revision\" "
    "INTEGER NOT NULL,\n  \"cursor\" INTEGER NOT NULL,\n  \"committed_at\" TEXT NOT NULL,\n  PRIMARY KEY (\"namespace\", \"key\")\n) WITHOUT ROWID",
    "CREATE INDEX \"vertices_by_kind\" ON \"vertices\" (\"namespace\", \"kind\", \"id\")",
    "CREATE INDEX \"edges_by_type\" ON \"edges\" (\"namespace\", \"type\", \"id\")",
    "CREATE INDEX \"edges_by_from\" ON \"edges\" (\"namespace\", \"from_id\", \"id\")",
    "CREATE INDEX \"edges_by_to\" ON \"edges\" (\"namespace\", \"to_id\", \"id\")",
    "CREATE INDEX \"changes_by_namespace\" ON \"changes\" (\"namespace\", \"cursor\")",
    "CREATE INDEX \"idempotency_by_cursor\" ON \"idempotency\" (\"cursor\")",
    NULL};

const char *const *ll_sqlite_layout(void) { return layout_statements; }

typedef struct {
  ll_backend backend;
  sqlite3 *db;
  sqlite3_int64 data_version;
  int in_tx, stale;
} sqlite_backend;

static int code_of(int rc) {
  switch (rc & 0xff) {
  case SQLITE_OK:
  case SQLITE_DONE:
  case SQLITE_ROW:
    return 0;
  case SQLITE_BUSY:
  case SQLITE_LOCKED:
    return EXT_LEDGER_BUSY;
  case SQLITE_NOMEM:
    return EXT_LEDGER_IO;
  default:
    return EXT_LEDGER_IO;
  }
}

static int exec(sqlite_backend *sb, const char *sql) { return code_of(sqlite3_exec(sb->db, sql, NULL, NULL, NULL)); }

/* ---- statements ---- */

typedef struct {
  sqlite3_stmt *st;
  int rc;
} stmt;

static stmt prepare(sqlite_backend *sb, const char *sql) {
  stmt s = {NULL, 0};
  int rc = sqlite3_prepare_v2(sb->db, sql, -1, &s.st, NULL);
  if (rc) s.rc = code_of(rc) ? code_of(rc) : EXT_LEDGER_IO;
  return s;
}

static void bind_text(stmt *s, int i, const char *p, size_t n) {
  if (!s->rc && sqlite3_bind_text(s->st, i, p ? p : "", (int)n, SQLITE_TRANSIENT)) s->rc = EXT_LEDGER_IO;
}
static void bind_s(stmt *s, int i, const ll_s *v) { bind_text(s, i, LL_S(*v), v->n); }
static void bind_u64(stmt *s, int i, uint64_t v) {
  if (!s->rc && sqlite3_bind_int64(s->st, i, (sqlite3_int64)v)) s->rc = EXT_LEDGER_IO;
}
/* Binds the buffer as text, or NULL when the buffer is empty. */
static void bind_json(stmt *s, int i, const lj_buf *b) {
  if (s->rc) return;
  if (b->oom) {
    s->rc = EXT_LEDGER_IO;
    return;
  }
  if (!b->len) {
    if (sqlite3_bind_null(s->st, i)) s->rc = EXT_LEDGER_IO;
    return;
  }
  bind_text(s, i, b->ptr, b->len);
}

/* Runs a statement that returns no rows and finalises it. */
static int done(stmt *s) {
  if (!s->rc) {
    int rc = sqlite3_step(s->st);
    if (rc != SQLITE_DONE) s->rc = code_of(rc) ? code_of(rc) : EXT_LEDGER_IO;
  }
  sqlite3_finalize(s->st);
  return s->rc;
}

static int step(stmt *s) { /* 1 for a row, 0 when done, an error code otherwise (negated) */
  if (s->rc) return -s->rc;
  int rc = sqlite3_step(s->st);
  if (rc == SQLITE_ROW) return 1;
  if (rc == SQLITE_DONE) return 0;
  s->rc = code_of(rc) ? code_of(rc) : EXT_LEDGER_IO;
  return -s->rc;
}

static const char *col_text(stmt *s, int i, size_t *n) {
  const char *p = (const char *)sqlite3_column_text(s->st, i);
  *n = (size_t)sqlite3_column_bytes(s->st, i);
  return p;
}

/* A time as the Go store writes it: RFC 3339 with nanoseconds, in UTC, unquoted. */
static void time_text(lj_buf *b, const ll_time *t) {
  ll_time u = ll_time_utc(*t);
  lj_buf q = {0};
  ll_time_write(&q, &u);
  if (q.oom || q.len < 2) b->oom = 1;
  else lj_buf_put(b, q.ptr + 1, q.len - 2);
  lj_buf_free(&q);
}

/* ---- reading ---- */

/* Adds a column holding JSON (or NULL) to a record object under key. */
static int put_json(lj_value *o, const char *key, stmt *s, int i) {
  if (sqlite3_column_type(s->st, i) == SQLITE_NULL) return 0;
  size_t n;
  const char *p = col_text(s, i, &n);
  lj_value *v = lj_parse(p, n, NULL);
  if (!v) return EXT_LEDGER_IO;
  return lj_object_set(o, key, strlen(key), v) ? EXT_LEDGER_IO : 0;
}

static int put_text(lj_value *o, const char *key, stmt *s, int i) {
  size_t n;
  const char *p = col_text(s, i, &n);
  return lj_object_set(o, key, strlen(key), lj_new_string(p ? p : "", n)) ? EXT_LEDGER_IO : 0;
}

/* Builds the record of a vertices or edges row: namespace, id, kind or (type, from_id, to_id), revision, created_at, updated_at,
 * labels, attributes, provenance. */
static int row_record(stmt *s, int edge, ll_record **out) {
  lj_value *o = lj_new(LJ_OBJECT);
  if (!o) return EXT_LEDGER_IO;
  int i = 0, rc = put_text(o, "namespace", s, i++);
  if (!rc) rc = put_text(o, "id", s, i++);
  if (!rc) rc = put_text(o, edge ? "type" : "kind", s, i++);
  if (!rc && edge) rc = put_text(o, "from", s, i++);
  if (!rc && edge) rc = put_text(o, "to", s, i++);
  if (!rc && lj_object_set(o, "revision", 8, lj_new_number((double)sqlite3_column_int64(s->st, i++)))) rc = EXT_LEDGER_IO;
  if (!rc) rc = put_text(o, "created_at", s, i++);
  if (!rc) rc = put_text(o, "updated_at", s, i++);
  if (!rc) rc = put_json(o, "labels", s, i++);
  if (!rc) rc = put_json(o, "attributes", s, i++);
  if (!rc) rc = put_json(o, "provenance", s, i++);
  if (!rc && ll_record_decode(o, edge, out)) rc = EXT_LEDGER_IO;
  lj_free(o);
  return rc;
}

static int read_namespaces(sqlite_backend *sb, ll_state *state, int with_revision) {
  stmt s = prepare(sb, "SELECT schema, revision FROM namespaces");
  int r;
  while ((r = step(&s)) == 1) {
    size_t n;
    const char *p = col_text(&s, 0, &n);
    lj_value *v = lj_parse(p, n, NULL);
    ll_schema schema;
    int rc = v ? ll_schema_decode(v, &schema) : EXT_LEDGER_IO;
    lj_free(v);
    if (!rc) {
      rc = ll_state_set_schema(state, &schema);
      if (!rc && with_revision) ll_state_ns(state, LL_S(schema.ns), schema.ns.n)->revision = (uint64_t)sqlite3_column_int64(s.st, 1);
      ll_schema_free(&schema);
    }
    if (rc) {
      s.rc = rc;
      r = -rc;
      break;
    }
  }
  sqlite3_finalize(s.st);
  return r < 0 ? -r : 0;
}

static int read_records(sqlite_backend *sb, ll_state *state, int edge) {
  stmt s = prepare(sb, edge ? "SELECT namespace, id, type, from_id, to_id, revision, created_at, updated_at, labels, attributes, provenance FROM edges"
                            : "SELECT namespace, id, kind, revision, created_at, updated_at, labels, attributes, provenance FROM vertices");
  int r;
  while ((r = step(&s)) == 1) {
    ll_record *rec;
    int rc = row_record(&s, edge, &rec);
    if (!rc) {
      rc = ll_state_put(state, rec);
      ll_record_unref(rec);
    }
    if (rc) {
      r = -rc;
      break;
    }
  }
  sqlite3_finalize(s.st);
  return r < 0 ? -r : 0;
}

/* Reads the changes above after, applying them to the graph when graph is set. */
static int read_changes(sqlite_backend *sb, ll_state *state, uint64_t after, int graph) {
  stmt s = prepare(sb, "SELECT body FROM changes WHERE cursor > ? ORDER BY cursor");
  bind_u64(&s, 1, after);
  ll_s none = {0};
  int r;
  while ((r = step(&s)) == 1) {
    size_t n;
    const char *p = col_text(&s, 0, &n);
    lj_value *v = lj_parse(p, n, NULL);
    ll_change *c = NULL;
    int rc = v ? ll_change_decode(v, &c) : EXT_LEDGER_IO;
    lj_free(v);
    if (!rc && graph && c->cursor != state->cursor + 1) rc = EXT_LEDGER_IO;
    if (!rc) rc = ll_state_apply(state, c, &none, &none, graph);
    ll_change_unref(c);
    if (rc) {
      r = -rc;
      break;
    }
  }
  sqlite3_finalize(s.st);
  return r < 0 ? -r : 0;
}

static int read_idempotency(sqlite_backend *sb, ll_state *state, uint64_t after) {
  stmt s = prepare(sb, "SELECT namespace, key, fingerprint, revision, cursor, committed_at FROM idempotency WHERE cursor > ?");
  bind_u64(&s, 1, after);
  int r;
  while ((r = step(&s)) == 1) {
    size_t nn, kn, fn, tn;
    const char *ns = col_text(&s, 0, &nn);
    ll_s nss = {(char *)ns, nn};
    const char *key = col_text(&s, 1, &kn);
    ll_s keys = {(char *)key, kn};
    const char *fp = col_text(&s, 2, &fn);
    ll_idem idem;
    memset(&idem, 0, sizeof idem);
    memcpy(idem.fingerprint, fp ? fp : "", fn < 64 ? fn : 64);
    idem.revision = (uint64_t)sqlite3_column_int64(s.st, 3);
    idem.cursor = (uint64_t)sqlite3_column_int64(s.st, 4);
    const char *t = col_text(&s, 5, &tn);
    int rc = ll_time_parse(t ? t : "", tn, &idem.committed) ? EXT_LEDGER_IO : 0;
    if (!rc) rc = ll_state_put_idem(state, &nss, &keys, &idem);
    if (rc) {
      r = -rc;
      break;
    }
  }
  sqlite3_finalize(s.st);
  return r < 0 ? -r : 0;
}

static int read_store_row(sqlite_backend *sb, uint64_t *cursor, uint64_t *floor) {
  stmt s = prepare(sb, "SELECT cursor, history_floor FROM store WHERE id = 1");
  int r = step(&s);
  if (r == 1) {
    *cursor = (uint64_t)sqlite3_column_int64(s.st, 0);
    *floor = (uint64_t)sqlite3_column_int64(s.st, 1);
  }
  sqlite3_finalize(s.st);
  return r == 1 ? 0 : r < 0 ? -r : EXT_LEDGER_IO;
}

static int read_data_version(sqlite_backend *sb, sqlite3_int64 *out) {
  stmt s = prepare(sb, "PRAGMA data_version");
  int r = step(&s);
  if (r == 1) *out = sqlite3_column_int64(s.st, 0);
  sqlite3_finalize(s.st);
  return r == 1 ? 0 : r < 0 ? -r : EXT_LEDGER_IO;
}

/* Reads in a transaction of its own unless a write transaction is open. */
static int begin_read_tx(sqlite_backend *sb) { return sb->in_tx ? 0 : exec(sb, "BEGIN"); }
static void end_read_tx(sqlite_backend *sb) {
  if (!sb->in_tx) exec(sb, "COMMIT");
}

static int reload(sqlite_backend *sb, ll_state *state) {
  ll_state fresh;
  ll_state_init(&fresh);
  int rc = begin_read_tx(sb);
  uint64_t cursor = 0, floor = 0;
  if (!rc) {
    rc = read_store_row(sb, &cursor, &floor);
    if (!rc) rc = read_namespaces(sb, &fresh, 1);
    if (!rc) rc = read_records(sb, &fresh, 0);
    if (!rc) rc = read_records(sb, &fresh, 1);
    if (!rc) rc = read_changes(sb, &fresh, floor, 0);
    if (!rc) rc = read_idempotency(sb, &fresh, floor);
    if (!rc) rc = read_data_version(sb, &sb->data_version);
    end_read_tx(sb);
  }
  if (rc) {
    ll_state_free(&fresh);
    return rc;
  }
  fresh.cursor = cursor;
  fresh.floor = floor;
  ll_state_free(state);
  *state = fresh;
  sb->stale = 0;
  return 0;
}

static int sb_refresh(ll_backend *b, ll_state *state) {
  sqlite_backend *sb = (sqlite_backend *)b;
  if (sb->stale) return reload(sb, state);
  int rc = begin_read_tx(sb);
  if (rc) return rc;
  sqlite3_int64 version;
  rc = read_data_version(sb, &version);
  if (!rc && version == sb->data_version) {
    end_read_tx(sb);
    return 0;
  }
  uint64_t cursor = 0, floor = 0;
  if (!rc) rc = read_store_row(sb, &cursor, &floor);
  if (!rc && (floor > state->cursor || cursor < state->cursor)) { /* history we never saw was dropped: start over */
    end_read_tx(sb);
    return reload(sb, state);
  }
  uint64_t before = state->cursor;
  if (!rc) rc = read_namespaces(sb, state, 0);
  if (!rc) rc = read_changes(sb, state, before, 1);
  if (!rc) rc = read_idempotency(sb, state, before);
  end_read_tx(sb);
  if (rc) {
    sb->stale = 1;
    return rc;
  }
  ll_state_set_floor(state, floor);
  sb->data_version = version;
  return 0;
}

/* ---- writing ---- */

static int sb_begin_write(ll_backend *b, const ext_ledger_context *ctx, ll_state *state) {
  (void)ctx;
  sqlite_backend *sb = (sqlite_backend *)b;
  int rc = exec(sb, "BEGIN IMMEDIATE");
  if (rc) return rc;
  sb->in_tx = 1;
  if ((rc = sb_refresh(b, state))) {
    exec(sb, "ROLLBACK");
    sb->in_tx = 0;
  }
  return rc;
}

static void sb_end_write(ll_backend *b) {
  sqlite_backend *sb = (sqlite_backend *)b;
  if (sb->in_tx) exec(sb, "ROLLBACK"); /* nothing was committed */
  sb->in_tx = 0;
}

static int commit(sqlite_backend *sb, int rc) {
  if (!rc) rc = exec(sb, "COMMIT");
  if (rc) {
    exec(sb, "ROLLBACK");
    sb->stale = 1; /* the state in memory holds what the database does not */
  }
  sb->in_tx = 0;
  return rc;
}

static int sb_write_schema(ll_backend *b, ll_state *state, const ll_schema *schema) {
  (void)state;
  sqlite_backend *sb = (sqlite_backend *)b;
  lj_buf json = {0};
  ll_schema_write(&json, schema);
  stmt s = prepare(sb,
                   "INSERT INTO namespaces (namespace, schema_version, schema, revision) VALUES (?, ?, ?, 0) "
                   "ON CONFLICT (namespace) DO UPDATE SET schema_version = excluded.schema_version, schema = excluded.schema");
  bind_s(&s, 1, &schema->ns);
  bind_s(&s, 2, &schema->version);
  bind_json(&s, 3, &json);
  int rc = done(&s);
  lj_buf_free(&json);
  return commit(sb, rc);
}

static int write_record(sqlite_backend *sb, const ll_record *r) {
  lj_buf labels = {0}, attrs = {0}, prov = {0}, created = {0}, updated = {0};
  if (r->labels) lj_write(&labels, r->labels);
  if (r->attrs) lj_write(&attrs, r->attrs);
  if (!ll_prov_is_zero(&r->prov)) ll_prov_write(&prov, &r->prov);
  time_text(&created, &r->created);
  time_text(&updated, &r->updated);
  stmt s = prepare(sb, r->is_edge ? "INSERT INTO edges (namespace, id, type, from_id, to_id, revision, created_at, updated_at, labels, attributes, "
                                    "provenance) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (namespace, id) DO UPDATE SET type = "
                                    "excluded.type, from_id = excluded.from_id, to_id = excluded.to_id, revision = excluded.revision, created_at = "
                                    "excluded.created_at, updated_at = excluded.updated_at, labels = excluded.labels, attributes = "
                                    "excluded.attributes, provenance = excluded.provenance"
                                  : "INSERT INTO vertices (namespace, id, kind, revision, created_at, updated_at, labels, attributes, provenance) "
                                    "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (namespace, id) DO UPDATE SET kind = excluded.kind, revision = "
                                    "excluded.revision, created_at = excluded.created_at, updated_at = excluded.updated_at, labels = "
                                    "excluded.labels, attributes = excluded.attributes, provenance = excluded.provenance");
  int i = 1;
  bind_s(&s, i++, &r->ns);
  bind_s(&s, i++, &r->id);
  bind_s(&s, i++, &r->kind);
  if (r->is_edge) {
    bind_s(&s, i++, &r->from);
    bind_s(&s, i++, &r->to);
  }
  bind_u64(&s, i++, r->revision);
  bind_json(&s, i++, &created);
  bind_json(&s, i++, &updated);
  bind_json(&s, i++, &labels);
  bind_json(&s, i++, &attrs);
  bind_json(&s, i++, &prov);
  int rc = done(&s);
  lj_buf_free(&labels);
  lj_buf_free(&attrs);
  lj_buf_free(&prov);
  lj_buf_free(&created);
  lj_buf_free(&updated);
  return rc;
}

static int delete_row(sqlite_backend *sb, const char *sql, const ll_s *ns, const ll_s *id) {
  stmt s = prepare(sb, sql);
  bind_s(&s, 1, ns);
  bind_s(&s, 2, id);
  return done(&s);
}

static int floor_delete(sqlite_backend *sb, const char *sql, uint64_t floor) {
  stmt s = prepare(sb, sql);
  bind_u64(&s, 1, floor);
  return done(&s);
}

static int sb_write_commit(ll_backend *b, ll_state *state, const ll_retained *r, uint64_t floor, int moved) {
  (void)state;
  sqlite_backend *sb = (sqlite_backend *)b;
  const ll_change *c = r->change;
  int rc = 0;
  for (size_t i = 0; !rc && i < c->ndeleted_edges; i++) rc = delete_row(sb, "DELETE FROM edges WHERE namespace = ? AND id = ?", &c->ns, &c->deleted_edges[i]);
  for (size_t i = 0; !rc && i < c->ndeleted_vertices; i++)
    rc = delete_row(sb, "DELETE FROM vertices WHERE namespace = ? AND id = ?", &c->ns, &c->deleted_vertices[i]);
  for (size_t i = 0; !rc && i < c->nvertices; i++) rc = write_record(sb, c->vertices[i]);
  for (size_t i = 0; !rc && i < c->nedges; i++) rc = write_record(sb, c->edges[i]);
  lj_buf body = {0}, at = {0};
  ll_change_write(&body, c);
  time_text(&at, &c->committed);
  if (!rc) {
    stmt s = prepare(sb, "UPDATE namespaces SET revision = ? WHERE namespace = ?");
    bind_u64(&s, 1, c->revision);
    bind_s(&s, 2, &c->ns);
    rc = done(&s);
  }
  if (!rc) {
    stmt s = prepare(sb, "UPDATE store SET cursor = ? WHERE id = 1");
    bind_u64(&s, 1, c->cursor);
    rc = done(&s);
  }
  if (!rc) {
    stmt s = prepare(sb, "INSERT INTO changes (cursor, namespace, revision, committed_at, size, body) VALUES (?, ?, ?, ?, ?, ?)");
    bind_u64(&s, 1, c->cursor);
    bind_s(&s, 2, &c->ns);
    bind_u64(&s, 3, c->revision);
    bind_json(&s, 4, &at);
    bind_u64(&s, 5, body.len);
    bind_json(&s, 6, &body);
    rc = done(&s);
  }
  if (!rc && r->key.n) {
    stmt s = prepare(sb, "INSERT INTO idempotency (namespace, key, fingerprint, revision, cursor, committed_at) VALUES (?, ?, ?, ?, ?, ?)");
    bind_s(&s, 1, &c->ns);
    bind_s(&s, 2, &r->key);
    bind_s(&s, 3, &r->fingerprint);
    bind_u64(&s, 4, c->revision);
    bind_u64(&s, 5, c->cursor);
    bind_json(&s, 6, &at);
    rc = done(&s);
  }
  lj_buf_free(&body);
  lj_buf_free(&at);
  if (!rc && moved) rc = floor_delete(sb, "DELETE FROM changes WHERE cursor <= ?", floor);
  if (!rc && moved) rc = floor_delete(sb, "DELETE FROM idempotency WHERE cursor <= ?", floor);
  if (!rc && moved) rc = floor_delete(sb, "UPDATE store SET history_floor = ? WHERE id = 1", floor);
  return commit(sb, rc);
}

static void sb_maintain(ll_backend *b, ll_state *state) {
  (void)b;
  (void)state; /* compaction happens inside the commit's transaction */
}

static void sb_close(ll_backend *b) {
  sqlite_backend *sb = (sqlite_backend *)b;
  sb_end_write(b);
  sqlite3_close_v2(sb->db);
  free(sb);
}

static const ll_backend_ops sqlite_ops = {sb_refresh, sb_begin_write, sb_end_write, sb_write_schema, sb_write_commit, sb_maintain, sb_close};

/* ---- opening ---- */

static int count(sqlite_backend *sb, const char *sql, int64_t *n) {
  stmt s = prepare(sb, sql);
  int r = step(&s);
  if (r == 1) *n = sqlite3_column_int64(s.st, 0);
  sqlite3_finalize(s.st);
  return r == 1 ? 0 : r < 0 ? -r : EXT_LEDGER_IO;
}

static int init(sqlite_backend *sb) {
  int rc = exec(sb, "BEGIN IMMEDIATE");
  if (rc) return rc;
  int64_t tables = 0;
  rc = count(sb, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'store'", &tables);
  if (!rc && tables == 0) {
    int64_t other = 0;
    rc = count(sb, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'", &other);
    if (!rc && other > 0) rc = EXT_LEDGER_UNSUPPORTED;
    for (int i = 0; !rc && layout_statements[i]; i++) rc = exec(sb, layout_statements[i]);
    if (!rc) rc = exec(sb, "INSERT INTO store (id, layout, cursor, history_floor) VALUES (1, '" SQLITE_LAYOUT "', 0, 0)");
  } else if (!rc) {
    stmt s = prepare(sb, "SELECT layout FROM store WHERE id = 1");
    int r = step(&s);
    if (r == 1) {
      size_t n;
      const char *layout = col_text(&s, 0, &n);
      if (!layout || strcmp(layout, SQLITE_LAYOUT) != 0) rc = EXT_LEDGER_UNSUPPORTED;
    } else {
      rc = EXT_LEDGER_UNSUPPORTED;
    }
    sqlite3_finalize(s.st);
  }
  if (rc) {
    exec(sb, "ROLLBACK");
    return rc;
  }
  return exec(sb, "COMMIT");
}

int ll_sqlite_open(const ext_ledger_context *ctx, const char *path, ll_state *state, ll_backend **out) {
  (void)ctx;
  int kind = ll_path_kind(path);
  if (kind < 0) return EXT_LEDGER_IO;
  if (kind == 1) return EXT_LEDGER_UNSUPPORTED;
  if (kind == 0) { /* create it with mode 0600, in a directory of mode 0700 */
    size_t n = strlen(path);
    char *parent = malloc(n + 1);
    if (!parent) return EXT_LEDGER_IO;
    memcpy(parent, path, n + 1);
    char *cut = NULL;
    for (char *p = parent; *p; p++)
      if (*p == '/' || *p == '\\') cut = p;
    int rc = 0;
    if (cut && cut != parent) {
      *cut = 0;
      rc = ll_mkdir(parent);
    }
    free(parent);
    ll_file *f;
    if (!rc) rc = ll_file_open(path, LL_READ | LL_WRITE | LL_CREATE, &f);
    if (rc) return rc;
    ll_file_close(f);
  }
  sqlite_backend *sb = calloc(1, sizeof *sb);
  if (!sb) return EXT_LEDGER_IO;
  sb->backend.ops = &sqlite_ops;
  int rc = code_of(sqlite3_open_v2(path, &sb->db, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE, NULL));
  if (!rc) rc = code_of(sqlite3_busy_timeout(sb->db, 5000));
  if (!rc) rc = exec(sb, "PRAGMA journal_mode = WAL");
  if (!rc) rc = exec(sb, "PRAGMA foreign_keys = ON");
  if (!rc) rc = init(sb);
  if (rc == EXT_LEDGER_IO && sqlite3_errcode(sb->db) == SQLITE_NOTADB) rc = EXT_LEDGER_UNSUPPORTED;
  if (!rc) rc = reload(sb, state);
  if (rc) {
    sb_close(&sb->backend);
    return rc;
  }
  *out = &sb->backend;
  return 0;
}
