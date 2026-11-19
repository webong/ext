/* The public store API: the checks every operation makes in rules.json order,
 * over the in-memory state and the backend that persists it. */
#include "ledger_internal.h"

#include <stdlib.h>
#include <string.h>

#define POLL_MS 150 /* how often a watch on a durable store looks for other writers' commits */

struct ext_ledger {
  int refs; /* the handle, and one per open watch */
  ll_mutex *mu;
  ll_cond *cond;
  ext_ledger_options options;
  ll_state state;
  ll_backend *backend; /* NULL for the memory store */
  int closed;
};

struct ext_ledger_watch {
  ext_ledger *store;
  ll_s ns;
  uint64_t after; /* the cursor of the last change queued */
  int64_t buffer;
  ll_change **queue;
  size_t head, len; /* a ring of buffer entries */
  int ended;
};

const char *ext_ledger_code_name(int code) {
  static const char *const names[] = {"",        "invalid",     "not_found",   "changed",  "exists", "denied",  "too_large",
                                      "expired", "unsupported", "unavailable", "busy",     "canceled", "timeout", "io"};
  if (code < 0 || code > EXT_LEDGER_IO) return "io";
  return names[code];
}

void ext_ledger_free(void *p) { free(p); }

int ll_ctx_check(const ext_ledger_context *ctx) {
  if (!ctx) return 0;
  if (ctx->canceled) return EXT_LEDGER_CANCELED;
  if (ctx->deadline_unix_ms && ll_now_unix_ms() >= ctx->deadline_unix_ms) return EXT_LEDGER_TIMEOUT;
  return 0;
}

static int finish(lj_buf *b, char **out, size_t *out_len) {
  char *p = lj_buf_take(b, out_len);
  if (!p) return EXT_LEDGER_IO;
  *out = p;
  return 0;
}

static int parse(const char *text, size_t len, lj_value **out) {
  int oom = 0;
  *out = text ? lj_parse(text, len, &oom) : NULL;
  if (!*out) return oom ? EXT_LEDGER_IO : EXT_LEDGER_INVALID;
  return 0;
}

/* ---- opening and closing ---- */

static int make_store(const ext_ledger_options *options, ext_ledger **out) {
  ext_ledger *s = calloc(1, sizeof *s);
  if (!s) return EXT_LEDGER_IO;
  s->refs = 1;
  s->mu = ll_mutex_new();
  s->cond = ll_cond_new();
  if (!s->mu || !s->cond) {
    ll_mutex_free(s->mu);
    ll_cond_free(s->cond);
    free(s);
    return EXT_LEDGER_IO;
  }
  s->options = ll_options_defaults(options);
  ll_state_init(&s->state);
  *out = s;
  return 0;
}

static void release(ext_ledger *s) {
  /* called with s->mu held; unlocks it */
  int last = --s->refs == 0;
  ll_mutex_unlock(s->mu);
  if (!last) return;
  if (s->backend) s->backend->ops->close(s->backend);
  ll_state_free(&s->state);
  ll_mutex_free(s->mu);
  ll_cond_free(s->cond);
  free(s);
}

int ext_ledger_open_memory(const ext_ledger_options *options, ext_ledger **out) {
  if (!out) return EXT_LEDGER_INVALID;
  *out = NULL;
  return make_store(options, out);
}

static int open_durable(const ext_ledger_context *ctx, const char *path, const ext_ledger_options *options, ext_ledger **out,
                        int (*opener)(const ext_ledger_context *, const char *, ll_state *, ll_backend **)) {
  if (!out) return EXT_LEDGER_INVALID;
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  if (!path || !*path) return EXT_LEDGER_INVALID;
  ext_ledger *s;
  if ((rc = make_store(options, &s))) return rc;
  if ((rc = opener(ctx, path, &s->state, &s->backend))) {
    ll_mutex_lock(s->mu);
    release(s);
    return rc;
  }
  *out = s;
  return 0;
}

static int open_writer(const ext_ledger_context *ctx, const char *dir, ll_state *state, ll_backend **out) {
  return ll_dir_open(ctx, dir, 0, state, out);
}

static int open_reader(const ext_ledger_context *ctx, const char *dir, ll_state *state, ll_backend **out) {
  return ll_dir_open(ctx, dir, 1, state, out);
}

int ext_ledger_open(const ext_ledger_context *ctx, const char *dir, const ext_ledger_options *options, ext_ledger **out) {
  return open_durable(ctx, dir, options, out, open_writer);
}

int ext_ledger_open_reader(const ext_ledger_context *ctx, const char *dir, const ext_ledger_options *options, ext_ledger **out) {
  return open_durable(ctx, dir, options, out, open_reader);
}

int ext_ledger_open_sqlite(const ext_ledger_context *ctx, const char *path, const ext_ledger_options *options, ext_ledger **out) {
  return open_durable(ctx, path, options, out, ll_sqlite_open);
}

void ext_ledger_close(ext_ledger *s) {
  if (!s) return;
  ll_mutex_lock(s->mu);
  if (s->closed) { /* closing twice is a no-op */
    ll_mutex_unlock(s->mu);
    return;
  }
  s->closed = 1;
  ll_cond_broadcast(s->cond);
  release(s);
}

/* Takes the mutex, refuses a closed store and catches up with other writers. */
static int begin_read(ext_ledger *s, const ext_ledger_context *ctx) {
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  ll_mutex_lock(s->mu);
  if (s->closed) {
    ll_mutex_unlock(s->mu);
    return EXT_LEDGER_UNAVAILABLE;
  }
  if (s->backend && (rc = s->backend->ops->refresh(s->backend, &s->state))) {
    ll_mutex_unlock(s->mu);
    return rc;
  }
  return 0;
}

/* ---- writes ---- */

int ext_ledger_register(ext_ledger *s, const ext_ledger_context *ctx, const char *text, size_t len) {
  if (!s) return EXT_LEDGER_INVALID;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  lj_value *v;
  if ((rc = parse(text, len, &v))) return rc;
  ll_schema schema;
  rc = ll_schema_decode(v, &schema);
  lj_free(v);
  if (rc) return rc;
  if ((rc = ll_schema_validate(&schema))) {
    ll_schema_free(&schema);
    return rc;
  }
  ll_schema_normalize(&schema);
  ll_mutex_lock(s->mu);
  if (s->closed) rc = EXT_LEDGER_UNAVAILABLE;
  else if (s->backend) rc = s->backend->ops->begin_write(s->backend, ctx, &s->state);
  if (!rc) {
    int changed;
    rc = ll_state_check_register(&s->state, &schema, &changed);
    if (!rc && changed) {
      rc = ll_state_set_schema(&s->state, &schema);
      if (!rc && s->backend) rc = s->backend->ops->write_schema(s->backend, &s->state, &schema);
    }
    if (s->backend) s->backend->ops->end_write(s->backend);
  }
  ll_mutex_unlock(s->mu);
  ll_schema_free(&schema);
  return rc;
}

static int write_commit(const ll_s *ns, uint64_t revision, uint64_t cursor, const ll_time *at, int replayed, char **out, size_t *out_len) {
  lj_buf b = {0};
  lj_buf_puts(&b, "{\"committed_at\":");
  ll_time_write(&b, at);
  lj_buf_puts(&b, ",\"cursor\":");
  lj_write_uint(&b, cursor);
  lj_buf_puts(&b, ",\"namespace\":");
  lj_write_string(&b, LL_S(*ns), ns->n);
  if (replayed) lj_buf_puts(&b, ",\"replayed\":true");
  lj_buf_puts(&b, ",\"revision\":");
  lj_write_uint(&b, revision);
  lj_buf_putc(&b, '}');
  return finish(&b, out, out_len);
}

static int fingerprint(const ll_tx *t, char out[65]) {
  lj_buf b = {0};
  ll_tx_write(&b, t);
  if (b.oom) {
    lj_buf_free(&b);
    return EXT_LEDGER_IO;
  }
  ll_sha256_hex(b.ptr, b.len, out);
  lj_buf_free(&b);
  return 0;
}

static int apply_locked(ext_ledger *s, const ext_ledger_context *ctx, const ll_tx *t, const char fp[65], char **out, size_t *out_len) {
  ll_namespace *ns = ll_state_ns(&s->state, LL_S(t->ns), t->ns.n);
  if (!ns) return EXT_LEDGER_NOT_FOUND;
  if (t->key.n) {
    const ll_idem *idem = ll_state_idempotent(&s->state, &t->ns, &t->key);
    if (idem) {
      if (strcmp(idem->fingerprint, fp) != 0) return EXT_LEDGER_EXISTS;
      return out ? write_commit(&t->ns, idem->revision, idem->cursor, &idem->committed, 1, out, out_len) : 0;
    }
  }
  if (t->has_expected && t->expected != ns->revision) return EXT_LEDGER_CHANGED;
  ll_time now = ll_time_now();
  ll_change *c;
  int rc = ll_state_prepare(&s->state, ctx, t, now, &c);
  if (rc) return rc;
  c->cursor = s->state.cursor + 1;
  ll_s fps = {(char *)fp, 64};
  if (!t->key.n) fps.n = 0;
  if ((rc = ll_state_apply(&s->state, c, &t->key, &fps, 1))) {
    ll_change_unref(c);
    return rc;
  }
  uint64_t before = s->state.floor;
  uint64_t floor = ll_state_compaction(&s->state, &s->options);
  /* Persist before dropping history, so the backend sees the commit it writes. */
  if (s->backend) {
    const ll_retained *r = ll_state_retained_at(&s->state, ll_state_retained(&s->state) - 1);
    rc = s->backend->ops->write_commit(s->backend, &s->state, r, floor, floor != before);
  }
  if (!rc) {
    ll_state_set_floor(&s->state, floor);
    if (s->backend) s->backend->ops->maintain(s->backend, &s->state);
  }
  if (!rc && out) rc = write_commit(&c->ns, c->revision, c->cursor, &c->committed, 0, out, out_len);
  ll_change_unref(c);
  ll_cond_broadcast(s->cond);
  return rc;
}

int ext_ledger_apply(ext_ledger *s, const ext_ledger_context *ctx, const char *text, size_t len, char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!s) return EXT_LEDGER_INVALID;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  lj_value *v;
  if ((rc = parse(text, len, &v))) return rc;
  ll_tx t;
  rc = ll_tx_decode(v, &t);
  lj_free(v);
  if (rc) return rc;
  char fp[65];
  if (!(rc = ll_check_tx(&t, &s->options))) rc = fingerprint(&t, fp);
  if (rc) {
    ll_tx_free(&t);
    return rc;
  }
  ll_mutex_lock(s->mu);
  if (s->closed) rc = EXT_LEDGER_UNAVAILABLE;
  else if (s->backend) rc = s->backend->ops->begin_write(s->backend, ctx, &s->state);
  if (!rc) {
    rc = apply_locked(s, ctx, &t, fp, out, out_len);
    if (s->backend) s->backend->ops->end_write(s->backend);
  }
  ll_mutex_unlock(s->mu);
  ll_tx_free(&t);
  return rc;
}

int ext_ledger_canonical_transaction(const char *text, size_t len, char **canonical, size_t *canonical_len, char fp[65]) {
  lj_value *v;
  int rc = parse(text, len, &v);
  if (rc) return rc;
  ll_tx t;
  rc = ll_tx_decode(v, &t);
  lj_free(v);
  if (rc) return rc;
  lj_buf b = {0};
  ll_tx_write(&b, &t);
  ll_tx_free(&t);
  if (!b.oom && fp) ll_sha256_hex(b.ptr, b.len, fp);
  if (canonical) return finish(&b, canonical, canonical_len);
  rc = b.oom ? EXT_LEDGER_IO : 0;
  lj_buf_free(&b);
  return rc;
}

/* ---- reads ---- */

static int get_record(ext_ledger *s, const ext_ledger_context *ctx, const char *ns, const char *id, int edge, char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!s || !ns || !id || !out) return EXT_LEDGER_INVALID;
  int rc = begin_read(s, ctx);
  if (rc) return rc;
  ll_namespace *n = ll_state_ns(&s->state, ns, strlen(ns));
  ll_record *r = n ? ll_map_get(edge ? &n->edges : &n->vertices, id, strlen(id)) : NULL;
  if (!r) {
    ll_mutex_unlock(s->mu);
    return EXT_LEDGER_NOT_FOUND;
  }
  lj_buf b = {0};
  ll_record_write(&b, r);
  ll_mutex_unlock(s->mu);
  return finish(&b, out, out_len);
}

int ext_ledger_get_vertex(ext_ledger *s, const ext_ledger_context *ctx, const char *ns, const char *id, char **out, size_t *out_len) {
  return get_record(s, ctx, ns, id, 0, out, out_len);
}

int ext_ledger_get_edge(ext_ledger *s, const ext_ledger_context *ctx, const char *ns, const char *id, char **out, size_t *out_len) {
  return get_record(s, ctx, ns, id, 1, out, out_len);
}

typedef struct {
  ll_s ns, kind, from, to;
  int64_t limit;
} query;

static void query_free(query *q) {
  ll_s_free(&q->ns);
  ll_s_free(&q->kind);
  ll_s_free(&q->from);
  ll_s_free(&q->to);
}

static int decode_query(const char *text, size_t len, int edges, query *q) {
  memset(q, 0, sizeof *q);
  lj_value *v;
  int rc = parse(text, len, &v);
  if (rc) return rc;
  if (v->type != LJ_OBJECT) rc = EXT_LEDGER_INVALID;
  const char *keys[4] = {"namespace", edges ? "type" : "kind", "from", "to"};
  ll_s *fields[4] = {&q->ns, &q->kind, &q->from, &q->to};
  for (int i = 0; !rc && i < (edges ? 4 : 2); i++) {
    const lj_value *f = lj_get(v, keys[i]);
    if (!f || f->type == LJ_NULL) continue;
    if (f->type != LJ_STRING) rc = EXT_LEDGER_INVALID;
    else rc = ll_s_set(fields[i], f->u.string.ptr, f->u.string.len);
  }
  const lj_value *limit = lj_get(v, "limit");
  if (!rc && limit && limit->type != LJ_NULL) {
    if (limit->type != LJ_NUMBER || limit->u.number != (double)(int64_t)limit->u.number) rc = EXT_LEDGER_INVALID;
    else q->limit = (int64_t)limit->u.number;
  }
  lj_free(v);
  if (rc) query_free(q);
  return rc;
}

/* Collects the records of map that keep passes, sorted by ID and cut to limit. */
static int collect(ll_map *m, int (*keep)(const ll_record *, const query *), const query *q, ll_record ***out, size_t *n) {
  *n = 0;
  *out = m->len ? malloc(m->len * sizeof **out) : NULL;
  if (m->len && !*out) return EXT_LEDGER_IO;
  LL_MAP_EACH(m, slot) {
    if (!keep || keep(slot->value, q)) (*out)[(*n)++] = slot->value;
  }
  if (*n) qsort(*out, *n, sizeof **out, ll_record_id_cmp);
  return 0;
}

static void write_record_array(lj_buf *b, ll_record **records, size_t n) {
  lj_buf_putc(b, '[');
  for (size_t i = 0; i < n; i++) {
    if (i) lj_buf_putc(b, ',');
    ll_record_write(b, records[i]);
  }
  lj_buf_putc(b, ']');
}

static int keep_vertex(const ll_record *r, const query *q) { return q->kind.n == 0 || ll_s_eq(&r->kind, &q->kind); }
static int keep_edge(const ll_record *r, const query *q) {
  return (q->kind.n == 0 || ll_s_eq(&r->kind, &q->kind)) && (q->from.n == 0 || ll_s_eq(&r->from, &q->from)) &&
         (q->to.n == 0 || ll_s_eq(&r->to, &q->to));
}

static int query_records(ext_ledger *s, const ext_ledger_context *ctx, const char *text, size_t len, int edges, char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!s || !out) return EXT_LEDGER_INVALID;
  query q;
  int rc = decode_query(text, len, edges, &q);
  if (rc) return rc;
  if ((rc = ll_check_limit(q.limit, &s->options)) || (rc = begin_read(s, ctx))) {
    query_free(&q);
    return rc;
  }
  lj_buf b = {0};
  ll_namespace *n = ll_state_ns(&s->state, LL_S(q.ns), q.ns.n);
  ll_record **records = NULL;
  size_t count = 0;
  if (n) rc = collect(edges ? &n->edges : &n->vertices, edges ? keep_edge : keep_vertex, &q, &records, &count);
  if ((int64_t)count > q.limit) count = (size_t)q.limit;
  if (!rc) write_record_array(&b, records, count);
  ll_mutex_unlock(s->mu);
  free(records);
  query_free(&q);
  if (rc) {
    lj_buf_free(&b);
    return rc;
  }
  return finish(&b, out, out_len);
}

int ext_ledger_query_vertices(ext_ledger *s, const ext_ledger_context *ctx, const char *text, size_t len, char **out, size_t *out_len) {
  return query_records(s, ctx, text, len, 0, out, out_len);
}

int ext_ledger_query_edges(ext_ledger *s, const ext_ledger_context *ctx, const char *text, size_t len, char **out, size_t *out_len) {
  return query_records(s, ctx, text, len, 1, out, out_len);
}

/* ---- traversal: breadth first along outgoing edges, edges in ID order ---- */

typedef struct {
  ll_record **vertices, **edges;
  size_t n; /* edges; vertices hold n + 1 */
} step;

static int traverse(ext_ledger *s, const ext_ledger_context *ctx, ll_namespace *ns, const char *start, int64_t max_depth, int64_t max_vertices,
                    lj_buf *b) {
  ll_record *first = ns ? ll_map_get(&ns->vertices, start, strlen(start)) : NULL;
  if (!first) return EXT_LEDGER_NOT_FOUND;
  (void)s;
  ll_map visited = {0};
  step *queue = malloc(sizeof *queue);
  size_t head = 0, tail = 0, cap = 1;
  int rc = 0;
  if (!queue || ll_map_put(&visited, start, strlen(start), (void *)1)) rc = EXT_LEDGER_IO;
  if (!rc) {
    queue[0].vertices = malloc(sizeof(ll_record *));
    queue[0].edges = NULL;
    queue[0].n = 0;
    if (!queue[0].vertices) rc = EXT_LEDGER_IO;
    else {
      queue[0].vertices[0] = first;
      tail = 1;
    }
  }
  int wrote = 0;
  lj_buf_putc(b, '[');
  while (!rc && head < tail) {
    if ((rc = ll_ctx_check(ctx))) break;
    step cur = queue[head++];
    if (cur.n > 0) {
      if (wrote++) lj_buf_putc(b, ',');
      lj_buf_puts(b, "{\"edges\":");
      write_record_array(b, cur.edges, cur.n);
      lj_buf_puts(b, ",\"vertices\":");
      write_record_array(b, cur.vertices, cur.n + 1);
      lj_buf_putc(b, '}');
    }
    if ((int64_t)cur.n < max_depth && (int64_t)visited.len < max_vertices) {
      ll_record **out;
      size_t nout;
      if ((rc = ll_state_incident(ns, &cur.vertices[cur.n]->id, 1, &out, &nout))) goto next;
      for (size_t i = 0; i < nout && (int64_t)visited.len < max_vertices; i++) {
        ll_record *e = out[i];
        if (ll_map_get(&visited, LL_S(e->to), e->to.n)) continue;
        ll_record *to = ll_map_get(&ns->vertices, LL_S(e->to), e->to.n);
        if (ll_map_put(&visited, LL_S(e->to), e->to.n, (void *)1)) {
          rc = EXT_LEDGER_IO;
          break;
        }
        if (tail == cap) {
          cap *= 2;
          step *p = realloc(queue, cap * sizeof *p);
          if (!p) {
            rc = EXT_LEDGER_IO;
            break;
          }
          queue = p;
        }
        step nx = {malloc((cur.n + 2) * sizeof(ll_record *)), malloc((cur.n + 1) * sizeof(ll_record *)), cur.n + 1};
        if (!nx.vertices || !nx.edges) {
          free(nx.vertices);
          free(nx.edges);
          rc = EXT_LEDGER_IO;
          break;
        }
        memcpy(nx.vertices, cur.vertices, (cur.n + 1) * sizeof(ll_record *));
        if (cur.n) memcpy(nx.edges, cur.edges, cur.n * sizeof(ll_record *));
        nx.vertices[cur.n + 1] = to;
        nx.edges[cur.n] = e;
        queue[tail++] = nx;
      }
      free(out);
    }
  next:
    free(cur.vertices);
    free(cur.edges);
  }
  lj_buf_putc(b, ']');
  for (size_t i = head; i < tail; i++) {
    free(queue[i].vertices);
    free(queue[i].edges);
  }
  free(queue);
  ll_map_free(&visited);
  return rc;
}

int ext_ledger_traverse(ext_ledger *s, const ext_ledger_context *ctx, const char *ns, const char *start, int64_t max_depth, int64_t max_vertices,
                        char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!s || !ns || !start || !out) return EXT_LEDGER_INVALID;
  if (max_depth < 0) return EXT_LEDGER_INVALID;
  int rc = ll_check_limit(max_vertices, &s->options);
  if (rc || (rc = begin_read(s, ctx))) return rc;
  lj_buf b = {0};
  rc = traverse(s, ctx, ll_state_ns(&s->state, ns, strlen(ns)), start, max_depth, max_vertices, &b);
  ll_mutex_unlock(s->mu);
  if (rc) {
    lj_buf_free(&b);
    return rc;
  }
  return finish(&b, out, out_len);
}

int ext_ledger_snapshot(ext_ledger *s, const ext_ledger_context *ctx, const char *ns, char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!s || !ns || !out) return EXT_LEDGER_INVALID;
  int rc = begin_read(s, ctx);
  if (rc) return rc;
  size_t nslen = strlen(ns);
  ll_namespace *n = ll_state_ns(&s->state, ns, nslen);
  ll_record **vertices = NULL, **edges = NULL;
  size_t nv = 0, ne = 0;
  if (n && ((int64_t)(n->vertices.len + n->edges.len) > s->options.max_snapshot_records)) rc = EXT_LEDGER_TOO_LARGE;
  if (!rc && n) rc = collect(&n->vertices, NULL, NULL, &vertices, &nv);
  if (!rc && n) rc = collect(&n->edges, NULL, NULL, &edges, &ne);
  lj_buf b = {0};
  if (!rc) {
    lj_buf_puts(&b, "{\"cursor\":");
    lj_write_uint(&b, s->state.cursor);
    lj_buf_puts(&b, ",\"edges\":");
    write_record_array(&b, edges, ne);
    if (s->state.floor) {
      lj_buf_puts(&b, ",\"history_floor\":");
      lj_write_uint(&b, s->state.floor);
    }
    lj_buf_puts(&b, ",\"namespace\":");
    lj_write_string(&b, ns, nslen);
    lj_buf_puts(&b, ",\"revision\":");
    lj_write_uint(&b, n ? n->revision : 0);
    lj_buf_puts(&b, ",\"vertices\":");
    write_record_array(&b, vertices, nv);
    lj_buf_putc(&b, '}');
  }
  ll_mutex_unlock(s->mu);
  free(vertices);
  free(edges);
  if (rc) {
    lj_buf_free(&b);
    return rc;
  }
  return finish(&b, out, out_len);
}

/* The retained changes after `after` in ns ("" for all), at most limit of them. */
static int changes_after(ext_ledger *s, const char *ns, size_t nslen, uint64_t after, int64_t limit, ll_change ***out, size_t *n) {
  *out = NULL;
  *n = 0;
  if (after < s->state.floor) return EXT_LEDGER_EXPIRED;
  size_t total = ll_state_retained(&s->state);
  for (size_t i = 0; i < total && (int64_t)*n < limit; i++) {
    ll_change *c = ll_state_retained_at(&s->state, i)->change;
    if (c->cursor <= after || (nslen && !(c->ns.n == nslen && memcmp(c->ns.p, ns, nslen) == 0))) continue;
    if (!*out && !(*out = malloc((size_t)(limit < (int64_t)total ? limit : (int64_t)total) * sizeof **out))) return EXT_LEDGER_IO;
    (*out)[(*n)++] = c;
  }
  return 0;
}

int ext_ledger_changes(ext_ledger *s, const ext_ledger_context *ctx, const char *ns, uint64_t after, int64_t limit, char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!s || !out) return EXT_LEDGER_INVALID;
  if (!ns) ns = "";
  int rc = ll_check_limit(limit, &s->options);
  if (rc || (rc = begin_read(s, ctx))) return rc;
  ll_change **changes;
  size_t n;
  lj_buf b = {0};
  rc = changes_after(s, ns, strlen(ns), after, limit, &changes, &n);
  if (!rc) {
    lj_buf_putc(&b, '[');
    for (size_t i = 0; i < n; i++) {
      if (i) lj_buf_putc(&b, ',');
      ll_change_write(&b, changes[i]);
    }
    lj_buf_putc(&b, ']');
  }
  ll_mutex_unlock(s->mu);
  free(changes);
  if (rc) {
    lj_buf_free(&b);
    return rc;
  }
  return finish(&b, out, out_len);
}

/* ---- watches ---- */

/* Queues the changes committed since the watch's cursor. Ends the watch when
 * they overflow its buffer or its cursor fell below the floor. */
static int fill(ext_ledger_watch *w) {
  ext_ledger *s = w->store;
  if (w->ended) return 0;
  if (s->closed) {
    w->ended = 1;
    return 0;
  }
  int64_t free_slots = w->buffer - (int64_t)w->len;
  ll_change **changes;
  size_t n;
  int rc = changes_after(s, LL_S(w->ns), w->ns.n, w->after, free_slots + 1, &changes, &n);
  if (rc == EXT_LEDGER_EXPIRED) {
    w->ended = 1;
    return 0;
  }
  if (rc) return rc;
  for (size_t i = 0; i < n; i++) {
    if ((int64_t)i == free_slots) { /* the buffer is full */
      w->ended = 1;
      break;
    }
    w->queue[(w->head + w->len) % (size_t)w->buffer] = ll_change_ref(changes[i]);
    w->len++;
    w->after = changes[i]->cursor;
  }
  free(changes);
  return 0;
}

int ext_ledger_watch_open(ext_ledger *s, const ext_ledger_context *ctx, const char *ns, uint64_t after, int64_t buffer, ext_ledger_watch **out) {
  if (out) *out = NULL;
  if (!s || !out) return EXT_LEDGER_INVALID;
  if (!ns) ns = "";
  int rc = ll_check_limit(buffer, &s->options);
  if (rc || (rc = begin_read(s, ctx))) return rc;
  ll_change **backlog;
  size_t n;
  size_t nslen = strlen(ns);
  rc = changes_after(s, ns, nslen, after, buffer + 1, &backlog, &n);
  if (!rc && (int64_t)n > buffer) rc = EXT_LEDGER_TOO_LARGE;
  free(backlog);
  ext_ledger_watch *w = NULL;
  if (!rc) {
    w = calloc(1, sizeof *w);
    if (!w || !(w->queue = calloc((size_t)buffer, sizeof *w->queue)) || ll_s_set(&w->ns, ns, nslen)) rc = EXT_LEDGER_IO;
  }
  if (!rc) {
    w->store = s;
    w->after = after;
    w->buffer = buffer;
    rc = fill(w);
  }
  if (rc) {
    if (w) {
      free(w->queue);
      ll_s_free(&w->ns);
      free(w);
    }
    ll_mutex_unlock(s->mu);
    return rc;
  }
  s->refs++;
  ll_mutex_unlock(s->mu);
  *out = w;
  return 0;
}

int ext_ledger_watch_next(ext_ledger_watch *w, int64_t timeout_ms, char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!w || !out) return EXT_LEDGER_INVALID;
  ext_ledger *s = w->store;
  int64_t deadline = timeout_ms < 0 ? -1 : ll_now_unix_ms() + timeout_ms;
  ll_mutex_lock(s->mu);
  int rc = 0;
  for (;;) {
    if (!w->len && !w->ended && !s->closed && s->backend && (rc = s->backend->ops->refresh(s->backend, &s->state))) break;
    if (!w->len && (rc = fill(w))) break;
    if (w->len) {
      ll_change *c = w->queue[w->head];
      w->queue[w->head] = NULL;
      w->head = (w->head + 1) % (size_t)w->buffer;
      w->len--;
      lj_buf b = {0};
      ll_change_write(&b, c);
      ll_change_unref(c);
      ll_mutex_unlock(s->mu);
      return finish(&b, out, out_len);
    }
    if (w->ended || s->closed) {
      w->ended = 1;
      rc = EXT_LEDGER_UNAVAILABLE;
      break;
    }
    int64_t wait = s->backend ? POLL_MS : -1;
    if (deadline >= 0) {
      int64_t left = deadline - ll_now_unix_ms();
      if (left <= 0) {
        rc = EXT_LEDGER_TIMEOUT;
        break;
      }
      if (wait < 0 || left < wait) wait = left;
    }
    ll_cond_wait(s->cond, s->mu, wait);
  }
  ll_mutex_unlock(s->mu);
  return rc;
}

void ext_ledger_watch_close(ext_ledger_watch *w) {
  if (!w) return;
  ext_ledger *s = w->store;
  ll_mutex_lock(s->mu);
  for (size_t i = 0; i < w->len; i++) ll_change_unref(w->queue[(w->head + i) % (size_t)w->buffer]);
  free(w->queue);
  ll_s_free(&w->ns);
  free(w);
  release(s);
}
