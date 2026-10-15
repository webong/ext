#include "fs.h"
#include "services.h"
#include "sha256.h"
#include <stdlib.h>
#include <string.h>

/* Package integrity: manifest validation, artifact digests and directory
 * digests. The operating-system access is in fs_posix.c and fs_win32.c; the
 * application must protect the root against modification while it is read. */

typedef struct {
  char *path;
  uint8_t sum[32];
} file;
typedef struct {
  file *files;
  size_t count, capacity;
} listing;

static ext_status collect(void *user, const char *path, const uint8_t sum[32]) {
  listing *list = user;
  if (list->count == list->capacity) {
    size_t capacity = list->capacity ? list->capacity * 2 : 64;
    if (capacity <= list->capacity || capacity > SIZE_MAX / sizeof(file))
      return EXT_NOMEM;
    file *files = realloc(list->files, capacity * sizeof(file));
    if (!files)
      return EXT_NOMEM;
    list->files = files;
    list->capacity = capacity;
  }
  file *f = &list->files[list->count];
  f->path = strdup(path);
  if (!f->path)
    return EXT_NOMEM;
  memcpy(f->sum, sum, 32);
  list->count++;
  return EXT_OK;
}
static int compare(const void *a, const void *b) {
  return strcmp(((const file *)a)->path, ((const file *)b)->path);
}
/* Digest of every regular file under the directory at path, as
 * plugin.DirectoryDigest computes it. A tree artifact must hold at least one
 * file. */
static ext_status digest_directory(ext_fs *fs, const char *path,
                                   uint8_t out[32], int need_files) {
  listing list = {0};
  ext_status s = ext_fs_walk(fs, path, collect, &list);
  if (!s && need_files && !list.count)
    s = EXT_INVALID;
  if (!s) {
    if (list.count > 1)
      qsort(list.files, list.count, sizeof(file), compare);
    ext_hash hash;
    ext_hash_init(&hash);
    for (size_t i = 0; i < list.count; i++) {
      char hex[65];
      ext_hash_hex(list.files[i].sum, hex);
      ext_hash_update(&hash, (const uint8_t *)"./", 2);
      ext_hash_update(&hash, (const uint8_t *)list.files[i].path,
                      strlen(list.files[i].path));
      ext_hash_update(&hash, (const uint8_t *)" ", 1);
      ext_hash_update(&hash, (const uint8_t *)hex, 64);
      ext_hash_update(&hash, (const uint8_t *)"\n", 1);
    }
    ext_hash_finish(&hash, out);
  }
  for (size_t i = 0; i < list.count; i++)
    free(list.files[i].path);
  free(list.files);
  return s;
}

ext_status ext_package_verify(const uint8_t *manifest, size_t len,
                              const char *root) {
  yyjson_doc *doc = ext_parse(manifest, len);
  if (!doc)
    return EXT_INVALID;
  yyjson_val *m = yyjson_doc_get_root(doc);
  ext_status s = ext_manifest_valid(m);
  ext_fs *fs = NULL;
  if (!s)
    s = ext_fs_open(root, &fs);
  if (!s) {
    size_t i, n;
    yyjson_val *a;
    yyjson_arr_foreach(yyjson_obj_get(m, "artifacts"), i, n, a) {
      uint8_t sum[32];
      char hex[65];
      yyjson_val *kind = yyjson_obj_get(a, "kind");
      const char *path = yyjson_get_str(yyjson_obj_get(a, "path"));
      s = yyjson_is_str(kind) && yyjson_equals_str(kind, "tree")
              ? digest_directory(fs, path, sum, 1)
              : ext_fs_hash_file(fs, path, sum);
      if (s)
        break;
      ext_hash_hex(sum, hex);
      if (!yyjson_equals_str(yyjson_obj_get(a, "sha256"), hex)) {
        s = EXT_MISMATCH;
        break;
      }
    }
  }
  ext_fs_close(fs);
  yyjson_doc_free(doc);
  return s;
}

ext_status ext_directory_digest(const char *root, uint8_t out[32]) {
  if (!out)
    return EXT_INVALID;
  ext_fs *fs = NULL;
  ext_status s = ext_fs_open(root, &fs);
  if (!s) {
    s = digest_directory(fs, "", out, 0);
    ext_fs_close(fs);
  }
  return s;
}

/* Digest of one artifact under root, addressed by a portable relative path and
 * never following symbolic links. A file artifact is its SHA-256; a tree
 * artifact is the directory digest of the tree, which must hold at least one
 * file. */
ext_status ext_artifact_digest(const char *root, const char *path, int tree,
                               uint8_t out[32]) {
  if (!root || !path || !out || !ext_portable_path(path, strlen(path)))
    return EXT_INVALID;
  ext_fs *fs = NULL;
  ext_status s = ext_fs_open(root, &fs);
  if (!s) {
    s = tree ? digest_directory(fs, path, out, 1)
             : ext_fs_hash_file(fs, path, out);
    ext_fs_close(fs);
  }
  return s;
}
