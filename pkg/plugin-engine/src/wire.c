#define _GNU_SOURCE
#include "wire.h"
#include <limits.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static int key_compare(const void *a, const void *b) {
  yyjson_val *x = *(yyjson_val *const *)a, *y = *(yyjson_val *const *)b;
  size_t nx = yyjson_get_len(x), ny = yyjson_get_len(y);
  int c = memcmp(yyjson_get_str(x), yyjson_get_str(y), nx < ny ? nx : ny);
  return c ? c : (nx > ny) - (nx < ny);
}
static int unique(yyjson_val *v, unsigned depth) {
  if (depth > 64)
    return 0;
  size_t i, n;
  yyjson_val *k, *child;
  if (yyjson_is_obj(v)) {
    n = yyjson_obj_size(v);
    yyjson_val **keys = n ? malloc(n * sizeof(*keys)) : NULL;
    if (n && !keys)
      return 0;
    yyjson_obj_foreach(v, i, n, k, child) {
      keys[i] = k;
      if (!unique(child, depth + 1)) {
        free(keys);
        return 0;
      }
    }
    if (n > 1)
      qsort(keys, n, sizeof(*keys), key_compare);
    for (i = 1; i < n; i++) {
      if (!key_compare(&keys[i - 1], &keys[i])) {
        free(keys);
        return 0;
      }
    }
    free(keys);
  } else if (yyjson_is_arr(v)) {
    yyjson_arr_foreach(v, i, n, child) if (!unique(child, depth + 1)) return 0;
  }
  return 1;
}
yyjson_doc *ctx_parse(const uint8_t *data, size_t len) {
  if (!data || !len || len > CTX_HOST_MAX_FRAME)
    return NULL;
  /* Bound nesting before parsing/allocating. Full syntax and UTF-8 are checked
   * by yyjson. Retain number ctx_text: payload precision must not depend on C
   * types. */
  unsigned depth = 0;
  int string = 0, escape = 0;
  for (size_t i = 0; i < len; i++) {
    uint8_t c = data[i];
    if (string) {
      if (escape)
        escape = 0;
      else if (c == '\\')
        escape = 1;
      else if (c == '"')
        string = 0;
    } else if (c == '"')
      string = 1;
    else if (c == '[' || c == '{') {
      if (++depth > 65)
        return NULL;
    } else if (c == ']' || c == '}') {
      if (!depth)
        return NULL;
      depth--;
    }
  }
  yyjson_doc *d =
      yyjson_read((const char *)data, len, YYJSON_READ_NUMBER_AS_RAW);
  if (d && !unique(yyjson_doc_get_root(d), 0)) {
    yyjson_doc_free(d);
    return NULL;
  }
  return d;
}
ctx_status ctx_host_validate_json(const uint8_t *data, size_t len) {
  yyjson_doc *d = ctx_parse(data, len);
  if (!d)
    return CTX_INVALID;
  yyjson_doc_free(d);
  return CTX_OK;
}
static yyjson_val *get(yyjson_val *o, const char *k) {
  return yyjson_obj_get(o, k);
}
int ctx_fields(yyjson_val *o, const char *const *names, size_t count) {
  if (!yyjson_is_obj(o))
    return 0;
  size_t i, n;
  yyjson_val *k, *v;
  yyjson_obj_foreach(o, i, n, k, v) {
    size_t j;
    for (j = 0; j < count; j++)
      if (yyjson_equals_str(k, names[j]))
        break;
    if (j == count)
      return 0;
  }
  return 1;
}
#define FIELDS(v, ...)                                                         \
  ctx_fields(v, (const char *const[]){__VA_ARGS__},                            \
             sizeof((const char *const[]){__VA_ARGS__}) / sizeof(char *))
int ctx_text(yyjson_val *v, int revision, int optional) {
  if (!v || yyjson_is_null(v))
    return optional;
  if (!yyjson_is_str(v))
    return 0;
  size_t n = yyjson_get_len(v);
  const unsigned char *s = (const unsigned char *)yyjson_get_str(v);
  if (!n)
    return optional;
  if (n > 256)
    return 0;
  for (size_t i = 0; i < n; i++) {
    int alnum = (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= '0' && s[i] <= '9') ||
                (revision && s[i] >= 'A' && s[i] <= 'Z');
    if (!alnum &&
        !(i && (s[i] == '.' || s[i] == '_' || s[i] == '/' || s[i] == '-' ||
                (revision && (s[i] == ':' || s[i] == '+')))))
      return 0;
  }
  return 1;
}
int ctx_same(yyjson_val *a, yyjson_val *b) {
  if (yyjson_is_null(a))
    a = NULL;
  if (yyjson_is_null(b))
    b = NULL;
  if (!a)
    return !b || yyjson_equals_str(b, "");
  if (!b)
    return yyjson_equals_str(a, "");
  return yyjson_is_str(a) && yyjson_is_str(b) &&
         yyjson_get_len(a) == yyjson_get_len(b) &&
         !memcmp(yyjson_get_str(a), yyjson_get_str(b), yyjson_get_len(a));
}
int ctx_identity(yyjson_val *v) {
  return FIELDS(v, "id", "revision", "version") &&
         ctx_text(get(v, "id"), 0, 0) && ctx_text(get(v, "revision"), 1, 0) &&
         ctx_text(get(v, "version"), 1, 1);
}
int ctx_same_identity(yyjson_val *a, yyjson_val *b) {
  return ctx_same(get(a, "id"), get(b, "id")) &&
         ctx_same(get(a, "revision"), get(b, "revision")) &&
         ctx_same(get(a, "version"), get(b, "version"));
}
int ctx_ref(yyjson_val *v) {
  return ctx_text(get(v, "name"), 0, 0) && ctx_text(get(v, "version"), 1, 0);
}
int ctx_same_ref(yyjson_val *a, yyjson_val *b) {
  return ctx_same(get(a, "name"), get(b, "name")) &&
         ctx_same(get(a, "version"), get(b, "version"));
}
yyjson_val *ctx_lookup(yyjson_val *d, yyjson_val *r, yyjson_val *name) {
  size_t i, n, j, m;
  yyjson_val *c, *o;
  yyjson_arr_foreach(get(d, "contracts"), i, n, c) if (ctx_same_ref(c, r)) {
    yyjson_arr_foreach(get(c, "operations"), j, m,
                       o) if (ctx_same(get(o, "name"), name)) return o;
  }
  return NULL;
}
int ctx_descriptor(yyjson_val *d) {
  if (!FIELDS(d, "apiVersion", "identity", "contracts") ||
      !yyjson_equals_str(get(d, "apiVersion"), "ext.plugin/v1") ||
      !ctx_identity(get(d, "identity")))
    return 0;
  yyjson_val *cs = get(d, "contracts"), *c, *o;
  size_t i, n, j, m;
  if (!yyjson_is_arr(cs) || !yyjson_arr_size(cs) || yyjson_arr_size(cs) > 64)
    return 0;
  yyjson_arr_foreach(cs, i, n, c) {
    if (!FIELDS(c, "name", "version", "operations") || !ctx_ref(c))
      return 0;
    for (size_t k = 0; k < i; k++)
      if (ctx_same_ref(c, yyjson_arr_get(cs, k)))
        return 0;
    yyjson_val *ops = get(c, "operations");
    if (!yyjson_is_arr(ops) || !yyjson_arr_size(ops) ||
        yyjson_arr_size(ops) > 256)
      return 0;
    yyjson_arr_foreach(ops, j, m, o) {
      if (!FIELDS(o, "name", "surface") || !ctx_text(get(o, "name"), 0, 0) ||
          yyjson_equals_str(get(o, "name"), "plugin.hello") ||
          !ctx_text(get(o, "surface"), 0, 1))
        return 0;
      for (size_t k = 0; k < j; k++)
        if (ctx_same(get(o, "name"), get(yyjson_arr_get(ops, k), "name")))
          return 0;
    }
  }
  return 1;
}
int ctx_match(yyjson_val *a, yyjson_val *b) {
  if (!ctx_descriptor(b) ||
      !ctx_same_identity(get(a, "identity"), get(b, "identity")) ||
      yyjson_arr_size(get(a, "contracts")) !=
          yyjson_arr_size(get(b, "contracts")))
    return 0;
  size_t i, n, j, m;
  yyjson_val *c, *o;
  yyjson_arr_foreach(get(a, "contracts"), i, n, c) {
    yyjson_val *match = NULL;
    size_t k, z;
    yyjson_val *bc;
    yyjson_arr_foreach(get(b, "contracts"), k, z, bc) if (ctx_same_ref(c, bc)) {
      match = bc;
      break;
    }
    if (!match || yyjson_arr_size(get(c, "operations")) !=
                      yyjson_arr_size(get(match, "operations")))
      return 0;
    yyjson_arr_foreach(get(c, "operations"), j, m, o) {
      yyjson_val *other = ctx_lookup(b, c, get(o, "name"));
      if (!other || !ctx_same(get(o, "surface"), get(other, "surface")))
        return 0;
    }
  }
  return 1;
}
int64_t ctx_wall_ms(void) {
  struct timespec ts;
  clock_gettime(CLOCK_REALTIME, &ts);
  return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}
static int digits(const char *s, size_t n) {
  int v = 0;
  for (size_t i = 0; i < n; i++) {
    if (s[i] < '0' || s[i] > '9')
      return -1;
    v = v * 10 + s[i] - '0';
  }
  return v;
}
static int64_t timestamp(yyjson_val *v) {
  if (!yyjson_is_str(v))
    return -1;
  const char *s = yyjson_get_str(v);
  size_t n = yyjson_get_len(v), p = 19;
  if (n < 20 || s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' ||
      s[16] != ':')
    return -1;
  int y = digits(s, 4), mo = digits(s + 5, 2), d = digits(s + 8, 2),
      h = digits(s + 11, 2), mi = digits(s + 14, 2), se = digits(s + 17, 2);
  static const int days[] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
  if (y < 1 || mo < 1 || mo > 12 || d < 1 ||
      d > days[mo - 1] +
              (mo == 2 && y % 4 == 0 && (y % 100 != 0 || y % 400 == 0)) ||
      h < 0 || h > 23 || mi < 0 || mi > 59 || se < 0 || se > 59)
    return -1;
  int ms = 0, submillisecond = 0;
  if (p < n && s[p] == '.') {
    size_t start = ++p;
    while (p < n && s[p] >= '0' && s[p] <= '9') {
      if (p - start < 3)
        ms = ms * 10 + s[p] - '0';
      else if (p - start < 9 && s[p] != '0')
        submillisecond = 1;
      p++;
    }
    if (p == start)
      return -1;
    for (size_t k = p - start; k < 3; k++)
      ms *= 10;
  }
  int offset = 0;
  if (p + 1 == n && s[p] == 'Z') {
  } else if (p + 6 == n && (s[p] == '+' || s[p] == '-') && s[p + 3] == ':') {
    int oh = digits(s + p + 1, 2), om = digits(s + p + 4, 2);
    if (oh < 0 || oh > 23 || om < 0 || om > 59)
      return -1;
    offset = (oh * 60 + om) * 60 * (s[p] == '+' ? 1 : -1);
  } else
    return -1;
  struct tm t = {0};
  t.tm_year = y - 1900;
  t.tm_mon = mo - 1;
  t.tm_mday = d;
  t.tm_hour = h;
  t.tm_min = mi;
  t.tm_sec = se;
  /* The ABI schedules in milliseconds. Round a wire deadline up so a guest
   * cannot finish early merely because a host supplied nanosecond precision. */
  int64_t result = ((int64_t)timegm(&t) - offset) * 1000 + ms + submillisecond;
  if (y == 1 && mo == 1 && d == 1 && !h && !mi && !se && !ms &&
      !submillisecond && !offset)
    return -1;
  return result;
}
ctx_status ctx_request(yyjson_val *d, yyjson_val *r, int64_t *deadline) {
  if (!FIELDS(r, "apiVersion", "id", "plugin", "contract", "operation",
              "surface", "deadline", "payload") ||
      !yyjson_equals_str(get(r, "apiVersion"), "ext.plugin/v1") ||
      !ctx_text(get(r, "id"), 1, 0) || !ctx_identity(get(r, "plugin")) ||
      !FIELDS(get(r, "contract"), "name", "version") ||
      !ctx_ref(get(r, "contract")))
    return CTX_INVALID;
  *deadline = timestamp(get(r, "deadline"));
  if (*deadline == -1)
    return CTX_INVALID;
  if (!ctx_same_identity(get(d, "identity"), get(r, "plugin")))
    return CTX_MISMATCH;
  yyjson_val *op = ctx_lookup(d, get(r, "contract"), get(r, "operation"));
  if (!op)
    return CTX_UNSUPPORTED;
  return ctx_same(get(op, "surface"), get(r, "surface")) ? CTX_OK
                                                         : CTX_MISMATCH;
}
ctx_status ctx_response(yyjson_val *r, const char *id) {
  if (!FIELDS(r, "apiVersion", "id", "payload", "error"))
    return CTX_INVALID;
  if (!yyjson_equals_str(get(r, "apiVersion"), "ext.plugin/v1") ||
      !yyjson_equals_str(get(r, "id"), id))
    return CTX_MISMATCH;
  yyjson_val *err = get(r, "error");
  int has_error = err && !yyjson_is_null(err);
  if (has_error == (get(r, "payload") != NULL))
    return CTX_INVALID;
  if (has_error) {
    if (!FIELDS(err, "code", "message", "retryAfterMilliseconds") ||
        !ctx_text(get(err, "code"), 0, 0) ||
        (get(err, "message") && !yyjson_is_null(get(err, "message")) &&
         !yyjson_is_str(get(err, "message"))) ||
        yyjson_get_len(get(err, "message")) > 4096)
      return CTX_INVALID;
    yyjson_val *retry = get(err, "retryAfterMilliseconds");
    if (retry && !yyjson_is_null(retry)) {
      const char *s = yyjson_get_raw(retry);
      size_t n = yyjson_get_len(retry);
      uint64_t x = 0;
      if (!s || !n)
        return CTX_INVALID;
      if (n == 2 && s[0] == '-' && s[1] == '0')
        return CTX_OK;
      for (size_t i = 0; i < n; i++) {
        if (s[i] < '0' || s[i] > '9' ||
            x > ((uint64_t)INT64_MAX - (s[i] - '0')) / 10)
          return CTX_INVALID;
        x = x * 10 + s[i] - '0';
      }
    }
  }
  return CTX_OK;
}
