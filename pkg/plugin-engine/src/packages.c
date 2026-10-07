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
  return absent(s) || ctx_schema_valid(s, 0, &budget);
}
static yyjson_val *named(yyjson_val *a, yyjson_val *name) {
  size_t i, n;
  yyjson_val *v;
  yyjson_arr_foreach(a, i, n, v) if (ctx_same(get(v, "name"), name)) return v;
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
static int portable(yyjson_val *v) {
  if (!string(v, 1, 1024))
    return 0;
  const char *s = yyjson_get_str(v);
  size_t n = yyjson_get_len(v), start = 0;
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
  size_t lo = 0, hi = sizeof(ctx_lower) / sizeof(ctx_lower[0]);
  while (lo < hi) {
    size_t m = lo + (hi - lo) / 2;
    if (ctx_lower[m][0] < c)
      lo = m + 1;
    else
      hi = m;
  }
  if (lo < sizeof(ctx_lower) / sizeof(ctx_lower[0]) && ctx_lower[lo][0] == c)
    return ctx_lower[lo][1];
  return c;
}
static int same_path(yyjson_val *a, yyjson_val *b) {
  const uint8_t *x = (const uint8_t *)yyjson_get_str(a),
                *y = (const uint8_t *)yyjson_get_str(b);
  const uint8_t *xe = x + yyjson_get_len(a), *ye = y + yyjson_get_len(b);
  while (x < xe && y < ye)
    if (lower_rune(&x) != lower_rune(&y))
      return 0;
  return x == xe && y == ye;
}
static int dependency(yyjson_val *r) {
  return CTX_FIELDS(r, "contract", "operation", "identity") &&
         CTX_FIELDS(get(r, "contract"), "name", "version") &&
         ctx_ref(get(r, "contract")) && ctx_text(get(r, "operation"), 0, 0) &&
         (absent(get(r, "identity")) || ctx_identity(get(r, "identity")));
}
static int same_dependency(yyjson_val *a, yyjson_val *b) {
  yyjson_val *ai = get(a, "identity"), *bi = get(b, "identity");
  return ctx_same_ref(get(a, "contract"), get(b, "contract")) &&
         ctx_same(get(a, "operation"), get(b, "operation")) &&
         ((absent(ai) && absent(bi)) ||
          (!absent(ai) && !absent(bi) && ctx_same_identity(ai, bi)));
}
ctx_status ctx_manifest_valid(yyjson_val *m) {
  if (!CTX_FIELDS(m, "apiVersion", "descriptor", "artifacts", "entrypoints",
                  "requires", "configuration", "payloads", "assets",
                  "sharedDependencies") ||
      !yyjson_equals_str(get(m, "apiVersion"), "ext.package/v1") ||
      !ctx_descriptor(get(m, "descriptor")))
    return CTX_INVALID;
  yyjson_val *a = get(m, "artifacts"), *e = get(m, "entrypoints"),
             *r = get(m, "requires"), *p = get(m, "payloads"),
             *assets = get(m, "assets"), *shared = get(m, "sharedDependencies");
  if (!yyjson_is_arr(a) || !yyjson_arr_size(a) || !array(a, 4096) ||
      !array(e, 64) || !array(r, 256) || !array(p, 1024) ||
      !array(assets, 1024) || !array(shared, 256) ||
      !schema(get(m, "configuration")))
    return CTX_INVALID;
  size_t i, n, j, k;
  yyjson_val *v, *w;
  yyjson_arr_foreach(a, i, n, v) {
    if (!CTX_FIELDS(v, "name", "path", "sha256", "os", "arch") ||
        !ctx_text(get(v, "name"), 0, 0) || !portable(get(v, "path")) ||
        !digest(get(v, "sha256")) || !ctx_text(get(v, "os"), 0, 1) ||
        !ctx_text(get(v, "arch"), 0, 1))
      return CTX_INVALID;
    for (j = 0; j < i; j++) {
      w = yyjson_arr_get(a, j);
      if (ctx_same(get(v, "name"), get(w, "name")) ||
          same_path(get(v, "path"), get(w, "path")))
        return CTX_INVALID;
    }
  }
  yyjson_arr_foreach(e, i, n, v) {
    if (!CTX_FIELDS(v, "name", "runtime", "artifact", "protocols") ||
        !ctx_text(get(v, "name"), 0, 0) || !ctx_text(get(v, "runtime"), 0, 0) ||
        !named(a, get(v, "artifact")) || !ctx_protocols(get(v, "protocols")))
      return CTX_INVALID;
    for (j = 0; j < i; j++)
      if (ctx_same(get(v, "name"), get(yyjson_arr_get(e, j), "name")))
        return CTX_INVALID;
  }
  yyjson_arr_foreach(r, i, n, v) {
    if (!dependency(v))
      return CTX_INVALID;
    for (j = 0; j < i; j++)
      if (same_dependency(v, yyjson_arr_get(r, j)))
        return CTX_INVALID;
  }
  yyjson_arr_foreach(p, i, n, v) {
    if (!CTX_FIELDS(v, "contract", "operation", "input", "output") ||
        !CTX_FIELDS(get(v, "contract"), "name", "version") ||
        !ctx_ref(get(v, "contract")) || !ctx_text(get(v, "operation"), 0, 0) ||
        !schema(get(v, "input")) || !schema(get(v, "output")))
      return CTX_INVALID;
    if (!ctx_lookup(get(m, "descriptor"), get(v, "contract"),
                    get(v, "operation")))
      return CTX_UNSUPPORTED;
    for (j = 0; j < i; j++) {
      w = yyjson_arr_get(p, j);
      if (ctx_same_ref(get(v, "contract"), get(w, "contract")) &&
          ctx_same(get(v, "operation"), get(w, "operation")))
        return CTX_INVALID;
    }
  }
  yyjson_arr_foreach(
      assets, i, n,
      v) if (!CTX_FIELDS(v, "artifact", "kind", "locale") ||
             !named(a, get(v, "artifact")) || !ctx_text(get(v, "kind"), 0, 0) ||
             (!absent(get(v, "locale")) &&
              !string(get(v, "locale"), 0, 64))) return CTX_INVALID;
  yyjson_arr_foreach(shared, i, n, v) {
    yyjson_val *versions = get(v, "versions");
    if (!CTX_FIELDS(v, "name", "versions") || !string(get(v, "name"), 1, 256) ||
        !yyjson_is_arr(versions) || !yyjson_arr_size(versions) ||
        !array(versions, 64))
      return CTX_INVALID;
    for (j = 0; j < i; j++)
      if (ctx_same(get(v, "name"), get(yyjson_arr_get(shared, j), "name")))
        return CTX_INVALID;
    yyjson_arr_foreach(versions, j, k, w) {
      if (!string(w, 1, 256))
        return CTX_INVALID;
      for (size_t x = 0; x < j; x++)
        if (ctx_same(w, yyjson_arr_get(versions, x)))
          return CTX_INVALID;
    }
  }
  return CTX_OK;
}
static ctx_status result(yyjson_mut_doc *doc, ctx_buffer *out, int ok) {
  if (ok)
    out->data = (uint8_t *)yyjson_mut_write(doc, 0, &out->len);
  yyjson_mut_doc_free(doc);
  return out->data ? CTX_OK : CTX_NOMEM;
}
static int copy(yyjson_mut_doc *d, yyjson_mut_val *o, const char *key,
                yyjson_val *v) {
  yyjson_mut_val *c = yyjson_val_mut_copy(d, v);
  return c && yyjson_mut_obj_add_val(d, o, key, c);
}
static ctx_status select_entry(yyjson_val *v, ctx_buffer *out) {
  if (!CTX_FIELDS(v, "manifest", "name", "environment"))
    return CTX_INVALID;
  yyjson_val *m = get(v, "manifest"), *env = get(v, "environment");
  ctx_status s = ctx_manifest_valid(m);
  if (s)
    return s;
  if (!string(get(v, "name"), 0, 256) ||
      !CTX_FIELDS(env, "os", "arch", "runtimes", "sharedVersions") ||
      (!absent(get(env, "runtimes")) && !yyjson_is_obj(get(env, "runtimes"))) ||
      (!absent(get(env, "sharedVersions")) &&
       !yyjson_is_obj(get(env, "sharedVersions"))))
    return CTX_INVALID;
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
                       candidate) if (ctx_same(version, candidate)) found = 1;
    if (!found)
      return CTX_UNSUPPORTED;
  }
  yyjson_val *entry = named(get(m, "entrypoints"), get(v, "name"));
  if (!entry)
    return CTX_NOT_FOUND;
  yyjson_val *runtime = get(entry, "runtime");
  yyjson_val *profile = yyjson_obj_getn(
      get(env, "runtimes"), yyjson_get_str(runtime), yyjson_get_len(runtime));
  if (!profile)
    return CTX_UNSUPPORTED;
  if (!ctx_protocols(get(profile, "protocols")))
    return CTX_INVALID;
  yyjson_val *protocol =
      ctx_negotiate(get(profile, "protocols"), get(entry, "protocols"));
  if (!protocol)
    return CTX_UNSUPPORTED;
  yyjson_val *artifact = named(get(m, "artifacts"), get(entry, "artifact"));
  const char *fields[] = {"os", "arch"};
  for (i = 0; i < 2; i++) {
    yyjson_val *value = get(artifact, fields[i]);
    if (yyjson_get_len(value) && !ctx_same(value, get(env, fields[i])))
      return CTX_UNSUPPORTED;
  }
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  if (!d)
    return CTX_NOMEM;
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
  ctx_engine_sha256((const uint8_t *)json, (size_t)n, out);
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
static ctx_status visit(node *nodes, size_t i, yyjson_mut_doc *d,
                        yyjson_mut_val *order) {
  node *p = &nodes[i];
  if (p->state == 1)
    return CTX_INVALID;
  if (p->state == 2)
    return CTX_OK;
  p->state = 1;
  for (size_t j = 0; j < p->count; j++) {
    ctx_status s = visit(nodes, p->deps[j], d, order);
    if (s)
      return s;
  }
  yyjson_mut_val *id = yyjson_val_mut_copy(d, p->id);
  if (!id || !yyjson_mut_arr_append(order, id))
    return CTX_NOMEM;
  p->state = 2;
  return CTX_OK;
}
static ctx_status resolve(yyjson_val *ms, ctx_buffer *out) {
  if (!yyjson_is_arr(ms) || yyjson_arr_size(ms) > 1024)
    return CTX_INVALID;
  size_t count = yyjson_arr_size(ms), i, n, j, k;
  yyjson_val *m, *r;
  ctx_status s = CTX_OK;
  node *nodes = calloc(count ? count : 1, sizeof(*nodes));
  sorted *sorted_nodes = calloc(count ? count : 1, sizeof(*sorted_nodes));
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  if (!nodes || !sorted_nodes || !d) {
    s = CTX_NOMEM;
    goto done;
  }
  yyjson_mut_val *root = yyjson_mut_obj(d), *order = yyjson_mut_arr(d),
                 *bindings = yyjson_mut_arr(d);
  yyjson_mut_doc_set_root(d, root);
  if (!root || !order || !bindings) {
    s = CTX_NOMEM;
    goto done;
  }
  yyjson_arr_foreach(ms, i, n, m) {
    if ((s = ctx_manifest_valid(m)))
      goto done;
    nodes[i].id = get(get(m, "descriptor"), "identity");
    for (j = 0; j < i; j++)
      if (ctx_same_identity(nodes[i].id, nodes[j].id)) {
        s = CTX_AMBIGUOUS;
        goto done;
      }
    sorted_nodes[i].index = i;
    identity_hash(nodes[i].id, sorted_nodes[i].hash);
  }
  yyjson_arr_foreach(ms, i, n, m) {
    nodes[i].count = yyjson_arr_size(get(m, "requires"));
    if (nodes[i].count) {
      nodes[i].deps = calloc(nodes[i].count, sizeof(size_t));
      if (!nodes[i].deps) {
        s = CTX_NOMEM;
        goto done;
      }
    }
    yyjson_arr_foreach(get(m, "requires"), j, k, r) {
      size_t found = 0, provider = 0;
      for (size_t x = 0; x < count; x++) {
        yyjson_val *desc = get(yyjson_arr_get(ms, x), "descriptor");
        if ((absent(get(r, "identity")) ||
             ctx_same_identity(get(r, "identity"), nodes[x].id)) &&
            ctx_lookup(desc, get(r, "contract"), get(r, "operation"))) {
          found++;
          provider = x;
        }
      }
      if (found != 1) {
        s = found ? CTX_AMBIGUOUS : CTX_NOT_FOUND;
        goto done;
      }
      nodes[i].deps[j] = provider;
      yyjson_mut_val *binding = yyjson_mut_obj(d);
      if (!binding || !copy(d, binding, "consumer", nodes[i].id) ||
          !copy(d, binding, "requirement", r) ||
          !copy(d, binding, "provider", nodes[provider].id) ||
          !yyjson_mut_arr_append(bindings, binding)) {
        s = CTX_NOMEM;
        goto done;
      }
    }
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
    s = CTX_NOMEM;
    goto done;
  }
  out->data = (uint8_t *)yyjson_mut_write(d, 0, &out->len);
  if (!out->data)
    s = CTX_NOMEM;
done:
  if (nodes) {
    for (i = 0; i < count; i++)
      free(nodes[i].deps);
  }
  free(nodes);
  free(sorted_nodes);
  yyjson_mut_doc_free(d);
  return s;
}
ctx_status ctx_package_service(const char *op, yyjson_val *v, ctx_buffer *out) {
  if (!strcmp(op, "package.validate")) {
    ctx_status s = ctx_manifest_valid(v);
    if (s)
      return s;
    out->data = (uint8_t *)yyjson_val_write(v, 0, &out->len);
    return out->data ? CTX_OK : CTX_NOMEM;
  }
  if (!strcmp(op, "package.select"))
    return select_entry(v, out);
  if (!strcmp(op, "package.resolve"))
    return resolve(v, out);
  return CTX_UNSUPPORTED;
}
