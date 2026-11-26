/* Named executable discovery. PATH is supplied by the caller so an embedding
 * application can exclude its own command shims. This code never runs a file. */
#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#include <dirent.h>
#else
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#endif

#include "collect.h"
#include "recorder.h"

#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
static wchar_t *wide(const char *value) {
  int n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value, -1, NULL, 0);
  if (n <= 0) return NULL;
  wchar_t *result = malloc((size_t)n * sizeof *result);
  if (!result || !MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value, -1, result, n)) {
    free(result);
    return NULL;
  }
  return result;
}

static char *utf8(const wchar_t *value) {
  int n = WideCharToMultiByte(CP_UTF8, 0, value, -1, NULL, 0, NULL, NULL);
  if (n <= 0) return NULL;
  char *result = malloc((size_t)n);
  if (!result || !WideCharToMultiByte(CP_UTF8, 0, value, -1, result, n, NULL, NULL)) {
    free(result);
    return NULL;
  }
  return result;
}
#endif

static int absolute(const char *path) {
  if (!path || !*path) return 0;
  if (strcmp(ll_goos(), "windows")) return path[0] == '/';
  return (((path[0] | 32) >= 'a' && (path[0] | 32) <= 'z') && path[1] == ':' &&
          (path[2] == '\\' || path[2] == '/')) || (path[0] == '\\' && path[1] == '\\');
}

static int valid_name(const lj_value *name) {
  if (!name || name->type != LJ_STRING || !name->u.string.len || name->u.string.len > 256 ||
      strlen(name->u.string.ptr) != name->u.string.len) return 0;
  for (size_t i = 0; i < name->u.string.len; i++)
    if (name->u.string.ptr[i] == '/' || name->u.string.ptr[i] == '\\') return 0;
  return strcmp(name->u.string.ptr, ".") && strcmp(name->u.string.ptr, "..");
}

static int name_matches(const char *file, const lj_value *names, const char **name) {
  size_t len = strlen(file), base = len;
  if (!strcmp(ll_goos(), "windows")) {
    const char *ext = strrchr(file, '.');
    if (!ext) return 0;
    /* A Windows PATH command is one of the executable extensions. */
    const char *allowed = ll_os_getenv("PATHEXT");
    const char *defaults = ".COM;.EXE;.BAT;.CMD";
    const char *list = allowed && *allowed ? allowed : defaults;
    int accepted = 0;
    size_t n = strlen(ext);
    for (const char *p = list; *p;) {
      const char *end = strchr(p, ';');
      size_t part = end ? (size_t)(end - p) : strlen(p);
      if (part == n) {
        accepted = 1;
        for (size_t i = 0; i < n; i++) {
          char a = p[i], b = ext[i];
          if (a >= 'a' && a <= 'z') a = (char)(a - 32);
          if (b >= 'a' && b <= 'z') b = (char)(b - 32);
          if (a != b) { accepted = 0; break; }
        }
      }
      if (accepted || !end) break;
      p = end + 1;
    }
    free((void *)allowed);
    if (!accepted) return 0;
    base = (size_t)(ext - file);
  }
  for (size_t i = 0; i < names->u.array.len; i++) {
    const lj_value *candidate = names->u.array.items[i];
    if (candidate->u.string.len == base && !memcmp(file, candidate->u.string.ptr, base)) {
      *name = candidate->u.string.ptr;
      return 1;
    }
  }
  return 0;
}

static int seen(const lj_value *out, const char *name, const char *real) {
  for (size_t i = 0; i < out->u.array.len; i++) {
    const lj_value *candidate = out->u.array.items[i];
    if (!strcmp(rec_field(candidate, "name"), name) && !strcmp(rec_field(candidate, "realpath"), real)) return 1;
  }
  return 0;
}

static int add_program(const char *dir, const char *file, const lj_value *names, lj_value *out) {
  const char *name = NULL;
  if (!name_matches(file, names, &name)) return 0;
  char *path = ll_path_join(dir, file);
  if (!path) return EXT_LEDGER_IO;
  if (!ll_is_executable(path)) { free(path); return 0; }
  char *real = ll_resolve_path(path);
  if (!real) { free(path); return 0; }
  if (seen(out, name, real)) { free(path); free(real); return 0; }
  lj_value *candidate = rec_obj();
  int bad = rec_set(candidate, "name", rec_str(name)) | rec_set(candidate, "path", rec_str(path)) |
            rec_set(candidate, "realpath", rec_str(real)) | rec_set(candidate, "source", rec_str("path")) |
            rec_set(candidate, "on_path", rec_bool(1));
  free(path);
  free(real);
  if (bad) { lj_free(candidate); return EXT_LEDGER_IO; }
  if (lj_array_push(out, candidate)) return EXT_LEDGER_IO;
  return 0;
}

static int cmp_file(const void *a, const void *b) { return strcmp(*(const char *const *)a, *(const char *const *)b); }

static int scan_directory(const ext_ledger_context *ctx, const char *dir, const lj_value *names, lj_value *out) {
  char **files = NULL;
  size_t count = 0;
  int rc = 0;
#ifndef _WIN32
  DIR *stream = opendir(dir);
  if (!stream) return 0;
  struct dirent *entry;
  while ((entry = readdir(stream)) != NULL && !rc) {
    if ((rc = ll_ctx_check(ctx))) break;
    const char *name = NULL;
    if (!name_matches(entry->d_name, names, &name)) continue;
    if (count >= 4096) { rc = EXT_LEDGER_TOO_LARGE; break; }
    char *copy = malloc(strlen(entry->d_name) + 1);
    if (!copy) { rc = EXT_LEDGER_IO; break; }
    char **grown = realloc(files, (count + 1) * sizeof *files);
    if (!grown) { free(copy); rc = EXT_LEDGER_IO; break; }
    files = grown;
    strcpy(copy, entry->d_name);
    files[count++] = copy;
  }
  closedir(stream);
#else
  char *pattern = ll_path_join(dir, "*");
  if (!pattern) return EXT_LEDGER_IO;
  wchar_t *wide_pattern = wide(pattern);
  free(pattern);
  if (!wide_pattern) return EXT_LEDGER_IO;
  WIN32_FIND_DATAW entry;
  HANDLE stream = FindFirstFileW(wide_pattern, &entry);
  free(wide_pattern);
  if (stream == INVALID_HANDLE_VALUE) return 0;
  do {
    if ((rc = ll_ctx_check(ctx))) break;
    char *entry_name = utf8(entry.cFileName);
    if (!entry_name) { rc = EXT_LEDGER_IO; break; }
    const char *name = NULL;
    if (!name_matches(entry_name, names, &name)) { free(entry_name); continue; }
    if (count >= 4096) { free(entry_name); rc = EXT_LEDGER_TOO_LARGE; break; }
    char **grown = realloc(files, (count + 1) * sizeof *files);
    if (!grown) { free(entry_name); rc = EXT_LEDGER_IO; break; }
    files = grown;
    files[count++] = entry_name;
  } while (FindNextFileW(stream, &entry));
  FindClose(stream);
#endif
  qsort(files, count, sizeof *files, cmp_file);
  for (size_t i = 0; i < count; i++) {
    if (!rc) rc = add_program(dir, files[i], names, out);
    free(files[i]);
  }
  free(files);
  return rc;
}

int ll_collect_programs(const ext_ledger_context *ctx, const lj_value *in, lj_value **out) {
  *out = NULL;
  const lj_value *names = lj_get(in, "names"), *path = lj_get(in, "search_path");
  if (!names || names->type != LJ_ARRAY || names->u.array.len > 256 ||
      !path || path->type != LJ_STRING || path->u.string.len > 65536 ||
      strlen(path->u.string.ptr) != path->u.string.len) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < names->u.array.len; i++) if (!valid_name(names->u.array.items[i])) return EXT_LEDGER_INVALID;
  lj_value *result = lj_new(LJ_ARRAY);
  if (!result) return EXT_LEDGER_IO;
  const char *p = path->u.string.ptr;
  int rc = 0;
  for (size_t dirs = 0; dirs < 4096; dirs++) {
    const char *end = strchr(p, ll_path_list_separator());
    size_t len = end ? (size_t)(end - p) : strlen(p);
    char *dir = malloc(len + 1);
    if (!dir) { rc = EXT_LEDGER_IO; break; }
    memcpy(dir, p, len);
    dir[len] = 0;
    if (absolute(dir)) rc = scan_directory(ctx, dir, names, result);
    free(dir);
    if (rc || !end) break;
    p = end + 1;
    if (dirs == 4095) rc = EXT_LEDGER_TOO_LARGE;
  }
  if (rc) { lj_free(result); return rc; }
  *out = result;
  return 0;
}

int ll_inspect_program(const ext_ledger_context *ctx, const lj_value *in, lj_value **out) {
  *out = NULL;
  const lj_value *name = lj_get(in, "name"), *path = lj_get(in, "path");
  if (!valid_name(name) || !path || path->type != LJ_STRING ||
      !path->u.string.len || path->u.string.len > 4096 ||
      strlen(path->u.string.ptr) != path->u.string.len || !absolute(path->u.string.ptr)) return EXT_LEDGER_INVALID;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  if (!ll_is_executable(path->u.string.ptr)) return EXT_LEDGER_NOT_FOUND;
  char *real = ll_resolve_path(path->u.string.ptr);
  if (!real) return EXT_LEDGER_NOT_FOUND;
  lj_value *candidate = rec_obj();
  int bad = rec_set(candidate, "name", rec_str(name->u.string.ptr)) |
            rec_set(candidate, "path", rec_str(path->u.string.ptr)) |
            rec_set(candidate, "realpath", rec_str(real));
  free(real);
  if (bad) { lj_free(candidate); return EXT_LEDGER_IO; }
  *out = candidate;
  return 0;
}
