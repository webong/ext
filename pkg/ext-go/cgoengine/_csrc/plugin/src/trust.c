#define _POSIX_C_SOURCE 200809L
#include "platform.h"
#include "services.h"
#include "sha256.h"
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifndef _WIN32
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#else
#include <io.h>
#include <windows.h>
#endif

#ifdef _WIN32
static wchar_t *wide(const char *s);
#endif
static FILE *open_trust(const char *path, int write) {
#ifdef _WIN32
  wchar_t *w = wide(path);
  FILE *f = w ? _wfopen(w, write ? L"wb" : L"rb") : NULL;
  free(w);
  return f;
#else
  return fopen(path, write ? "wb" : "rb");
#endif
}

/* A trust record is an explicit, scoped review of one exact package digest.
 * The file is host-owned; this module has no default root or product names. */
#define TRUST_MAX_BYTES (4u << 20)
#define TRUST_MAX_RECORDS 4096u
#define TRUST_MAX_KEY 256u
typedef struct {
  char *scope, *id;
  char digest[65];
} record;
typedef struct {
  record *items;
  size_t count;
} records;

static int key_valid(const char *s) {
  if (!s || !*s)
    return 0;
  size_t n = strlen(s);
  if (n > TRUST_MAX_KEY)
    return 0;
  for (size_t i = 0; i < n; i++) {
    char c = s[i];
    if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
          (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' ||
          c == '/'))
      return 0;
  }
  return 1;
}
static int hex_valid(const char *s) {
  if (!s || strlen(s) != 64)
    return 0;
  for (size_t i = 0; i < 64; i++)
    if (!((s[i] >= 'a' && s[i] <= 'f') || (s[i] >= '0' && s[i] <= '9')))
      return 0;
  return 1;
}
static void clear(records *r) {
  for (size_t i = 0; i < r->count; i++) {
    free(r->items[i].scope);
    free(r->items[i].id);
  }
  free(r->items);
  *r = (records){0};
}
static long find(const records *r, const char *scope, const char *id) {
  for (size_t i = 0; i < r->count; i++)
    if (!strcmp(r->items[i].scope, scope) && !strcmp(r->items[i].id, id))
      return (long)i;
  return -1;
}
static ext_status append(records *r, const char *scope, const char *id,
                         const char *digest) {
  if (r->count >= TRUST_MAX_RECORDS)
    return EXT_CAPACITY;
  record *items = realloc(r->items, (r->count + 1) * sizeof(*items));
  if (!items)
    return EXT_NOMEM;
  r->items = items;
  record *item = &r->items[r->count];
  item->scope = strdup(scope);
  item->id = strdup(id);
  if (!item->scope || !item->id) {
    free(item->scope);
    free(item->id);
    return EXT_NOMEM;
  }
  memcpy(item->digest, digest, 65);
  r->count++;
  return EXT_OK;
}
static ext_status load(const char *path, records *out) {
  FILE *f = open_trust(path, 0);
  if (!f)
    return errno == ENOENT ? EXT_OK : EXT_IO;
  uint8_t *data = malloc(TRUST_MAX_BYTES + 1);
  if (!data) {
    fclose(f);
    return EXT_NOMEM;
  }
  size_t n = fread(data, 1, TRUST_MAX_BYTES + 1, f);
  ext_status s = ferror(f) ? EXT_IO : n > TRUST_MAX_BYTES ? EXT_INVALID : EXT_OK;
  if (fclose(f) && !s)
    s = EXT_IO;
  if (!s) {
    yyjson_doc *doc = ext_parse(data, n);
    if (!doc)
      s = EXT_INVALID;
    else {
      yyjson_val *root = yyjson_doc_get_root(doc);
      yyjson_val *version = yyjson_obj_get(root, "apiVersion");
      yyjson_val *list = yyjson_obj_get(root, "records");
      if (!yyjson_is_obj(root) || yyjson_obj_size(root) != 2 ||
          !yyjson_equals_str(version, "ext.trust/v1") ||
          !yyjson_is_arr(list) || yyjson_arr_size(list) > TRUST_MAX_RECORDS)
        s = EXT_INVALID;
      else {
        size_t i, count;
        yyjson_val *entry;
        yyjson_arr_foreach(list, i, count, entry) {
          yyjson_val *sv = yyjson_obj_get(entry, "scope");
          yyjson_val *iv = yyjson_obj_get(entry, "id");
          yyjson_val *dv = yyjson_obj_get(entry, "digest");
          const char *scope = yyjson_get_str(sv), *id = yyjson_get_str(iv),
                     *digest = yyjson_get_str(dv);
          if (!yyjson_is_obj(entry) || yyjson_obj_size(entry) != 3 ||
              !key_valid(scope) || !key_valid(id) || !hex_valid(digest) ||
              find(out, scope, id) >= 0) {
            s = EXT_INVALID;
            break;
          }
          s = append(out, scope, id, digest);
          if (s)
            break;
        }
      }
      yyjson_doc_free(doc);
    }
  }
  free(data);
  if (s)
    clear(out);
  return s;
}
static int compare(const void *a, const void *b) {
  const record *x = a, *y = b;
  int scope = strcmp(x->scope, y->scope);
  return scope ? scope : strcmp(x->id, y->id);
}
static ext_status encode(records *r, char **out, size_t *length) {
  qsort(r->items, r->count, sizeof(*r->items), compare);
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  if (!doc)
    return EXT_NOMEM;
  yyjson_mut_val *root = yyjson_mut_obj(doc), *list = yyjson_mut_arr(doc);
  if (root)
    yyjson_mut_doc_set_root(doc, root);
  int ok = root && list &&
           yyjson_mut_obj_add_str(doc, root, "apiVersion", "ext.trust/v1") &&
           yyjson_mut_obj_add_val(doc, root, "records", list);
  for (size_t i = 0; ok && i < r->count; i++) {
    yyjson_mut_val *entry = yyjson_mut_obj(doc);
    ok = entry && yyjson_mut_obj_add_strcpy(doc, entry, "scope", r->items[i].scope) &&
         yyjson_mut_obj_add_strcpy(doc, entry, "id", r->items[i].id) &&
         yyjson_mut_obj_add_str(doc, entry, "digest", r->items[i].digest) &&
         yyjson_mut_arr_append(list, entry);
  }
  *out = ok ? yyjson_mut_write(doc, 0, length) : NULL;
  yyjson_mut_doc_free(doc);
  return *out ? EXT_OK : EXT_NOMEM;
}
#ifdef _WIN32
static wchar_t *wide(const char *s) {
  int n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, s, -1, NULL, 0);
  if (n <= 0)
    return NULL;
  wchar_t *w = malloc((size_t)n * sizeof(*w));
  if (w && !MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, s, -1, w, n)) {
    free(w);
    return NULL;
  }
  return w;
}
static int replace(const char *source, const char *target) {
  wchar_t *from = wide(source), *to = wide(target);
  int ok = from && to && MoveFileExW(from, to, MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH);
  free(from);
  free(to);
  return ok;
}
static void remove_temp(const char *path) {
  wchar_t *w = wide(path);
  if (w)
    DeleteFileW(w);
  free(w);
}
#else
static int replace(const char *source, const char *target) {
  return !rename(source, target);
}
static void remove_temp(const char *path) { remove(path); }
#endif
static ext_status save(const char *path, records *r) {
  char *data = NULL;
  size_t length = 0;
  ext_status s = encode(r, &data, &length);
  if (s)
    return s;
  if (length > TRUST_MAX_BYTES) {
    free(data);
    return EXT_CAPACITY;
  }
  uint8_t random[8];
  s = ext_random_bytes(random, sizeof(random));
  if (s) {
    free(data);
    return s;
  }
  char suffix[17];
  static const char digits[] = "0123456789abcdef";
  for (size_t i = 0; i < sizeof(random); i++) {
    suffix[2 * i] = digits[random[i] >> 4];
    suffix[2 * i + 1] = digits[random[i] & 15];
  }
  suffix[16] = 0;
  size_t n = strlen(path);
  char *tmp = malloc(n + sizeof(suffix) + 6);
  if (!tmp) {
    free(data);
    return EXT_NOMEM;
  }
  snprintf(tmp, n + sizeof(suffix) + 6, "%s.tmp-%s", path, suffix);
#ifdef _WIN32
  FILE *f = open_trust(tmp, 1);
#else
  int fd = open(tmp, O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
  FILE *f = fd < 0 ? NULL : fdopen(fd, "wb");
  if (!f && fd >= 0)
    close(fd);
#endif
  if (!f)
    s = EXT_IO;
  else {
    if (fwrite(data, 1, length, f) != length || fputc('\n', f) == EOF ||
        fflush(f))
      s = EXT_IO;
#ifndef _WIN32
    if (!s && fsync(fileno(f)))
      s = EXT_IO;
#else
    if (!s && _commit(_fileno(f)))
      s = EXT_IO;
#endif
    if (fclose(f) && !s)
      s = EXT_IO;
  }
  if (!s && !replace(tmp, path))
    s = EXT_IO;
  if (s)
    remove_temp(tmp);
  free(tmp);
  free(data);
  return s;
}
ext_status ext_trust_state_of(const char *path, const char *scope,
                              const char *id, const uint8_t current[32],
                              ext_trust_state *out) {
  if (!path || !*path || !key_valid(scope) || !key_valid(id) || !current || !out)
    return EXT_INVALID;
  records r = {0};
  ext_status s = load(path, &r);
  if (!s) {
    long index = find(&r, scope, id);
    if (index < 0)
      *out = EXT_TRUST_UNTRUSTED;
    else {
      char hex[65];
      ext_hash_hex(current, hex);
      *out = !strcmp(r.items[index].digest, hex) ? EXT_TRUST_TRUSTED : EXT_TRUST_CHANGED;
    }
  }
  clear(&r);
  return s;
}
ext_status ext_trust_record(const char *path, const char *scope,
                            const char *id, const uint8_t reviewed[32]) {
  if (!path || !*path || !key_valid(scope) || !key_valid(id) || !reviewed)
    return EXT_INVALID;
  records r = {0};
  ext_status s = load(path, &r);
  if (!s) {
    char hex[65];
    ext_hash_hex(reviewed, hex);
    long index = find(&r, scope, id);
    if (index < 0)
      s = append(&r, scope, id, hex);
    else
      memcpy(r.items[index].digest, hex, 65);
    if (!s)
      s = save(path, &r);
  }
  clear(&r);
  return s;
}
ext_status ext_trust_revoke(const char *path, const char *scope,
                            const char *id) {
  if (!path || !*path || !key_valid(scope) || !key_valid(id))
    return EXT_INVALID;
  records r = {0};
  ext_status s = load(path, &r);
  if (!s) {
    long index = find(&r, scope, id);
    if (index >= 0) {
      size_t at = (size_t)index;
      free(r.items[at].scope);
      free(r.items[at].id);
      memmove(&r.items[at], &r.items[at + 1], (r.count - at - 1) * sizeof(record));
      r.count--;
      s = save(path, &r);
    }
  }
  clear(&r);
  return s;
}
