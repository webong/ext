#define _POSIX_C_SOURCE 200809L
#include "fs.h"
#include "services.h"
#include <stdlib.h>
#include <string.h>

#define STORE_MANIFEST_MAX (16u << 20)

static int valid_id(const char *id) {
  if (!id || !*id)
    return 0;
  size_t n = strlen(id);
  if (n > 256 || !((id[0] >= 'a' && id[0] <= 'z') ||
                   (id[0] >= '0' && id[0] <= '9')))
    return 0;
  for (size_t i = 1; i < n; i++) {
    char c = id[i];
    if (!((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
          c == '.' || c == '_' || c == '/' || c == '-'))
      return 0;
  }
  return 1;
}
static char *package_dir(const char *root, const char *id) {
  size_t n = strlen(root), m = strlen(id);
  if (n > SIZE_MAX - m * 3 - 2)
    return NULL;
  char *path = malloc(n + m * 3 + 2);
  if (!path)
    return NULL;
  memcpy(path, root, n);
  if (n && path[n - 1] != '/' && path[n - 1] != '\\')
    path[n++] = '/';
  for (size_t i = 0; i < m; i++) {
    if (id[i] == '/') {
      memcpy(path + n, "%2f", 3);
      n += 3;
    } else
      path[n++] = id[i];
  }
  path[n] = 0;
  return path;
}
typedef struct {
  yyjson_val *manifest;
} extra_check;
static ext_status listed(void *user, const char *path,
                         const uint8_t sum[32]) {
  (void)sum;
  extra_check *ctx = user;
  if (ext_path_fold_equal(path, "plugin.json"))
    return EXT_OK;
  yyjson_val *artifacts = yyjson_obj_get(ctx->manifest, "artifacts");
  size_t i, n;
  yyjson_val *artifact;
  yyjson_arr_foreach(artifacts, i, n, artifact) {
    const char *declared = yyjson_get_str(yyjson_obj_get(artifact, "path"));
    yyjson_val *kind = yyjson_obj_get(artifact, "kind");
    if (yyjson_is_str(kind) && yyjson_equals_str(kind, "tree")) {
      if (ext_path_fold_tree_contains(declared, path))
        return EXT_OK;
    } else if (ext_path_fold_equal(declared, path))
      return EXT_OK;
  }
  return EXT_INVALID;
}
ext_status ext_store_digest(const char *root, const char *id, uint8_t out[32]) {
  if (!root || !*root || !valid_id(id) || !out)
    return EXT_INVALID;
  char *dir = package_dir(root, id);
  if (!dir)
    return EXT_NOMEM;
  ext_fs *fs = NULL;
  ext_status s = ext_fs_open(dir, &fs);
  ext_buffer manifest = {0};
  if (!s)
    s = ext_fs_read_file(fs, "plugin.json", STORE_MANIFEST_MAX, &manifest);
  yyjson_doc *doc = NULL;
  yyjson_val *m = NULL;
  if (!s) {
    doc = ext_parse(manifest.data, manifest.len);
    m = doc ? yyjson_doc_get_root(doc) : NULL;
    s = doc ? ext_manifest_valid(m) : EXT_INVALID;
  }
  if (!s) {
    yyjson_val *descriptor = yyjson_obj_get(m, "descriptor");
    yyjson_val *identity = yyjson_obj_get(descriptor, "identity");
    if (!yyjson_equals_str(yyjson_obj_get(identity, "id"), id))
      s = EXT_MISMATCH;
  }
  if (!s)
    s = ext_package_verify(manifest.data, manifest.len, dir);
  if (!s) {
    extra_check check = {m};
    s = ext_fs_walk(fs, "", listed, &check);
  }
  if (!s)
    s = ext_directory_digest(dir, out);
  yyjson_doc_free(doc);
  ext_buffer_free(&manifest);
  ext_fs_close(fs);
  free(dir);
  return s;
}
