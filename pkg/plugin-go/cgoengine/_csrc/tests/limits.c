/* Manifest validation and plan resolution must stay cheap on input a peer
 * controls. These tests build manifests in memory at the documented limits and
 * assert both the verdict and a CPU-time bound, so a quadratic comparison that
 * sanitizers cannot see fails here. They also compare the sorted duplicate and
 * overlap checks and the resolve index with a deliberately naive reference over
 * thousands of small random manifests, and run a deterministic mutation pass
 * with a per-input time limit.
 *
 * Time bounds are generous (the regressions they guard took minutes) so that
 * sanitizer and debug builds pass; they are CPU seconds, not wall seconds. */
#include "../src/services.h"
#include "../src/unicode_lower.h"
#include "../src/wire.h"
#include <assert.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static double cpu_seconds(void) { return (double)clock() / CLOCKS_PER_SEC; }

typedef struct {
  char *p;
  size_t n, cap;
} text;
static void text_add(text *t, const char *s, size_t n) {
  if (t->n + n + 1 > t->cap) {
    size_t cap = t->cap ? t->cap : 256;
    while (t->n + n + 1 > cap)
      cap *= 2;
    t->p = realloc(t->p, cap);
    assert(t->p);
    t->cap = cap;
  }
  memcpy(t->p + t->n, s, n);
  t->n += n;
  t->p[t->n] = 0;
}
static void text_str(text *t, const char *s) { text_add(t, s, strlen(s)); }
static void text_fmt(text *t, const char *format, ...) {
  char buf[2048];
  va_list args;
  va_start(args, format);
  int n = vsnprintf(buf, sizeof(buf), format, args);
  va_end(args);
  assert(n >= 0 && (size_t)n < sizeof(buf));
  text_add(t, buf, (size_t)n);
}

static uint64_t rng = 0x9e3779b97f4a7c15ull;
static uint32_t rnd(uint32_t n) {
  rng ^= rng >> 12;
  rng ^= rng << 25;
  rng ^= rng >> 27;
  return (uint32_t)(((rng * 0x2545f4914f6cdd1dull) >> 33) % n);
}

#define DIGEST                                                                 \
  "0000000000000000000000000000000000000000000000000000000000000000"
#define DESCRIPTOR                                                             \
  "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"x.y\","            \
  "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"c\",\"version\":\"1\","     \
  "\"operations\":[{\"name\":\"op\",\"surface\":\"\"}]}]}"
#define MANIFEST_HEAD                                                          \
  "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":" DESCRIPTOR              \
  ",\"artifacts\":["

typedef struct {
  const char *name, *path;
  int tree;
} spec;

/* Parses json and validates it as a manifest; the status, or -1 when the
 * document does not even parse. */
static int validate(const char *json, size_t n, double *seconds) {
  double start = cpu_seconds();
  yyjson_doc *doc = ext_parse((const uint8_t *)json, n);
  int status = -1;
  if (doc) {
    status = (int)ext_manifest_valid(yyjson_doc_get_root(doc));
    yyjson_doc_free(doc);
  }
  if (seconds)
    *seconds = cpu_seconds() - start;
  return status;
}
static void add_artifact(text *t, size_t index, const spec *a) {
  text_fmt(t, "%s{\"name\":\"%s\",\"path\":\"%s\",\"sha256\":\"" DIGEST "\"",
           index ? "," : "", a->name, a->path);
  if (a->tree)
    text_str(t, ",\"kind\":\"tree\"");
  text_str(t, "}");
}
static int verdict(const spec *artifacts, size_t n) {
  text t = {0};
  text_str(&t, MANIFEST_HEAD);
  for (size_t i = 0; i < n; i++)
    add_artifact(&t, i, &artifacts[i]);
  text_str(&t, "]}");
  int status = validate(t.p, t.n, NULL);
  free(t.p);
  return status;
}

/* ---- Stress at the documented limits ------------------------------------ */

static void stress_artifacts(size_t count, size_t prefix, int tree,
                             double limit) {
  text t = {0};
  char *shared = malloc(prefix + 1);
  assert(shared);
  memset(shared, 'a', prefix);
  shared[prefix] = 0;
  text_str(&t, MANIFEST_HEAD);
  for (size_t i = 0; i < count; i++) {
    if (tree)
      text_fmt(&t,
               "%s{\"name\":\"art%zu\",\"path\":\"%s%zu\",\"sha256\":\"" DIGEST
               "\",\"kind\":\"tree\"}",
               i ? "," : "", i, shared, i);
    else
      text_fmt(&t, "%s{\"name\":\"art%zu\",\"path\":\"%s/f%zu\",\"sha256\":\"" DIGEST
                   "\"}",
               i ? "," : "", i, shared, i);
  }
  text_str(&t, "]}");
  double seconds;
  int status = validate(t.p, t.n, &seconds);
  printf("  %zu %s artifacts, %zu-byte shared prefix (%zu bytes): %.3fs\n", count,
         tree ? "tree" : "file", prefix, t.n, seconds);
  assert(status == EXT_OK);
  assert(seconds < limit);
  free(shared);
  free(t.p);
}

static void stress_other_arrays(void) {
  /* Every other array filled to its limit: 1024 payloads and assets, 256
   * requirements and shared dependencies, 64 entrypoints. */
  text t = {0};
  text_str(&t, "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":"
               "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"x.y\","
               "\"revision\":\"r1\"},\"contracts\":[");
  for (int c = 0; c < 64; c++) {
    text_fmt(&t, "%s{\"name\":\"c%d\",\"version\":\"1\",\"operations\":[", c ? "," : "", c);
    for (int o = 0; o < 16; o++)
      text_fmt(&t, "%s{\"name\":\"op%d\",\"surface\":\"\"}", o ? "," : "", o);
    text_str(&t, "]}");
  }
  text_str(&t, "]},\"artifacts\":[");
  for (int i = 0; i < 4096; i++)
    text_fmt(&t, "%s{\"name\":\"art%d\",\"path\":\"bin/f%d\",\"sha256\":\"" DIGEST "\"}",
             i ? "," : "", i, i);
  text_str(&t, "],\"entrypoints\":[");
  for (int i = 0; i < 64; i++)
    text_fmt(&t, "%s{\"name\":\"e%d\",\"runtime\":\"process\",\"artifact\":\"art%d\","
                 "\"protocols\":[\"ext.plugin/v1\"]}",
             i ? "," : "", i, i);
  text_str(&t, "],\"requires\":[");
  for (int i = 0; i < 256; i++)
    text_fmt(&t, "%s{\"contract\":{\"name\":\"r%d\",\"version\":\"1\"},\"operation\":\"op\"}",
             i ? "," : "", i);
  text_str(&t, "],\"payloads\":[");
  for (int i = 0; i < 1024; i++)
    text_fmt(&t, "%s{\"contract\":{\"name\":\"c%d\",\"version\":\"1\"},"
                 "\"operation\":\"op%d\"}",
             i ? "," : "", i / 16, i % 16);
  text_str(&t, "],\"assets\":[");
  for (int i = 0; i < 1024; i++)
    text_fmt(&t, "%s{\"artifact\":\"art%d\",\"kind\":\"icon\",\"locale\":\"l%d\"}",
             i ? "," : "", 4095 - i, i);
  text_str(&t, "],\"sharedDependencies\":[");
  for (int i = 0; i < 256; i++) {
    text_fmt(&t, "%s{\"name\":\"s%d\",\"versions\":[", i ? "," : "", i);
    for (int v = 0; v < 64; v++)
      text_fmt(&t, "%s\"v%d\"", v ? "," : "", v);
    text_str(&t, "]}");
  }
  text_str(&t, "]}");
  double seconds;
  int status = validate(t.p, t.n, &seconds);
  printf("  every array at its limit (%zu bytes): %.3fs\n", t.n, seconds);
  assert(status == EXT_OK);
  assert(seconds < 15.0);
  free(t.p);
}

static void stress_resolve(size_t manifests, size_t requires, double limit) {
  text t = {0};
  text_str(&t, "[");
  for (size_t i = 0; i < manifests; i++) {
    text_fmt(&t,
             "%s{\"apiVersion\":\"ext.package/v1\",\"descriptor\":{"
             "\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"x.m%zu\","
             "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"c%zu\","
             "\"version\":\"1\",\"operations\":[{\"name\":\"op\","
             "\"surface\":\"\"}]}]},\"artifacts\":[{\"name\":\"a\","
             "\"path\":\"bin/a\",\"sha256\":\"" DIGEST "\"}],\"requires\":[",
             i ? "," : "", i, i);
    int first = 1;
    for (size_t k = 0; k < requires && k < i; k++) {
      text_fmt(&t, "%s{\"contract\":{\"name\":\"c%zu\",\"version\":\"1\"},"
                   "\"operation\":\"op\"}",
               first ? "" : ",", k);
      first = 0;
    }
    text_str(&t, "]}");
  }
  text_str(&t, "]");
  double start = cpu_seconds();
  yyjson_doc *doc = ext_parse((const uint8_t *)t.p, t.n);
  assert(doc);
  ext_buffer out = {0};
  ext_status status =
      ext_package_service("package.resolve", yyjson_doc_get_root(doc), &out);
  double seconds = cpu_seconds() - start;
  printf("  resolve %zu manifests, up to %zu requirements each (%zu bytes): %.3fs\n",
         manifests, requires, t.n, seconds);
  assert(status == EXT_OK);
  assert(seconds < limit);
  ext_buffer_free(&out);
  yyjson_doc_free(doc);
  free(t.p);
}

/* ---- Verdicts that must not change -------------------------------------- */

static void overlap_cases(void) {
  struct {
    spec items[4];
    size_t n;
    int status;
    const char *why;
  } cases[] = {
      {{{"a", "d", 1}, {"b", "d/f", 0}}, 2, EXT_INVALID, "file inside a tree"},
      {{{"a", "d", 0}, {"b", "d/f", 1}}, 2, EXT_INVALID, "tree below a file"},
      {{{"a", "d", 1}, {"b", "d-x", 0}}, 2, EXT_OK, "sibling sharing a prefix"},
      {{{"a", "d", 1}, {"b", "d+x", 0}, {"c", "d.x", 0}, {"e", "d x", 0}}, 4,
       EXT_OK, "siblings that sort between a tree and its children"},
      {{{"a", "d", 1}, {"b", "d+x", 0}, {"c", "d/y", 0}}, 3, EXT_INVALID,
       "a child after a sibling that sorts first"},
      {{{"a", "d/b", 1}, {"b", "d/c", 1}}, 2, EXT_OK, "sibling trees"},
      {{{"a", "D", 1}, {"b", "d/f", 0}}, 2, EXT_INVALID, "case differs"},
      {{{"a", "d", 0}, {"b", "d/f", 0}}, 2, EXT_OK, "files may nest"},
      {{{"a", "d", 1}, {"b", "d/b/c", 1}}, 2, EXT_INVALID, "nested trees"},
      {{{"a", "d", 0}, {"b", "d/b", 0}, {"c", "d/b/c", 1}}, 3, EXT_INVALID,
       "a file above a tree"},
      {{{"a", "Dir/F", 0}, {"b", "dir/f", 0}}, 2, EXT_INVALID, "path case"},
      {{{"a", "\xc3\x84/x", 0}, {"b", "\xc3\xa4/x", 0}}, 2, EXT_INVALID,
       "Unicode lower case"},
      {{{"a", "\xc7\x85", 0}, {"b", "\xc7\x86", 0}}, 2, EXT_INVALID,
       "title case maps to lower case"},
      {{{"a", "x", 0}, {"a", "y", 0}}, 2, EXT_INVALID, "repeated name"},
      {{{"a", "x", 0}, {"b", "y", 0}, {"c", "z", 1}}, 3, EXT_OK, "distinct"},
  };
  for (size_t i = 0; i < sizeof(cases) / sizeof(*cases); i++) {
    int status = verdict(cases[i].items, cases[i].n);
    if (status != cases[i].status)
      fprintf(stderr, "overlap case %zu (%s): status %d, want %d\n", i,
              cases[i].why, status, cases[i].status);
    assert(status == cases[i].status);
  }
}

/* ---- The pre-index logic, kept as the reference for random manifests ---- */

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
static int same_path(const char *a, const char *b) {
  const uint8_t *x = (const uint8_t *)a, *y = (const uint8_t *)b;
  const uint8_t *xe = x + strlen(a), *ye = y + strlen(b);
  while (x < xe && y < ye)
    if (lower_rune(&x) != lower_rune(&y))
      return 0;
  return x == xe && y == ye;
}
static int path_contains(const char *a, const char *b) {
  const uint8_t *x = (const uint8_t *)a, *y = (const uint8_t *)b;
  const uint8_t *xe = x + strlen(a), *ye = y + strlen(b);
  while (x < xe && y < ye)
    if (lower_rune(&x) != lower_rune(&y))
      return 0;
  return x == xe && (y == ye || *y == '/');
}
static int reference_verdict(const spec *a, size_t n) {
  for (size_t i = 0; i < n; i++)
    for (size_t j = 0; j < i; j++)
      if (!strcmp(a[i].name, a[j].name) || same_path(a[i].path, a[j].path))
        return EXT_INVALID;
  for (size_t i = 0; i < n; i++) {
    if (!a[i].tree)
      continue;
    for (size_t j = 0; j < n; j++)
      if (j != i && (path_contains(a[i].path, a[j].path) ||
                     path_contains(a[j].path, a[i].path)))
        return EXT_INVALID;
  }
  return EXT_OK;
}

static void random_artifacts(void) {
  static const char *parts[] = {"a",   "A",   "b",   "\xc3\xa4", "\xc3\x84", "a-b",
                                "a+b", "a b", "x.y", "\xc7\x85", "\xc7\x86"};
  static const char *names[] = {"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"};
  size_t invalid = 0, valid = 0;
  for (int round = 0; round < 40000; round++) {
    spec items[8];
    char paths[8][64];
    size_t n = 1 + rnd(7);
    for (size_t i = 0; i < n; i++) {
      size_t depth = 1 + rnd(3);
      paths[i][0] = 0;
      for (size_t d = 0; d < depth; d++) {
        if (d)
          strcat(paths[i], "/");
        strcat(paths[i], parts[rnd(sizeof(parts) / sizeof(*parts))]);
      }
      items[i].path = paths[i];
      items[i].name = names[rnd(n + 3 < 8 ? n + 3 : 8)];
      items[i].tree = rnd(3) == 0;
    }
    int want = reference_verdict(items, n), got = verdict(items, n);
    if (want != got) {
      fprintf(stderr, "round %d: reference %d, engine %d\n", round, want, got);
      for (size_t i = 0; i < n; i++)
        fprintf(stderr, "  %s %s%s\n", items[i].name, items[i].path,
                items[i].tree ? " (tree)" : "");
    }
    assert(want == got);
    if (got == EXT_OK)
      valid++;
    else
      invalid++;
  }
  printf("  40000 random manifests agree with the reference (%zu valid, %zu invalid)\n",
         valid, invalid);
  assert(valid > 2000 && invalid > 2000);
}

/* ---- package.resolve against a naive scan ------------------------------- */

typedef struct {
  int contract;     /* offered contract index */
  unsigned ops;     /* bit set of offered operations */
  int need[4];      /* required contract, -1 for none */
  int need_op[4];
  int need_who[4];  /* required provider, -1 for any */
} plan_manifest;

static const char *contract_names[] = {"c0", "c1", "c2"};
static const char *op_names[] = {"o0", "o1", "o2"};

static void random_plans(void) {
  size_t outcomes[16] = {0};
  for (int round = 0; round < 6000; round++) {
    plan_manifest m[6];
    size_t n = 1 + rnd(6);
    for (size_t i = 0; i < n; i++) {
      m[i].contract = (int)rnd(3);
      m[i].ops = 1 + rnd(7);
    }
    for (size_t i = 0; i < n; i++)
      for (int r = 0; r < 4; r++) {
        m[i].need[r] = -1;
        m[i].need_op[r] = 0;
        m[i].need_who[r] = -1;
        if (rnd(2))
          continue;
        if (rnd(8) == 0) { /* often unsatisfied */
          m[i].need[r] = (int)rnd(3);
          m[i].need_op[r] = (int)rnd(3);
        } else { /* usually something that exists */
          size_t x = rnd((uint32_t)n);
          m[i].need[r] = m[x].contract;
          do
            m[i].need_op[r] = (int)rnd(3);
          while (!(m[x].ops >> m[i].need_op[r] & 1));
          if (rnd(2))
            m[i].need_who[r] = (int)x;
        }
        /* skip a repeat of an earlier requirement: that is invalid on its own */
        for (int q = 0; q < r; q++)
          if (m[i].need[q] == m[i].need[r] &&
              m[i].need_op[q] == m[i].need_op[r] &&
              m[i].need_who[q] == m[i].need_who[r])
            m[i].need[r] = -1;
      }
    /* the reference: scan every requirement against every manifest */
    int want = EXT_OK;
    int provider[6][4] = {{0}};
    for (size_t i = 0; i < n && want == EXT_OK; i++)
      for (int r = 0; r < 4 && want == EXT_OK; r++) {
        if (m[i].need[r] < 0)
          continue;
        int found = 0;
        for (size_t x = 0; x < n; x++)
          if ((m[i].need_who[r] < 0 || m[i].need_who[r] == (int)x) &&
              m[x].contract == m[i].need[r] && (m[x].ops >> m[i].need_op[r] & 1)) {
            found++;
            provider[i][r] = (int)x;
          }
        if (found != 1)
          want = found ? EXT_AMBIGUOUS : EXT_NOT_FOUND;
      }
    if (want == EXT_OK) { /* a cycle among the providers is invalid */
      int state[6] = {0};
      int changed = 1;
      int resolved = 0;
      while (changed) {
        changed = 0;
        for (size_t i = 0; i < n; i++) {
          if (state[i])
            continue;
          int ready = 1;
          for (int r = 0; r < 4; r++)
            if (m[i].need[r] >= 0 && !state[provider[i][r]])
              ready = 0;
          if (ready) {
            state[i] = 1;
            changed = 1;
            resolved++;
          }
        }
      }
      if (resolved != (int)n)
        want = EXT_INVALID;
    }
    text t = {0};
    text_str(&t, "[");
    for (size_t i = 0; i < n; i++) {
      text_fmt(&t,
               "%s{\"apiVersion\":\"ext.package/v1\",\"descriptor\":{"
               "\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"x.m%zu\","
               "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"%s\","
               "\"version\":\"1\",\"operations\":[",
               i ? "," : "", i, contract_names[m[i].contract]);
      int first = 1;
      for (int o = 0; o < 3; o++)
        if (m[i].ops >> o & 1) {
          text_fmt(&t, "%s{\"name\":\"%s\",\"surface\":\"\"}", first ? "" : ",",
                   op_names[o]);
          first = 0;
        }
      text_str(&t, "]}]},\"artifacts\":[{\"name\":\"a\",\"path\":\"bin/a\","
                   "\"sha256\":\"" DIGEST "\"}],\"requires\":[");
      first = 1;
      for (int r = 0; r < 4; r++) {
        if (m[i].need[r] < 0)
          continue;
        text_fmt(&t, "%s{\"contract\":{\"name\":\"%s\",\"version\":\"1\"},"
                     "\"operation\":\"%s\"",
                 first ? "" : ",", contract_names[m[i].need[r]],
                 op_names[m[i].need_op[r]]);
        if (m[i].need_who[r] >= 0)
          text_fmt(&t, ",\"identity\":{\"id\":\"x.m%d\",\"revision\":\"r1\"}",
                   m[i].need_who[r]);
        text_str(&t, "}");
        first = 0;
      }
      text_str(&t, "]}");
    }
    text_str(&t, "]");
    yyjson_doc *doc = ext_parse((const uint8_t *)t.p, t.n);
    assert(doc);
    ext_buffer out = {0};
    int got = (int)ext_package_service("package.resolve", yyjson_doc_get_root(doc), &out);
    if (got != want) {
      fprintf(stderr, "plan %d: reference %d, engine %d\n%s\n", round, want, got, t.p);
    }
    assert(got == want);
    outcomes[want]++;
    if (want == EXT_OK) {
      yyjson_doc *result = ext_parse(out.data, out.len);
      assert(result);
      yyjson_val *bindings = yyjson_obj_get(yyjson_doc_get_root(result), "bindings");
      size_t b = 0, total = 0;
      yyjson_val *binding;
      for (size_t i = 0; i < n; i++)
        for (int r = 0; r < 4; r++)
          total += m[i].need[r] >= 0;
      assert(yyjson_arr_size(bindings) == total);
      for (size_t i = 0; i < n; i++)
        for (int r = 0; r < 4; r++) {
          if (m[i].need[r] < 0)
            continue;
          binding = yyjson_arr_get(bindings, b++);
          char id[16];
          snprintf(id, sizeof(id), "x.m%d", provider[i][r]);
          yyjson_val *got_provider = yyjson_obj_get(binding, "provider");
          assert(yyjson_equals_str(yyjson_obj_get(got_provider, "id"), id));
        }
      yyjson_doc_free(result);
    }
    ext_buffer_free(&out);
    yyjson_doc_free(doc);
    free(t.p);
  }
  printf("  6000 random plans agree with the reference (ok %zu, not found %zu, "
         "ambiguous %zu, cycle %zu)\n",
         outcomes[EXT_OK], outcomes[EXT_NOT_FOUND], outcomes[EXT_AMBIGUOUS],
         outcomes[EXT_INVALID]);
  assert(outcomes[EXT_OK] > 50 && outcomes[EXT_NOT_FOUND] > 100 &&
         outcomes[EXT_AMBIGUOUS] > 100 && outcomes[EXT_INVALID] > 10);
}

/* ---- Mutation pass with a time limit per input -------------------------- */

static const char *SEED_MANIFEST =
    "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":" DESCRIPTOR
    ",\"license\":\"MIT\",\"artifacts\":[{\"name\":\"bin\",\"path\":\"bin/x\","
    "\"sha256\":\"" DIGEST "\"},{\"name\":\"data\",\"path\":\"data\",\"kind\":\"tree\","
    "\"sha256\":\"" DIGEST "\"}],\"entrypoints\":[{\"name\":\"main\","
    "\"runtime\":\"process\",\"artifact\":\"bin\",\"protocols\":[\"ext.plugin/v1\"]}],"
    "\"requires\":[{\"contract\":{\"name\":\"d\",\"version\":\"1\"},\"operation\":\"go\"}],"
    "\"payloads\":[{\"contract\":{\"name\":\"c\",\"version\":\"1\"},\"operation\":\"op\","
    "\"input\":{\"type\":\"object\"},\"output\":{\"type\":\"object\"}}],"
    "\"assets\":[{\"artifact\":\"data\",\"kind\":\"icon\",\"locale\":\"en\"}],"
    "\"sharedDependencies\":[{\"name\":\"rt\",\"versions\":[\"1\",\"2\"]}]}";
static const char *SEED_REQUEST =
    "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"x.y\","
    "\"revision\":\"r1\"},\"contract\":{\"name\":\"c\",\"version\":\"1\"},"
    "\"operation\":\"op\",\"deadline\":\"2099-01-01T00:00:00Z\",\"payload\":{\"value\":7}}";

static double exercise(const char *data, size_t n) {
  double start = cpu_seconds();
  yyjson_doc *doc = ext_parse((const uint8_t *)data, n);
  if (doc) {
    yyjson_val *root = yyjson_doc_get_root(doc);
    int64_t deadline = 0;
    yyjson_doc *descriptor =
        ext_parse((const uint8_t *)DESCRIPTOR, strlen(DESCRIPTOR));
    (void)ext_manifest_valid(root);
    (void)ext_descriptor(root);
    (void)ext_request(yyjson_doc_get_root(descriptor), root, &deadline);
    const char *services[] = {"package.validate", "package.select",
                              "package.resolve"};
    for (size_t i = 0; i < 3; i++) {
      ext_buffer out = {0};
      (void)ext_package_service(services[i], root, &out);
      ext_buffer_free(&out);
    }
    yyjson_doc_free(descriptor);
    yyjson_doc_free(doc);
  }
  return cpu_seconds() - start;
}
static void mutate(text *t, const char *seed) {
  size_t n = strlen(seed);
  text_add(t, seed, n);
  unsigned edits = 1 + rnd(4);
  while (edits-- && t->n) {
    size_t at = rnd((uint32_t)t->n);
    switch (rnd(4)) {
    case 0: /* flip a byte */
      t->p[at] = (char)rnd(256);
      break;
    case 1: /* delete a span */ {
      size_t len = 1 + rnd(8);
      if (at + len > t->n)
        len = t->n - at;
      memmove(t->p + at, t->p + at + len, t->n - at - len + 1);
      t->n -= len;
      break;
    }
    case 2: /* repeat a span, which is how arrays grow */ {
      size_t len = 1 + rnd(40);
      if (at + len > t->n)
        len = t->n - at;
      char copy[64];
      memcpy(copy, t->p + at, len);
      text grown = {0};
      text_add(&grown, t->p, at);
      for (size_t times = 1 + rnd(30); times; times--)
        text_add(&grown, copy, len);
      text_add(&grown, t->p + at, t->n - at);
      free(t->p);
      *t = grown;
      break;
    }
    default: /* swap a digit or letter for another character class */
      t->p[at] = "0aZ/.\"{}[],:"[rnd(13)];
    }
  }
}
static void mutations(void) {
  double worst = 0;
  for (int round = 0; round < 6000; round++) {
    text t = {0};
    mutate(&t, round & 1 ? SEED_MANIFEST : SEED_REQUEST);
    double seconds = exercise(t.p, t.n);
    if (seconds > worst)
      worst = seconds;
    assert(seconds < 10.0);
    free(t.p);
  }
  /* Arrays grown to their limits by structure, which byte edits rarely reach:
   * the artifacts array with every shape of colliding path. */
  for (int shape = 0; shape < 6; shape++) {
    text t = {0};
    text_str(&t, MANIFEST_HEAD);
    for (int i = 0; i < 4096; i++) {
      char path[1100];
      switch (shape) {
      case 0: snprintf(path, sizeof(path), "%0900d/f%d", 0, i); break;
      case 1: snprintf(path, sizeof(path), "d/%d", i); break;
      case 2: snprintf(path, sizeof(path), "D%d/%d", i % 7, i); break;
      case 3: snprintf(path, sizeof(path), "%d", i % 5); break;
      case 4: snprintf(path, sizeof(path), "a/%0100d/%d", 0, i); break;
      default: snprintf(path, sizeof(path), "z%d", i); break;
      }
      char name[32];
      snprintf(name, sizeof(name), "n%d", shape == 3 ? i % 11 : i);
      spec a = {name, path, shape == 5 && i % 2};
      add_artifact(&t, (size_t)i, &a);
    }
    text_str(&t, "]}");
    double seconds = exercise(t.p, t.n);
    if (seconds > worst)
      worst = seconds;
    assert(seconds < 15.0);
    free(t.p);
  }
  printf("  6006 mutated and limit-sized inputs, slowest %.3fs\n", worst);
}

int main(void) {
  printf("manifest limits\n");
  overlap_cases();
  stress_artifacts(4096, 10, 0, 15.0);
  stress_artifacts(4096, 900, 0, 15.0);
  stress_artifacts(4096, 900, 1, 15.0);
  stress_other_arrays();
  stress_resolve(512, 128, 15.0);
  random_artifacts();
  random_plans();
  mutations();
  printf("manifest limits passed\n");
  return 0;
}
