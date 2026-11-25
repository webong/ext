/* Host inventory: shells and filesystems assembled from what each platform
 * reports. Discovery checks paths and permissions; it never runs a
 * shell or reads a filesystem's contents. */
#include "collect.h"
#include "recorder.h"

#include <stdlib.h>
#include <string.h>

static int is_windows(void) { return strcmp(ll_goos(), "windows") == 0; }

/* The cleaned path, lower-cased on Windows: how host paths are compared. */
static char *path_key(const char *path) {
  char *k = ll_path_clean(path);
  if (k && is_windows())
    for (char *p = k; *p; p++)
      if (*p >= 'A' && *p <= 'Z') *p = (char)(*p + 32);
  return k;
}

static int is_abs(const char *p) {
  if (!is_windows()) return p[0] == '/';
  return (((p[0] | 32) >= 'a' && (p[0] | 32) <= 'z') && p[1] == ':' && (p[2] == '\\' || p[2] == '/')) || (p[0] == '\\' && p[1] == '\\');
}

static char *base_name(const char *path) {
  const char *b = path;
  for (const char *p = path; *p; p++)
    if (*p == '/' || (is_windows() && *p == '\\')) b = p + 1;
  size_t n = strlen(b);
  if (is_windows()) { /* without its extension */
    const char *dot = strrchr(b, '.');
    if (dot) n = (size_t)(dot - b);
  }
  char *out = malloc(n + 1);
  if (out) {
    memcpy(out, b, n);
    out[n] = 0;
  }
  return out;
}

typedef struct {
  char *key;
  lj_value *shell;
} found_shell;

static int cmp_found(const void *a, const void *b) { return strcmp(((const found_shell *)a)->key, ((const found_shell *)b)->key); }

static int cmp_strings(const void *a, const void *b) {
  return strcmp((*(lj_value *const *)a)->u.string.ptr, (*(lj_value *const *)b)->u.string.ptr);
}

int ll_collect_shells(const ext_ledger_context *ctx, const char *search_path, lj_value **out) {
  *out = NULL;
  ll_shell_path *candidates = NULL;
  size_t n = 0, cap = 0;
  int rc = ll_platform_shell_paths(ctx, &candidates, &n);
  if (rc) return rc;
  cap = n;
#define ADD(p, s)                                                                    \
  do {                                                                               \
    if (n == cap) {                                                                  \
      cap = cap ? cap * 2 : 32;                                                      \
      ll_shell_path *g = realloc(candidates, cap * sizeof *candidates);              \
      if (!g) {                                                                      \
        rc = EXT_LEDGER_IO;                                                          \
        break;                                                                       \
      }                                                                              \
      candidates = g;                                                                \
    }                                                                                \
    candidates[n].path = (p);                                                        \
    candidates[n].source = (s);                                                      \
    n++;                                                                             \
  } while (0)
  char *configured = ll_os_getenv(is_windows() ? "ComSpec" : "SHELL");
  if (configured && *configured) ADD(configured, "configured-default");
  else free(configured);
  static const char *const names[] = {"sh",  "bash", "dash", "ash",        "zsh", "ksh", "mksh",   "fish",  "csh",
                                      "tcsh", "pwsh", "powershell", "cmd", "nu",  "elvish", "xonsh", "osh", "ysh"};
  const char *suffix = ll_executable_suffix(), *p = search_path ? search_path : "";
  char sep = ll_path_list_separator();
  while (!rc) {
    const char *end = strchr(p, sep);
    size_t len = end ? (size_t)(end - p) : strlen(p);
    char *dir = malloc(len + 1);
    if (!dir) rc = EXT_LEDGER_IO;
    else {
      memcpy(dir, p, len);
      dir[len] = 0;
      if (is_abs(dir))
        for (size_t i = 0; !rc && i < sizeof names / sizeof *names; i++) {
          size_t nl = strlen(names[i]) + strlen(suffix) + 1;
          char *file = malloc(nl);
          if (!file) {
            rc = EXT_LEDGER_IO;
            break;
          }
          strcpy(file, names[i]);
          strcat(file, suffix);
          char *joined = ll_path_join(dir, file);
          free(file);
          if (joined) ADD(joined, "path");
          else rc = EXT_LEDGER_IO;
        }
      free(dir);
    }
    if (!end) break;
    p = end + 1;
  }
#undef ADD
  found_shell *found = NULL;
  size_t nfound = 0;
  for (size_t i = 0; !rc && i < n; i++) {
    if ((rc = ll_ctx_check(ctx))) break;
    const char *candidate = candidates[i].path;
    if (!is_abs(candidate) || !ll_is_executable(candidate)) continue;
    char *path = ll_path_clean(candidate), *key = path ? path_key(path) : NULL;
    if (!path || !key) {
      free(path);
      free(key);
      rc = EXT_LEDGER_IO;
      break;
    }
    found_shell *f = NULL;
    for (size_t k = 0; k < nfound && !f; k++)
      if (strcmp(found[k].key, key) == 0) f = &found[k];
    if (!f) {
      char *resolved = ll_resolve_path(path);
      if (!resolved) {
        free(path);
        free(key);
        continue;
      }
      found_shell *g = realloc(found, (nfound + 1) * sizeof *found);
      char *name = base_name(path);
      lj_value *shell = rec_obj();
      if (!g || !name || rec_set(shell, "name", rec_str(name)) | rec_set(shell, "path", rec_str(path)) | rec_set(shell, "resolved_path", rec_str(resolved)) |
                             rec_set(shell, "configured_default", rec_bool(0)) | rec_set(shell, "sources", lj_new(LJ_ARRAY))) {
        if (g) found = g;
        free(name);
        free(resolved);
        free(path);
        free(key);
        lj_free(shell);
        rc = EXT_LEDGER_IO;
        break;
      }
      found = g;
      found[nfound].key = key;
      found[nfound].shell = shell;
      f = &found[nfound++];
      key = NULL;
      free(name);
      free(resolved);
    }
    free(key);
    free(path);
    lj_value *sources = lj_get(f->shell, "sources");
    int have = 0;
    for (size_t k = 0; k < sources->u.array.len && !have; k++) have = strcmp(sources->u.array.items[k]->u.string.ptr, candidates[i].source) == 0;
    if (!have && lj_array_push(sources, rec_str(candidates[i].source))) rc = EXT_LEDGER_IO;
    if (sources->u.array.len > 1) qsort(sources->u.array.items, sources->u.array.len, sizeof *sources->u.array.items, cmp_strings);
    if (strcmp(candidates[i].source, "configured-default") == 0) rc |= rec_set(f->shell, "configured_default", rec_bool(1)) ? EXT_LEDGER_IO : 0;
  }
  for (size_t i = 0; i < n; i++) free(candidates[i].path);
  free(candidates);
  if (nfound > 1) qsort(found, nfound, sizeof *found, cmp_found);
  lj_value *shells = rc ? NULL : lj_new(LJ_ARRAY);
  for (size_t i = 0; i < nfound; i++) {
    if (shells && lj_array_push(shells, found[i].shell)) {
      lj_free(shells);
      shells = NULL;
      rc = EXT_LEDGER_IO;
    } else if (!shells) {
      lj_free(found[i].shell);
    }
    free(found[i].key);
  }
  free(found);
  if (!rc && !shells) rc = EXT_LEDGER_IO;
  *out = rc ? NULL : shells;
  return rc;
}

lj_value *ll_filesystem_space(uint64_t blocks, uint64_t free_blocks, uint64_t available, uint64_t size) {
  if (size == 0 || blocks > UINT64_MAX / size || free_blocks > UINT64_MAX / size || available > UINT64_MAX / size) return NULL;
  lj_value *s = rec_obj();
  if (rec_set(s, "total_bytes", rec_num((double)(blocks * size))) | rec_set(s, "free_bytes", rec_num((double)(free_blocks * size))) |
      rec_set(s, "available_bytes", rec_num((double)(available * size)))) {
    lj_free(s);
    return NULL;
  }
  return s;
}

typedef struct {
  char *key, *rest; /* PathKey(mount point); source + type */
  lj_value *fs;
} sorted_fs;

static int cmp_fs(const void *a, const void *b) {
  const sorted_fs *x = a, *y = b;
  int c = strcmp(x->key, y->key);
  return c ? c : strcmp(x->rest, y->rest);
}

int ll_collect_filesystems(const ext_ledger_context *ctx, lj_value **out) {
  *out = NULL;
  lj_value *all = lj_new(LJ_ARRAY);
  if (!all) return EXT_LEDGER_IO;
  int rc = ll_platform_filesystems(ctx, all);
  size_t n = all->u.array.len;
  sorted_fs *s = n ? calloc(n, sizeof *s) : NULL;
  if (!rc && n && !s) rc = EXT_LEDGER_IO;
  for (size_t i = 0; !rc && i < n; i++) {
    lj_value *fs = all->u.array.items[i];
    char *redacted = rec_redact_source(rec_field(fs, "source"));
    if (!redacted || rec_set(fs, "source", rec_str(redacted))) rc = EXT_LEDGER_IO;
    free(redacted);
    s[i].fs = fs;
    s[i].key = path_key(rec_field(fs, "mount_point"));
    size_t a = strlen(rec_field(fs, "source")), b = strlen(rec_field(fs, "type"));
    if ((s[i].rest = malloc(a + b + 1))) {
      memcpy(s[i].rest, rec_field(fs, "source"), a);
      memcpy(s[i].rest + a, rec_field(fs, "type"), b + 1);
    }
    if (!s[i].key || !s[i].rest) rc = EXT_LEDGER_IO;
  }
  if (!rc && n > 1) {
    qsort(s, n, sizeof *s, cmp_fs);
    for (size_t i = 0; i < n; i++) all->u.array.items[i] = s[i].fs;
  }
  for (size_t i = 0; s && i < n; i++) {
    free(s[i].key);
    free(s[i].rest);
  }
  free(s);
  if (rc) {
    lj_free(all);
    return rc;
  }
  *out = all;
  return 0;
}
