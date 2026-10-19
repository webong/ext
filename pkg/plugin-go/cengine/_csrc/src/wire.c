#define _GNU_SOURCE
#include "platform.h"
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
yyjson_doc *ext_parse(const uint8_t *data, size_t len) {
  if (!data || !len || len > EXT_HOST_MAX_FRAME)
    return NULL;
  /* Bound nesting before parsing/allocating. Full syntax and UTF-8 are checked
   * by yyjson. Retain number ext_text: payload precision must not depend on C
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
ext_status ext_host_validate_json(const uint8_t *data, size_t len) {
  yyjson_doc *d = ext_parse(data, len);
  if (!d)
    return EXT_INVALID;
  yyjson_doc_free(d);
  return EXT_OK;
}
static yyjson_val *get(yyjson_val *o, const char *k) {
  return yyjson_obj_get(o, k);
}
int ext_fields(yyjson_val *o, const char *const *names, size_t count) {
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
  ext_fields(v, (const char *const[]){__VA_ARGS__},                            \
             sizeof((const char *const[]){__VA_ARGS__}) / sizeof(char *))
int ext_text(yyjson_val *v, int revision, int optional) {
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
int ext_same(yyjson_val *a, yyjson_val *b) {
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
int ext_identity(yyjson_val *v) {
  return FIELDS(v, "id", "revision", "version") &&
         ext_text(get(v, "id"), 0, 0) && ext_text(get(v, "revision"), 1, 0) &&
         ext_text(get(v, "version"), 1, 1);
}
int ext_same_identity(yyjson_val *a, yyjson_val *b) {
  return ext_same(get(a, "id"), get(b, "id")) &&
         ext_same(get(a, "revision"), get(b, "revision")) &&
         ext_same(get(a, "version"), get(b, "version"));
}
int ext_ref(yyjson_val *v) {
  return ext_text(get(v, "name"), 0, 0) && ext_text(get(v, "version"), 1, 0);
}
int ext_same_ref(yyjson_val *a, yyjson_val *b) {
  return ext_same(get(a, "name"), get(b, "name")) &&
         ext_same(get(a, "version"), get(b, "version"));
}
yyjson_val *ext_lookup(yyjson_val *d, yyjson_val *r, yyjson_val *name) {
  size_t i, n, j, m;
  yyjson_val *c, *o;
  yyjson_arr_foreach(get(d, "contracts"), i, n, c) if (ext_same_ref(c, r)) {
    yyjson_arr_foreach(get(c, "operations"), j, m,
                       o) if (ext_same(get(o, "name"), name)) return o;
  }
  return NULL;
}
/* A list of contracts, as in a descriptor's "contracts" and "reverse": distinct
 * name/version references, each with 1..256 operations of distinct names.
 * min and max bound the list length. progress says whether an operation may
 * carry the optional boolean "progress": a plugin declares it for operations it
 * serves, never for the host operations it calls. */
static int contracts_valid(yyjson_val *cs, size_t min, size_t max,
                           int progress) {
  yyjson_val *c, *o;
  size_t i, n, j, m;
  if (!yyjson_is_arr(cs) || yyjson_arr_size(cs) < min ||
      yyjson_arr_size(cs) > max)
    return 0;
  yyjson_arr_foreach(cs, i, n, c) {
    if (!FIELDS(c, "name", "version", "operations") || !ext_ref(c))
      return 0;
    size_t k, z;
    yyjson_val *earlier;
    EXT_ARR_BEFORE(cs, i, k, z, earlier) if (ext_same_ref(c, earlier)) return 0;
    yyjson_val *ops = get(c, "operations");
    if (!yyjson_is_arr(ops) || !yyjson_arr_size(ops) ||
        yyjson_arr_size(ops) > 256)
      return 0;
    yyjson_arr_foreach(ops, j, m, o) {
      yyjson_val *flag = get(o, "progress");
      if (flag && yyjson_is_null(flag))
        flag = NULL; /* null decodes as false in Go */
      if (!FIELDS(o, "name", "surface", "progress") ||
          !ext_text(get(o, "name"), 0, 0) ||
          yyjson_equals_str(get(o, "name"), "plugin.hello") ||
          !ext_text(get(o, "surface"), 0, 1) ||
          (flag && !yyjson_is_bool(flag)) ||
          (flag && yyjson_get_bool(flag) && !progress))
        return 0;
      size_t x, y;
      yyjson_val *sibling;
      EXT_ARR_BEFORE(ops, j, x, y, sibling)
      if (ext_same(get(o, "name"), get(sibling, "name"))) return 0;
    }
  }
  return 1;
}
int ext_descriptor(yyjson_val *d) {
  if (!FIELDS(d, "apiVersion", "identity", "contracts", "reverse") ||
      !yyjson_equals_str(get(d, "apiVersion"), "ext.plugin/v1") ||
      !ext_identity(get(d, "identity")) ||
      !contracts_valid(get(d, "contracts"), 1, 64, 1))
    return 0;
  yyjson_val *reverse = get(d, "reverse");
  /* An absent list and a null one (Go encodes none) both mean no reverse. */
  return !reverse || yyjson_is_null(reverse) ||
         contracts_valid(reverse, 0, 64, 0);
}
/* "progress" absent and false are the same declaration. */
static int progress_of(yyjson_val *op) {
  yyjson_val *flag = get(op, "progress");
  return flag && yyjson_is_bool(flag) && yyjson_get_bool(flag);
}
/* The two lists declare the same contracts with the same operations, surfaces
 * and progress flags, in any order. Both are valid lists. */
static int contracts_match(yyjson_val *a, yyjson_val *b) {
  size_t i, n, j, m;
  yyjson_val *c, *o;
  size_t na = (a && yyjson_is_arr(a)) ? yyjson_arr_size(a) : 0;
  size_t nb = (b && yyjson_is_arr(b)) ? yyjson_arr_size(b) : 0;
  if (na != nb)
    return 0;
  if (!na)
    return 1;
  yyjson_arr_foreach(a, i, n, c) {
    yyjson_val *match = NULL;
    size_t k, z;
    yyjson_val *bc;
    yyjson_arr_foreach(b, k, z, bc) if (ext_same_ref(c, bc)) {
      match = bc;
      break;
    }
    if (!match || yyjson_arr_size(get(c, "operations")) !=
                      yyjson_arr_size(get(match, "operations")))
      return 0;
    yyjson_arr_foreach(get(c, "operations"), j, m, o) {
      yyjson_val *other = NULL, *candidate;
      size_t x, y;
      yyjson_arr_foreach(get(match, "operations"), x, y, candidate) if (
          ext_same(get(o, "name"), get(candidate, "name"))) {
        other = candidate;
        break;
      }
      if (!other || !ext_same(get(o, "surface"), get(other, "surface")) ||
          progress_of(o) != progress_of(other))
        return 0;
    }
  }
  return 1;
}
int ext_match(yyjson_val *a, yyjson_val *b) {
  return ext_descriptor(b) &&
         ext_same_identity(get(a, "identity"), get(b, "identity")) &&
         contracts_match(get(a, "contracts"), get(b, "contracts")) &&
         contracts_match(get(a, "reverse"), get(b, "reverse"));
}
yyjson_val **ext_arr_snapshot(yyjson_val *arr) {
  size_t n = yyjson_arr_size(arr), i, max;
  yyjson_val *v, **out = malloc((n ? n : 1) * sizeof(*out));
  if (!out)
    return NULL;
  yyjson_arr_foreach(arr, i, max, v) out[i] = v;
  return out;
}
int64_t ext_wall_ms(void) {
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
  int64_t result = ((int64_t)ext_timegm(&t) - offset) * 1000 + ms + submillisecond;
  if (y == 1 && mo == 1 && d == 1 && !h && !mi && !se && !ms &&
      !submillisecond && !offset)
    return -1;
  return result;
}
/* The optional event rate a host sends for an operation that declares progress:
 * integers, or null. Their range is checked by the side that applies them. */
/* A JSON integer that fits int64, which is what Go decodes into an int. The
 * parser keeps numbers as raw text, so check the digits: an optional minus, then
 * digits only (no fraction or exponent). */
static int int64_value(yyjson_val *v) {
  if (!yyjson_is_raw(v))
    return 0;
  const char *s = yyjson_get_raw(v);
  size_t n = yyjson_get_len(v), i = 0;
  int negative = 0;
  if (!s || !n)
    return 0;
  if (s[0] == '-') {
    negative = 1;
    i = 1;
  }
  if (i == n)
    return 0;
  uint64_t limit = (uint64_t)INT64_MAX + (negative ? 1u : 0u), x = 0;
  for (; i < n; i++) {
    if (s[i] < '0' || s[i] > '9' || x > (limit - (uint64_t)(s[i] - '0')) / 10)
      return 0;
    x = x * 10 + (uint64_t)(s[i] - '0');
  }
  return 1;
}
static int progress_limits_valid(yyjson_val *v) {
  if (!v || yyjson_is_null(v))
    return 1;
  yyjson_val *events, *bytes;
  if (!FIELDS(v, "eventsPerSecond", "bytesPerSecond"))
    return 0;
  events = get(v, "eventsPerSecond");
  bytes = get(v, "bytesPerSecond");
  return (!events || yyjson_is_null(events) || int64_value(events)) &&
         (!bytes || yyjson_is_null(bytes) || int64_value(bytes));
}
int ext_descriptor_needs_frames(yyjson_val *d) {
  yyjson_val *reverse = get(d, "reverse"), *cs = get(d, "contracts"), *c, *o;
  size_t i, n, j, m;
  if (reverse && yyjson_is_arr(reverse) && yyjson_arr_size(reverse))
    return 1;
  yyjson_arr_foreach(cs, i, n, c) {
    yyjson_arr_foreach(get(c, "operations"), j, m, o) if (progress_of(o))
        return 1;
  }
  return 0;
}
ext_status ext_request(yyjson_val *d, yyjson_val *r, int64_t *deadline) {
  yyjson_val *parent = get(r, "parent");
  if (!FIELDS(r, "apiVersion", "id", "plugin", "contract", "operation",
              "surface", "deadline", "payload", "progress", "parent") ||
      !progress_limits_valid(get(r, "progress")) ||
      (parent && !yyjson_is_null(parent) && !yyjson_is_str(parent)) ||
      !yyjson_equals_str(get(r, "apiVersion"), "ext.plugin/v1") ||
      !ext_text(get(r, "id"), 1, 0) || !ext_identity(get(r, "plugin")) ||
      !FIELDS(get(r, "contract"), "name", "version") ||
      !ext_ref(get(r, "contract")))
    return EXT_INVALID;
  *deadline = timestamp(get(r, "deadline"));
  if (*deadline == -1)
    return EXT_INVALID;
  if (!ext_same_identity(get(d, "identity"), get(r, "plugin")))
    return EXT_MISMATCH;
  yyjson_val *op = ext_lookup(d, get(r, "contract"), get(r, "operation"));
  if (!op)
    return EXT_UNSUPPORTED;
  return ext_same(get(op, "surface"), get(r, "surface")) ? EXT_OK
                                                         : EXT_MISMATCH;
}
ext_status ext_response(yyjson_val *r, const char *id) {
  if (!FIELDS(r, "apiVersion", "id", "payload", "error"))
    return EXT_INVALID;
  if (!yyjson_equals_str(get(r, "apiVersion"), "ext.plugin/v1") ||
      !yyjson_equals_str(get(r, "id"), id))
    return EXT_MISMATCH;
  yyjson_val *err = get(r, "error");
  int has_error = err && !yyjson_is_null(err);
  if (has_error == (get(r, "payload") != NULL))
    return EXT_INVALID;
  if (has_error) {
    if (!FIELDS(err, "code", "message", "retryAfterMilliseconds") ||
        !ext_text(get(err, "code"), 0, 0) ||
        (get(err, "message") && !yyjson_is_null(get(err, "message")) &&
         !yyjson_is_str(get(err, "message"))) ||
        yyjson_get_len(get(err, "message")) > 4096)
      return EXT_INVALID;
    yyjson_val *retry = get(err, "retryAfterMilliseconds");
    if (retry && !yyjson_is_null(retry)) {
      const char *s = yyjson_get_raw(retry);
      size_t n = yyjson_get_len(retry);
      uint64_t x = 0;
      if (!s || !n)
        return EXT_INVALID;
      if (n == 2 && s[0] == '-' && s[1] == '0')
        return EXT_OK;
      for (size_t i = 0; i < n; i++) {
        if (s[i] < '0' || s[i] > '9' ||
            x > ((uint64_t)INT64_MAX - (s[i] - '0')) / 10)
          return EXT_INVALID;
        x = x * 10 + s[i] - '0';
      }
    }
  }
  return EXT_OK;
}
