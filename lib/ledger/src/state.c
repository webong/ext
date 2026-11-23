/* The graph state every backend keeps in memory, and the computation of the
 * change a transaction makes (rules.json, order). */
#include "ledger_internal.h"

#include <stdlib.h>
#include <string.h>

void ll_state_init(ll_state *s) { memset(s, 0, sizeof *s); }

static void free_namespace(ll_namespace *n) {
  if (!n) return;
  ll_schema_free(&n->schema);
  LL_MAP_EACH(&n->vertices, slot) ll_record_unref(slot->value);
  LL_MAP_EACH(&n->edges, slot) ll_record_unref(slot->value);
  ll_map_free(&n->vertices);
  ll_map_free(&n->edges);
  free(n);
}

static void free_retained(ll_retained *r) {
  ll_change_unref(r->change);
  ll_s_free(&r->key);
  ll_s_free(&r->fingerprint);
}

void ll_state_free(ll_state *s) {
  LL_MAP_EACH(&s->namespaces, slot) free_namespace(slot->value);
  ll_map_free(&s->namespaces);
  for (size_t i = s->first; i < s->nchanges; i++) free_retained(&s->changes[i]);
  free(s->changes);
  LL_MAP_EACH(&s->idempotency, slot) free(slot->value);
  ll_map_free(&s->idempotency);
  memset(s, 0, sizeof *s);
}

ll_namespace *ll_state_ns(ll_state *s, const char *ns, size_t n) { return ll_map_get(&s->namespaces, ns, n); }

int ll_state_set_schema(ll_state *s, const ll_schema *schema) {
  ll_namespace *n = ll_state_ns(s, LL_S(schema->ns), schema->ns.n);
  ll_schema copy;
  if (ll_schema_copy(schema, &copy)) return EXT_LEDGER_IO;
  if (!n) {
    n = calloc(1, sizeof *n);
    if (!n || ll_map_put(&s->namespaces, LL_S(schema->ns), schema->ns.n, n)) {
      free(n);
      ll_schema_free(&copy);
      return EXT_LEDGER_IO;
    }
  } else {
    ll_schema_free(&n->schema);
  }
  n->schema = copy;
  return 0;
}

int ll_state_check_register(ll_state *s, const ll_schema *schema, int *changed) {
  ll_namespace *n = ll_state_ns(s, LL_S(schema->ns), schema->ns.n);
  *changed = 1;
  if (!n) return 0;
  int same = ll_schema_same(&n->schema, schema);
  if (!same && ll_s_eq(&n->schema.version, &schema->version)) return EXT_LEDGER_EXISTS;
  *changed = !same;
  return 0;
}

int ll_record_id_cmp(const void *a, const void *b) {
  const ll_record *x = *(const ll_record *const *)a, *y = *(const ll_record *const *)b;
  return ll_s_cmp(&x->id, &y->id);
}

int ll_state_incident(ll_namespace *ns, const ll_s *id, int outgoing_only, ll_record ***out, size_t *n) {
  *out = NULL;
  *n = 0;
  size_t cap = 0;
  LL_MAP_EACH(&ns->edges, slot) {
    ll_record *e = slot->value;
    if (!ll_s_eq(&e->from, id) && (outgoing_only || !ll_s_eq(&e->to, id))) continue;
    if (*n == cap) {
      cap = cap ? cap * 2 : 8;
      ll_record **p = realloc(*out, cap * sizeof *p);
      if (!p) {
        free(*out);
        *out = NULL;
        *n = 0;
        return EXT_LEDGER_IO;
      }
      *out = p;
    }
    (*out)[(*n)++] = e;
  }
  if (*n) qsort(*out, *n, sizeof **out, ll_record_id_cmp);
  return 0;
}

/* ---- preparing a change ---- */

typedef struct {
  ll_namespace *ns;
  ll_change *change;
  ll_map vertex_at;  /* id -> index + 1 into change->vertices */
  ll_map edge_at;    /* id -> index + 1 into change->edges */
  ll_map deleted_v;  /* id -> (void*)1 */
  ll_map deleted_e;  /* id -> (void*)1 */
} prep;

#define MARK ((void *)1)

static void prep_free(prep *p) {
  ll_map_free(&p->vertex_at);
  ll_map_free(&p->edge_at);
  ll_map_free(&p->deleted_v);
  ll_map_free(&p->deleted_e);
}

static int push(void **items, size_t *n, size_t *cap, size_t size, const void *item) {
  if (*n == *cap) {
    size_t c = *cap ? *cap * 2 : 4;
    void *p = realloc(*items, c * size);
    if (!p) return EXT_LEDGER_IO;
    *items = p;
    *cap = c;
  }
  memcpy((char *)*items + *n * size, item, size);
  (*n)++;
  return 0;
}

static int push_id(ll_s **ids, size_t *n, size_t *cap, const ll_s *id) {
  ll_s copy;
  if (ll_s_set(&copy, LL_S(*id), id->n)) return EXT_LEDGER_IO;
  if (push((void **)ids, n, cap, sizeof copy, &copy)) {
    ll_s_free(&copy);
    return EXT_LEDGER_IO;
  }
  return 0;
}

static const ll_record *vertex_at(prep *p, const ll_s *id) {
  uintptr_t i = (uintptr_t)ll_map_get(&p->vertex_at, LL_S(*id), id->n);
  if (i) return p->change->vertices[i - 1];
  if (ll_map_get(&p->deleted_v, LL_S(*id), id->n)) return NULL;
  return ll_map_get(&p->ns->vertices, LL_S(*id), id->n);
}

static int set_identity(ll_record *r, const ll_s *ns, uint64_t revision, ll_time now, const ll_record *old, const ll_time *given) {
  if (old) r->created = old->created;
  else if (ll_time_is_zero(given)) r->created = now;
  else r->created = *given;
  r->created = ll_time_utc(r->created);
  ll_s_free(&r->ns);
  if (ll_s_set(&r->ns, LL_S(*ns), ns->n)) return EXT_LEDGER_IO;
  r->revision = revision;
  r->updated = now;
  return 0;
}

static int check_schema(prep *p, const ll_schema *schema, const ext_ledger_context *ctx) {
  ll_change *c = p->change;
  if (schema->nkinds) {
    for (size_t i = 0; i < c->nvertices; i++) {
      int allowed = 0;
      for (size_t k = 0; k < schema->nkinds && !allowed; k++) allowed = ll_s_eq(&schema->kinds[k], &c->vertices[i]->kind);
      if (!allowed) return EXT_LEDGER_INVALID;
    }
  }
  if (!schema->nrels) return 0;
  for (size_t i = 0; i < c->nedges; i++) {
    int rc = ll_ctx_check(ctx);
    if (rc) return rc;
    const ll_record *e = c->edges[i];
    const ll_record *from = vertex_at(p, &e->from), *to = vertex_at(p, &e->to);
    int declared = 0, matched = 0;
    for (size_t r = 0; r < schema->nrels && !matched; r++) {
      const ll_rel *rel = &schema->rels[r];
      if (!ll_s_eq(&rel->type, &e->kind)) continue;
      declared = 1;
      matched = (rel->from.n == 0 || (from && ll_s_eq(&rel->from, &from->kind))) && (rel->to.n == 0 || (to && ll_s_eq(&rel->to, &to->kind)));
    }
    if (!declared || !matched) return EXT_LEDGER_INVALID;
  }
  return 0;
}

static int prepare(prep *p, const ext_ledger_context *ctx, const ll_tx *t, uint64_t revision, ll_time now) {
  ll_change *c = p->change;
  int rc;
  for (size_t i = 0; i < t->ndelete_edges; i++) {
    if ((rc = ll_ctx_check(ctx))) return rc;
    const ll_s *id = &t->delete_edges[i].id;
    if (ll_map_get(&p->deleted_e, LL_S(*id), id->n) || !ll_map_get(&p->ns->edges, LL_S(*id), id->n)) return EXT_LEDGER_NOT_FOUND;
    if (ll_map_put(&p->deleted_e, LL_S(*id), id->n, MARK) || push_id(&c->deleted_edges, &c->ndeleted_edges, &c->capde, id)) return EXT_LEDGER_IO;
  }
  for (size_t i = 0; i < t->ndelete_vertices; i++) {
    if ((rc = ll_ctx_check(ctx))) return rc;
    const ll_s *id = &t->delete_vertices[i].id;
    if (ll_map_get(&p->deleted_v, LL_S(*id), id->n) || !ll_map_get(&p->ns->vertices, LL_S(*id), id->n)) return EXT_LEDGER_NOT_FOUND;
    ll_record **incident;
    size_t n;
    if (ll_state_incident(p->ns, id, 0, &incident, &n)) return EXT_LEDGER_IO;
    int attached = 0;
    for (size_t k = 0; k < n && !attached; k++) attached = !ll_map_get(&p->deleted_e, LL_S(incident[k]->id), incident[k]->id.n);
    if (attached && !t->delete_incident) {
      free(incident);
      return EXT_LEDGER_INVALID;
    }
    for (size_t k = 0; attached && k < n; k++) {
      const ll_s *eid = &incident[k]->id;
      if (ll_map_get(&p->deleted_e, LL_S(*eid), eid->n)) continue;
      if (ll_map_put(&p->deleted_e, LL_S(*eid), eid->n, MARK) || push_id(&c->deleted_edges, &c->ndeleted_edges, &c->capde, eid)) {
        free(incident);
        return EXT_LEDGER_IO;
      }
    }
    free(incident);
    if (ll_map_put(&p->deleted_v, LL_S(*id), id->n, MARK) || push_id(&c->deleted_vertices, &c->ndeleted_vertices, &c->capdv, id))
      return EXT_LEDGER_IO;
  }
  for (size_t i = 0; i < t->nvertices; i++) {
    const ll_record *given = t->vertices[i];
    const ll_record *old = vertex_at(p, &given->id);
    ll_record *v = ll_record_copy(given);
    if (!v) return EXT_LEDGER_IO;
    if (set_identity(v, &t->ns, revision, now, old, &given->created)) {
      ll_record_unref(v);
      return EXT_LEDGER_IO;
    }
    uintptr_t at = (uintptr_t)ll_map_get(&p->vertex_at, LL_S(v->id), v->id.n);
    if (at) {
      ll_record_unref(c->vertices[at - 1]);
      c->vertices[at - 1] = v;
    } else if (push((void **)&c->vertices, &c->nvertices, &c->capv, sizeof v, &v) ||
               ll_map_put(&p->vertex_at, LL_S(v->id), v->id.n, (void *)(uintptr_t)c->nvertices)) {
      ll_record_unref(v);
      return EXT_LEDGER_IO;
    }
  }
  for (size_t i = 0; i < t->nedges; i++) {
    const ll_record *given = t->edges[i];
    if (!vertex_at(p, &given->from) || !vertex_at(p, &given->to)) return EXT_LEDGER_NOT_FOUND;
    uintptr_t at = (uintptr_t)ll_map_get(&p->edge_at, LL_S(given->id), given->id.n);
    const ll_record *old = NULL;
    if (at) old = c->edges[at - 1];
    else if (!ll_map_get(&p->deleted_e, LL_S(given->id), given->id.n)) old = ll_map_get(&p->ns->edges, LL_S(given->id), given->id.n);
    ll_record *e = ll_record_copy(given);
    if (!e) return EXT_LEDGER_IO;
    if (set_identity(e, &t->ns, revision, now, old, &given->created)) {
      ll_record_unref(e);
      return EXT_LEDGER_IO;
    }
    if (at) {
      ll_record_unref(c->edges[at - 1]);
      c->edges[at - 1] = e;
    } else if (push((void **)&c->edges, &c->nedges, &c->cape, sizeof e, &e) ||
               ll_map_put(&p->edge_at, LL_S(e->id), e->id.n, (void *)(uintptr_t)c->nedges)) {
      ll_record_unref(e);
      return EXT_LEDGER_IO;
    }
  }
  return check_schema(p, &p->ns->schema, ctx);
}

int ll_state_prepare(ll_state *s, const ext_ledger_context *ctx, const ll_tx *t, ll_time now, ll_change **out) {
  *out = NULL;
  ll_namespace *ns = ll_state_ns(s, LL_S(t->ns), t->ns.n);
  if (!ns) return EXT_LEDGER_NOT_FOUND;
  prep p;
  memset(&p, 0, sizeof p);
  p.ns = ns;
  p.change = ll_change_new();
  if (!p.change) return EXT_LEDGER_IO;
  p.change->revision = ns->revision + 1;
  p.change->committed = now;
  if (ll_s_set(&p.change->ns, LL_S(t->ns), t->ns.n)) {
    ll_change_unref(p.change);
    return EXT_LEDGER_IO;
  }
  int rc = prepare(&p, ctx, t, p.change->revision, now);
  prep_free(&p);
  if (rc) {
    ll_change_unref(p.change);
    return rc;
  }
  *out = p.change;
  return 0;
}

/* ---- applying a change ---- */

static void put_record(ll_map *m, ll_record *r) {
  ll_record *old = ll_map_get(m, LL_S(r->id), r->id.n);
  if (ll_map_put(m, LL_S(r->id), r->id.n, ll_record_ref(r))) {
    ll_record_unref(r);
    return;
  }
  ll_record_unref(old);
}

static size_t change_size(const ll_change *c) {
  lj_buf b = {0};
  ll_change_write(&b, c);
  size_t n = b.len;
  lj_buf_free(&b);
  return n;
}

int ll_state_apply(ll_state *s, ll_change *c, const ll_s *key, const ll_s *fingerprint, int graph) {
  ll_namespace *ns = ll_state_ns(s, LL_S(c->ns), c->ns.n);
  if (!ns) return EXT_LEDGER_IO; /* a commit to a namespace never registered: the storage is corrupt */
  ll_retained r;
  memset(&r, 0, sizeof r);
  r.size = change_size(c);
  if (ll_s_set(&r.key, LL_S(*key), key->n) || ll_s_set(&r.fingerprint, LL_S(*fingerprint), fingerprint->n)) {
    ll_s_free(&r.key);
    ll_s_free(&r.fingerprint);
    return EXT_LEDGER_IO;
  }
  ll_idem *idem = NULL;
  lj_buf k = {0};
  if (key->n) {
    idem = calloc(1, sizeof *idem);
    lj_buf_put(&k, LL_S(c->ns), c->ns.n);
    lj_buf_putc(&k, 0);
    lj_buf_put(&k, LL_S(*key), key->n);
    if (!idem || k.oom) goto oom;
    memcpy(idem->fingerprint, LL_S(*fingerprint), fingerprint->n < 64 ? fingerprint->n : 64);
    idem->cursor = c->cursor;
    idem->revision = c->revision;
    idem->committed = c->committed;
  }
  if (s->first > 0 && s->first * 2 >= s->nchanges) { /* reclaim the space of dropped changes */
    memmove(s->changes, s->changes + s->first, (s->nchanges - s->first) * sizeof *s->changes);
    s->nchanges -= s->first;
    s->first = 0;
  }
  r.change = ll_change_ref(c);
  if (push((void **)&s->changes, &s->nchanges, &s->capchanges, sizeof r, &r)) {
    ll_change_unref(c);
    goto oom;
  }
  if (idem) {
    ll_idem *old = ll_map_get(&s->idempotency, k.ptr, k.len);
    if (ll_map_put(&s->idempotency, k.ptr, k.len, idem)) free(idem);
    else free(old);
  }
  lj_buf_free(&k);
  s->change_bytes += r.size;
  if (!graph) return 0;
  for (size_t i = 0; i < c->ndeleted_edges; i++) ll_record_unref(ll_map_del(&ns->edges, LL_S(c->deleted_edges[i]), c->deleted_edges[i].n));
  for (size_t i = 0; i < c->ndeleted_vertices; i++)
    ll_record_unref(ll_map_del(&ns->vertices, LL_S(c->deleted_vertices[i]), c->deleted_vertices[i].n));
  for (size_t i = 0; i < c->nvertices; i++) put_record(&ns->vertices, c->vertices[i]);
  for (size_t i = 0; i < c->nedges; i++) put_record(&ns->edges, c->edges[i]);
  ns->revision = c->revision;
  s->cursor = c->cursor;
  return 0;
oom:
  free(idem);
  lj_buf_free(&k);
  ll_s_free(&r.key);
  ll_s_free(&r.fingerprint);
  return EXT_LEDGER_IO;
}

int ll_state_put(ll_state *s, ll_record *r) {
  ll_namespace *ns = ll_state_ns(s, LL_S(r->ns), r->ns.n);
  if (!ns) return EXT_LEDGER_IO;
  put_record(r->is_edge ? &ns->edges : &ns->vertices, r);
  return 0;
}

int ll_state_put_idem(ll_state *s, const ll_s *ns, const ll_s *key, const ll_idem *idem) {
  lj_buf k = {0};
  lj_buf_put(&k, LL_S(*ns), ns->n);
  lj_buf_putc(&k, 0);
  lj_buf_put(&k, LL_S(*key), key->n);
  ll_idem *copy = malloc(sizeof *copy);
  if (k.oom || !copy) {
    free(copy);
    lj_buf_free(&k);
    return EXT_LEDGER_IO;
  }
  *copy = *idem;
  ll_idem *old = ll_map_get(&s->idempotency, k.ptr, k.len);
  int rc = ll_map_put(&s->idempotency, k.ptr, k.len, copy);
  lj_buf_free(&k);
  if (rc) {
    free(copy);
    return EXT_LEDGER_IO;
  }
  free(old);
  return 0;
}

size_t ll_state_retained(const ll_state *s) { return s->nchanges - s->first; }
const ll_retained *ll_state_retained_at(const ll_state *s, size_t i) { return &s->changes[s->first + i]; }

uint64_t ll_state_compaction(const ll_state *s, const ext_ledger_store_options *o) {
  size_t count = ll_state_retained(s), bytes = s->change_bytes, i = s->first;
  uint64_t floor = s->floor;
  while (count > 1 && ((int64_t)count > o->max_changes || (int64_t)bytes > o->max_change_bytes)) {
    bytes -= s->changes[i].size;
    floor = s->changes[i].change->cursor;
    count--;
    i++;
  }
  return floor;
}

void ll_state_set_floor(ll_state *s, uint64_t floor) {
  if (floor <= s->floor) return;
  s->floor = floor;
  while (s->first < s->nchanges && s->changes[s->first].change->cursor <= floor) {
    s->change_bytes -= s->changes[s->first].size;
    free_retained(&s->changes[s->first]);
    s->first++;
  }
  LL_MAP_EACH(&s->idempotency, slot) {
    ll_idem *idem = slot->value;
    if (idem->cursor <= floor) {
      free(ll_map_del(&s->idempotency, LL_S(slot->key), slot->key.n));
    }
  }
}

const ll_idem *ll_state_idempotent(const ll_state *s, const ll_s *ns, const ll_s *key) {
  lj_buf k = {0};
  lj_buf_put(&k, LL_S(*ns), ns->n);
  lj_buf_putc(&k, 0);
  lj_buf_put(&k, LL_S(*key), key->n);
  const ll_idem *idem = k.oom ? NULL : ll_map_get(&s->idempotency, k.ptr, k.len);
  lj_buf_free(&k);
  return idem;
}
