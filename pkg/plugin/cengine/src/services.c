#define _POSIX_C_SOURCE 200809L
#include "services.h"
#include "wire.h"
#include <math.h>
#include <stdlib.h>
#include <string.h>
#define get yyjson_obj_get
#define eq yyjson_equals_str
#define absent(v) (!(v) || yyjson_is_null(v))

static ctx_status output(yyjson_val *value, ctx_buffer *out) {
  if (value)
    out->data = (uint8_t *)yyjson_val_write(value, 0, &out->len);
  else {
    out->data = (uint8_t *)strdup("null");
    out->len = 4;
  }
  return out->data ? CTX_OK : CTX_NOMEM;
}
static int list(yyjson_val *v, size_t min, size_t max) {
  return yyjson_is_arr(v) && yyjson_arr_size(v) >= min &&
         yyjson_arr_size(v) <= max;
}
static int contains(yyjson_val *array, yyjson_val *item) {
  size_t i, n;
  yyjson_val *v;
  yyjson_arr_foreach(array, i, n, v) if (ctx_same(v, item)) return 1;
  return 0;
}
int ctx_protocols(yyjson_val *v) {
  if (!list(v, 1, 32))
    return 0;
  size_t i, n, j;
  yyjson_val *p;
  yyjson_arr_foreach(v, i, n, p) {
    if (!ctx_text(p, 0, 0))
      return 0;
    for (j = 0; j < i; j++)
      if (ctx_same(p, yyjson_arr_get(v, j)))
        return 0;
  }
  return 1;
}
yyjson_val *ctx_negotiate(yyjson_val *preferred, yyjson_val *offered) {
  size_t i, n;
  yyjson_val *p;
  yyjson_arr_foreach(preferred, i, n, p) if (contains(offered, p)) return p;
  return NULL;
}
static int requirement(yyjson_val *r) {
  return CTX_FIELDS(r, "identity", "contract", "operation") &&
         (absent(get(r, "identity")) || ctx_identity(get(r, "identity"))) &&
         CTX_FIELDS(get(r, "contract"), "name", "version") &&
         ctx_ref(get(r, "contract")) && ctx_text(get(r, "operation"), 0, 0);
}
static int satisfies(yyjson_val *d, yyjson_val *r) {
  return (absent(get(r, "identity")) ||
          ctx_same_identity(get(d, "identity"), get(r, "identity"))) &&
         ctx_lookup(d, get(r, "contract"), get(r, "operation")) != NULL;
}
static ctx_status select_descriptor(yyjson_val *ds, yyjson_val *r,
                                    yyjson_val **chosen) {
  if (!yyjson_is_arr(ds) || !requirement(r))
    return CTX_INVALID;
  size_t i, n, count = 0;
  yyjson_val *d;
  yyjson_arr_foreach(ds, i, n, d) {
    if (!ctx_descriptor(d))
      return CTX_INVALID;
    if (satisfies(d, r)) {
      *chosen = d;
      count++;
    }
  }
  return count == 1 ? CTX_OK : count ? CTX_AMBIGUOUS : CTX_NOT_FOUND;
}
/* Decode one raw number without strtod/locale dependence. */
static int number(yyjson_val *v, double *out) {
  if (!yyjson_is_raw(v))
    return 0;
  yyjson_val parsed;
  if (!yyjson_read_number(yyjson_get_raw(v), &parsed, 0, NULL, NULL))
    return 0;
  *out = yyjson_get_num(&parsed);
  return isfinite(*out);
}
static int limit(yyjson_val *s, const char *name, double *out) {
  yyjson_val *v = get(s, name);
  *out = 0;
  if (absent(v))
    return 1;
  if (!yyjson_is_raw(v))
    return 0;
  const char *p = yyjson_get_raw(v);
  size_t n = yyjson_get_len(v);
  uint64_t value = 0;
  if (n == 2 && p[0] == '-' && p[1] == '0')
    return 1;
  if (!n)
    return 0;
  for (size_t i = 0; i < n; i++) {
    if (p[i] < '0' || p[i] > '9')
      return 0;
    unsigned digit = (unsigned)(p[i] - '0');
    if (value > (INT64_MAX - digit) / 10)
      return 0;
    value = value * 10 + digit;
  }
  *out = (double)value;
  return 1;
}
static int optional_bool(yyjson_val *v) {
  return absent(v) || yyjson_is_bool(v);
}
int ctx_schema_valid(yyjson_val *s, unsigned depth, unsigned *budget) {
  if (depth > 16 || !*budget)
    return 0;
  (*budget)--;
  if (!CTX_FIELDS(s, "type", "properties", "required", "additionalProperties",
                  "items", "enum", "minimum", "maximum", "minLength",
                  "maxLength", "minItems", "maxItems"))
    return 0;
  yyjson_val *type = get(s, "type"), *props = get(s, "properties"),
             *required = get(s, "required"), *items = get(s, "items"),
             *en = get(s, "enum"), *add = get(s, "additionalProperties"),
             *low = get(s, "minimum"), *high = get(s, "maximum");
  if (!optional_bool(add) || (!absent(props) && !yyjson_is_obj(props)) ||
      (!absent(required) && !yyjson_is_arr(required)) ||
      (!absent(en) && !yyjson_is_arr(en)))
    return 0;
  double minlen, maxlen, minitems, maxitems, lo = 0, hi = 0;
  if (!limit(s, "minLength", &minlen) || !limit(s, "maxLength", &maxlen) ||
      !limit(s, "minItems", &minitems) || !limit(s, "maxItems", &maxitems) ||
      (maxlen > 0 && minlen > maxlen) || (maxitems > 0 && minitems > maxitems))
    return 0;
  if ((!absent(low) && !number(low, &lo)) ||
      (!absent(high) && !number(high, &hi)) ||
      (!absent(low) && !absent(high) && lo > hi))
    return 0;
  if (!eq(type, "object") &&
      (yyjson_obj_size(props) || yyjson_arr_size(required) ||
       yyjson_get_bool(add)))
    return 0;
  if (!eq(type, "array") && (!absent(items) || minitems || maxitems))
    return 0;
  if (!eq(type, "string") && (yyjson_arr_size(en) || minlen || maxlen))
    return 0;
  if (!eq(type, "number") && !eq(type, "integer") &&
      (!absent(low) || !absent(high)))
    return 0;
  size_t i, n, j;
  yyjson_val *k, *v;
  if (eq(type, "object")) {
    if (yyjson_obj_size(props) > 256)
      return 0;
    yyjson_obj_foreach(props, i, n, k, v) {
      if (!yyjson_get_len(k) || yyjson_get_len(k) > 256 ||
          !ctx_schema_valid(v, depth + 1, budget))
        return 0;
    }
    yyjson_arr_foreach(required, i, n, k) {
      if (!yyjson_is_str(k) ||
          !yyjson_obj_getn(props, yyjson_get_str(k), yyjson_get_len(k)))
        return 0;
      for (j = 0; j < i; j++)
        if (ctx_same(k, yyjson_arr_get(required, j)))
          return 0;
    }
    return 1;
  }
  if (eq(type, "array"))
    return !absent(items) && ctx_schema_valid(items, depth + 1, budget);
  if (eq(type, "string")) {
    if (yyjson_arr_size(en) > 256)
      return 0;
    yyjson_arr_foreach(en, i, n, v) {
      if (!yyjson_is_str(v) || yyjson_get_len(v) > 4096)
        return 0;
      for (j = 0; j < i; j++)
        if (ctx_same(v, yyjson_arr_get(en, j)))
          return 0;
    }
    return 1;
  }
  return eq(type, "number") || eq(type, "integer") || eq(type, "boolean") ||
         eq(type, "null");
}
int ctx_schema_check(yyjson_val *s, yyjson_val *v) {
  yyjson_val *type = get(s, "type");
  size_t i, n;
  yyjson_val *k, *item;
  double min, max, x;
  if (eq(type, "object")) {
    if (!yyjson_is_obj(v))
      return 0;
    yyjson_val *required = get(s, "required"), *props = get(s, "properties");
    yyjson_arr_foreach(required, i, n,
                       k) if (!yyjson_obj_getn(v, yyjson_get_str(k),
                                               yyjson_get_len(k))) return 0;
    yyjson_obj_foreach(v, i, n, k, item) {
      yyjson_val *field =
          yyjson_obj_getn(props, yyjson_get_str(k), yyjson_get_len(k));
      if (field ? !ctx_schema_check(field, item)
                : !yyjson_get_bool(get(s, "additionalProperties")))
        return 0;
    }
    return 1;
  }
  if (eq(type, "array")) {
    if (!yyjson_is_arr(v))
      return 0;
    limit(s, "minItems", &min);
    limit(s, "maxItems", &max);
    if (yyjson_arr_size(v) < min || (max > 0 && yyjson_arr_size(v) > max))
      return 0;
    yyjson_arr_foreach(
        v, i, n, item) if (!ctx_schema_check(get(s, "items"), item)) return 0;
    return 1;
  }
  if (eq(type, "string")) {
    if (!yyjson_is_str(v))
      return 0;
    size_t runes = 0;
    const unsigned char *str = (const unsigned char *)yyjson_get_str(v);
    for (i = 0; i < yyjson_get_len(v); i++)
      if ((str[i] & 0xc0) != 0x80)
        runes++;
    limit(s, "minLength", &min);
    limit(s, "maxLength", &max);
    return runes >= min && (!max || runes <= max) &&
           (!yyjson_arr_size(get(s, "enum")) || contains(get(s, "enum"), v));
  }
  if (eq(type, "boolean"))
    return yyjson_is_bool(v);
  if (eq(type, "null"))
    return yyjson_is_null(v);
  if (!number(v, &x) || (eq(type, "integer") && trunc(x) != x))
    return 0;
  yyjson_val *lo = get(s, "minimum"), *hi = get(s, "maximum");
  if (!absent(lo)) {
    number(lo, &min);
    if (x < min)
      return 0;
  }
  if (!absent(hi)) {
    number(hi, &max);
    if (x > max)
      return 0;
  }
  return 1;
}
static int profile(yyjson_val *p) {
  if (!CTX_FIELDS(p, "name", "protocols", "concurrent", "cancellation",
                  "processOwner", "nativeStreaming", "nativeCallbacks") ||
      !ctx_text(get(p, "name"), 0, 0) || !ctx_protocols(get(p, "protocols")))
    return 0;
  const char *flags[] = {"concurrent", "nativeStreaming", "nativeCallbacks"};
  for (size_t i = 0; i < 3; i++)
    if (!optional_bool(get(p, flags[i])))
      return 0;
  return (absent(get(p, "cancellation")) ||
          yyjson_is_str(get(p, "cancellation"))) &&
         (absent(get(p, "processOwner")) ||
          yyjson_is_str(get(p, "processOwner")));
}
static int unique_name(yyjson_val *array, size_t i, yyjson_val *p) {
  for (size_t j = 0; j < i; j++)
    if (ctx_same(get(p, "name"), get(yyjson_arr_get(array, j), "name")))
      return 0;
  return 1;
}
static ctx_status route_result(yyjson_val *h, yyjson_val *g, yyjson_val *bridge,
                               yyjson_val *protocol, yyjson_val *want,
                               ctx_buffer *out) {
  const char *flags[] = {"concurrent", "nativeStreaming", "nativeCallbacks"};
  int features[3];
  for (size_t i = 0; i < 3; i++) {
    features[i] =
        yyjson_get_bool(get(h, flags[i])) && yyjson_get_bool(get(g, flags[i]));
    if (bridge)
      features[i] = features[i] &&
                    yyjson_get_bool(get(get(bridge, "frontend"), flags[i])) &&
                    yyjson_get_bool(get(get(bridge, "backend"), flags[i]));
    if (yyjson_get_bool(get(want, flags[i])) && !features[i])
      return CTX_UNSUPPORTED;
  }
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  if (!d)
    return CTX_NOMEM;
  yyjson_mut_val *root = yyjson_mut_obj(d);
  yyjson_mut_doc_set_root(d, root);
  int ok = root != NULL;
  const char *names[] = {"protocol", "host", "guest", "bridge"};
  yyjson_val *vals[] = {protocol, h, g, bridge ? get(bridge, "name") : NULL};
  for (size_t i = 0; i < 4 && ok; i++)
    if (vals[i]) {
      yyjson_mut_val *copy = yyjson_val_mut_copy(d, vals[i]);
      ok = copy && yyjson_mut_obj_add_val(d, root, names[i], copy);
    }
  for (size_t i = 0; i < 3 && ok; i++)
    ok = yyjson_mut_obj_add_bool(d, root, flags[i], features[i]);
  if (ok)
    out->data = (uint8_t *)yyjson_mut_write(d, 0, &out->len);
  yyjson_mut_doc_free(d);
  return out->data ? CTX_OK : CTX_NOMEM;
}
static ctx_status routes(yyjson_val *v, ctx_buffer *out) {
  if (!CTX_FIELDS(v, "hosts", "guests", "bridges", "requirements"))
    return CTX_INVALID;
  yyjson_val *hosts = get(v, "hosts"), *guests = get(v, "guests"),
             *bridges = get(v, "bridges"), *want = get(v, "requirements");
  if (!list(hosts, 1, 64) || !list(guests, 1, 64) ||
      (!absent(bridges) && !list(bridges, 0, 64)))
    return CTX_INVALID;
  if (!absent(want) &&
      (!CTX_FIELDS(want, "concurrent", "nativeStreaming", "nativeCallbacks") ||
       !optional_bool(get(want, "concurrent")) ||
       !optional_bool(get(want, "nativeStreaming")) ||
       !optional_bool(get(want, "nativeCallbacks"))))
    return CTX_INVALID;
  size_t i, n, j, m, k, z, q, count;
  yyjson_val *h, *g, *b, *p;
  yyjson_arr_foreach(hosts, i, n,
                     h) if (!profile(h) ||
                            !unique_name(hosts, i, h)) return CTX_INVALID;
  yyjson_arr_foreach(guests, i, n,
                     g) if (!profile(g) ||
                            !unique_name(guests, i, g)) return CTX_INVALID;
  yyjson_arr_foreach(bridges, i, n,
                     b) if (!CTX_FIELDS(b, "name", "frontend", "backend") ||
                            !ctx_text(get(b, "name"), 0, 0) ||
                            !unique_name(bridges, i, b) ||
                            !profile(get(b, "frontend")) ||
                            !profile(get(b, "backend"))) return CTX_INVALID;
  yyjson_arr_foreach(hosts, i, n, h) {
    yyjson_arr_foreach(guests, j, m, g) {
      if (!ctx_same(get(h, "name"), get(g, "name")))
        continue;
      p = ctx_negotiate(get(h, "protocols"), get(g, "protocols"));
      if (!p)
        continue;
      ctx_status s = route_result(h, g, NULL, p, want, out);
      if (s != CTX_UNSUPPORTED)
        return s;
    }
  }
  yyjson_arr_foreach(hosts, i, n, h) {
    yyjson_arr_foreach(guests, j, m, g) {
      yyjson_arr_foreach(bridges, k, z, b) {
        yyjson_val *front = get(b, "frontend"), *back = get(b, "backend");
        if (!ctx_same(get(h, "name"), get(front, "name")) ||
            !ctx_same(get(g, "name"), get(back, "name")))
          continue;
        yyjson_arr_foreach(get(h, "protocols"), q, count, p) {
          if (!contains(get(g, "protocols"), p) ||
              !contains(get(front, "protocols"), p) ||
              !contains(get(back, "protocols"), p))
            continue;
          ctx_status s = route_result(h, g, b, p, want, out);
          if (s != CTX_UNSUPPORTED)
            return s;
        }
      }
    }
  }
  return CTX_UNSUPPORTED;
}

static ctx_status dispatch(const char *op, yyjson_val *v, ctx_buffer *out) {
  yyjson_val *result = NULL;
  if (!strncmp(op, "package.", 8))
    return ctx_package_service(op, v, out);
  if (!strcmp(op, "route.resolve"))
    return routes(v, out);
  if (!strcmp(op, "descriptor.validate")) {
    if (!ctx_descriptor(v))
      return CTX_INVALID;
    result = v;
  } else if (!strcmp(op, "descriptor.match")) {
    if (!CTX_FIELDS(v, "selected", "actual") ||
        !ctx_descriptor(get(v, "selected")) ||
        !ctx_descriptor(get(v, "actual")))
      return CTX_INVALID;
    if (!ctx_match(get(v, "selected"), get(v, "actual")))
      return CTX_MISMATCH;
  } else if (!strcmp(op, "descriptor.select")) {
    if (!CTX_FIELDS(v, "candidates", "requirement"))
      return CTX_INVALID;
    ctx_status s =
        select_descriptor(get(v, "candidates"), get(v, "requirement"), &result);
    if (s)
      return s;
  } else if (!strcmp(op, "descriptor.requirements")) {
    if (!CTX_FIELDS(v, "descriptor", "requirements") ||
        !ctx_descriptor(get(v, "descriptor")) ||
        !yyjson_is_arr(get(v, "requirements")))
      return CTX_INVALID;
    size_t i, n;
    yyjson_val *r;
    yyjson_arr_foreach(get(v, "requirements"), i, n, r) {
      if (!requirement(r))
        return CTX_INVALID;
      if (!satisfies(get(v, "descriptor"), r))
        return CTX_NOT_FOUND;
    }
  } else if (!strcmp(op, "protocol.negotiate")) {
    if (!CTX_FIELDS(v, "preferred", "offered") ||
        !ctx_protocols(get(v, "preferred")) ||
        !ctx_protocols(get(v, "offered")))
      return CTX_INVALID;
    result = ctx_negotiate(get(v, "preferred"), get(v, "offered"));
    if (!result)
      return CTX_UNSUPPORTED;
  } else if (!strcmp(op, "request.validate")) {
    if (!CTX_FIELDS(v, "descriptor", "request") ||
        !ctx_descriptor(get(v, "descriptor")))
      return CTX_INVALID;
    int64_t deadline;
    ctx_status s =
        ctx_request(get(v, "descriptor"), get(v, "request"), &deadline);
    if (s)
      return s;
    result = get(v, "request");
  } else if (!strcmp(op, "response.validate")) {
    if (!CTX_FIELDS(v, "response", "requestID") ||
        !ctx_text(get(v, "requestID"), 1, 0))
      return CTX_INVALID;
    ctx_status s =
        ctx_response(get(v, "response"), yyjson_get_str(get(v, "requestID")));
    if (s)
      return s;
    result = get(v, "response");
  } else if (!strcmp(op, "schema.validate")) {
    unsigned budget = 1024;
    if (!ctx_schema_valid(v, 0, &budget))
      return CTX_INVALID;
    result = v;
  } else if (!strcmp(op, "schema.check")) {
    if (!CTX_FIELDS(v, "schema", "value") || !get(v, "value"))
      return CTX_INVALID;
    unsigned budget = 1024;
    if (!ctx_schema_valid(get(v, "schema"), 0, &budget) ||
        !ctx_schema_check(get(v, "schema"), get(v, "value")))
      return CTX_INVALID;
  } else
    return CTX_UNSUPPORTED;
  return output(result, out);
}
ctx_status ctx_engine_call(const char *operation, const uint8_t *input,
                           size_t len, ctx_buffer *out) {
  if (!out)
    return CTX_INVALID;
  *out = (ctx_buffer){0};
  if (!operation)
    return CTX_INVALID;
  yyjson_doc *doc = ctx_parse(input, len);
  if (!doc)
    return CTX_INVALID;
  ctx_status s = dispatch(operation, yyjson_doc_get_root(doc), out);
  yyjson_doc_free(doc);
  if (s)
    ctx_buffer_free(out);
  return s;
}
