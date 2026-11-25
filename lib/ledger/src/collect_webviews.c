/* Installed webview runtimes:
 * the shared WebKitGTK and Qt WebEngine libraries in the linker's directories
 * on Linux (checked to be ELF shared objects), the system WebKit framework on
 * macOS (its version read through CoreFoundation, no command run), and the
 * WebView2 runtime's registration on Windows. Evidence of installation, not
 * a usable backend. */
#if defined(__linux__)
#define _GNU_SOURCE
#endif
#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#endif

#include "collect.h"
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(__linux__)
#include <dirent.h>
#include <fnmatch.h>
#include <sys/stat.h>
#elif defined(__APPLE__)
#include <CoreFoundation/CoreFoundation.h>
#include <TargetConditionals.h>
#include <sys/stat.h>
#endif

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

static lj_value *webview(const char *name, const char *engine, const char *api, const char *location, const char *scope, const char *source) {
  lj_value *w = rec_obj();
  if (set(w, "name", rec_str(name)) | set(w, "engine", rec_str(engine)) | set(w, "api", rec_str(api)) | set(w, "location", rec_str(location)) |
      set(w, "scope", rec_str(scope)) | set(w, "source", rec_str(source))) {
    lj_free(w);
    return NULL;
  }
  return w;
}

#if defined(__linux__)
typedef struct {
  char **items;
  size_t n, cap;
} strings;

static int cmp_str(const void *a, const void *b) { return strcmp(*(char *const *)a, *(char *const *)b); }

/* The paths a pattern with wildcards in its last component matches, sorted as glob sorts them
 * (glob itself is missing from older Android). */
static char **match_paths(const char *pattern, size_t *count) {
  *count = 0;
  const char *slash = strrchr(pattern, '/');
  if (!slash) return NULL;
  size_t dl = (size_t)(slash - pattern);
  char dir[4096];
  if (dl >= sizeof dir) return NULL;
  memcpy(dir, pattern, dl);
  dir[dl] = 0;
  if (!dl) strcpy(dir, "/");
  DIR *d = opendir(dir);
  if (!d) return NULL;
  char **out = NULL;
  size_t cap = 0;
  struct dirent *e;
  while ((e = readdir(d)) != NULL) {
    if (e->d_name[0] == '.' && slash[1] != '.') continue;
    if (fnmatch(slash + 1, e->d_name, FNM_PERIOD) != 0) continue;
    if (*count == cap) {
      cap = cap ? cap * 2 : 16;
      char **g = realloc(out, cap * sizeof *g);
      if (!g) break;
      out = g;
    }
    size_t n = dl + strlen(e->d_name) + 2;
    if (!(out[*count] = malloc(n))) break;
    snprintf(out[(*count)++], n, "%s/%s", dl ? dir : "", e->d_name);
  }
  closedir(d);
  if (*count > 1) qsort(out, *count, sizeof *out, cmp_str);
  return out;
}

static void free_paths(char **paths, size_t count) {
  for (size_t i = 0; i < count; i++) free(paths[i]);
  free(paths);
}

static int add_dir(strings *s, const char *path) {
  if (path[0] != '/') return 0;
  char *clean = rec_clean(path);
  if (!clean) return EXT_LEDGER_IO;
  for (size_t i = 0; i < s->n; i++)
    if (strcmp(s->items[i], clean) == 0) {
      free(clean);
      return 0;
    }
  if (s->n == s->cap) {
    s->cap = s->cap ? s->cap * 2 : 32;
    char **g = realloc(s->items, s->cap * sizeof *g);
    if (!g) {
      free(clean);
      return EXT_LEDGER_IO;
    }
    s->items = g;
  }
  s->items[s->n++] = clean;
  return 0;
}

static int read_ld_config(const ext_ledger_context *ctx, strings *dirs, strings *visited, const char *path, int depth) {
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  char *clean = rec_clean(path);
  if (!clean) return EXT_LEDGER_IO;
  for (size_t i = 0; i < visited->n; i++)
    if (strcmp(visited->items[i], clean) == 0) {
      free(clean);
      return 0;
    }
  if (depth > 16 || visited->n >= 256) {
    free(clean);
    return EXT_LEDGER_IO; /* too many shared library configuration includes */
  }
  if ((rc = add_dir(visited, clean[0] == '/' ? clean : "/"))) {
    free(clean);
    return rc;
  }
  FILE *f = fopen(clean, "r");
  if (!f) {
    free(clean);
    return 0; /* absent or unreadable; a missing config is common */
  }
  char line[4096];
  while (!rc && fgets(line, sizeof line, f)) {
    char *hash = strchr(line, '#');
    if (hash) *hash = 0;
    char *start = line;
    while (*start == ' ' || *start == '\t') start++;
    char *end = start + strlen(start);
    while (end > start && (end[-1] == ' ' || end[-1] == '\t' || end[-1] == '\n' || end[-1] == '\r')) *--end = 0;
    if (!*start) continue;
    if (strncmp(start, "include", 7) != 0 || (start[7] != ' ' && start[7] != '\t')) {
      rc = add_dir(dirs, start);
      continue;
    }
    for (char *pattern = strtok(start + 7, " \t"); !rc && pattern; pattern = strtok(NULL, " \t")) {
      char full[4096];
      if (pattern[0] == '/') snprintf(full, sizeof full, "%s", pattern);
      else {
        char *dir = rec_dir(clean);
        snprintf(full, sizeof full, "%s/%s", dir ? dir : ".", pattern);
        free(dir);
      }
      size_t count;
      char **paths = match_paths(full, &count);
      for (size_t i = 0; !rc && i < count; i++) rc = read_ld_config(ctx, dirs, visited, paths[i], depth + 1);
      free_paths(paths, count);
    }
  }
  fclose(f);
  free(clean);
  return rc;
}

/* The machine of an ELF shared object, as Go's debug/elf names it; 0 when the file is no shared object. */
static int elf_shared(const char *path, char machine[32]) {
  FILE *f = fopen(path, "rb");
  if (!f) return 0;
  unsigned char h[20];
  size_t n = fread(h, 1, sizeof h, f);
  fclose(f);
  if (n < 20 || h[0] != 0x7f || h[1] != 'E' || h[2] != 'L' || h[3] != 'F' || (h[4] != 1 && h[4] != 2) || (h[5] != 1 && h[5] != 2)) return 0;
  int le = h[5] == 1;
  unsigned type = le ? (unsigned)(h[16] | h[17] << 8) : (unsigned)(h[16] << 8 | h[17]);
  unsigned m = le ? (unsigned)(h[18] | h[19] << 8) : (unsigned)(h[18] << 8 | h[19]);
  if (type != 3) return 0; /* ET_DYN */
  static const struct {
    unsigned id;
    const char *name;
  } names[] = {{3, "EM_386"},  {8, "EM_MIPS"},  {20, "EM_PPC"},       {21, "EM_PPC64"}, {22, "EM_S390"},
               {40, "EM_ARM"}, {62, "EM_X86_64"}, {183, "EM_AARCH64"}, {243, "EM_RISCV"}, {258, "EM_LOONGARCH"}};
  for (size_t i = 0; i < sizeof names / sizeof *names; i++)
    if (names[i].id == m) {
      snprintf(machine, 32, "%s", names[i].name);
      return 1;
    }
  snprintf(machine, 32, "%u", m);
  return 1;
}

static int platform_webviews(const ext_ledger_context *ctx, lj_value *list) {
  strings dirs = {0}, visited = {0}, seen = {0};
  int rc = 0;
  char *ld = ll_os_getenv("LD_LIBRARY_PATH");
  for (char *p = ld ? strtok(ld, ":") : NULL; !rc && p; p = strtok(NULL, ":")) rc = add_dir(&dirs, p);
  free(ld);
  static const char *const fixed[] = {"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib", "/usr/local/lib64"};
  for (size_t i = 0; !rc && i < 6; i++) rc = add_dir(&dirs, fixed[i]);
  static const char *const patterns[] = {"/lib/*-linux-gnu", "/usr/lib/*-linux-gnu"};
  for (size_t i = 0; !rc && i < 2; i++) {
    size_t count;
    char **paths = match_paths(patterns[i], &count);
    for (size_t k = 0; !rc && k < count; k++) rc = add_dir(&dirs, paths[k]);
    free_paths(paths, count);
  }
  if (!rc) rc = read_ld_config(ctx, &dirs, &visited, "/etc/ld.so.conf", 0);
  static const struct {
    const char *prefix, *name, *engine, *api, *abi;
  } types[] = {{"libwebkit2gtk-4.0.so", "WebKitGTK", "webkit", "WebKitWebView", "4.0"},
               {"libwebkit2gtk-4.1.so", "WebKitGTK", "webkit", "WebKitWebView", "4.1"},
               {"libwebkitgtk-6.0.so", "WebKitGTK", "webkit", "WebKitWebView", "6.0"},
               {"libQt5WebEngineCore.so", "Qt WebEngine", "blink", "QtWebEngineCore", "5"},
               {"libQt6WebEngineCore.so", "Qt WebEngine", "blink", "QtWebEngineCore", "6"}};
  for (size_t d = 0; !rc && d < dirs.n; d++) {
    if ((rc = ll_ctx_check(ctx))) break;
    struct dirent **entries;
    int count = scandir(dirs.items[d], &entries, NULL, alphasort); /* stable directory order */
    if (count < 0) continue;
    for (int e = 0; e < count; e++) {
      const char *name = entries[e]->d_name;
      char path[4096];
      snprintf(path, sizeof path, "%s/%s", dirs.items[d], name);
      struct stat st;
      if (!rc && lstat(path, &st) == 0 && !S_ISDIR(st.st_mode))
        for (size_t k = 0; !rc && k < sizeof types / sizeof *types; k++) {
          size_t pl = strlen(types[k].prefix);
          if (strcmp(name, types[k].prefix) != 0 && !(strncmp(name, types[k].prefix, pl) == 0 && name[pl] == '.')) continue;
          char *resolved = ll_resolve_path(path), machine[32];
          int dup = 0;
          for (size_t s = 0; resolved && s < seen.n && !dup; s++) dup = strcmp(seen.items[s], resolved) == 0;
          if (!resolved || dup || !elf_shared(resolved, machine)) { /* a dangling link, an alias, or no shared object */
            free(resolved);
            continue;
          }
          rc = add_dir(&seen, resolved);
          lj_value *w = webview(types[k].name, types[k].engine, types[k].api, path, "shared", "shared-library");
          if (!w || set(w, "abi_version", rec_str(types[k].abi)) | set(w, "resolved_path", rec_str(resolved)) |
                        set(w, "architecture", rec_str(machine)) | lj_array_push(list, w))
            rc = EXT_LEDGER_IO;
          free(resolved);
        }
      free(entries[e]);
    }
    free(entries);
  }
  strings *all[3] = {&dirs, &visited, &seen};
  for (int i = 0; i < 3; i++) {
    for (size_t k = 0; k < all[i]->n; k++) free(all[i]->items[k]);
    free(all[i]->items);
  }
  return rc;
}
#elif defined(__APPLE__) && !TARGET_OS_IPHONE
static int platform_webviews(const ext_ledger_context *ctx, lj_value *list) {
  (void)ctx;
  const char *framework = "/System/Library/Frameworks/WebKit.framework";
  struct stat st;
  if (stat(framework, &st) != 0 || !S_ISDIR(st.st_mode)) return 0;
  lj_value *w = webview("WebKit", "webkit", "WKWebView", framework, "system", "system-framework");
  if (!w) return EXT_LEDGER_IO;
  char detail[512] = "";
  char *resolved = ll_resolve_path(framework);
  int bad = 0;
  if (resolved) bad |= set(w, "resolved_path", rec_str(resolved));
  else snprintf(detail, sizeof detail, "framework path unavailable");
  free(resolved);
  CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)framework, (CFIndex)strlen(framework), true);
  CFBundleRef bundle = url ? CFBundleCreate(NULL, url) : NULL;
  CFTypeRef version = bundle ? CFBundleGetValueForInfoDictionaryKey(bundle, CFSTR("CFBundleShortVersionString")) : NULL;
  char text[128];
  if (version && CFGetTypeID(version) == CFStringGetTypeID() && CFStringGetCString((CFStringRef)version, text, sizeof text, kCFStringEncodingUTF8)) {
    bad |= set(w, "version", rec_str(text));
  } else {
    size_t at = strlen(detail);
    snprintf(detail + at, sizeof detail - at, "%sframework version unavailable", at ? "; " : "");
  }
  if (bundle) CFRelease(bundle);
  if (url) CFRelease(url);
  if (*detail) bad |= set(w, "detail_error", rec_str(detail));
  if (bad || lj_array_push(list, w)) return EXT_LEDGER_IO;
  return 0;
}
#elif defined(_WIN32)
static int positive_version(const char *v) {
  int parts = 0, positive = 0;
  for (const char *p = v;;) {
    char *end;
    if (*p < '0' || *p > '9') return 0;
    unsigned long n = strtoul(p, &end, 10);
    if (n > 0xFFFFFFFFUL) return 0;
    positive |= n > 0;
    parts++;
    if (*end == 0) break;
    if (*end != '.') return 0;
    p = end + 1;
  }
  return parts == 4 && positive;
}

static int platform_webviews(const ext_ledger_context *ctx, lj_value *list) {
  static const wchar_t key[] = L"SOFTWARE\\Microsoft\\EdgeUpdate\\Clients\\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}";
  static const char keya[] = "SOFTWARE\\Microsoft\\EdgeUpdate\\Clients\\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}";
  struct {
    HKEY handle;
    const char *name, *scope;
  } roots[] = {{HKEY_LOCAL_MACHINE, "HKEY_LOCAL_MACHINE", "system"}, {HKEY_CURRENT_USER, "HKEY_CURRENT_USER", "user"}};
  char seen[4][320];
  int nseen = 0;
  for (int r = 0; r < 2; r++) {
    struct {
      REGSAM flag;
      const char *name;
    } views[2] = {{KEY_WOW64_32KEY, "32"}, {KEY_WOW64_64KEY, "64"}};
    if (r == 1) { /* the user hive prefers the 64-bit view */
      views[0].flag = KEY_WOW64_64KEY;
      views[0].name = "64";
      views[1].flag = KEY_WOW64_32KEY;
      views[1].name = "32";
    }
    for (int v = 0; v < 2; v++) {
      int rc = ll_ctx_check(ctx);
      if (rc) return rc;
      HKEY h;
      LONG err = RegOpenKeyExW(roots[r].handle, key, 0, KEY_QUERY_VALUE | views[v].flag, &h);
      if (err == ERROR_FILE_NOT_FOUND || err == ERROR_PATH_NOT_FOUND) continue;
      if (err != ERROR_SUCCESS) return EXT_LEDGER_UNAVAILABLE;
      wchar_t value[512];
      DWORD type = 0, size = sizeof value;
      err = RegQueryValueExW(h, L"pv", NULL, &type, (BYTE *)value, &size);
      RegCloseKey(h);
      if (err == ERROR_FILE_NOT_FOUND) continue;
      if (err != ERROR_SUCCESS || type != REG_SZ || size % 2) return EXT_LEDGER_UNAVAILABLE;
      value[size / 2 < 511 ? size / 2 : 511] = 0;
      char version[256];
      int n = WideCharToMultiByte(CP_UTF8, 0, value, -1, version, sizeof version, NULL, NULL);
      if (n <= 0) continue;
      char *start = version, *end = version + strlen(version);
      while (*start == ' ') start++;
      while (end > start && end[-1] == ' ') *--end = 0;
      char identity[320];
      snprintf(identity, sizeof identity, "%s|%s", roots[r].scope, start);
      int dup = 0;
      for (int k = 0; k < nseen && !dup; k++) dup = strcmp(seen[k], identity) == 0;
      if (!positive_version(start) || dup) continue;
      if (nseen < 4) strcpy(seen[nseen++], identity);
      char location[512];
      snprintf(location, sizeof location, "%s\\%s (view %s)", roots[r].name, keya, views[v].name);
      lj_value *w = webview("WebView2", "blink", "WebView2", location, roots[r].scope, "registry");
      if (!w || set(w, "version", rec_str(start)) || lj_array_push(list, w)) return EXT_LEDGER_IO;
    }
  }
  return 0;
}
#else
static int platform_webviews(const ext_ledger_context *ctx, lj_value *list) {
  (void)ctx;
  (void)list;
  (void)webview;
  return 0; /* no webview runtime this library knows how to find here */
}
#endif

typedef struct {
  char *key;
  lj_value *w;
} keyed;

static int cmp_keyed(const void *a, const void *b) {
  const keyed *x = a, *y = b;
  return strcmp(x->key, y->key);
}

/* name \1 scope \1 path key: the identity with its NULs made printable for sorting. */
static char *identity(const lj_value *w) {
  char *k = ll_path_clean(rec_field(w, "location"));
  if (!k) return NULL;
  if (strcmp(ll_goos(), "windows") == 0)
    for (char *p = k; *p; p++)
      if (*p >= 'A' && *p <= 'Z') *p = (char)(*p + 32);
  size_t n = strlen(rec_field(w, "name")) + strlen(rec_field(w, "scope")) + strlen(k) + 3;
  char *id = malloc(n);
  if (id) snprintf(id, n, "%s\1%s\1%s", rec_field(w, "name"), rec_field(w, "scope"), k);
  free(k);
  return id;
}

int ll_collect_webviews(const ext_ledger_context *ctx, lj_value **out) {
  *out = NULL;
  lj_value *all = lj_new(LJ_ARRAY);
  if (!all) return EXT_LEDGER_IO;
  int rc = platform_webviews(ctx, all);
  size_t n = all->u.array.len;
  keyed *k = n ? calloc(n, sizeof *k) : NULL;
  if (!rc && n && !k) rc = EXT_LEDGER_IO;
  size_t kept = 0;
  for (size_t i = 0; !rc && i < n; i++) {
    lj_value *w = all->u.array.items[i];
    char *id = identity(w);
    if (!id) {
      rc = EXT_LEDGER_IO;
      break;
    }
    int dup = 0;
    for (size_t j = 0; j < kept && !dup; j++) dup = strcmp(k[j].key, id) == 0;
    if (dup) {
      free(id);
      lj_free(w);
      all->u.array.items[i] = NULL; /* freed: the list must not free it again */
      continue;
    }
    k[kept].key = id;
    k[kept++].w = w;
  }
  lj_value *result = NULL;
  if (!rc) {
    if (kept > 1) qsort(k, kept, sizeof *k, cmp_keyed);
    result = lj_new(LJ_ARRAY);
    for (size_t i = 0; i < kept; i++)
      if (!result || lj_array_push(result, k[i].w)) rc = EXT_LEDGER_IO;
    all->u.array.len = 0; /* the kept records moved to result, the duplicates were freed */
  }
  for (size_t i = 0; k && i < kept; i++) free(k[i].key);
  free(k);
  lj_free(all);
  if (rc) {
    lj_free(result);
    return rc;
  }
  *out = result;
  return 0;
}
