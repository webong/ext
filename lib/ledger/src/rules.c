/* The rules of rules.json that need no stored state, applied in its order. */
#include "ledger_internal.h"

#include <stdlib.h>
#include <string.h>

int ll_check_name(const char *s, size_t n) {
  if (n < 1 || n > 255) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < n; i++) {
    char c = s[i];
    int alnum = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9');
    if (i == 0 && !alnum) return EXT_LEDGER_INVALID;
    if (!alnum && c != '.' && c != '_' && c != '/' && c != '-') return EXT_LEDGER_INVALID;
    if (c == '/' && i + 1 < n && s[i + 1] == '/') return EXT_LEDGER_INVALID;
  }
  return 0;
}

int ll_qualified(const ll_s *ns, const ll_s *name) {
  return ll_check_name(LL_S(*name), name->n) == 0 && name->n > ns->n + 1 && memcmp(name->p, LL_S(*ns), ns->n) == 0 && name->p[ns->n] == '/';
}

/* ---- secrets ---- */

static uint32_t rune_at(const unsigned char *s, size_t n, size_t *width) {
  /* A lenient decoder: the strings here are already valid UTF-8 (the JSON reader replaced the rest). */
  unsigned char c = s[0];
  if (c < 0x80 || n < 2) {
    *width = 1;
    return c;
  }
  if (c < 0xE0) {
    *width = 2;
    return ((uint32_t)(c & 0x1F) << 6) | (s[1] & 0x3F);
  }
  if (c < 0xF0 || n < 4) {
    *width = n < 3 ? n : 3;
    return n < 3 ? 0xFFFD : ((uint32_t)(c & 0x0F) << 12) | ((uint32_t)(s[1] & 0x3F) << 6) | (s[2] & 0x3F);
  }
  *width = 4;
  return ((uint32_t)(c & 0x07) << 18) | ((uint32_t)(s[1] & 0x3F) << 12) | ((uint32_t)(s[2] & 0x3F) << 6) | (s[3] & 0x3F);
}

/* Case-insensitive equality of a rune and a lowercase ASCII letter, under the
 * simple case folding a (?i) regular expression uses: k also matches the
 * Kelvin sign and s the long s. */
static int folds_to(uint32_t r, char lower) {
  if (r == (uint32_t)lower || r == (uint32_t)(lower - 32)) return 1;
  if (lower == 'k' && r == 0x212A) return 1;
  if (lower == 's' && r == 0x017F) return 1;
  return 0;
}

/* Matches pattern at s: letters match case-insensitively, and a '.' stands for
 * the regular expression ".?": any one rune but a newline, or none. */
static int match_at(const unsigned char *s, size_t n, const char *pattern) {
  if (!*pattern) return 1;
  if (*pattern == '.') {
    if (match_at(s, n, pattern + 1)) return 1;
    if (!n) return 0;
    size_t w;
    uint32_t r = rune_at(s, n, &w);
    return r != '\n' && match_at(s + w, n - w, pattern + 1);
  }
  if (!n) return 0;
  size_t w;
  uint32_t r = rune_at(s, n, &w);
  return folds_to(r, *pattern) && match_at(s + w, n - w, pattern + 1);
}

/* (?i)(secret|password|passwd|token|credential|private.?key|api.?key) anywhere in s. */
static int secret_key(const char *s, size_t n) {
  static const char *const patterns[] = {"secret", "password", "passwd", "token", "credential", "private.key", "api.key"};
  const unsigned char *p = (const unsigned char *)s;
  for (size_t i = 0; i < n;) {
    for (size_t k = 0; k < sizeof patterns / sizeof *patterns; k++)
      if (match_at(p + i, n - i, patterns[k])) return 1;
    size_t w;
    rune_at(p + i, n - i, &w);
    i += w;
  }
  return 0;
}

/* The lowercase of a rune where it lowers to ASCII, as Go's unicode.ToLower does. */
static uint32_t lower_rune(uint32_t r) {
  if (r >= 'A' && r <= 'Z') return r + 32;
  if (r == 0x212A) return 'k';
  if (r == 0x0130) return 'i';
  return r;
}

static int has_lower_prefix(const char *s, size_t n, const char *prefix) {
  const unsigned char *p = (const unsigned char *)s;
  size_t i = 0;
  for (; *prefix; prefix++) {
    if (i >= n) return 0;
    size_t w;
    uint32_t r = rune_at(p + i, n - i, &w);
    if (lower_rune(r) != (uint32_t)(unsigned char)*prefix) return 0;
    i += w;
  }
  return 1;
}

static int secret_marker(const char *s, size_t n) { return has_lower_prefix(s, n, "secret-value:") || has_lower_prefix(s, n, "private-key:"); }

#define MAX_STRING_BYTES 4096

static int check_value(const lj_value *v);

int ll_check_attributes(const lj_value *o) {
  if (!o || o->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < o->u.object.len; i++) {
    const lj_member *m = &o->u.object.members[i];
    if (secret_key(m->key.ptr, m->key.len)) return EXT_LEDGER_DENIED;
    int rc = check_value(m->value);
    if (rc) return rc;
  }
  return 0;
}

static int check_value(const lj_value *v) {
  switch (v->type) {
  case LJ_OBJECT:
    return ll_check_attributes(v);
  case LJ_ARRAY:
    for (size_t i = 0; i < v->u.array.len; i++) {
      int rc = check_value(v->u.array.items[i]);
      if (rc) return rc;
    }
    return 0;
  case LJ_STRING:
    if (secret_marker(v->u.string.ptr, v->u.string.len)) return EXT_LEDGER_DENIED;
    if (v->u.string.len > MAX_STRING_BYTES) return EXT_LEDGER_TOO_LARGE;
    return 0;
  default:
    return 0;
  }
}

static int check_record(const ll_record *r, const ext_ledger_store_options *o) {
  int rc;
  if (r->attrs && (rc = ll_check_attributes(r->attrs))) return rc;
  if (r->labels) {
    for (size_t i = 0; i < r->labels->u.object.len; i++) {
      const lj_member *m = &r->labels->u.object.members[i];
      const lj_str *value = &m->value->u.string;
      if (secret_key(m->key.ptr, m->key.len) || secret_marker(value->ptr, value->len)) return EXT_LEDGER_DENIED;
      if (value->len > MAX_STRING_BYTES) return EXT_LEDGER_TOO_LARGE;
    }
  }
  const ll_s *prov[2] = {&r->prov.source, &r->prov.operation};
  for (int i = 0; i < 2; i++) {
    if (secret_marker(LL_S(*prov[i]), prov[i]->n)) return EXT_LEDGER_DENIED;
    if (prov[i]->n > MAX_STRING_BYTES) return EXT_LEDGER_TOO_LARGE;
  }
  size_t size = 0;
  lj_buf b = {0};
  if (r->labels) {
    lj_write(&b, r->labels);
    size += b.len;
    b.len = 0;
  }
  if (r->attrs) {
    lj_write(&b, r->attrs);
    size += b.len;
  }
  int oom = b.oom;
  lj_buf_free(&b);
  if (oom) return EXT_LEDGER_IO;
  if ((int64_t)size > o->max_record_bytes) return EXT_LEDGER_TOO_LARGE;
  return 0;
}

ext_ledger_store_options ll_options_defaults(const ext_ledger_store_options *given) {
  ext_ledger_store_options o = {0};
  if (given) o = *given;
  if (o.max_changes <= 0) o.max_changes = 4096;
  if (o.max_change_bytes <= 0) o.max_change_bytes = 16 << 20;
  if (o.max_transaction_records <= 0) o.max_transaction_records = 65536;
  if (o.max_record_bytes <= 0) o.max_record_bytes = 1 << 20;
  if (o.max_results <= 0) o.max_results = 65536;
  if (o.max_snapshot_records <= 0) o.max_snapshot_records = 1 << 20;
  return o;
}

int ll_check_limit(int64_t limit, const ext_ledger_store_options *o) {
  if (limit <= 0) return EXT_LEDGER_INVALID;
  if (limit > o->max_results) return EXT_LEDGER_TOO_LARGE;
  return 0;
}

static int ref_outside(const ll_ref *ref, const ll_s *ns) { return (ref->ns.n && !ll_s_eq(&ref->ns, ns)) || ref->id.n == 0; }

int ll_check_tx(const ll_tx *t, const ext_ledger_store_options *o) {
  if (ll_check_name(LL_S(t->ns), t->ns.n)) return EXT_LEDGER_INVALID;
  if (t->key.n > 255) return EXT_LEDGER_TOO_LARGE;
  if ((int64_t)(t->nvertices + t->nedges + t->ndelete_vertices + t->ndelete_edges) > o->max_transaction_records) return EXT_LEDGER_TOO_LARGE;
  for (size_t i = 0; i < t->ndelete_vertices; i++)
    if (ref_outside(&t->delete_vertices[i], &t->ns)) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < t->ndelete_edges; i++)
    if (ref_outside(&t->delete_edges[i], &t->ns)) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < t->nvertices; i++) {
    const ll_record *v = t->vertices[i];
    if (v->ns.n && !ll_s_eq(&v->ns, &t->ns)) return EXT_LEDGER_INVALID;
    if (v->id.n == 0 || v->id.n > 1024) return EXT_LEDGER_INVALID;
    if (!ll_qualified(&t->ns, &v->kind)) return EXT_LEDGER_INVALID;
    int rc = check_record(v, o);
    if (rc) return rc;
  }
  for (size_t i = 0; i < t->nedges; i++) {
    const ll_record *e = t->edges[i];
    if (e->ns.n && !ll_s_eq(&e->ns, &t->ns)) return EXT_LEDGER_INVALID;
    if (e->id.n == 0 || e->from.n == 0 || e->to.n == 0 || e->id.n > 1024) return EXT_LEDGER_INVALID;
    if (!ll_qualified(&t->ns, &e->kind)) return EXT_LEDGER_INVALID;
    int rc = check_record(e, o);
    if (rc) return rc;
  }
  return 0;
}

/* ---- the hash map ---- */

static uint64_t hash(const char *k, size_t n) {
  uint64_t h = 1469598103934665603ULL;
  for (size_t i = 0; i < n; i++) {
    h ^= (unsigned char)k[i];
    h *= 1099511628211ULL;
  }
  return h;
}

static ll_slot *find(const ll_map *m, const char *k, size_t n) {
  if (!m->cap) return NULL;
  size_t mask = m->cap - 1;
  for (size_t i = (size_t)hash(k, n) & mask;; i = (i + 1) & mask) {
    ll_slot *s = &m->slots[i];
    if (s->used == 0) return NULL;
    if (s->used == 1 && s->key.n == n && (n == 0 || memcmp(s->key.p, k, n) == 0)) return s;
  }
}

void *ll_map_get(const ll_map *m, const char *k, size_t n) {
  ll_slot *s = find(m, k, n);
  return s ? s->value : NULL;
}

static int grow(ll_map *m) {
  size_t cap = m->cap ? m->cap * 2 : 16;
  if ((m->len + 1) * 4 < m->cap * 3) cap = m->cap; /* only tombstones to clear */
  ll_slot *slots = calloc(cap, sizeof *slots);
  if (!slots) return EXT_LEDGER_IO;
  for (size_t i = 0; i < m->cap; i++) {
    ll_slot *s = &m->slots[i];
    if (s->used != 1) {
      if (s->used == 2) ll_s_free(&s->key);
      continue;
    }
    size_t j = (size_t)hash(LL_S(s->key), s->key.n) & (cap - 1);
    while (slots[j].used) j = (j + 1) & (cap - 1);
    slots[j] = *s;
  }
  free(m->slots);
  m->slots = slots;
  m->cap = cap;
  m->deleted = 0;
  return 0;
}

int ll_map_put(ll_map *m, const char *k, size_t n, void *value) {
  ll_slot *s = find(m, k, n);
  if (s) {
    s->value = value;
    return 0;
  }
  if ((m->len + m->deleted + 1) * 4 >= m->cap * 3 && grow(m)) return EXT_LEDGER_IO;
  size_t mask = m->cap - 1;
  size_t i = (size_t)hash(k, n) & mask;
  while (m->slots[i].used == 1) i = (i + 1) & mask;
  s = &m->slots[i];
  if (s->used == 2) {
    ll_s_free(&s->key);
    m->deleted--;
  }
  if (ll_s_set(&s->key, k, n)) {
    s->used = 0;
    return EXT_LEDGER_IO;
  }
  s->value = value;
  s->used = 1;
  m->len++;
  return 0;
}

void *ll_map_del(ll_map *m, const char *k, size_t n) {
  ll_slot *s = find(m, k, n);
  if (!s) return NULL;
  void *v = s->value;
  s->used = 2;
  s->value = NULL;
  m->len--;
  m->deleted++;
  return v;
}

void ll_map_free(ll_map *m) {
  for (size_t i = 0; i < m->cap; i++)
    if (m->slots[i].used) ll_s_free(&m->slots[i].key);
  free(m->slots);
  memset(m, 0, sizeof *m);
}
