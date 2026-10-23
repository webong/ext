#include "endpoint.h"
#include <string.h>

typedef struct {
  const char *scheme, *address, *path, *token;
  size_t scheme_len, address_len, path_len, token_len;
  int64_t expires_seconds;
  int32_t expires_nanoseconds;
} endpoint_value;

/* A field Go decodes into a string: absent and null are empty, anything else
 * that is not a string is a decoding error. */
static int string_field(yyjson_val *object, const char *key, const char **s,
                        size_t *n) {
  yyjson_val *v = yyjson_obj_get(object, key);
  *s = "";
  *n = 0;
  if (!v || yyjson_is_null(v))
    return 1;
  if (!yyjson_is_str(v))
    return 0;
  *s = yyjson_get_str(v);
  *n = yyjson_get_len(v);
  return 1;
}

/* Decode: the strict reading of an Endpoint. Unknown and duplicate fields are
 * refused (the parser refuses duplicates), a field of the wrong type is, and so
 * is a time that is not RFC 3339. A time left out is Go's zero time. null decodes
 * to the empty endpoint, which Check then refuses. */
static int decode(yyjson_val *v, endpoint_value *e) {
  *e = (endpoint_value){0};
  e->expires_seconds = -62135596800LL; /* the zero time */
  if (yyjson_is_null(v))
    return 1;
  if (!EXT_FIELDS(v, "scheme", "address", "path", "token", "oneTime",
                  "expiresAt") ||
      !string_field(v, "scheme", &e->scheme, &e->scheme_len) ||
      !string_field(v, "address", &e->address, &e->address_len) ||
      !string_field(v, "path", &e->path, &e->path_len) ||
      !string_field(v, "token", &e->token, &e->token_len))
    return 0;
  yyjson_val *one_time = yyjson_obj_get(v, "oneTime"),
             *expires = yyjson_obj_get(v, "expiresAt");
  if (one_time && !yyjson_is_null(one_time) && !yyjson_is_bool(one_time))
    return 0;
  if (expires && !yyjson_is_null(expires) &&
      !ext_instant(expires, &e->expires_seconds, &e->expires_nanoseconds))
    return 0;
  return 1;
}

static int same(const char *s, size_t n, const char *word) {
  return n == strlen(word) && !memcmp(s, word, n);
}
static int known_scheme(const char *s, size_t n) {
  return same(s, n, "ws") || same(s, n, "wss") || same(s, n, "http") ||
         same(s, n, "https") || same(s, n, "tcp");
}
/* A byte at or below a space, or a delete: Go's hasControlOrSpace. */
static int control_or_space(const char *s, size_t n) {
  for (size_t i = 0; i < n; i++)
    if ((unsigned char)s[i] <= ' ' || s[i] == 0x7f)
      return 1;
  return 0;
}

/* ---- the address ---- */

/* An IPv4 address as four dotted decimal fields without leading zeros
 * (netip.parseIPv4Fields). */
static int ip4_fields(const char *s, size_t n, uint8_t out[4]) {
  int value = 0, position = 0, digits = 0;
  for (size_t i = 0; i < n; i++) {
    if (s[i] >= '0' && s[i] <= '9') {
      if (digits == 1 && value == 0)
        return 0; /* a leading zero */
      value = value * 10 + (s[i] - '0');
      digits++;
      if (value > 255)
        return 0;
    } else if (s[i] == '.') {
      if (i == 0 || i == n - 1 || s[i - 1] == '.' || position == 3)
        return 0;
      out[position++] = (uint8_t)value;
      value = 0;
      digits = 0;
    } else {
      return 0;
    }
  }
  if (position < 3)
    return 0;
  out[3] = (uint8_t)value;
  return 1;
}
/* An IPv6 address without a zone (netip.parseIPv6): groups of one to four hex
 * digits, at most one "::" standing for at least one group of zeros, and an
 * embedded IPv4 address in the last two groups. */
static int ip6_parse(const char *s, size_t n, uint8_t ip[16]) {
  memset(ip, 0, 16);
  int ellipsis = -1;
  if (n >= 2 && s[0] == ':' && s[1] == ':') {
    ellipsis = 0;
    s += 2;
    n -= 2;
    if (!n)
      return 1; /* "::" alone */
  }
  int i = 0;
  while (i < 16) {
    size_t off = 0;
    uint32_t value = 0;
    for (; off < n; off++) {
      char c = s[off];
      int digit;
      if (c >= '0' && c <= '9')
        digit = c - '0';
      else if (c >= 'a' && c <= 'f')
        digit = c - 'a' + 10;
      else if (c >= 'A' && c <= 'F')
        digit = c - 'A' + 10;
      else
        break;
      value = (value << 4) + (uint32_t)digit;
      if (off > 3 || value > 0xffff)
        return 0; /* more than four digits */
    }
    if (!off)
      return 0;
    if (off < n && s[off] == '.') {
      /* The tail of the address is an IPv4 address, from the start of this field. */
      if ((ellipsis < 0 && i != 12) || i + 4 > 16 || !ip4_fields(s, n, ip + i))
        return 0;
      s += n;
      n = 0;
      i += 4;
      break;
    }
    ip[i] = (uint8_t)(value >> 8);
    ip[i + 1] = (uint8_t)value;
    i += 2;
    s += off;
    n -= off;
    if (!n)
      break;
    if (s[0] != ':' || n == 1)
      return 0;
    s++;
    n--;
    if (s[0] == ':') {
      if (ellipsis >= 0)
        return 0;
      ellipsis = i;
      s++;
      n--;
      if (!n)
        break;
    }
  }
  if (n)
    return 0;
  if (i < 16) {
    if (ellipsis < 0)
      return 0;
    int gap = 16 - i;
    for (int j = i - 1; j >= ellipsis; j--)
      ip[j + gap] = ip[j];
    memset(ip + ellipsis, 0, (size_t)gap);
  } else if (ellipsis >= 0) {
    return 0; /* the "::" must stand for something */
  }
  return 1;
}
/* net.ParseIP: a dotted IPv4 or a colon IPv6 address, no zone. */
static int ip_parse(const char *s, size_t n, uint8_t ip[16]) {
  for (size_t i = 0; i < n; i++) {
    if (s[i] == '.') {
      uint8_t v4[4];
      if (!ip4_fields(s, n, v4))
        return 0;
      memset(ip, 0, 10);
      ip[10] = ip[11] = 0xff; /* IPv4 in IPv6 form, as As16 gives it */
      memcpy(ip + 12, v4, 4);
      return 1;
    }
    if (s[i] == ':')
      return ip6_parse(s, n, ip);
    if (s[i] == '%')
      return 0;
  }
  return 0;
}
/* IP.IsLoopback: 127.0.0.0/8, including IPv4 in IPv6 form, and ::1. */
static int loopback(const uint8_t ip[16]) {
  static const uint8_t prefix[12] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff};
  if (!memcmp(ip, prefix, 12))
    return ip[12] == 127;
  static const uint8_t one[16] = {0, 0, 0, 0, 0, 0, 0, 0,
                                  0, 0, 0, 0, 0, 0, 0, 1};
  return !memcmp(ip, one, 16);
}
/* net.SplitHostPort. host and port point into address. */
static int split_host_port(const char *a, size_t n, const char **host,
                           size_t *host_len, const char **port,
                           size_t *port_len) {
  size_t colon = n, bracket = 0, after = 0;
  for (size_t k = n; k-- > 0;)
    if (a[k] == ':') {
      colon = k;
      break;
    }
  if (colon == n)
    return 0; /* no port */
  if (a[0] == '[') {
    size_t end = n;
    for (size_t k = 0; k < n; k++)
      if (a[k] == ']') {
        end = k;
        break;
      }
    if (end == n || end + 1 != colon)
      return 0; /* no closing bracket, or something between it and the port */
    *host = a + 1;
    *host_len = end - 1;
    bracket = 1;
    after = end + 1;
  } else {
    *host = a;
    *host_len = colon;
    if (memchr(a, ':', colon))
      return 0; /* too many colons */
  }
  if (memchr(a + bracket, '[', n - bracket) || memchr(a + after, ']', n - after))
    return 0;
  *port = a + colon + 1;
  *port_len = n - colon - 1;
  return 1;
}

/* validAddress: a host and a port, the host being "localhost" or a loopback IP
 * literal. */
static ext_status address_ok(const char *a, size_t n) {
  if (!n || n > EXT_ENDPOINT_MAX_ADDRESS || control_or_space(a, n))
    return EXT_INVALID;
  const char *host, *port;
  size_t host_len, port_len;
  if (!split_host_port(a, n, &host, &host_len, &port, &port_len))
    return EXT_INVALID;
  /* The port is plain decimal, from 1 to 65535, written the way a number is. */
  if (!port_len || port_len > 5 || port[0] == '0')
    return EXT_INVALID;
  uint32_t number = 0;
  for (size_t i = 0; i < port_len; i++) {
    if (port[i] < '0' || port[i] > '9')
      return EXT_INVALID;
    number = number * 10 + (uint32_t)(port[i] - '0');
  }
  if (number > 65535)
    return EXT_INVALID;
  if (same(host, host_len, "localhost"))
    return EXT_OK;
  uint8_t ip[16];
  if (!ip_parse(host, host_len, ip) || !loopback(ip))
    return EXT_DENIED;
  return EXT_OK;
}

/* Check, in Go's order: the first rule an endpoint breaks decides the status. */
static ext_status check(const endpoint_value *e) {
  if (!known_scheme(e->scheme, e->scheme_len))
    return EXT_INVALID;
  ext_status s = address_ok(e->address, e->address_len);
  if (s)
    return s;
  if (e->path_len > EXT_ENDPOINT_MAX_PATH)
    return EXT_INVALID;
  if (e->path_len &&
      (e->path[0] != '/' || memchr(e->path, '?', e->path_len) ||
       memchr(e->path, '#', e->path_len) || memchr(e->path, '\\', e->path_len) ||
       control_or_space(e->path, e->path_len)))
    return EXT_INVALID;
  if (e->token_len > EXT_ENDPOINT_MAX_TOKEN ||
      control_or_space(e->token, e->token_len))
    return EXT_INVALID;
  if (ext_instant_is_zero(e->expires_seconds, e->expires_nanoseconds))
    return EXT_INVALID; /* an endpoint needs an expiry */
  return EXT_OK;
}

ext_status ext_endpoint_check(yyjson_val *endpoint) {
  endpoint_value e;
  if (!decode(endpoint, &e))
    return EXT_INVALID;
  return check(&e);
}

int ext_endpoint_policy_parse(yyjson_val *object, ext_endpoint_policy *policy) {
  *policy = (ext_endpoint_policy){0};
  if (!EXT_FIELDS(object, "schemes", "maxLifetimeMilliseconds", "now"))
    return 0;
  yyjson_val *schemes = yyjson_obj_get(object, "schemes"),
             *lifetime = yyjson_obj_get(object, "maxLifetimeMilliseconds"),
             *now = yyjson_obj_get(object, "now");
  if (schemes && !yyjson_is_null(schemes)) {
    size_t i, n;
    yyjson_val *scheme;
    if (!yyjson_is_arr(schemes))
      return 0;
    yyjson_arr_foreach(schemes, i, n, scheme) if (!yyjson_is_str(scheme))
        return 0;
    policy->schemes = schemes;
  }
  if (lifetime && !yyjson_is_null(lifetime) &&
      !ext_int64(lifetime, &policy->lifetime_ms))
    return 0;
  if (now && !yyjson_is_null(now)) {
    if (!ext_instant(now, &policy->now_seconds, &policy->now_nanoseconds))
      return 0;
    policy->has_now = 1;
  }
  return 1;
}

/* Whether (a_seconds, a_nanoseconds) is later than (b_seconds, b_nanoseconds). */
static int later(int64_t a_seconds, int32_t a_nanoseconds, int64_t b_seconds,
                 int32_t b_nanoseconds) {
  return a_seconds > b_seconds ||
         (a_seconds == b_seconds && a_nanoseconds > b_nanoseconds);
}

ext_status ext_endpoint_validate(yyjson_val *endpoint,
                                 const ext_endpoint_policy *policy) {
  endpoint_value e;
  if (!decode(endpoint, &e))
    return EXT_INVALID;
  /* Validate: a policy that admits nothing denies, one that names a scheme there
   * is none of is invalid whatever the endpoint is, and only then does the
   * endpoint's own shape matter. */
  if (!policy->schemes || !yyjson_arr_size(policy->schemes))
    return EXT_DENIED;
  int allowed = 0;
  size_t i, n;
  yyjson_val *scheme;
  yyjson_arr_foreach(policy->schemes, i, n, scheme) {
    if (!known_scheme(yyjson_get_str(scheme), yyjson_get_len(scheme)))
      return EXT_INVALID;
    if (yyjson_get_len(scheme) == e.scheme_len &&
        !memcmp(yyjson_get_str(scheme), e.scheme, e.scheme_len))
      allowed = 1;
  }
  ext_status s = check(&e);
  if (s)
    return s;
  if (!allowed)
    return EXT_DENIED;
  int64_t now_seconds = policy->now_seconds;
  int32_t now_nanoseconds = policy->now_nanoseconds;
  if (!policy->has_now)
    ext_wall_clock(&now_seconds, &now_nanoseconds);
  if (!later(e.expires_seconds, e.expires_nanoseconds, now_seconds,
             now_nanoseconds))
    return EXT_EXPIRED;
  int64_t lifetime = policy->lifetime_ms > 0 ? policy->lifetime_ms
                                              : EXT_ENDPOINT_DEFAULT_LIFETIME_MS;
  int64_t limit_seconds = now_seconds + lifetime / 1000;
  int32_t limit_nanoseconds = now_nanoseconds + (int32_t)(lifetime % 1000) * 1000000;
  if (limit_nanoseconds >= 1000000000) {
    limit_seconds++;
    limit_nanoseconds -= 1000000000;
  }
  if (later(e.expires_seconds, e.expires_nanoseconds, limit_seconds,
            limit_nanoseconds))
    return EXT_INVALID; /* it outlives the lifetime the host admits */
  return EXT_OK;
}
