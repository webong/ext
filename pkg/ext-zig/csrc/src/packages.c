#define _POSIX_C_SOURCE 200809L
#include "services.h"
#include "sha256.h"
#include "unicode_lower.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define get yyjson_obj_get
#define absent(v) (!(v) || yyjson_is_null(v))
static int array(yyjson_val *v, size_t max) {
  return absent(v) || (yyjson_is_arr(v) && yyjson_arr_size(v) <= max);
}
static int string(yyjson_val *v, size_t min, size_t max) {
  return yyjson_is_str(v) && yyjson_get_len(v) >= min &&
         yyjson_get_len(v) <= max;
}
static int schema(yyjson_val *s) {
  unsigned budget = 1024;
  return absent(s) || ext_schema_valid(s, 0, &budget);
}
static yyjson_val *named(yyjson_val *a, yyjson_val *name) {
  size_t i, n;
  yyjson_val *v;
  yyjson_arr_foreach(a, i, n, v) if (ext_same(get(v, "name"), name)) return v;
  return NULL;
}
static int digest(yyjson_val *v) {
  if (!string(v, 64, 64))
    return 0;
  const char *s = yyjson_get_str(v);
  for (size_t i = 0; i < 64; i++)
    if (!((s[i] >= 'a' && s[i] <= 'f') || (s[i] >= '0' && s[i] <= '9')))
      return 0;
  return 1;
}
/* Whether s (n bytes, valid UTF-8) is a portable relative path: slash separated,
 * no empty, "." or ".." components, no backslash, colon, NUL or line break, no
 * component ending in "." or space, no Windows device names, at most 1024
 * bytes. Matches packagekit. */
int ext_portable_path(const char *s, size_t n) {
  if (!n || n > 1024)
    return 0;
  size_t start = 0;
  for (size_t i = 0; i <= n; i++) {
    if (i < n &&
        (s[i] == '\\' || s[i] == ':' || !s[i] || s[i] == '\r' || s[i] == '\n'))
      return 0;
    if (i < n && s[i] != '/')
      continue;
    size_t len = i - start;
    if (!len || s[i - 1] == '.' || s[i - 1] == ' ')
      return 0;
    char device[5] = {0};
    size_t count = 0;
    for (size_t j = start; j < i && s[j] != '.'; j++) {
      if (count == 4) {
        count = 5;
        break;
      }
      char c = s[j];
      device[count++] = c >= 'A' && c <= 'Z' ? c + 'a' - 'A' : c;
    }
    if (count <= 4 &&
        (!strcmp(device, "con") || !strcmp(device, "prn") ||
         !strcmp(device, "aux") || !strcmp(device, "nul") ||
         (count == 4 &&
          (!memcmp(device, "com", 3) || !memcmp(device, "lpt", 3)) &&
          device[3] >= '1' && device[3] <= '9')))
      return 0;
    start = i + 1;
  }
  return 1;
}
static int portable(yyjson_val *v) {
  return string(v, 1, 1024) &&
         ext_portable_path(yyjson_get_str(v), yyjson_get_len(v));
}
/* Inputs have already passed strict UTF-8 validation. Match Go's simple
 * Unicode lowercase mapping, rather than locale-dependent tolower/casefold. */
static uint32_t lower_rune(const uint8_t **p) {
  uint32_t c = *(*p)++;
  unsigned extra = 0;
  if (c >= 0xf0) {
    c &= 7;
    extra = 3;
  } else if (c >= 0xe0) {
    c &= 15;
    extra = 2;
  } else if (c >= 0xc0) {
    c &= 31;
    extra = 1;
  }
  while (extra--)
    c = (c << 6) | (*(*p)++ & 63);
  size_t lo = 0, hi = sizeof(ext_lower) / sizeof(ext_lower[0]);
  while (lo < hi) {
    size_t m = lo + (hi - lo) / 2;
    if (ext_lower[m][0] < c)
      lo = m + 1;
    else
      hi = m;
  }
  if (lo < sizeof(ext_lower) / sizeof(ext_lower[0]) && ext_lower[lo][0] == c)
    return ext_lower[lo][1];
  return c;
}
/* The store uses the same Unicode simple-lower comparison as manifest
 * duplicate and overlap checks. Invalid filesystem names never match a
 * declared artifact and are therefore rejected as unlisted content. */
static int valid_utf8(const uint8_t *p) {
  while (*p) {
    uint32_t c = *p++;
    if (c < 0x80)
      continue;
    unsigned more;
    uint32_t min;
    if (c >= 0xc2 && c <= 0xdf) { more = 1; min = 0x80; c &= 0x1f; }
    else if (c >= 0xe0 && c <= 0xef) { more = 2; min = 0x800; c &= 0x0f; }
    else if (c >= 0xf0 && c <= 0xf4) { more = 3; min = 0x10000; c &= 0x07; }
    else return 0;
    while (more--) {
      if (!*p || (*p & 0xc0) != 0x80)
        return 0;
      c = (c << 6) | (*p++ & 0x3f);
    }
    if (c < min || c > 0x10ffff || (c >= 0xd800 && c <= 0xdfff))
      return 0;
  }
  return 1;
}
int ext_path_fold_equal(const char *a, const char *b) {
  if (!a || !b || !valid_utf8((const uint8_t *)a) ||
      !valid_utf8((const uint8_t *)b))
    return 0;
  const uint8_t *x = (const uint8_t *)a, *y = (const uint8_t *)b;
  while (*x && *y)
    if (lower_rune(&x) != lower_rune(&y))
      return 0;
  return !*x && !*y;
}
char *ext_path_fold_key(const char *path) {
  if (!path || !valid_utf8((const uint8_t *)path))
    return NULL;
  size_t n = strlen(path);
  if (n > (SIZE_MAX - 1) / 4)
    return NULL;
  uint8_t *key = malloc(n * 4 + 1), *out = key;
  if (!key)
    return NULL;
  const uint8_t *p = (const uint8_t *)path;
  while (*p) {
    uint32_t c = lower_rune(&p);
    if (c < 0x80)
      *out++ = (uint8_t)c;
    else if (c < 0x800) {
      *out++ = (uint8_t)(0xc0 | (c >> 6));
      *out++ = (uint8_t)(0x80 | (c & 63));
    } else if (c < 0x10000) {
      *out++ = (uint8_t)(0xe0 | (c >> 12));
      *out++ = (uint8_t)(0x80 | ((c >> 6) & 63));
      *out++ = (uint8_t)(0x80 | (c & 63));
    } else {
      *out++ = (uint8_t)(0xf0 | (c >> 18));
      *out++ = (uint8_t)(0x80 | ((c >> 12) & 63));
      *out++ = (uint8_t)(0x80 | ((c >> 6) & 63));
      *out++ = (uint8_t)(0x80 | (c & 63));
    }
  }
  *out = 0;
  return (char *)key;
}
int ext_path_fold_tree_contains(const char *tree, const char *path) {
  if (!tree || !path || !valid_utf8((const uint8_t *)tree) ||
      !valid_utf8((const uint8_t *)path))
    return 0;
  const uint8_t *x = (const uint8_t *)tree, *y = (const uint8_t *)path;
  while (*x && *y)
    if (lower_rune(&x) != lower_rune(&y))
      return 0;
  return !*x && *y == '/';
}
/* Optional SPDX license expression: 1 to 128 characters, starting with a
 * letter, digit or "(", using letters, digits and ". + ( ) -" and single interior
 * spaces. It is recorded and never interpreted. Matches packagekit. */
static int license_valid(yyjson_val *v) {
  if (absent(v))
    return 1;
  if (!yyjson_is_str(v))
    return 0;
  size_t n = yyjson_get_len(v);
  const unsigned char *s = (const unsigned char *)yyjson_get_str(v);
  if (!n)
    return 1;
  if (n > 128 || s[n - 1] == ' ')
    return 0;
  for (size_t i = 0; i < n; i++) {
    int alnum = (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') ||
                (s[i] >= '0' && s[i] <= '9');
    int other = s[i] == '.' || s[i] == '+' || s[i] == '(' || s[i] == ')' ||
                s[i] == '-' || s[i] == ' ';
    if (!(alnum || (i && other) || (!i && s[i] == '(')) ||
        (s[i] == ' ' && i && s[i - 1] == ' '))
      return 0;
  }
  return 1;
}
/* A metadata key is namespaced and versioned, such as "kvlite.module/v1":
 * [a-z0-9][a-z0-9._-]{0,127}/v[1-9][0-9]{0,8}. */
static int metadata_key(yyjson_val *k) {
  const unsigned char *s = (const unsigned char *)yyjson_get_str(k);
  size_t n = yyjson_get_len(k), i = 0;
  if (!n || !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= '0' && s[0] <= '9')))
    return 0;
  while (i < n && s[i] != '/') {
    int ok = (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= '0' && s[i] <= '9') ||
             s[i] == '.' || s[i] == '_' || s[i] == '-';
    if (!ok || i >= 128)
      return 0;
    i++;
  }
  if (i == n || n - i < 3 || s[i + 1] != 'v' || s[i + 2] < '1' || s[i + 2] > '9')
    return 0;
  size_t digits = n - i - 2;
  if (digits > 9)
    return 0;
  for (size_t j = i + 3; j < n; j++)
    if (s[j] < '0' || s[j] > '9')
      return 0;
  return 1;
}
/* Size of a metadata value: the byte length of every string (keys included),
 * 8 for each number, boolean and null, and 2 for each object and array. It does
 * not depend on source whitespace, escapes or number spelling, so every engine
 * agrees on it. Nesting is already bounded by the parser. */
static size_t metadata_weight(yyjson_val *v) {
  size_t i, n, total;
  yyjson_val *key, *child;
  if (yyjson_is_str(v))
    return yyjson_get_len(v);
  if (yyjson_is_obj(v)) {
    total = 2;
    yyjson_obj_foreach(v, i, n, key, child)
        total += yyjson_get_len(key) + metadata_weight(child);
    return total;
  }
  if (yyjson_is_arr(v)) {
    total = 2;
    yyjson_arr_foreach(v, i, n, child) total += metadata_weight(child);
    return total;
  }
  return 8;
}
/* Optional consumer-defined data: at most 64 keys, each value a JSON object of
 * weight at most 64 KiB, and at most 256 KiB in all with the keys. ext checks
 * the shape and bounds only and never interprets a value. */
static int metadata_valid(yyjson_val *v) {
  if (absent(v))
    return 1;
  if (!yyjson_is_obj(v) || yyjson_obj_size(v) > 64)
    return 0;
  size_t i, n, total = 0;
  yyjson_val *key, *value;
  yyjson_obj_foreach(v, i, n, key, value) {
    if (!metadata_key(key) || !yyjson_is_obj(value))
      return 0;
    size_t weight = metadata_weight(value);
    total += yyjson_get_len(key) + weight;
    if (weight > (64u << 10) || total > (256u << 10))
      return 0;
  }
  return 1;
}
static int tree_artifact(yyjson_val *a) {
  yyjson_val *k = get(a, "kind");
  return yyjson_is_str(k) && yyjson_equals_str(k, "tree");
}
/* Optional empty provider IDs have the same meaning as an omitted selector. */
static yyjson_val *provider_id(yyjson_val *r) {
  yyjson_val *v = get(r, "provider");
  return yyjson_equals_str(v, "") ? NULL : v;
}
static int dependency(yyjson_val *r) {
  return EXT_FIELDS(r, "contract", "operation", "identity", "provider", "os", "arch") &&
         EXT_FIELDS(get(r, "contract"), "name", "version") &&
         ext_ref(get(r, "contract")) && ext_text(get(r, "operation"), 0, 0) &&
         ext_text(get(r, "os"), 0, 1) && ext_text(get(r, "arch"), 0, 1) &&
         (absent(get(r, "identity")) || ext_identity(get(r, "identity"))) &&
         (absent(get(r, "provider")) || ext_text(get(r, "provider"), 0, 1)) &&
         (absent(provider_id(r)) || absent(get(r, "identity")));
}
static int same_optional_text(yyjson_val *a, yyjson_val *b) {
  return (!yyjson_get_len(a) && !yyjson_get_len(b)) || ext_same(a, b);
}
static int same_dependency(yyjson_val *a, yyjson_val *b) {
  yyjson_val *ai = get(a, "identity"), *bi = get(b, "identity");
  return same_optional_text(get(a, "os"), get(b, "os")) &&
         same_optional_text(get(a, "arch"), get(b, "arch")) &&
         ext_same_ref(get(a, "contract"), get(b, "contract")) &&
         ext_same(get(a, "operation"), get(b, "operation")) &&
         ((absent(provider_id(a)) && absent(provider_id(b))) ||
          ext_same(provider_id(a), provider_id(b))) &&
         ((absent(ai) && absent(bi)) ||
          (!absent(ai) && !absent(bi) && ext_same_identity(ai, bi)));
}
/* One artifact for the duplicate and overlap checks. key is its path folded to
 * lower case, as Go's strings.ToLower does for the same comparison, with "/"
 * replaced by NUL: a path cannot hold NUL, so comparing keys as bytes sorts a
 * directory immediately before everything below it. */
typedef struct {
  yyjson_val *v;
  uint8_t *key;
  size_t len;
  int tree;
} artifact;
static size_t rune_size(uint32_t c) {
  return c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
}
static uint8_t *put_rune(uint8_t *out, uint32_t c) {
  if (c < 0x80)
    *out++ = (uint8_t)c;
  else if (c < 0x800) {
    *out++ = (uint8_t)(0xc0 | (c >> 6));
    *out++ = (uint8_t)(0x80 | (c & 63));
  } else if (c < 0x10000) {
    *out++ = (uint8_t)(0xe0 | (c >> 12));
    *out++ = (uint8_t)(0x80 | ((c >> 6) & 63));
    *out++ = (uint8_t)(0x80 | (c & 63));
  } else {
    *out++ = (uint8_t)(0xf0 | (c >> 18));
    *out++ = (uint8_t)(0x80 | ((c >> 12) & 63));
    *out++ = (uint8_t)(0x80 | ((c >> 6) & 63));
    *out++ = (uint8_t)(0x80 | (c & 63));
  }
  return out;
}
static int fold_path(yyjson_val *path, artifact *out) {
  const uint8_t *p = (const uint8_t *)yyjson_get_str(path),
                *end = p + yyjson_get_len(path), *q = p;
  size_t size = 0;
  while (q < end)
    size += rune_size(lower_rune(&q));
  out->key = malloc(size ? size : 1);
  if (!out->key)
    return 0;
  uint8_t *o = out->key;
  for (q = p; q < end;) {
    uint32_t c = lower_rune(&q);
    if (c == '/')
      *o++ = 0;
    else
      o = put_rune(o, c);
  }
  out->len = size;
  return 1;
}
static int key_order(const void *a, const void *b) {
  const artifact *x = *(const artifact *const *)a,
                 *y = *(const artifact *const *)b;
  size_t n = x->len < y->len ? x->len : y->len;
  int c = memcmp(x->key, y->key, n);
  return c ? c : (x->len > y->len) - (x->len < y->len);
}
typedef struct {
  const char *s;
  size_t len;
} span;
static int span_order(const void *a, const void *b) {
  const span *x = a, *y = b;
  size_t n = x->len < y->len ? x->len : y->len;
  int c = memcmp(x->s, y->s, n);
  return c ? c : (x->len > y->len) - (x->len < y->len);
}
/* Whether parent is a directory above child: a proper prefix that ends at a
 * path separator. */
static int above(const artifact *parent, const artifact *child) {
  return parent->len < child->len &&
         !memcmp(parent->key, child->key, parent->len) &&
         child->key[parent->len] == 0;
}
/* No two artifacts share a name or, ignoring case, a path; a tree owns
 * everything below its directory, so no other artifact may share, contain or sit
 * inside that path. Sorting makes this O(n log n) in the artifact count:
 * comparing every pair of 4096 artifacts, each reached through
 * yyjson_arr_get, took minutes. */
static ext_plugin_status artifacts_distinct(yyjson_val *a) {
  size_t count = yyjson_arr_size(a), i, n;
  yyjson_val *v;
  ext_plugin_status status = EXT_PLUGIN_OK;
  artifact *items = calloc(count, sizeof(*items));
  artifact **order = malloc(count * sizeof(*order));
  artifact **open = malloc(count * sizeof(*open));
  span *names = malloc(count * sizeof(*names));
  if (!items || !order || !open || !names) {
    status = EXT_PLUGIN_NOMEM;
    goto done;
  }
  yyjson_arr_foreach(a, i, n, v) {
    items[i].v = v;
    items[i].tree = tree_artifact(v);
    if (!fold_path(get(v, "path"), &items[i])) {
      status = EXT_PLUGIN_NOMEM;
      goto done;
    }
    order[i] = &items[i];
    names[i].s = yyjson_get_str(get(v, "name"));
    names[i].len = yyjson_get_len(get(v, "name"));
  }
  qsort(names, count, sizeof(*names), span_order);
  for (i = 1; i < count; i++)
    if (!span_order(&names[i - 1], &names[i])) {
      status = EXT_PLUGIN_INVALID;
      goto done;
    }
  qsort(order, count, sizeof(*order), key_order);
  for (i = 1; i < count; i++)
    if (!key_order(&order[i - 1], &order[i])) {
      status = EXT_PLUGIN_INVALID;
      goto done;
    }
  /* In this order everything below a directory follows it directly, so the
   * artifacts that can still contain the next one are a chain, popped as soon
   * as the next path is outside them. */
  size_t depth = 0, trees = 0;
  for (i = 0; i < count; i++) {
    while (depth && !above(open[depth - 1], order[i])) {
      depth--;
      trees -= (size_t)open[depth]->tree;
    }
    if (depth && (order[i]->tree || trees)) {
      status = EXT_PLUGIN_INVALID;
      goto done;
    }
    open[depth++] = order[i];
    trees += (size_t)order[i]->tree;
  }
done:
  if (items)
    for (i = 0; i < count; i++)
      free(items[i].key);
  free(items);
  free(order);
  free(open);
  free(names);
  return status;
}
yyjson_mut_val *ext_manifest_descriptor(yyjson_mut_doc *d, yyjson_val *m) {
 yyjson_mut_val *v=yyjson_mut_obj(d);
 if (!v || !yyjson_mut_obj_add_str(d,v,"apiVersion","ext.io.pc/v1")) return NULL;
 const char *keys[]={"identity","contracts","reverse"};
 for(size_t i=0;i<3;i++) {yyjson_val *field=get(m,keys[i]);if(field && !yyjson_mut_obj_add_val(d,v,keys[i],yyjson_val_mut_copy(d,field))) return NULL;}
 return v;
}
ext_plugin_status ext_manifest_valid(yyjson_val *m) {
  if (!EXT_FIELDS(m, "apiVersion", "kind", "identity", "contracts", "reverse", "license", "artifacts",
                  "entrypoints", "requires", "configuration", "payloads",
                  "assets", "sharedDependencies", "metadata") ||
      !license_valid(get(m, "license")) || !metadata_valid(get(m, "metadata")) ||
      !yyjson_equals_str(get(m, "apiVersion"), "ext.io/v1") ||
      !yyjson_equals_str(get(m,"kind"),"package") || !ext_descriptor_fields(m))
    return EXT_PLUGIN_INVALID;
  yyjson_val *a = get(m, "artifacts"), *e = get(m, "entrypoints"),
             *r = get(m, "requires"), *p = get(m, "payloads"),
             *assets = get(m, "assets"), *shared = get(m, "sharedDependencies");
  if (!array(a, 4096) ||
      !array(e, 64) || !array(r, 256) || !array(p, 1024) ||
      !array(assets, 1024) || !array(shared, 256) ||
      !schema(get(m, "configuration")))
    return EXT_PLUGIN_INVALID;
  size_t i, n, j, k;
  yyjson_val *v, *w;
  yyjson_arr_foreach(a, i, n, v) {
    yyjson_val *kind = get(v, "kind");
    if (!EXT_FIELDS(v, "name", "path", "kind", "sha256", "os", "arch") ||
        (!absent(kind) && !(yyjson_is_str(kind) && (!yyjson_get_len(kind) ||
                                                    yyjson_equals_str(kind, "tree")))) ||
        !ext_text(get(v, "name"), 0, 0) || !portable(get(v, "path")) ||
        !digest(get(v, "sha256")) || !ext_text(get(v, "os"), 0, 1) ||
        !ext_text(get(v, "arch"), 0, 1))
      return EXT_PLUGIN_INVALID;
  }
  ext_plugin_status distinct = artifacts_distinct(a);
  if (distinct)
    return distinct;
  /* An entrypoint may name a tree except under the process runtime, which
   * starts a single executable file. */
  yyjson_arr_foreach(e, i, n, v) {
    yyjson_val *target = named(a, get(v, "artifact"));
    if (!EXT_FIELDS(v, "name", "runtime", "artifact", "protocols") ||
        !ext_text(get(v, "name"), 0, 0) || !ext_text(get(v, "runtime"), 0, 0) ||
        !target ||
        (tree_artifact(target) && yyjson_equals_str(get(v, "runtime"), "process")) ||
        !ext_protocols(get(v, "protocols")))
      return EXT_PLUGIN_INVALID;
    EXT_ARR_BEFORE(e, i, j, k, w)
    if (ext_same(get(v, "name"), get(w, "name"))) return EXT_PLUGIN_INVALID;
  }
  yyjson_arr_foreach(r, i, n, v) {
    if (!dependency(v))
      return EXT_PLUGIN_INVALID;
    EXT_ARR_BEFORE(r, i, j, k, w)
    if (same_dependency(v, w)) return EXT_PLUGIN_INVALID;
  }
  yyjson_arr_foreach(p, i, n, v) {
    if (!EXT_FIELDS(v, "contract", "operation", "input", "output") ||
        !EXT_FIELDS(get(v, "contract"), "name", "version") ||
        !ext_ref(get(v, "contract")) || !ext_text(get(v, "operation"), 0, 0) ||
        !schema(get(v, "input")) || !schema(get(v, "output")))
      return EXT_PLUGIN_INVALID;
    if (!ext_lookup(m, get(v, "contract"),
                    get(v, "operation")))
      return EXT_PLUGIN_UNSUPPORTED;
    EXT_ARR_BEFORE(p, i, j, k, w)
    if (ext_same_ref(get(v, "contract"), get(w, "contract")) &&
        ext_same(get(v, "operation"), get(w, "operation")))
      return EXT_PLUGIN_INVALID;
  }
  yyjson_arr_foreach(
      assets, i, n,
      v) if (!EXT_FIELDS(v, "artifact", "kind", "locale") ||
             !named(a, get(v, "artifact")) || !ext_text(get(v, "kind"), 0, 0) ||
             (!absent(get(v, "locale")) &&
              !string(get(v, "locale"), 0, 64))) return EXT_PLUGIN_INVALID;
  yyjson_arr_foreach(shared, i, n, v) {
    yyjson_val *versions = get(v, "versions");
    if (!EXT_FIELDS(v, "name", "versions") || !string(get(v, "name"), 1, 256) ||
        !yyjson_is_arr(versions) || !yyjson_arr_size(versions) ||
        !array(versions, 64))
      return EXT_PLUGIN_INVALID;
    EXT_ARR_BEFORE(shared, i, j, k, w)
    if (ext_same(get(v, "name"), get(w, "name"))) return EXT_PLUGIN_INVALID;
    yyjson_arr_foreach(versions, j, k, w) {
      if (!string(w, 1, 256))
        return EXT_PLUGIN_INVALID;
      size_t x, y;
      yyjson_val *earlier;
      EXT_ARR_BEFORE(versions, j, x, y, earlier)
      if (ext_same(w, earlier)) return EXT_PLUGIN_INVALID;
    }
  }
  return EXT_PLUGIN_OK;
}
static ext_plugin_status result(yyjson_mut_doc *doc, ext_plugin_buffer *out, int ok) {
  if (ok)
    out->data = (uint8_t *)yyjson_mut_write(doc, 0, &out->len);
  yyjson_mut_doc_free(doc);
  return out->data ? EXT_PLUGIN_OK : EXT_PLUGIN_NOMEM;
}
static int copy(yyjson_mut_doc *d, yyjson_mut_val *o, const char *key,
                yyjson_val *v) {
  yyjson_mut_val *c = yyjson_val_mut_copy(d, v);
  return c && yyjson_mut_obj_add_val(d, o, key, c);
}
static ext_plugin_status select_entry(yyjson_val *v, ext_plugin_buffer *out) {
  if (!EXT_FIELDS(v, "manifest", "name", "environment"))
    return EXT_PLUGIN_INVALID;
  yyjson_val *m = get(v, "manifest"), *env = get(v, "environment");
  ext_plugin_status s = ext_manifest_valid(m);
  if (s)
    return s;
  if (!string(get(v, "name"), 0, 256) ||
      !EXT_FIELDS(env, "os", "arch", "runtimes", "sharedVersions") ||
      (!absent(get(env, "runtimes")) && !yyjson_is_obj(get(env, "runtimes"))) ||
      (!absent(get(env, "sharedVersions")) &&
       !yyjson_is_obj(get(env, "sharedVersions"))))
    return EXT_PLUGIN_INVALID;
  size_t i, n;
  yyjson_val *dep;
  yyjson_arr_foreach(get(m, "sharedDependencies"), i, n, dep) {
    yyjson_val *version = yyjson_obj_getn(get(env, "sharedVersions"),
                                          yyjson_get_str(get(dep, "name")),
                                          yyjson_get_len(get(dep, "name")));
    int found = 0;
    size_t j, k;
    yyjson_val *candidate;
    yyjson_arr_foreach(get(dep, "versions"), j, k,
                       candidate) if (ext_same(version, candidate)) found = 1;
    if (!found)
      return EXT_PLUGIN_UNSUPPORTED;
  }
  yyjson_val *entry = named(get(m, "entrypoints"), get(v, "name"));
  if (!entry)
    return EXT_PLUGIN_NOT_FOUND;
  yyjson_val *runtime = get(entry, "runtime");
  yyjson_val *profile = yyjson_obj_getn(
      get(env, "runtimes"), yyjson_get_str(runtime), yyjson_get_len(runtime));
  if (!profile)
    return EXT_PLUGIN_UNSUPPORTED;
  if (!ext_protocols(get(profile, "protocols")))
    return EXT_PLUGIN_INVALID;
  yyjson_val *protocol =
      ext_negotiate(get(profile, "protocols"), get(entry, "protocols"));
  if (!protocol)
    return EXT_PLUGIN_UNSUPPORTED;
  yyjson_val *artifact = named(get(m, "artifacts"), get(entry, "artifact"));
  const char *fields[] = {"os", "arch"};
  for (i = 0; i < 2; i++) {
    yyjson_val *value = get(artifact, fields[i]);
    if (yyjson_get_len(value) && !ext_same(value, get(env, fields[i])))
      return EXT_PLUGIN_UNSUPPORTED;
  }
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  if (!d)
    return EXT_PLUGIN_NOMEM;
  yyjson_mut_val *o = yyjson_mut_obj(d);
  yyjson_mut_doc_set_root(d, o);
  return result(d, out,
                o && copy(d, o, "entrypoint", entry) &&
                    copy(d, o, "artifact", artifact) &&
                    copy(d, o, "protocol", protocol));
}
/* Identity characters are restricted ASCII and cannot require JSON escaping.
 * Build field order and omitempty exactly as the public Identity contract. */
static void identity_hash(yyjson_val *id, uint8_t out[32]) {
  char json[1024];
  const char *version = yyjson_get_str(get(id, "version"));
  int n =
      version && *version
          ? snprintf(json, sizeof(json),
                     "{\"id\":\"%s\",\"revision\":\"%s\",\"version\":\"%s\"}",
                     yyjson_get_str(get(id, "id")),
                     yyjson_get_str(get(id, "revision")), version)
          : snprintf(json, sizeof(json), "{\"id\":\"%s\",\"revision\":\"%s\"}",
                     yyjson_get_str(get(id, "id")),
                     yyjson_get_str(get(id, "revision")));
  ext_plugin_sha256((const uint8_t *)json, (size_t)n, out);
}
typedef struct {
  size_t index;
  uint8_t hash[32];
} sorted;
static int compare(const void *a, const void *b) {
  return memcmp(((const sorted *)a)->hash, ((const sorted *)b)->hash, 32);
}
typedef struct {
  yyjson_val *id;
  size_t count;
  size_t *deps;
  int state;
} node;
static ext_plugin_status visit(node *nodes, size_t i, yyjson_mut_doc *d,
                        yyjson_mut_val *order) {
  node *p = &nodes[i];
  if (p->state == 1)
    return EXT_PLUGIN_INVALID;
  if (p->state == 2)
    return EXT_PLUGIN_OK;
  p->state = 1;
  for (size_t j = 0; j < p->count; j++) {
    ext_plugin_status s = visit(nodes, p->deps[j], d, order);
    if (s)
      return s;
  }
  yyjson_mut_val *id = yyjson_val_mut_copy(d, p->id);
  if (!id || !yyjson_mut_arr_append(order, id))
    return EXT_PLUGIN_NOMEM;
  p->state = 2;
  return EXT_PLUGIN_OK;
}
/* Which manifests offer a contract operation. package.resolve used to scan
 * every manifest for every requirement and search each descriptor with
 * ext_lookup, which is far worse than quadratic on a plan of many manifests;
 * this index answers one requirement in expected constant time. */
typedef struct {
  uint64_t hash;
  size_t manifest;
  yyjson_val *contract, *operation;
} offer;
typedef struct {
  offer *offers;
  size_t count;
  size_t *slots; /* offer index + 1, 0 for empty; the size is a power of two */
  size_t mask;
} offers;
static uint64_t hash_text(uint64_t h, yyjson_val *v) {
  const char *s = yyjson_is_str(v) ? yyjson_get_str(v) : "";
  size_t n = yyjson_is_str(v) ? yyjson_get_len(v) : 0;
  for (size_t i = 0; i < n; i++)
    h = (h ^ (uint8_t)s[i]) * 1099511628211ull;
  return (h ^ 0xff) * 1099511628211ull;
}
static uint64_t offer_hash(yyjson_val *contract, yyjson_val *operation) {
  uint64_t h = 14695981039346656037ull;
  h = hash_text(h, get(contract, "name"));
  h = hash_text(h, get(contract, "version"));
  return hash_text(h, operation);
}
static void offers_free(offers *o) {
  free(o->offers);
  free(o->slots);
  memset(o, 0, sizeof(*o));
}
static ext_plugin_status offers_build(offers *o, yyjson_val *ms) {
  size_t i, n, j, k, x, y, total = 0;
  yyjson_val *m, *c, *op;
  yyjson_arr_foreach(ms, i, n, m) yyjson_arr_foreach(
      get(m, "contracts"), j, k,
      c) total += yyjson_arr_size(get(c, "operations"));
  size_t slots = 16;
  while (slots / 2 < total) {
    if (slots > SIZE_MAX / 4)
      return EXT_PLUGIN_NOMEM;
    slots *= 2;
  }
  o->offers = calloc(total ? total : 1, sizeof(*o->offers));
  o->slots = calloc(slots, sizeof(*o->slots));
  if (!o->offers || !o->slots) {
    offers_free(o);
    return EXT_PLUGIN_NOMEM;
  }
  o->mask = slots - 1;
  yyjson_arr_foreach(ms, i, n, m) yyjson_arr_foreach(
      get(m, "contracts"), j, k,
      c) yyjson_arr_foreach(get(c, "operations"), x, y, op) {
    offer *e = &o->offers[o->count];
    e->hash = offer_hash(c, get(op, "name"));
    e->manifest = i;
    e->contract = c;
    e->operation = get(op, "name");
    size_t slot = (size_t)e->hash & o->mask;
    while (o->slots[slot])
      slot = (slot + 1) & o->mask;
    o->slots[slot] = ++o->count;
  }
  return EXT_PLUGIN_OK;
}
static ext_plugin_status resolve(yyjson_val *ms, const char *os, const char *arch, ext_plugin_buffer *out) {
  if (!yyjson_is_arr(ms) || yyjson_arr_size(ms) > 1024)
    return EXT_PLUGIN_INVALID;
  size_t count = yyjson_arr_size(ms), i, n, j, k;
  yyjson_val *m, *r;
  ext_plugin_status s = EXT_PLUGIN_OK;
  offers index = {0};
  node *nodes = calloc(count ? count : 1, sizeof(*nodes));
  sorted *sorted_nodes = calloc(count ? count : 1, sizeof(*sorted_nodes));
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  if (!nodes || !sorted_nodes || !d) {
    s = EXT_PLUGIN_NOMEM;
    goto done;
  }
  yyjson_mut_val *root = yyjson_mut_obj(d), *order = yyjson_mut_arr(d),
                 *bindings = yyjson_mut_arr(d);
  yyjson_mut_doc_set_root(d, root);
  if (!root || !order || !bindings) {
    s = EXT_PLUGIN_NOMEM;
    goto done;
  }
  yyjson_arr_foreach(ms, i, n, m) {
    if ((s = ext_manifest_valid(m)))
      goto done;
    nodes[i].id = get(m, "identity");
    for (j = 0; j < i; j++)
      if (ext_same_identity(nodes[i].id, nodes[j].id)) {
        s = EXT_PLUGIN_AMBIGUOUS;
        goto done;
      }
    sorted_nodes[i].index = i;
    identity_hash(nodes[i].id, sorted_nodes[i].hash);
  }
  if ((s = offers_build(&index, ms)))
    goto done;
  yyjson_arr_foreach(ms, i, n, m) {
    nodes[i].count = yyjson_arr_size(get(m, "requires"));
    if (nodes[i].count) {
      nodes[i].deps = calloc(nodes[i].count, sizeof(size_t));
      if (!nodes[i].deps) {
        s = EXT_PLUGIN_NOMEM;
        goto done;
      }
    }
    size_t active = 0;
    yyjson_arr_foreach(get(m, "requires"), j, k, r) {
      yyjson_val *required_os = get(r, "os"), *required_arch = get(r, "arch");
      if ((yyjson_get_len(required_os) || yyjson_get_len(required_arch)) && (!os || !arch)) {
        s = EXT_PLUGIN_INVALID;
        goto done;
      }
      if ((yyjson_get_len(required_os) && !yyjson_equals_str(required_os, os)) ||
          (yyjson_get_len(required_arch) && !yyjson_equals_str(required_arch, arch)))
        continue;
      size_t found = 0, provider = 0;
      yyjson_val *want = get(r, "identity");
      uint64_t hash = offer_hash(get(r, "contract"), get(r, "operation"));
      for (size_t slot = (size_t)hash & index.mask; index.slots[slot];
           slot = (slot + 1) & index.mask) {
        const offer *e = &index.offers[index.slots[slot] - 1];
        if (e->hash == hash &&
            ext_same_ref(e->contract, get(r, "contract")) &&
            ext_same(e->operation, get(r, "operation")) &&
            (absent(provider_id(r)) || ext_same(provider_id(r), get(nodes[e->manifest].id, "id"))) &&
            (absent(want) ||
             ext_same_identity(want, nodes[e->manifest].id))) {
          found++;
          provider = e->manifest;
        }
      }
      if (found != 1) {
        s = found ? EXT_PLUGIN_AMBIGUOUS : EXT_PLUGIN_NOT_FOUND;
        goto done;
      }
      nodes[i].deps[active++] = provider;
      yyjson_mut_val *binding = yyjson_mut_obj(d);
      if (!binding || !copy(d, binding, "consumer", nodes[i].id) ||
          !copy(d, binding, "requirement", r) ||
          !copy(d, binding, "provider", nodes[provider].id) ||
          !yyjson_mut_arr_append(bindings, binding)) {
        s = EXT_PLUGIN_NOMEM;
        goto done;
      }
    }
    nodes[i].count = active;
  }
  qsort(sorted_nodes, count, sizeof(*sorted_nodes), compare);
  for (i = 0; i < count; i++)
    if ((s = visit(nodes, sorted_nodes[i].index, d, order)))
      goto done;
  /* Preserve the reference's nil slices for an empty plan. */
  if (!yyjson_mut_obj_add_val(d, root, "order",
                              count ? order : yyjson_mut_null(d)) ||
      !yyjson_mut_obj_add_val(
          d, root, "bindings",
          yyjson_mut_arr_size(bindings) ? bindings : yyjson_mut_null(d))) {
    s = EXT_PLUGIN_NOMEM;
    goto done;
  }
  out->data = (uint8_t *)yyjson_mut_write(d, 0, &out->len);
  if (!out->data)
    s = EXT_PLUGIN_NOMEM;
done:
  if (nodes) {
    for (i = 0; i < count; i++)
      free(nodes[i].deps);
  }
  free(nodes);
  free(sorted_nodes);
  offers_free(&index);
  yyjson_mut_doc_free(d);
  return s;
}
/* Ext graph is shared data, not a dependency on the ledger runtime. */
void ext_package_graph_identity(yyjson_val *identity, char key[65]) {
  uint8_t hash[32];
  identity_hash(identity, hash);
  ext_hash_hex(hash, key);
}
static ext_plugin_status package_graph(yyjson_val *input, ext_plugin_buffer *out) {
  if (!EXT_FIELDS(input, "packages", "os", "arch") ||
      !ext_text(get(input, "os"), 0, 0) || !ext_text(get(input, "arch"), 0, 0))
    return EXT_PLUGIN_INVALID;
  ext_plugin_buffer plan = {0};
  ext_plugin_status s = resolve(get(input, "packages"), yyjson_get_str(get(input, "os")),
                                yyjson_get_str(get(input, "arch")), &plan);
  if (s) return s;
  yyjson_doc *parsed = ext_parse(plan.data, plan.len);
  ext_plugin_buffer_free(&plan);
  if (!parsed) return EXT_PLUGIN_NOMEM;
  const char *schema_json = "{\"namespace\":\"ext.plugin\",\"version\":\"1\","
    "\"kinds\":[\"ext.plugin/package\"],\"relationships\":[{\"type\":\"ext.plugin/requires\","
    "\"from\":\"ext.plugin/package\",\"to\":\"ext.plugin/package\"}]}";
  yyjson_doc *schema_doc = yyjson_read(schema_json, strlen(schema_json), 0);
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *root = d ? yyjson_mut_obj(d) : NULL;
  yyjson_mut_val *tx = d ? yyjson_mut_obj(d) : NULL;
  yyjson_mut_val *vertices = d ? yyjson_mut_arr(d) : NULL;
  yyjson_mut_val *edges = d ? yyjson_mut_arr(d) : NULL;
  int ok = schema_doc && root && tx && vertices && edges;
  if (ok) {
    yyjson_mut_doc_set_root(d, root);
    ok = copy(d, root, "schema", yyjson_doc_get_root(schema_doc)) &&
      yyjson_mut_obj_add_val(d, root, "transaction", tx) &&
      yyjson_mut_obj_add_str(d, tx, "namespace", "ext.plugin") &&
      yyjson_mut_obj_add_val(d, tx, "vertices", vertices) &&
      yyjson_mut_obj_add_val(d, tx, "edges", edges);
  }
  size_t i, n;
  yyjson_val *value;
  yyjson_arr_foreach(get(yyjson_doc_get_root(parsed), "order"), i, n, value) {
    if (!ok) break;
    char key[65];
    ext_package_graph_identity(value, key);
    yyjson_mut_val *v = yyjson_mut_obj(d), *attrs = yyjson_mut_obj(d);
    ok = v && attrs && yyjson_mut_obj_add_strcpy(d, v, "id", key) &&
      yyjson_mut_obj_add_str(d, v, "kind", "ext.plugin/package") &&
      copy(d, attrs, "identity", value) && yyjson_mut_obj_add_val(d, v, "attributes", attrs) &&
      yyjson_mut_arr_append(vertices, v);
  }
  yyjson_arr_foreach(get(yyjson_doc_get_root(parsed), "bindings"), i, n, value) {
    if (!ok) break;
    char from[65], to[65], key[96];
    ext_package_graph_identity(get(value, "consumer"), from);
    ext_package_graph_identity(get(value, "provider"), to);
    snprintf(key, sizeof key, "requires-%s-%zu", from, i);
    yyjson_mut_val *e = yyjson_mut_obj(d), *attrs = yyjson_mut_obj(d);
    ok = e && attrs && yyjson_mut_obj_add_strcpy(d, e, "id", key) &&
      yyjson_mut_obj_add_strcpy(d, e, "from", from) && yyjson_mut_obj_add_strcpy(d, e, "to", to) &&
      yyjson_mut_obj_add_str(d, e, "type", "ext.plugin/requires") &&
      copy(d, attrs, "requirement", get(value, "requirement")) &&
      yyjson_mut_obj_add_val(d, e, "attributes", attrs) && yyjson_mut_arr_append(edges, e);
  }
  yyjson_doc_free(schema_doc);
  yyjson_doc_free(parsed);
  return result(d, out, ok);
}

/* Compile only declared reverse operations backed by resolved package edges.
 * The returned identities are candidates for application authorization, not grants. */
static ext_plugin_status resolved_routes(yyjson_val *ms, yyjson_val *bindings, yyjson_val *consumer, ext_plugin_buffer *out) {
  ext_plugin_status s = EXT_PLUGIN_OK;
  yyjson_val *source = NULL, *m, *binding;
  size_t i, n, j, k;
  yyjson_arr_foreach(ms, i, n, m)
    if (ext_same_identity(get(m, "identity"), consumer)) source = m;
  if (!source) return EXT_PLUGIN_NOT_FOUND;
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *routes = doc ? yyjson_mut_arr(doc) : NULL;
  if (!routes) { yyjson_mut_doc_free(doc); return EXT_PLUGIN_NOMEM; }
  yyjson_mut_doc_set_root(doc, routes);
  yyjson_arr_foreach(bindings, i, n, binding) {
    yyjson_val *req = get(binding, "requirement"), *ref = get(req, "contract"), *op = get(req, "operation");
    if (!ext_same_identity(get(binding, "consumer"), consumer) ||
        !ext_lookup_in(get(source, "reverse"), ref, op)) continue;
    int duplicate = 0;
    yyjson_val *prior;
    EXT_ARR_BEFORE(bindings, i, j, k, prior) {
      yyjson_val *other = get(prior, "requirement");
      if (!ext_same_identity(get(prior, "consumer"), consumer) ||
          !ext_same_ref(get(other, "contract"), ref) || !ext_same(get(other, "operation"), op)) continue;
      if (!ext_same_identity(get(prior, "provider"), get(binding, "provider"))) { s = EXT_PLUGIN_AMBIGUOUS; break; }
      duplicate = 1;
    }
    if (s) break;
    if (duplicate) continue;
    yyjson_mut_val *route = yyjson_mut_obj(doc);
    if (!route || !copy(doc, route, "contract", ref) || !copy(doc, route, "operation", op) ||
        !copy(doc, route, "provider", get(binding, "provider")) || !yyjson_mut_arr_append(routes, route)) {
      s = EXT_PLUGIN_NOMEM; break;
    }
  }
  if (!s) {
    out->data = (uint8_t *)yyjson_mut_write(doc, 0, &out->len);
    if (!out->data) s = EXT_PLUGIN_NOMEM;
  }
  yyjson_mut_doc_free(doc);
  return s;
}

static ext_plugin_status package_routes(yyjson_val *input, ext_plugin_buffer *out) {
  yyjson_val *ms = get(input, "packages"), *consumer = get(input, "consumer");
  if (!EXT_FIELDS(input, "packages", "os", "arch", "consumer") || !ext_identity(consumer) ||
      !ext_text(get(input, "os"), 0, 0) || !ext_text(get(input, "arch"), 0, 0))
    return EXT_PLUGIN_INVALID;
  ext_plugin_buffer plan = {0};
  ext_plugin_status s = resolve(ms, yyjson_get_str(get(input, "os")), yyjson_get_str(get(input, "arch")), &plan);
  if (s) return s;
  yyjson_doc *parsed = ext_parse(plan.data, plan.len);
  ext_plugin_buffer_free(&plan);
  if (!parsed) return EXT_PLUGIN_NOMEM;
  s = resolved_routes(ms, get(yyjson_doc_get_root(parsed), "bindings"), consumer, out);
  yyjson_doc_free(parsed);
  return s;
}

/* A bounded execution plan is distinct from the declarative graph export.
 * Nodes are keyed by full identity and selected contract, not only package ID. */
typedef struct {
  yyjson_val *identity, *contract;
  yyjson_doc *routes;
  int visiting;
} execution_node;
typedef struct {
  yyjson_val *packages, *bindings;
  execution_node *nodes;
  size_t count, limit;
  yyjson_mut_doc *doc;
  yyjson_mut_val *order;
} execution_plan;
static ext_plugin_status execution_visit(execution_plan *p, yyjson_val *identity,
                                         yyjson_val *contract, size_t *index) {
  for (size_t i = 0; i < p->count; i++) {
    if (ext_same_identity(p->nodes[i].identity, identity) && ext_same_ref(p->nodes[i].contract, contract)) {
      *index = i;
      return p->nodes[i].visiting ? EXT_PLUGIN_INVALID : EXT_PLUGIN_OK;
    }
  }
  if (p->count >= p->limit) return EXT_PLUGIN_CAPACITY;
  yyjson_val *manifest = NULL, *m, *c;
  size_t i, n;
  yyjson_arr_foreach(p->packages, i, n, m)
    if (ext_same_identity(get(m, "identity"), identity)) manifest = m;
  if (!manifest) return EXT_PLUGIN_NOT_FOUND;
  int offered = 0;
  yyjson_arr_foreach(get(manifest, "contracts"), i, n, c)
    if (ext_same_ref(c, contract)) offered = 1;
  if (!offered) return EXT_PLUGIN_NOT_FOUND;
  *index = p->count++;
  execution_node *node = &p->nodes[*index];
  node->identity = identity; node->contract = contract; node->visiting = 1;
  ext_plugin_buffer routes = {0};
  ext_plugin_status s = resolved_routes(p->packages, p->bindings, identity, &routes);
  if (s) return s;
  node->routes = ext_parse(routes.data, routes.len);
  ext_plugin_buffer_free(&routes);
  if (!node->routes) return EXT_PLUGIN_NOMEM;
  yyjson_val *route;
  yyjson_arr_foreach(yyjson_doc_get_root(node->routes), i, n, route) {
    size_t target;
    s = execution_visit(p, get(route, "provider"), get(route, "contract"), &target);
    if (s) return s;
  }
  node->visiting = 0;
  return yyjson_mut_arr_append(p->order, yyjson_mut_uint(p->doc, *index)) ? EXT_PLUGIN_OK : EXT_PLUGIN_NOMEM;
}
static ext_plugin_status package_execution(yyjson_val *input, ext_plugin_buffer *out) {
  yyjson_val *root = get(input, "root"), *limit = get(input, "max_hosts");
  int64_t maximum = 128;
  if (!EXT_FIELDS(input, "packages", "os", "arch", "root", "max_hosts") ||
      !EXT_FIELDS(root, "identity", "contract") || !ext_identity(get(root, "identity")) ||
      !EXT_FIELDS(get(root, "contract"), "name", "version") ||
      !ext_ref(get(root, "contract")) || !ext_text(get(input, "os"), 0, 0) ||
      !ext_text(get(input, "arch"), 0, 0) ||
      (limit && (!ext_int64(limit, &maximum) || maximum < 1 || maximum > 1024)))
    return EXT_PLUGIN_INVALID;
  ext_plugin_buffer resolved = {0};
  ext_plugin_status s = resolve(get(input, "packages"), yyjson_get_str(get(input, "os")),
                                yyjson_get_str(get(input, "arch")), &resolved);
  if (s) return s;
  yyjson_doc *parsed = ext_parse(resolved.data, resolved.len);
  ext_plugin_buffer_free(&resolved);
  if (!parsed) return EXT_PLUGIN_NOMEM;
  execution_plan p = {0};
  p.limit = (size_t)maximum;
  p.nodes = calloc(p.limit, sizeof *p.nodes);
  p.packages = get(input, "packages"); p.bindings = get(yyjson_doc_get_root(parsed), "bindings");
  p.doc = yyjson_mut_doc_new(NULL);
  p.order = p.doc ? yyjson_mut_arr(p.doc) : NULL;
  yyjson_mut_val *output = p.doc ? yyjson_mut_obj(p.doc) : NULL;
  yyjson_mut_val *nodes = p.doc ? yyjson_mut_arr(p.doc) : NULL;
  if (!p.nodes || !p.order || !output || !nodes) s = EXT_PLUGIN_NOMEM;
  size_t root_index = 0;
  if (!s) s = execution_visit(&p, get(root, "identity"), get(root, "contract"), &root_index);
  for (size_t i = 0; !s && i < p.count; i++) {
    execution_node *node = &p.nodes[i];
    yyjson_mut_val *v = yyjson_mut_obj(p.doc);
    if (!v || !copy(p.doc, v, "identity", node->identity) || !copy(p.doc, v, "contract", node->contract) ||
        !copy(p.doc, v, "routes", yyjson_doc_get_root(node->routes)) || !yyjson_mut_arr_append(nodes, v))
      s = EXT_PLUGIN_NOMEM;
  }
  if (!s) {
    yyjson_mut_doc_set_root(p.doc, output);
    if (!yyjson_mut_obj_add_uint(p.doc, output, "root", root_index) ||
        !yyjson_mut_obj_add_val(p.doc, output, "nodes", nodes) ||
        !yyjson_mut_obj_add_val(p.doc, output, "order", p.order)) s = EXT_PLUGIN_NOMEM;
    else {
      out->data = (uint8_t *)yyjson_mut_write(p.doc, 0, &out->len);
      if (!out->data) s = EXT_PLUGIN_NOMEM;
    }
  }
  for (size_t i = 0; i < p.count; i++) yyjson_doc_free(p.nodes[i].routes);
  free(p.nodes);
  yyjson_mut_doc_free(p.doc);
  yyjson_doc_free(parsed);
  return s;
}

/* A bundle is a reproducible selection of existing package archives, not a
 * second package format. Its complete dependency graph must resolve on its own. */
/* Native jobs are portable bundle envelopes. Citation semantics and driver
 * artifacts are validated by the existing native-library fetch layer. */
static int bundle_native(yyjson_val *jobs) {
  if (!jobs) return 1;
  if (!yyjson_is_arr(jobs) || yyjson_arr_size(jobs) > 64) return 0;
  size_t i, n, j, m;
  yyjson_val *job, *driver;
  yyjson_arr_foreach(jobs, i, n, job) {
    yyjson_val *dir = get(job, "dir"), *drivers = get(job, "drivers"),
               *citation = get(job, "citation");
    if (!EXT_FIELDS(job, "citation", "drivers", "dir", "package") ||
        !yyjson_is_obj(citation) ||
        !yyjson_equals_str(get(citation, "schema"), "ext.library.citation/v1") ||
        !string(dir, 1, 1024) ||
        !ext_portable_path(yyjson_get_str(dir), yyjson_get_len(dir)) ||
        !yyjson_is_arr(drivers) || !yyjson_arr_size(drivers) ||
        yyjson_arr_size(drivers) > 3 ||
        (get(job, "package") && !ext_text(get(job, "package"), 0, 0)))
      return 0;
    yyjson_arr_foreach(drivers, j, m, driver) {
      if (!yyjson_equals_str(driver, "cgo") && !yyjson_equals_str(driver, "c2go") &&
          !yyjson_equals_str(driver, "purego")) return 0;
      for (size_t k = 0; k < j; k++)
        if (ext_same(driver, yyjson_arr_get(drivers, k))) return 0;
    }
  }
  return 1;
}
static ext_plugin_status bundle_plan(yyjson_val *v, ext_plugin_buffer *out) {
  yyjson_val *items = get(v, "packages"), *native = get(v, "native");
  if (!EXT_FIELDS(v, "apiVersion", "target", "packages", "native") ||
      !yyjson_equals_str(get(v, "apiVersion"), "ext.bundle/v1") ||
      !ext_text(get(v, "target"), 0, 0) || !yyjson_is_arr(items) ||
      (!yyjson_arr_size(items) && !yyjson_arr_size(native)) ||
      yyjson_arr_size(items) > 256 || !bundle_native(native))
    return EXT_PLUGIN_INVALID;
  const char *target = yyjson_get_str(get(v, "target"));
  const char *slash = strchr(target, '/');
  if (!slash || slash == target || !slash[1] || strchr(slash + 1, '/'))
    return EXT_PLUGIN_INVALID;
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  if (!doc) return EXT_PLUGIN_NOMEM;
  yyjson_mut_val *manifests = yyjson_mut_arr(doc);
  yyjson_mut_doc_set_root(doc, manifests);
  ext_plugin_status status = manifests ? EXT_PLUGIN_OK : EXT_PLUGIN_NOMEM;
  size_t i, n, j, k;
  yyjson_val *item, *other;
  yyjson_arr_foreach(items, i, n, item) {
    yyjson_val *archive = get(item, "archive"), *source = get(archive, "source");
    if (!EXT_FIELDS(item, "manifest", "archive") ||
        !EXT_FIELDS(archive, "source", "sha256") ||
        !yyjson_is_str(source) || !yyjson_get_len(source) || yyjson_get_len(source) > 4096 ||
        !digest(get(archive, "sha256"))) { status = EXT_PLUGIN_INVALID; break; }
    const char *text = yyjson_get_str(source);
    for (size_t c = 0; c < yyjson_get_len(source); c++)
      if ((unsigned char)text[c] < 32 || (unsigned char)text[c] == 127)
        status = EXT_PLUGIN_INVALID;
    if (status || (status = ext_manifest_valid(get(item, "manifest")))) break;
    yyjson_val *id = get(get(item, "manifest"), "identity");
    EXT_ARR_BEFORE(items, i, j, k, other) {
      yyjson_val *prior = get(get(other, "manifest"), "identity");
      if (ext_same(get(id, "id"), get(prior, "id"))) status = EXT_PLUGIN_AMBIGUOUS;
    }
    if (status) break;
    yyjson_mut_val *copy = yyjson_val_mut_copy(doc, get(item, "manifest"));
    if (!copy || !yyjson_mut_arr_append(manifests, copy)) { status = EXT_PLUGIN_NOMEM; break; }
  }
  if (!status) {
    size_t len = 0;
    char *encoded = yyjson_mut_write(doc, 0, &len);
    yyjson_doc *parsed = encoded ? ext_parse((const uint8_t *)encoded, len) : NULL;
    char *os = malloc((size_t)(slash - target) + 1);
    if (os) { memcpy(os, target, (size_t)(slash - target)); os[slash - target] = 0; }
    status = parsed && os ? resolve(yyjson_doc_get_root(parsed), os, slash + 1, out) : EXT_PLUGIN_NOMEM;
    free(os);
    yyjson_doc_free(parsed);
    free(encoded);
  }
  yyjson_mut_doc_free(doc);
  return status;
}

ext_plugin_status ext_package_service(const char *op, yyjson_val *v, ext_plugin_buffer *out) {
  if (!strcmp(op, "package.execution"))
    return package_execution(v, out);
  if (!strcmp(op, "package.graph"))
    return package_graph(v, out);
  if (!strcmp(op, "package.routes"))
    return package_routes(v, out);
  if (!strcmp(op, "bundle.plan"))
    return bundle_plan(v, out);
  if (!strcmp(op, "package.validate")) {
    ext_plugin_status s = ext_manifest_valid(v);
    if (s)
      return s;
    out->data = (uint8_t *)yyjson_val_write(v, 0, &out->len);
    return out->data ? EXT_PLUGIN_OK : EXT_PLUGIN_NOMEM;
  }
  if (!strcmp(op, "package.select"))
    return select_entry(v, out);
  if (!strcmp(op, "package.resolve"))
    return resolve(v, NULL, NULL, out);
  if (!strcmp(op, "package.resolve_for")) {
    if (!EXT_FIELDS(v, "packages", "os", "arch") || !ext_text(get(v, "os"), 0, 0) ||
        !ext_text(get(v, "arch"), 0, 0)) return EXT_PLUGIN_INVALID;
    return resolve(get(v, "packages"), yyjson_get_str(get(v, "os")), yyjson_get_str(get(v, "arch")), out);
  }
  return EXT_PLUGIN_UNSUPPORTED;
}
