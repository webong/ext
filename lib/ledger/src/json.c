#include "json.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define LJ_MAX_DEPTH 1000

/* ---- buffers ---- */

void lj_buf_put(lj_buf *b, const char *s, size_t n) {
  if (b->oom) return;
  if (b->len + n + 1 > b->cap) {
    size_t cap = b->cap ? b->cap : 64;
    while (cap < b->len + n + 1) {
      if (cap > SIZE_MAX / 2) {
        b->oom = 1;
        return;
      }
      cap *= 2;
    }
    char *p = realloc(b->ptr, cap);
    if (!p) {
      b->oom = 1;
      return;
    }
    b->ptr = p;
    b->cap = cap;
  }
  if (n) memcpy(b->ptr + b->len, s, n);
  b->len += n;
  b->ptr[b->len] = 0;
}

void lj_buf_puts(lj_buf *b, const char *s) { lj_buf_put(b, s, strlen(s)); }
void lj_buf_putc(lj_buf *b, char c) { lj_buf_put(b, &c, 1); }

void lj_buf_free(lj_buf *b) {
  free(b->ptr);
  b->ptr = NULL;
  b->len = b->cap = 0;
}

char *lj_buf_take(lj_buf *b, size_t *len) {
  if (b->oom) {
    lj_buf_free(b);
    return NULL;
  }
  if (!b->ptr) lj_buf_put(b, "", 0);
  if (b->oom) return NULL;
  char *p = b->ptr;
  if (len) *len = b->len;
  b->ptr = NULL;
  b->len = b->cap = 0;
  return p;
}

/* ---- values ---- */

lj_value *lj_new(lj_type type) {
  lj_value *v = calloc(1, sizeof *v);
  if (v) v->type = type;
  return v;
}

static int str_set(lj_str *s, const char *p, size_t len) {
  s->ptr = malloc(len + 1);
  if (!s->ptr) return -1;
  if (len) memcpy(s->ptr, p, len);
  s->ptr[len] = 0;
  s->len = len;
  return 0;
}

lj_value *lj_new_string(const char *s, size_t len) {
  lj_value *v = lj_new(LJ_STRING);
  if (!v) return NULL;
  if (str_set(&v->u.string, s, len)) {
    free(v);
    return NULL;
  }
  return v;
}

lj_value *lj_new_number(double n) {
  lj_value *v = lj_new(LJ_NUMBER);
  if (v) v->u.number = n;
  return v;
}

void lj_free(lj_value *v) {
  if (!v) return;
  switch (v->type) {
  case LJ_STRING:
    free(v->u.string.ptr);
    break;
  case LJ_ARRAY:
    for (size_t i = 0; i < v->u.array.len; i++) lj_free(v->u.array.items[i]);
    free(v->u.array.items);
    break;
  case LJ_OBJECT:
    for (size_t i = 0; i < v->u.object.len; i++) {
      free(v->u.object.members[i].key.ptr);
      lj_free(v->u.object.members[i].value);
    }
    free(v->u.object.members);
    break;
  default:
    break;
  }
  free(v);
}

int lj_array_push(lj_value *a, lj_value *item) {
  if (!item) return -1;
  if (a->u.array.len == a->u.array.cap) {
    size_t cap = a->u.array.cap ? a->u.array.cap * 2 : 4;
    lj_value **p = realloc(a->u.array.items, cap * sizeof *p);
    if (!p) {
      lj_free(item);
      return -1;
    }
    a->u.array.items = p;
    a->u.array.cap = cap;
  }
  a->u.array.items[a->u.array.len++] = item;
  return 0;
}

int lj_object_set(lj_value *o, const char *key, size_t klen, lj_value *value) {
  if (!value) return -1;
  for (size_t i = 0; i < o->u.object.len; i++) {
    lj_member *m = &o->u.object.members[i];
    if (m->key.len == klen && memcmp(m->key.ptr, key, klen) == 0) {
      lj_free(m->value);
      m->value = value;
      return 0;
    }
  }
  if (o->u.object.len == o->u.object.cap) {
    size_t cap = o->u.object.cap ? o->u.object.cap * 2 : 4;
    lj_member *p = realloc(o->u.object.members, cap * sizeof *p);
    if (!p) {
      lj_free(value);
      return -1;
    }
    o->u.object.members = p;
    o->u.object.cap = cap;
  }
  lj_member *m = &o->u.object.members[o->u.object.len];
  if (str_set(&m->key, key, klen)) {
    lj_free(value);
    return -1;
  }
  m->value = value;
  o->u.object.len++;
  return 0;
}

lj_value *lj_get(const lj_value *o, const char *key) {
  if (!o || o->type != LJ_OBJECT) return NULL;
  size_t klen = strlen(key);
  for (size_t i = 0; i < o->u.object.len; i++) {
    const lj_member *m = &o->u.object.members[i];
    if (m->key.len == klen && memcmp(m->key.ptr, key, klen) == 0) return m->value;
  }
  return NULL;
}

lj_value *lj_clone(const lj_value *v) {
  if (!v) return NULL;
  switch (v->type) {
  case LJ_NULL:
    return lj_new(LJ_NULL);
  case LJ_BOOL: {
    lj_value *c = lj_new(LJ_BOOL);
    if (c) c->u.boolean = v->u.boolean;
    return c;
  }
  case LJ_NUMBER:
    return lj_new_number(v->u.number);
  case LJ_STRING:
    return lj_new_string(v->u.string.ptr, v->u.string.len);
  case LJ_ARRAY: {
    lj_value *c = lj_new(LJ_ARRAY);
    if (!c) return NULL;
    for (size_t i = 0; i < v->u.array.len; i++) {
      if (lj_array_push(c, lj_clone(v->u.array.items[i]))) {
        lj_free(c);
        return NULL;
      }
    }
    return c;
  }
  case LJ_OBJECT: {
    lj_value *c = lj_new(LJ_OBJECT);
    if (!c) return NULL;
    for (size_t i = 0; i < v->u.object.len; i++) {
      const lj_member *m = &v->u.object.members[i];
      if (lj_object_set(c, m->key.ptr, m->key.len, lj_clone(m->value))) {
        lj_free(c);
        return NULL;
      }
    }
    return c;
  }
  }
  return NULL;
}

int lj_equal(const lj_value *a, const lj_value *b) {
  if (!a || !b) return a == b;
  if (a->type != b->type) return 0;
  switch (a->type) {
  case LJ_NULL:
    return 1;
  case LJ_BOOL:
    return a->u.boolean == b->u.boolean;
  case LJ_NUMBER:
    return a->u.number == b->u.number;
  case LJ_STRING:
    return a->u.string.len == b->u.string.len && memcmp(a->u.string.ptr, b->u.string.ptr, a->u.string.len) == 0;
  case LJ_ARRAY:
    if (a->u.array.len != b->u.array.len) return 0;
    for (size_t i = 0; i < a->u.array.len; i++)
      if (!lj_equal(a->u.array.items[i], b->u.array.items[i])) return 0;
    return 1;
  case LJ_OBJECT:
    if (a->u.object.len != b->u.object.len) return 0;
    for (size_t i = 0; i < a->u.object.len; i++) {
      const lj_member *m = &a->u.object.members[i];
      const lj_value *other = NULL;
      for (size_t j = 0; j < b->u.object.len; j++) {
        const lj_member *n = &b->u.object.members[j];
        if (n->key.len == m->key.len && memcmp(n->key.ptr, m->key.ptr, m->key.len) == 0) {
          other = n->value;
          break;
        }
      }
      if (!other || !lj_equal(m->value, other)) return 0;
    }
    return 1;
  }
  return 0;
}

/* ---- UTF-8 ---- */

/* Decodes one rune as Go's utf8.DecodeRune does: an invalid sequence is U+FFFD of width 1. */
static uint32_t decode_rune(const unsigned char *s, size_t n, size_t *width) {
  unsigned char c = s[0];
  *width = 1;
  if (c < 0x80) return c;
  if (c < 0xC2 || c > 0xF4) return 0xFFFD;
  if (c < 0xE0) {
    if (n < 2 || (s[1] & 0xC0) != 0x80) return 0xFFFD;
    *width = 2;
    return ((uint32_t)(c & 0x1F) << 6) | (s[1] & 0x3F);
  }
  if (c < 0xF0) {
    if (n < 3 || (s[1] & 0xC0) != 0x80 || (s[2] & 0xC0) != 0x80) return 0xFFFD;
    if (c == 0xE0 && s[1] < 0xA0) return 0xFFFD;
    if (c == 0xED && s[1] > 0x9F) return 0xFFFD;
    *width = 3;
    return ((uint32_t)(c & 0x0F) << 12) | ((uint32_t)(s[1] & 0x3F) << 6) | (s[2] & 0x3F);
  }
  if (n < 4 || (s[1] & 0xC0) != 0x80 || (s[2] & 0xC0) != 0x80 || (s[3] & 0xC0) != 0x80) return 0xFFFD;
  if (c == 0xF0 && s[1] < 0x90) return 0xFFFD;
  if (c == 0xF4 && s[1] > 0x8F) return 0xFFFD;
  *width = 4;
  return ((uint32_t)(c & 0x07) << 18) | ((uint32_t)(s[1] & 0x3F) << 12) | ((uint32_t)(s[2] & 0x3F) << 6) | (s[3] & 0x3F);
}

static void put_rune(lj_buf *b, uint32_t r) {
  char out[4];
  if (r < 0x80) {
    out[0] = (char)r;
    lj_buf_put(b, out, 1);
  } else if (r < 0x800) {
    out[0] = (char)(0xC0 | (r >> 6));
    out[1] = (char)(0x80 | (r & 0x3F));
    lj_buf_put(b, out, 2);
  } else if (r < 0x10000) {
    out[0] = (char)(0xE0 | (r >> 12));
    out[1] = (char)(0x80 | ((r >> 6) & 0x3F));
    out[2] = (char)(0x80 | (r & 0x3F));
    lj_buf_put(b, out, 3);
  } else {
    out[0] = (char)(0xF0 | (r >> 18));
    out[1] = (char)(0x80 | ((r >> 12) & 0x3F));
    out[2] = (char)(0x80 | ((r >> 6) & 0x3F));
    out[3] = (char)(0x80 | (r & 0x3F));
    lj_buf_put(b, out, 4);
  }
}

/* ---- parsing ---- */

typedef struct {
  const char *p, *end;
  int oom;
  int depth;
} parser;

static void skip_ws(parser *ps) {
  while (ps->p < ps->end && (*ps->p == ' ' || *ps->p == '\t' || *ps->p == '\n' || *ps->p == '\r')) ps->p++;
}

static int hex4(const char *p, const char *end, uint32_t *out) {
  if (end - p < 4) return -1;
  uint32_t v = 0;
  for (int i = 0; i < 4; i++) {
    char c = p[i];
    v <<= 4;
    if (c >= '0' && c <= '9') v |= (uint32_t)(c - '0');
    else if (c >= 'a' && c <= 'f') v |= (uint32_t)(c - 'a' + 10);
    else if (c >= 'A' && c <= 'F') v |= (uint32_t)(c - 'A' + 10);
    else return -1;
  }
  *out = v;
  return 0;
}

static int parse_string(parser *ps, lj_buf *out) {
  ps->p++; /* opening quote */
  while (ps->p < ps->end) {
    unsigned char c = (unsigned char)*ps->p;
    if (c == '"') {
      ps->p++;
      return out->oom ? -1 : 0;
    }
    if (c < 0x20) return -1;
    if (c == '\\') {
      if (ps->end - ps->p < 2) return -1;
      char e = ps->p[1];
      ps->p += 2;
      switch (e) {
      case '"': lj_buf_putc(out, '"'); break;
      case '\\': lj_buf_putc(out, '\\'); break;
      case '/': lj_buf_putc(out, '/'); break;
      case 'b': lj_buf_putc(out, '\b'); break;
      case 'f': lj_buf_putc(out, '\f'); break;
      case 'n': lj_buf_putc(out, '\n'); break;
      case 'r': lj_buf_putc(out, '\r'); break;
      case 't': lj_buf_putc(out, '\t'); break;
      case 'u': {
        uint32_t r;
        if (hex4(ps->p, ps->end, &r)) return -1;
        ps->p += 4;
        if (r >= 0xD800 && r < 0xE000) {
          uint32_t r2;
          if (r < 0xDC00 && ps->end - ps->p >= 6 && ps->p[0] == '\\' && ps->p[1] == 'u' && hex4(ps->p + 2, ps->end, &r2) == 0 &&
              r2 >= 0xDC00 && r2 < 0xE000) {
            r = 0x10000 + ((r - 0xD800) << 10) + (r2 - 0xDC00);
            ps->p += 6;
          } else {
            r = 0xFFFD;
          }
        }
        put_rune(out, r);
        break;
      }
      default:
        return -1;
      }
      continue;
    }
    if (c < 0x80) {
      lj_buf_putc(out, (char)c);
      ps->p++;
      continue;
    }
    size_t width;
    uint32_t r = decode_rune((const unsigned char *)ps->p, (size_t)(ps->end - ps->p), &width);
    if (r == 0xFFFD && width == 1) put_rune(out, 0xFFFD);
    else lj_buf_put(out, ps->p, width);
    ps->p += width;
  }
  return -1;
}

static lj_value *parse_value(parser *ps);

static lj_value *fail_oom(parser *ps, lj_value *v) {
  ps->oom = 1;
  lj_free(v);
  return NULL;
}

static lj_value *parse_number(parser *ps) {
  const char *start = ps->p;
  if (ps->p < ps->end && *ps->p == '-') ps->p++;
  if (ps->p >= ps->end) return NULL;
  if (*ps->p == '0') {
    ps->p++;
  } else if (*ps->p >= '1' && *ps->p <= '9') {
    while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
  } else {
    return NULL;
  }
  if (ps->p < ps->end && *ps->p == '.') {
    ps->p++;
    if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') return NULL;
    while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
  }
  if (ps->p < ps->end && (*ps->p == 'e' || *ps->p == 'E')) {
    ps->p++;
    if (ps->p < ps->end && (*ps->p == '+' || *ps->p == '-')) ps->p++;
    if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') return NULL;
    while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
  }
  size_t n = (size_t)(ps->p - start);
  char small[64];
  char *text = n < sizeof small ? small : malloc(n + 1);
  if (!text) {
    ps->oom = 1;
    return NULL;
  }
  memcpy(text, start, n);
  text[n] = 0;
  double d = strtod(text, NULL);
  if (text != small) free(text);
  if (isinf(d) || isnan(d)) return NULL; /* out of range for a double */
  lj_value *v = lj_new_number(d);
  if (!v) ps->oom = 1;
  return v;
}

static lj_value *parse_value(parser *ps) {
  skip_ws(ps);
  if (ps->p >= ps->end) return NULL;
  char c = *ps->p;
  if (c == '{') {
    if (++ps->depth > LJ_MAX_DEPTH) return NULL;
    ps->p++;
    lj_value *o = lj_new(LJ_OBJECT);
    if (!o) return fail_oom(ps, NULL);
    skip_ws(ps);
    if (ps->p < ps->end && *ps->p == '}') {
      ps->p++;
      ps->depth--;
      return o;
    }
    for (;;) {
      skip_ws(ps);
      if (ps->p >= ps->end || *ps->p != '"') break;
      lj_buf key = {0};
      if (parse_string(ps, &key)) {
        if (key.oom) ps->oom = 1;
        lj_buf_free(&key);
        break;
      }
      skip_ws(ps);
      if (ps->p >= ps->end || *ps->p != ':') {
        lj_buf_free(&key);
        break;
      }
      ps->p++;
      lj_value *item = parse_value(ps);
      if (!item) {
        lj_buf_free(&key);
        break;
      }
      int rc = lj_object_set(o, key.ptr ? key.ptr : "", key.len, item);
      lj_buf_free(&key);
      if (rc) return fail_oom(ps, o);
      skip_ws(ps);
      if (ps->p < ps->end && *ps->p == ',') {
        ps->p++;
        continue;
      }
      if (ps->p < ps->end && *ps->p == '}') {
        ps->p++;
        ps->depth--;
        return o;
      }
      break;
    }
    lj_free(o);
    return NULL;
  }
  if (c == '[') {
    if (++ps->depth > LJ_MAX_DEPTH) return NULL;
    ps->p++;
    lj_value *a = lj_new(LJ_ARRAY);
    if (!a) return fail_oom(ps, NULL);
    skip_ws(ps);
    if (ps->p < ps->end && *ps->p == ']') {
      ps->p++;
      ps->depth--;
      return a;
    }
    for (;;) {
      lj_value *item = parse_value(ps);
      if (!item) break;
      if (lj_array_push(a, item)) return fail_oom(ps, a);
      skip_ws(ps);
      if (ps->p < ps->end && *ps->p == ',') {
        ps->p++;
        continue;
      }
      if (ps->p < ps->end && *ps->p == ']') {
        ps->p++;
        ps->depth--;
        return a;
      }
      break;
    }
    lj_free(a);
    return NULL;
  }
  if (c == '"') {
    lj_buf s = {0};
    if (parse_string(ps, &s)) {
      if (s.oom) ps->oom = 1;
      lj_buf_free(&s);
      return NULL;
    }
    lj_value *v = lj_new(LJ_STRING);
    if (!v) {
      lj_buf_free(&s);
      return fail_oom(ps, NULL);
    }
    size_t len;
    char *p = lj_buf_take(&s, &len);
    if (!p) return fail_oom(ps, v);
    v->u.string.ptr = p;
    v->u.string.len = len;
    return v;
  }
  if (c == 't' && ps->end - ps->p >= 4 && memcmp(ps->p, "true", 4) == 0) {
    ps->p += 4;
    lj_value *v = lj_new(LJ_BOOL);
    if (!v) return fail_oom(ps, NULL);
    v->u.boolean = 1;
    return v;
  }
  if (c == 'f' && ps->end - ps->p >= 5 && memcmp(ps->p, "false", 5) == 0) {
    ps->p += 5;
    lj_value *v = lj_new(LJ_BOOL);
    if (!v) return fail_oom(ps, NULL);
    return v;
  }
  if (c == 'n' && ps->end - ps->p >= 4 && memcmp(ps->p, "null", 4) == 0) {
    ps->p += 4;
    lj_value *v = lj_new(LJ_NULL);
    if (!v) return fail_oom(ps, NULL);
    return v;
  }
  if (c == '-' || (c >= '0' && c <= '9')) return parse_number(ps);
  return NULL;
}

lj_value *lj_parse(const char *text, size_t len, int *oom) {
  parser ps = {text, text + len, 0, 0};
  lj_value *v = parse_value(&ps);
  if (v) {
    skip_ws(&ps);
    if (ps.p != ps.end) {
      lj_free(v);
      v = NULL;
    }
  }
  if (oom) *oom = ps.oom;
  return v;
}

/* ---- canonical writing ---- */

/* The next UTF-16 code unit of s, decoding UTF-8 as Go ranges over a string. */
static int next_unit(const unsigned char **s, const unsigned char *end, uint16_t *pending, uint16_t *unit) {
  if (*pending) {
    *unit = *pending;
    *pending = 0;
    return 1;
  }
  if (*s >= end) return 0;
  size_t width;
  uint32_t r = decode_rune(*s, (size_t)(end - *s), &width);
  *s += width;
  if (r >= 0x10000) {
    r -= 0x10000;
    *unit = (uint16_t)(0xD800 + (r >> 10));
    *pending = (uint16_t)(0xDC00 + (r & 0x3FF));
  } else {
    *unit = (uint16_t)r;
  }
  return 1;
}

int lj_key_compare(const char *a, size_t alen, const char *b, size_t blen) {
  const unsigned char *x = (const unsigned char *)a, *xe = x + alen;
  const unsigned char *y = (const unsigned char *)b, *ye = y + blen;
  uint16_t px = 0, py = 0;
  for (;;) {
    uint16_t ux, uy;
    int hx = next_unit(&x, xe, &px, &ux);
    int hy = next_unit(&y, ye, &py, &uy);
    if (!hx || !hy) return hx - hy;
    if (ux != uy) return ux < uy ? -1 : 1;
  }
}

void lj_write_string(lj_buf *b, const char *s, size_t len) {
  static const char hexd[] = "0123456789abcdef";
  const unsigned char *p = (const unsigned char *)s, *end = p + len;
  lj_buf_putc(b, '"');
  while (p < end) {
    unsigned char c = *p;
    if (c < 0x80) {
      switch (c) {
      case '"': lj_buf_put(b, "\\\"", 2); break;
      case '\\': lj_buf_put(b, "\\\\", 2); break;
      case '\b': lj_buf_put(b, "\\b", 2); break;
      case '\f': lj_buf_put(b, "\\f", 2); break;
      case '\n': lj_buf_put(b, "\\n", 2); break;
      case '\r': lj_buf_put(b, "\\r", 2); break;
      case '\t': lj_buf_put(b, "\\t", 2); break;
      default:
        if (c < 0x20) {
          char esc[6] = {'\\', 'u', '0', '0', hexd[c >> 4], hexd[c & 0xF]};
          lj_buf_put(b, esc, 6);
        } else {
          lj_buf_putc(b, (char)c);
        }
      }
      p++;
      continue;
    }
    size_t width;
    uint32_t r = decode_rune(p, (size_t)(end - p), &width);
    if (r == 0xFFFD && width == 1) put_rune(b, 0xFFFD);
    else lj_buf_put(b, (const char *)p, width);
    p += width;
  }
  lj_buf_putc(b, '"');
}

void lj_write_uint(lj_buf *b, uint64_t n) {
  char tmp[24];
  int len = snprintf(tmp, sizeof tmp, "%llu", (unsigned long long)n);
  lj_buf_put(b, tmp, (size_t)len);
}

/* Shortest round-trip digits, printed as ECMAScript (and Go's encoding/json) prints a double. */
void lj_write_number(lj_buf *b, double d) {
  if (d == 0) {
    lj_buf_puts(b, signbit(d) ? "-0" : "0");
    return;
  }
  char tmp[40];
  int prec;
  for (prec = 1; prec <= 17; prec++) {
    snprintf(tmp, sizeof tmp, "%.*e", prec - 1, d);
    for (char *q = tmp; *q; q++)
      if (*q == ',') *q = '.'; /* a host whose locale prints a comma */
    if (strtod(tmp, NULL) == d) break;
  }
  /* tmp is [-]D[.DDD]e[+-]XX */
  const char *q = tmp;
  int negative = 0;
  if (*q == '-') {
    negative = 1;
    q++;
  }
  char digits[24];
  int nd = 0;
  while (*q && *q != 'e') {
    if (*q >= '0' && *q <= '9') digits[nd++] = *q;
    q++;
  }
  while (nd > 1 && digits[nd - 1] == '0') nd--;
  int exp10 = atoi(q + 1); /* value = d.ddd x 10^exp10 */
  if (negative) lj_buf_putc(b, '-');
  double a = fabs(d);
  if (a < 1e-6 || a >= 1e21) {
    lj_buf_putc(b, digits[0]);
    if (nd > 1) {
      lj_buf_putc(b, '.');
      lj_buf_put(b, digits + 1, (size_t)(nd - 1));
    }
    char e[8];
    int len = snprintf(e, sizeof e, "e%c%d", exp10 < 0 ? '-' : '+', exp10 < 0 ? -exp10 : exp10);
    lj_buf_put(b, e, (size_t)len);
    return;
  }
  int point = exp10 + 1; /* digits before the decimal point */
  if (point <= 0) {
    lj_buf_puts(b, "0.");
    for (int i = 0; i < -point; i++) lj_buf_putc(b, '0');
    lj_buf_put(b, digits, (size_t)nd);
  } else if (point >= nd) {
    lj_buf_put(b, digits, (size_t)nd);
    for (int i = nd; i < point; i++) lj_buf_putc(b, '0');
  } else {
    lj_buf_put(b, digits, (size_t)point);
    lj_buf_putc(b, '.');
    lj_buf_put(b, digits + point, (size_t)(nd - point));
  }
}

static int member_cmp(const void *x, const void *y) {
  const lj_member *a = *(const lj_member *const *)x, *b = *(const lj_member *const *)y;
  return lj_key_compare(a->key.ptr, a->key.len, b->key.ptr, b->key.len);
}

void lj_write(lj_buf *b, const lj_value *v) {
  switch (v->type) {
  case LJ_NULL:
    lj_buf_puts(b, "null");
    break;
  case LJ_BOOL:
    lj_buf_puts(b, v->u.boolean ? "true" : "false");
    break;
  case LJ_NUMBER:
    lj_write_number(b, v->u.number);
    break;
  case LJ_STRING:
    lj_write_string(b, v->u.string.ptr, v->u.string.len);
    break;
  case LJ_ARRAY:
    lj_buf_putc(b, '[');
    for (size_t i = 0; i < v->u.array.len; i++) {
      if (i) lj_buf_putc(b, ',');
      lj_write(b, v->u.array.items[i]);
    }
    lj_buf_putc(b, ']');
    break;
  case LJ_OBJECT: {
    size_t n = v->u.object.len;
    const lj_member **order = n ? malloc(n * sizeof *order) : NULL;
    if (n && !order) {
      b->oom = 1;
      return;
    }
    for (size_t i = 0; i < n; i++) order[i] = &v->u.object.members[i];
    qsort(order, n, sizeof *order, member_cmp);
    lj_buf_putc(b, '{');
    for (size_t i = 0; i < n; i++) {
      if (i) lj_buf_putc(b, ',');
      lj_write_string(b, order[i]->key.ptr, order[i]->key.len);
      lj_buf_putc(b, ':');
      lj_write(b, order[i]->value);
    }
    lj_buf_putc(b, '}');
    free(order);
    break;
  }
  }
}
