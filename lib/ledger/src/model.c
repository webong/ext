/* The entities of entities.schema.json: decoding their wire form and writing
 * it as canonical JSON. Field names, omitted zero values and time forms follow
 * the wire form the contract fixes. */
#include "ledger_internal.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* ---- strings ---- */

int ll_s_set(ll_s *s, const char *p, size_t n) {
  s->p = malloc(n + 1);
  if (!s->p) {
    s->n = 0;
    return EXT_LEDGER_IO;
  }
  if (n) memcpy(s->p, p, n);
  s->p[n] = 0;
  s->n = n;
  return 0;
}

void ll_s_free(ll_s *s) {
  free(s->p);
  s->p = NULL;
  s->n = 0;
}

int ll_s_eq(const ll_s *a, const ll_s *b) { return a->n == b->n && (a->n == 0 || memcmp(a->p, b->p, a->n) == 0); }
int ll_s_eqz(const ll_s *a, const char *z) {
  size_t n = strlen(z);
  return a->n == n && (n == 0 || memcmp(a->p, z, n) == 0);
}
int ll_s_cmp(const ll_s *a, const ll_s *b) {
  size_t n = a->n < b->n ? a->n : b->n;
  int c = n ? memcmp(a->p, b->p, n) : 0;
  if (c) return c;
  return a->n < b->n ? -1 : a->n > b->n;
}

static void write_key(lj_buf *b, int *first, const char *key) {
  if (!*first) lj_buf_putc(b, ',');
  *first = 0;
  lj_buf_putc(b, '"');
  lj_buf_puts(b, key);
  lj_buf_put(b, "\":", 2);
}

static void write_s(lj_buf *b, const ll_s *s) { lj_write_string(b, LL_S(*s), s->n); }

/* ---- time ---- */

static int64_t days_from_civil(int64_t y, int m, int d) {
  y -= m <= 2;
  int64_t era = (y >= 0 ? y : y - 399) / 400;
  int64_t yoe = y - era * 400;
  int64_t doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
  int64_t doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
  return era * 146097 + doe - 719468;
}

static void civil_from_days(int64_t z, int64_t *y, int *m, int *d) {
  z += 719468;
  int64_t era = (z >= 0 ? z : z - 146096) / 146097;
  int64_t doe = z - era * 146097;
  int64_t yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
  int64_t doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
  int64_t mp = (5 * doy + 2) / 153;
  *d = (int)(doy - (153 * mp + 2) / 5 + 1);
  *m = (int)(mp < 10 ? mp + 3 : mp - 9);
  *y = yoe + era * 400 + (*m <= 2);
}

static int digits(const char *s, int n, int *out) {
  int v = 0;
  for (int i = 0; i < n; i++) {
    if (s[i] < '0' || s[i] > '9') return -1;
    v = v * 10 + (s[i] - '0');
  }
  *out = v;
  return 0;
}

static int days_in_month(int64_t y, int m) {
  static const int days[] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
  if (m == 2 && ((y % 4 == 0 && y % 100 != 0) || y % 400 == 0)) return 29;
  return days[m - 1];
}

int ll_time_parse(const char *s, size_t n, ll_time *out) {
  int y, mo, d, h, mi, se;
  if (n < 20 || s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' || s[16] != ':') return -1;
  if (digits(s, 4, &y) || digits(s + 5, 2, &mo) || digits(s + 8, 2, &d) || digits(s + 11, 2, &h) || digits(s + 14, 2, &mi) ||
      digits(s + 17, 2, &se))
    return -1;
  if (mo < 1 || mo > 12 || d < 1 || d > days_in_month(y, mo) || h > 23 || mi > 59 || se > 59) return -1;
  size_t i = 19;
  int32_t nsec = 0;
  if (i < n && s[i] == '.') {
    i++;
    size_t start = i;
    int scale = 100000000;
    while (i < n && s[i] >= '0' && s[i] <= '9') {
      nsec += (s[i] - '0') * scale;
      scale /= 10;
      i++;
    }
    if (i == start) return -1;
  }
  int32_t off;
  if (i < n && s[i] == 'Z') {
    off = 0;
    i++;
  } else if (i + 6 == n && (s[i] == '+' || s[i] == '-') && s[i + 3] == ':') {
    int oh, om;
    if (digits(s + i + 1, 2, &oh) || digits(s + i + 4, 2, &om) || oh > 23 || om > 59) return -1;
    off = (oh * 3600 + om * 60) * (s[i] == '-' ? -1 : 1);
    i += 6;
  } else {
    return -1;
  }
  if (i != n) return -1;
  out->set = 1;
  out->sec = days_from_civil(y, mo, d) * 86400 + h * 3600 + mi * 60 + se - off;
  out->nsec = nsec;
  out->off = off;
  return 0;
}

#define ZERO_TIME_SEC (-62135596800LL) /* 0001-01-01T00:00:00Z */

int ll_time_is_zero(const ll_time *t) { return !t->set || (t->sec == ZERO_TIME_SEC && t->nsec == 0); }

void ll_time_write(lj_buf *b, const ll_time *t) {
  int64_t local = t->sec + t->off;
  int64_t days = local >= 0 ? local / 86400 : -((-local + 86399) / 86400);
  int64_t rem = local - days * 86400;
  int64_t y;
  int m, d;
  civil_from_days(days, &y, &m, &d);
  char tmp[64];
  int len = snprintf(tmp, sizeof tmp, "\"%04lld-%02d-%02dT%02d:%02d:%02d", (long long)y, m, d, (int)(rem / 3600), (int)(rem % 3600 / 60),
                     (int)(rem % 60));
  lj_buf_put(b, tmp, (size_t)len);
  if (t->nsec) {
    char frac[16];
    snprintf(frac, sizeof frac, ".%09d", (int)t->nsec);
    size_t fl = strlen(frac);
    while (frac[fl - 1] == '0') fl--;
    lj_buf_put(b, frac, fl);
  }
  if (t->off == 0) {
    lj_buf_puts(b, "Z\"");
  } else {
    int o = t->off < 0 ? -t->off : t->off;
    len = snprintf(tmp, sizeof tmp, "%c%02d:%02d\"", t->off < 0 ? '-' : '+', o / 3600, o % 3600 / 60);
    lj_buf_put(b, tmp, (size_t)len);
  }
}

ll_time ll_time_utc(ll_time t) {
  t.off = 0;
  return t;
}

ll_time ll_time_now(void) {
  int64_t ms = ll_now_unix_ms();
  ll_time t = {1, 0, 0, 0};
  t.sec = ms >= 0 ? ms / 1000 : -((-ms + 999) / 1000);
  t.nsec = (int32_t)((ms - t.sec * 1000) * 1000000);
  return t;
}

/* ---- decoding helpers ---- */

static int get_string(const lj_value *o, const char *key, ll_s *out) {
  const lj_value *v = lj_get(o, key);
  out->p = NULL;
  out->n = 0;
  if (!v || v->type == LJ_NULL) return 0;
  if (v->type != LJ_STRING) return EXT_LEDGER_INVALID;
  return ll_s_set(out, v->u.string.ptr, v->u.string.len);
}

static int as_uint(const lj_value *v, uint64_t *out) {
  if (v->type != LJ_NUMBER) return EXT_LEDGER_INVALID;
  double d = v->u.number;
  if (d < 0 || d != floor(d) || d >= 18446744073709551616.0) return EXT_LEDGER_INVALID;
  *out = (uint64_t)d;
  return 0;
}

static int get_uint(const lj_value *o, const char *key, uint64_t *out) {
  const lj_value *v = lj_get(o, key);
  *out = 0;
  if (!v || v->type == LJ_NULL) return 0;
  return as_uint(v, out);
}

static int get_time(const lj_value *o, const char *key, ll_time *out) {
  const lj_value *v = lj_get(o, key);
  memset(out, 0, sizeof *out);
  if (!v || v->type == LJ_NULL) return 0;
  if (v->type != LJ_STRING || ll_time_parse(v->u.string.ptr, v->u.string.len, out)) return EXT_LEDGER_INVALID;
  if (out->sec < ZERO_TIME_SEC || out->sec > 253402300799LL) return EXT_LEDGER_INVALID; /* years 1..9999 */
  return 0;
}

static int get_array(const lj_value *o, const char *key, const lj_value **out) {
  const lj_value *v = lj_get(o, key);
  *out = NULL;
  if (!v || v->type == LJ_NULL) return 0;
  if (v->type != LJ_ARRAY) return EXT_LEDGER_INVALID;
  *out = v;
  return 0;
}

/* ---- records ---- */

ll_record *ll_record_new(int is_edge) {
  ll_record *r = calloc(1, sizeof *r);
  if (r) {
    r->refs = 1;
    r->is_edge = is_edge;
  }
  return r;
}

ll_record *ll_record_ref(ll_record *r) {
  if (r) r->refs++;
  return r;
}

void ll_record_unref(ll_record *r) {
  if (!r || --r->refs > 0) return;
  ll_s_free(&r->ns);
  ll_s_free(&r->id);
  ll_s_free(&r->kind);
  ll_s_free(&r->from);
  ll_s_free(&r->to);
  lj_free(r->labels);
  lj_free(r->attrs);
  ll_s_free(&r->prov.source);
  ll_s_free(&r->prov.operation);
  free(r);
}

ll_record *ll_record_copy(const ll_record *r) {
  ll_record *c = ll_record_new(r->is_edge);
  if (!c) return NULL;
  int rc = ll_s_set(&c->ns, LL_S(r->ns), r->ns.n) | ll_s_set(&c->id, LL_S(r->id), r->id.n) | ll_s_set(&c->kind, LL_S(r->kind), r->kind.n) |
           ll_s_set(&c->from, LL_S(r->from), r->from.n) | ll_s_set(&c->to, LL_S(r->to), r->to.n) |
           ll_s_set(&c->prov.source, LL_S(r->prov.source), r->prov.source.n) |
           ll_s_set(&c->prov.operation, LL_S(r->prov.operation), r->prov.operation.n);
  c->revision = r->revision;
  c->created = r->created;
  c->updated = r->updated;
  c->prov.at = r->prov.at;
  if (r->labels && !(c->labels = lj_clone(r->labels))) rc = 1;
  if (r->attrs && !(c->attrs = lj_clone(r->attrs))) rc = 1;
  if (rc) {
    ll_record_unref(c);
    return NULL;
  }
  return c;
}

int ll_prov_is_zero(const ll_prov *p) { return p->source.n == 0 && p->operation.n == 0 && ll_time_is_zero(&p->at); }

void ll_prov_write(lj_buf *b, const ll_prov *p) {
  int pf = 1;
  lj_buf_putc(b, '{');
  if (!ll_time_is_zero(&p->at)) {
    write_key(b, &pf, "at");
    ll_time_write(b, &p->at);
  }
  if (p->operation.n) {
    write_key(b, &pf, "operation");
    write_s(b, &p->operation);
  }
  if (p->source.n) {
    write_key(b, &pf, "source");
    write_s(b, &p->source);
  }
  lj_buf_putc(b, '}');
}

void ll_record_write(lj_buf *b, const ll_record *r) {
  int first = 1;
  lj_buf_putc(b, '{');
  if (r->attrs) {
    write_key(b, &first, "attributes");
    lj_write(b, r->attrs);
  }
  if (!ll_time_is_zero(&r->created)) {
    write_key(b, &first, "created_at");
    ll_time_write(b, &r->created);
  }
  if (r->is_edge) {
    write_key(b, &first, "from");
    write_s(b, &r->from);
  }
  write_key(b, &first, "id");
  write_s(b, &r->id);
  if (!r->is_edge) {
    write_key(b, &first, "kind");
    write_s(b, &r->kind);
  }
  if (r->labels) {
    write_key(b, &first, "labels");
    lj_write(b, r->labels);
  }
  if (r->ns.n) {
    write_key(b, &first, "namespace");
    write_s(b, &r->ns);
  }
  if (!ll_prov_is_zero(&r->prov)) {
    write_key(b, &first, "provenance");
    ll_prov_write(b, &r->prov);
  }
  if (r->revision) {
    write_key(b, &first, "revision");
    lj_write_uint(b, r->revision);
  }
  if (r->is_edge) {
    write_key(b, &first, "to");
    write_s(b, &r->to);
    write_key(b, &first, "type");
    write_s(b, &r->kind);
  }
  if (!ll_time_is_zero(&r->updated)) {
    write_key(b, &first, "updated_at");
    ll_time_write(b, &r->updated);
  }
  lj_buf_putc(b, '}');
}

int ll_record_decode(const lj_value *v, int is_edge, ll_record **out) {
  *out = NULL;
  if (!v || v->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  ll_record *r = ll_record_new(is_edge);
  if (!r) return EXT_LEDGER_IO;
  int rc = get_string(v, "namespace", &r->ns);
  if (!rc) rc = get_string(v, "id", &r->id);
  if (!rc) rc = get_string(v, is_edge ? "type" : "kind", &r->kind);
  if (!rc && is_edge) rc = get_string(v, "from", &r->from);
  if (!rc && is_edge) rc = get_string(v, "to", &r->to);
  if (!rc) rc = get_uint(v, "revision", &r->revision);
  if (!rc) rc = get_time(v, "created_at", &r->created);
  if (!rc) rc = get_time(v, "updated_at", &r->updated);
  if (!rc) {
    const lj_value *labels = lj_get(v, "labels");
    if (labels && labels->type != LJ_NULL) {
      if (labels->type != LJ_OBJECT) {
        rc = EXT_LEDGER_INVALID;
      } else if (labels->u.object.len) {
        if (!(r->labels = lj_new(LJ_OBJECT))) rc = EXT_LEDGER_IO;
        for (size_t i = 0; !rc && i < labels->u.object.len; i++) {
          const lj_member *m = &labels->u.object.members[i];
          lj_value *value;
          if (m->value->type == LJ_NULL) value = lj_new_string("", 0);
          else if (m->value->type == LJ_STRING) value = lj_clone(m->value);
          else {
            rc = EXT_LEDGER_INVALID;
            break;
          }
          if (lj_object_set(r->labels, m->key.ptr, m->key.len, value)) rc = EXT_LEDGER_IO;
        }
      }
    }
  }
  if (!rc) {
    const lj_value *attrs = lj_get(v, "attributes");
    if (attrs && attrs->type != LJ_NULL) {
      if (attrs->type != LJ_OBJECT) rc = EXT_LEDGER_INVALID;
      else if (attrs->u.object.len && !(r->attrs = lj_clone(attrs))) rc = EXT_LEDGER_IO;
    }
  }
  if (!rc) {
    const lj_value *p = lj_get(v, "provenance");
    if (p && p->type != LJ_NULL) {
      if (p->type != LJ_OBJECT) rc = EXT_LEDGER_INVALID;
      if (!rc) rc = get_string(p, "source", &r->prov.source);
      if (!rc) rc = get_string(p, "operation", &r->prov.operation);
      if (!rc) rc = get_time(p, "at", &r->prov.at);
    }
  }
  if (rc) {
    ll_record_unref(r);
    return rc;
  }
  *out = r;
  return 0;
}

/* ---- schemas ---- */

void ll_schema_free(ll_schema *s) {
  ll_s_free(&s->ns);
  ll_s_free(&s->version);
  for (size_t i = 0; i < s->nkinds; i++) ll_s_free(&s->kinds[i]);
  for (size_t i = 0; i < s->nrels; i++) {
    ll_s_free(&s->rels[i].type);
    ll_s_free(&s->rels[i].from);
    ll_s_free(&s->rels[i].to);
  }
  free(s->kinds);
  free(s->rels);
  memset(s, 0, sizeof *s);
}

int ll_schema_decode(const lj_value *v, ll_schema *out) {
  memset(out, 0, sizeof *out);
  if (!v || v->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  int rc = get_string(v, "namespace", &out->ns);
  if (!rc) rc = get_string(v, "version", &out->version);
  const lj_value *kinds = NULL, *rels = NULL;
  if (!rc) rc = get_array(v, "kinds", &kinds);
  if (!rc) rc = get_array(v, "relationships", &rels);
  if (!rc && kinds && kinds->u.array.len) {
    out->kinds = calloc(kinds->u.array.len, sizeof *out->kinds);
    if (!out->kinds) rc = EXT_LEDGER_IO;
    for (size_t i = 0; !rc && i < kinds->u.array.len; i++) {
      const lj_value *k = kinds->u.array.items[i];
      if (k->type == LJ_NULL) rc = ll_s_set(&out->kinds[i], "", 0);
      else if (k->type != LJ_STRING) rc = EXT_LEDGER_INVALID;
      else rc = ll_s_set(&out->kinds[i], k->u.string.ptr, k->u.string.len);
      if (!rc) out->nkinds++;
    }
  }
  if (!rc && rels && rels->u.array.len) {
    out->rels = calloc(rels->u.array.len, sizeof *out->rels);
    if (!out->rels) rc = EXT_LEDGER_IO;
    for (size_t i = 0; !rc && i < rels->u.array.len; i++) {
      const lj_value *r = rels->u.array.items[i];
      if (r->type != LJ_OBJECT) {
        rc = EXT_LEDGER_INVALID;
        break;
      }
      out->nrels++;
      rc = get_string(r, "type", &out->rels[i].type);
      if (!rc) rc = get_string(r, "from", &out->rels[i].from);
      if (!rc) rc = get_string(r, "to", &out->rels[i].to);
    }
  }
  if (rc) ll_schema_free(out);
  return rc;
}

int ll_schema_copy(const ll_schema *s, ll_schema *out) {
  memset(out, 0, sizeof *out);
  int rc = ll_s_set(&out->ns, LL_S(s->ns), s->ns.n) | ll_s_set(&out->version, LL_S(s->version), s->version.n);
  if (s->nkinds && !(out->kinds = calloc(s->nkinds, sizeof *out->kinds))) rc = 1;
  for (size_t i = 0; !rc && i < s->nkinds; i++, out->nkinds++) rc = ll_s_set(&out->kinds[i], LL_S(s->kinds[i]), s->kinds[i].n);
  if (!rc && s->nrels && !(out->rels = calloc(s->nrels, sizeof *out->rels))) rc = 1;
  for (size_t i = 0; !rc && i < s->nrels; i++, out->nrels++) {
    rc = ll_s_set(&out->rels[i].type, LL_S(s->rels[i].type), s->rels[i].type.n) |
         ll_s_set(&out->rels[i].from, LL_S(s->rels[i].from), s->rels[i].from.n) |
         ll_s_set(&out->rels[i].to, LL_S(s->rels[i].to), s->rels[i].to.n);
  }
  if (rc) {
    ll_schema_free(out);
    return EXT_LEDGER_IO;
  }
  return 0;
}

void ll_schema_write(lj_buf *b, const ll_schema *s) {
  int first = 1;
  lj_buf_putc(b, '{');
  if (s->nkinds) {
    write_key(b, &first, "kinds");
    lj_buf_putc(b, '[');
    for (size_t i = 0; i < s->nkinds; i++) {
      if (i) lj_buf_putc(b, ',');
      write_s(b, &s->kinds[i]);
    }
    lj_buf_putc(b, ']');
  }
  write_key(b, &first, "namespace");
  write_s(b, &s->ns);
  if (s->nrels) {
    write_key(b, &first, "relationships");
    lj_buf_putc(b, '[');
    for (size_t i = 0; i < s->nrels; i++) {
      if (i) lj_buf_putc(b, ',');
      int rf = 1;
      lj_buf_putc(b, '{');
      if (s->rels[i].from.n) {
        write_key(b, &rf, "from");
        write_s(b, &s->rels[i].from);
      }
      if (s->rels[i].to.n) {
        write_key(b, &rf, "to");
        write_s(b, &s->rels[i].to);
      }
      write_key(b, &rf, "type");
      write_s(b, &s->rels[i].type);
      lj_buf_putc(b, '}');
    }
    lj_buf_putc(b, ']');
  }
  write_key(b, &first, "version");
  write_s(b, &s->version);
  lj_buf_putc(b, '}');
}

static int kind_cmp(const void *a, const void *b) { return ll_s_cmp(a, b); }
static int rel_cmp(const void *x, const void *y) {
  const ll_rel *a = x, *b = y;
  int c = ll_s_cmp(&a->type, &b->type);
  if (!c) c = ll_s_cmp(&a->from, &b->from);
  if (!c) c = ll_s_cmp(&a->to, &b->to);
  return c;
}

int ll_schema_normalize(ll_schema *s) {
  qsort(s->kinds, s->nkinds, sizeof *s->kinds, kind_cmp);
  size_t w = 0;
  for (size_t i = 0; i < s->nkinds; i++) {
    if (w && ll_s_eq(&s->kinds[w - 1], &s->kinds[i])) ll_s_free(&s->kinds[i]);
    else s->kinds[w++] = s->kinds[i];
  }
  s->nkinds = w;
  qsort(s->rels, s->nrels, sizeof *s->rels, rel_cmp);
  w = 0;
  for (size_t i = 0; i < s->nrels; i++) {
    if (w && rel_cmp(&s->rels[w - 1], &s->rels[i]) == 0) {
      ll_s_free(&s->rels[i].type);
      ll_s_free(&s->rels[i].from);
      ll_s_free(&s->rels[i].to);
    } else {
      s->rels[w++] = s->rels[i];
    }
  }
  s->nrels = w;
  return 0;
}

int ll_schema_validate(const ll_schema *s) {
  if (ll_check_name(LL_S(s->ns), s->ns.n)) return EXT_LEDGER_INVALID;
  if (s->version.n == 0 || s->version.n > 255) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < s->nkinds; i++)
    if (!ll_qualified(&s->ns, &s->kinds[i])) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < s->nrels; i++) {
    if (!ll_qualified(&s->ns, &s->rels[i].type)) return EXT_LEDGER_INVALID;
    if (s->rels[i].from.n && !ll_qualified(&s->ns, &s->rels[i].from)) return EXT_LEDGER_INVALID;
    if (s->rels[i].to.n && !ll_qualified(&s->ns, &s->rels[i].to)) return EXT_LEDGER_INVALID;
  }
  return 0;
}

int ll_schema_same(const ll_schema *a, const ll_schema *b) {
  if (!ll_s_eq(&a->ns, &b->ns) || !ll_s_eq(&a->version, &b->version) || a->nkinds != b->nkinds || a->nrels != b->nrels) return 0;
  for (size_t i = 0; i < a->nkinds; i++)
    if (!ll_s_eq(&a->kinds[i], &b->kinds[i])) return 0;
  for (size_t i = 0; i < a->nrels; i++)
    if (rel_cmp(&a->rels[i], &b->rels[i])) return 0;
  return 1;
}

/* ---- transactions ---- */

void ll_tx_free(ll_tx *t) {
  ll_s_free(&t->ns);
  ll_s_free(&t->key);
  for (size_t i = 0; i < t->nvertices; i++) ll_record_unref(t->vertices[i]);
  for (size_t i = 0; i < t->nedges; i++) ll_record_unref(t->edges[i]);
  for (size_t i = 0; i < t->ndelete_vertices; i++) {
    ll_s_free(&t->delete_vertices[i].ns);
    ll_s_free(&t->delete_vertices[i].id);
  }
  for (size_t i = 0; i < t->ndelete_edges; i++) {
    ll_s_free(&t->delete_edges[i].ns);
    ll_s_free(&t->delete_edges[i].id);
  }
  free(t->vertices);
  free(t->edges);
  free(t->delete_vertices);
  free(t->delete_edges);
  memset(t, 0, sizeof *t);
}

static int decode_records(const lj_value *a, int is_edge, ll_record ***out, size_t *n) {
  if (!a || !a->u.array.len) return 0;
  *out = calloc(a->u.array.len, sizeof **out);
  if (!*out) return EXT_LEDGER_IO;
  for (size_t i = 0; i < a->u.array.len; i++) {
    int rc = ll_record_decode(a->u.array.items[i], is_edge, &(*out)[i]);
    if (rc) return rc;
    (*n)++;
  }
  return 0;
}

static int decode_refs(const lj_value *a, ll_ref **out, size_t *n) {
  if (!a || !a->u.array.len) return 0;
  *out = calloc(a->u.array.len, sizeof **out);
  if (!*out) return EXT_LEDGER_IO;
  for (size_t i = 0; i < a->u.array.len; i++) {
    const lj_value *r = a->u.array.items[i];
    if (r->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
    (*n)++;
    int rc = get_string(r, "namespace", &(*out)[i].ns);
    if (!rc) rc = get_string(r, "id", &(*out)[i].id);
    if (rc) return rc;
  }
  return 0;
}

int ll_tx_decode(const lj_value *v, ll_tx *out) {
  memset(out, 0, sizeof *out);
  if (!v || v->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  const lj_value *a;
  int rc = get_string(v, "namespace", &out->ns);
  if (!rc) rc = get_string(v, "idempotency_key", &out->key);
  if (!rc) {
    const lj_value *e = lj_get(v, "expected_revision");
    if (e && e->type != LJ_NULL) {
      out->has_expected = 1;
      rc = as_uint(e, &out->expected);
    }
  }
  if (!rc && !(rc = get_array(v, "vertices", &a))) rc = decode_records(a, 0, &out->vertices, &out->nvertices);
  if (!rc && !(rc = get_array(v, "edges", &a))) rc = decode_records(a, 1, &out->edges, &out->nedges);
  if (!rc && !(rc = get_array(v, "delete_vertices", &a))) rc = decode_refs(a, &out->delete_vertices, &out->ndelete_vertices);
  if (!rc && !(rc = get_array(v, "delete_edges", &a))) rc = decode_refs(a, &out->delete_edges, &out->ndelete_edges);
  if (!rc) {
    const lj_value *d = lj_get(v, "delete_incident_edges");
    if (d && d->type != LJ_NULL) {
      if (d->type != LJ_BOOL) rc = EXT_LEDGER_INVALID;
      else out->delete_incident = d->u.boolean;
    }
  }
  if (rc) ll_tx_free(out);
  return rc;
}

static void write_refs(lj_buf *b, const ll_ref *refs, size_t n) {
  lj_buf_putc(b, '[');
  for (size_t i = 0; i < n; i++) {
    if (i) lj_buf_putc(b, ',');
    lj_buf_puts(b, "{\"id\":");
    write_s(b, &refs[i].id);
    if (refs[i].ns.n) {
      lj_buf_puts(b, ",\"namespace\":");
      write_s(b, &refs[i].ns);
    }
    lj_buf_putc(b, '}');
  }
  lj_buf_putc(b, ']');
}

static void write_records(lj_buf *b, ll_record *const *records, size_t n) {
  lj_buf_putc(b, '[');
  for (size_t i = 0; i < n; i++) {
    if (i) lj_buf_putc(b, ',');
    ll_record_write(b, records[i]);
  }
  lj_buf_putc(b, ']');
}

static void write_ids(lj_buf *b, const ll_s *ids, size_t n) {
  lj_buf_putc(b, '[');
  for (size_t i = 0; i < n; i++) {
    if (i) lj_buf_putc(b, ',');
    write_s(b, &ids[i]);
  }
  lj_buf_putc(b, ']');
}

void ll_tx_write(lj_buf *b, const ll_tx *t) {
  int first = 1;
  lj_buf_putc(b, '{');
  if (t->ndelete_edges) {
    write_key(b, &first, "delete_edges");
    write_refs(b, t->delete_edges, t->ndelete_edges);
  }
  if (t->delete_incident) {
    write_key(b, &first, "delete_incident_edges");
    lj_buf_puts(b, "true");
  }
  if (t->ndelete_vertices) {
    write_key(b, &first, "delete_vertices");
    write_refs(b, t->delete_vertices, t->ndelete_vertices);
  }
  if (t->nedges) {
    write_key(b, &first, "edges");
    write_records(b, t->edges, t->nedges);
  }
  if (t->has_expected) {
    write_key(b, &first, "expected_revision");
    lj_write_uint(b, t->expected);
  }
  if (t->key.n) {
    write_key(b, &first, "idempotency_key");
    write_s(b, &t->key);
  }
  write_key(b, &first, "namespace");
  write_s(b, &t->ns);
  if (t->nvertices) {
    write_key(b, &first, "vertices");
    write_records(b, t->vertices, t->nvertices);
  }
  lj_buf_putc(b, '}');
}

/* ---- changes ---- */

ll_change *ll_change_new(void) {
  ll_change *c = calloc(1, sizeof *c);
  if (c) c->refs = 1;
  return c;
}

ll_change *ll_change_ref(ll_change *c) {
  if (c) c->refs++;
  return c;
}

void ll_change_unref(ll_change *c) {
  if (!c || --c->refs > 0) return;
  ll_s_free(&c->ns);
  for (size_t i = 0; i < c->nvertices; i++) ll_record_unref(c->vertices[i]);
  for (size_t i = 0; i < c->nedges; i++) ll_record_unref(c->edges[i]);
  for (size_t i = 0; i < c->ndeleted_vertices; i++) ll_s_free(&c->deleted_vertices[i]);
  for (size_t i = 0; i < c->ndeleted_edges; i++) ll_s_free(&c->deleted_edges[i]);
  free(c->vertices);
  free(c->edges);
  free(c->deleted_vertices);
  free(c->deleted_edges);
  free(c);
}

void ll_change_write(lj_buf *b, const ll_change *c) {
  lj_buf_puts(b, "{\"committed_at\":");
  ll_time_write(b, &c->committed);
  lj_buf_puts(b, ",\"cursor\":");
  lj_write_uint(b, c->cursor);
  if (c->ndeleted_edges) {
    lj_buf_puts(b, ",\"deleted_edges\":");
    write_ids(b, c->deleted_edges, c->ndeleted_edges);
  }
  if (c->ndeleted_vertices) {
    lj_buf_puts(b, ",\"deleted_vertices\":");
    write_ids(b, c->deleted_vertices, c->ndeleted_vertices);
  }
  if (c->nedges) {
    lj_buf_puts(b, ",\"edges\":");
    write_records(b, c->edges, c->nedges);
  }
  lj_buf_puts(b, ",\"namespace\":");
  write_s(b, &c->ns);
  lj_buf_puts(b, ",\"revision\":");
  lj_write_uint(b, c->revision);
  if (c->nvertices) {
    lj_buf_puts(b, ",\"vertices\":");
    write_records(b, c->vertices, c->nvertices);
  }
  lj_buf_putc(b, '}');
}

static int decode_ids(const lj_value *a, ll_s **out, size_t *n, size_t *cap) {
  if (!a || !a->u.array.len) return 0;
  *out = calloc(a->u.array.len, sizeof **out);
  if (!*out) return EXT_LEDGER_IO;
  *cap = a->u.array.len;
  for (size_t i = 0; i < a->u.array.len; i++) {
    const lj_value *s = a->u.array.items[i];
    if (s->type != LJ_STRING) return EXT_LEDGER_INVALID;
    if (ll_s_set(&(*out)[i], s->u.string.ptr, s->u.string.len)) return EXT_LEDGER_IO;
    (*n)++;
  }
  return 0;
}

int ll_change_decode(const lj_value *v, ll_change **out) {
  *out = NULL;
  if (!v || v->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  ll_change *c = ll_change_new();
  if (!c) return EXT_LEDGER_IO;
  const lj_value *a;
  int rc = get_uint(v, "cursor", &c->cursor);
  if (!rc) rc = get_uint(v, "revision", &c->revision);
  if (!rc) rc = get_string(v, "namespace", &c->ns);
  if (!rc) rc = get_time(v, "committed_at", &c->committed);
  if (!rc && !(rc = get_array(v, "vertices", &a))) {
    rc = decode_records(a, 0, &c->vertices, &c->nvertices);
    c->capv = c->nvertices;
  }
  if (!rc && !(rc = get_array(v, "edges", &a))) {
    rc = decode_records(a, 1, &c->edges, &c->nedges);
    c->cape = c->nedges;
  }
  if (!rc && !(rc = get_array(v, "deleted_vertices", &a))) rc = decode_ids(a, &c->deleted_vertices, &c->ndeleted_vertices, &c->capdv);
  if (!rc && !(rc = get_array(v, "deleted_edges", &a))) rc = decode_ids(a, &c->deleted_edges, &c->ndeleted_edges, &c->capde);
  if (rc) {
    ll_change_unref(c);
    return rc;
  }
  *out = c;
  return 0;
}
