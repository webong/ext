#define _POSIX_C_SOURCE 200809L
#include "fs.h"
#include "services.h"
#include "sha256.h"
#include "sync.h"
#include <stdlib.h>
#include <string.h>

/* The package store: install, upgrade, replace, remove and load. The rules are
 * those of the Go store (engine/host/store) and the two are held together by
 * differential tests. A package is installed by copying it into a staging
 * directory below the root, verifying the copy, and renaming it into place; an
 * upgrade moves the old directory aside first and puts it back if the new one
 * cannot take its place. */

#define STORE_MANIFEST "plugin.json"
#define STORE_MANIFEST_MAX (16u << 20)
#define STORE_TREE_DEPTH 1024u
#define STORE_ARTIFACT_BYTES (4ull << 30)
#define STORE_TREE_FILES 250000u
#define STORE_TREE_BYTES (16ull << 30)
#define STORE_STAGING ".install-"
#define STORE_BACKUP ".backup-"

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
/* The directory name of a package id: '/' is encoded as "%2f". */
static char *dir_name(const char *id) {
  size_t m = strlen(id);
  char *name = malloc(m * 3 + 1);
  if (!name)
    return NULL;
  size_t n = 0;
  for (size_t i = 0; i < m; i++) {
    if (id[i] == '/') {
      memcpy(name + n, "%2f", 3);
      n += 3;
    } else
      name[n++] = id[i];
  }
  name[n] = 0;
  return name;
}
static char *join(const char *base, const char *leaf) {
  size_t n = strlen(base), m = strlen(leaf);
  if (n > SIZE_MAX - m - 2)
    return NULL;
  char *path = malloc(n + m + 2);
  if (!path)
    return NULL;
  memcpy(path, base, n);
  if (n && path[n - 1] != '/' && path[n - 1] != '\\')
    path[n++] = '/';
  memcpy(path + n, leaf, m + 1);
  return path;
}
static char *package_dir(const char *root, const char *id) {
  char *name = dir_name(id);
  if (!name)
    return NULL;
  char *path = join(root, name);
  free(name);
  return path;
}
static const char *manifest_string(yyjson_val *m, const char *a, const char *b,
                                   const char *c) {
  yyjson_val *v = yyjson_obj_get(m, a);
  if (b)
    v = yyjson_obj_get(v, b);
  if (c)
    v = yyjson_obj_get(v, c);
  return yyjson_get_str(v);
}
static int is_tree(yyjson_val *artifact) {
  yyjson_val *kind = yyjson_obj_get(artifact, "kind");
  return yyjson_is_str(kind) && yyjson_equals_str(kind, "tree");
}

typedef struct {
  yyjson_val *manifest;
} extra_check;
static ext_status listed(void *user, const char *path,
                         const uint8_t sum[32]) {
  (void)sum;
  extra_check *ctx = user;
  if (ext_path_fold_equal(path, STORE_MANIFEST))
    return EXT_OK;
  yyjson_val *artifacts = yyjson_obj_get(ctx->manifest, "artifacts");
  size_t i, n;
  yyjson_val *artifact;
  yyjson_arr_foreach(artifacts, i, n, artifact) {
    const char *declared = yyjson_get_str(yyjson_obj_get(artifact, "path"));
    if (is_tree(artifact)) {
      if (ext_path_fold_tree_contains(declared, path))
        return EXT_OK;
    } else if (ext_path_fold_equal(declared, path))
      return EXT_OK;
  }
  return EXT_INVALID;
}
/* Verifies the package in the directory at dir: a valid manifest (whose id is
 * want_id when given, EXT_MISMATCH otherwise), every listed artifact, and no
 * other file. A package being installed may not list the manifest's own name as
 * an artifact. On success the manifest bytes and document are the caller's. */
static ext_status check_directory(const char *dir, const char *want_id,
                                  int source, ext_buffer *manifest,
                                  yyjson_doc **doc_out) {
  ext_fs *fs = NULL;
  ext_buffer bytes = {0};
  yyjson_doc *doc = NULL;
  yyjson_val *m = NULL;
  ext_status s = ext_fs_open(dir, &fs);
  if (!s)
    s = ext_fs_read_file(fs, STORE_MANIFEST, STORE_MANIFEST_MAX, &bytes);
  if (!s) {
    doc = ext_parse(bytes.data, bytes.len);
    m = doc ? yyjson_doc_get_root(doc) : NULL;
    s = doc ? ext_manifest_valid(m) : EXT_INVALID;
  }
  if (!s && source) {
    size_t i, n;
    yyjson_val *artifact;
    yyjson_arr_foreach(yyjson_obj_get(m, "artifacts"), i, n, artifact) {
      if (ext_path_fold_equal(
              yyjson_get_str(yyjson_obj_get(artifact, "path")), STORE_MANIFEST))
        s = EXT_INVALID;
    }
  }
  if (!s && want_id) {
    const char *id = manifest_string(m, "descriptor", "identity", "id");
    if (!id || strcmp(id, want_id))
      s = EXT_MISMATCH;
  }
  if (!s)
    s = ext_package_verify(bytes.data, bytes.len, dir);
  if (!s) {
    extra_check check = {m};
    s = ext_fs_walk(fs, "", listed, &check);
  }
  ext_fs_close(fs);
  if (s) {
    yyjson_doc_free(doc);
    ext_buffer_free(&bytes);
    return s;
  }
  *manifest = bytes;
  *doc_out = doc;
  return EXT_OK;
}

ext_status ext_store_digest(const char *root, const char *id, uint8_t out[32]) {
  if (!root || !*root || !valid_id(id) || !out)
    return EXT_INVALID;
  char *dir = package_dir(root, id);
  if (!dir)
    return EXT_NOMEM;
  ext_buffer manifest = {0};
  yyjson_doc *doc = NULL;
  ext_status s = check_directory(dir, id, 0, &manifest, &doc);
  if (!s) {
    s = ext_directory_digest(dir, out);
    yyjson_doc_free(doc);
    ext_buffer_free(&manifest);
  }
  free(dir);
  return s;
}

struct ext_store {
  char *root;
  ext_store_limits limits;
  pthread_mutex_t mu;
};

ext_status ext_store_open(const char *root, const ext_store_limits *limits,
                          ext_store **out) {
  if (!root || !*root || !out)
    return EXT_INVALID;
  ext_store *store = calloc(1, sizeof(*store));
  if (!store)
    return EXT_NOMEM;
  store->root = strdup(root);
  if (!store->root || pthread_mutex_init(&store->mu, NULL)) {
    free(store->root);
    free(store);
    return EXT_NOMEM;
  }
  if (limits)
    store->limits = *limits;
  if (!store->limits.max_artifact_bytes)
    store->limits.max_artifact_bytes = STORE_ARTIFACT_BYTES;
  if (!store->limits.max_tree_files)
    store->limits.max_tree_files = STORE_TREE_FILES;
  if (!store->limits.max_tree_bytes)
    store->limits.max_tree_bytes = STORE_TREE_BYTES;
  *out = store;
  return EXT_OK;
}
void ext_store_close(ext_store *store) {
  if (!store)
    return;
  pthread_mutex_destroy(&store->mu);
  free(store->root);
  free(store);
}
/* Opens the root, making it first when create is set. A root that is not there
 * is *missing with no error: an empty store. */
static ext_status open_root(const ext_store *store, int create, ext_fs **fs,
                            int *missing) {
  *fs = NULL;
  *missing = 0;
  if (create) {
    ext_status s = ext_fs_make_root(store->root);
    if (s)
      return s;
  } else {
    ext_fs_kind kind;
    ext_status s = ext_fs_probe(store->root, &kind);
    if (s)
      return s;
    if (kind == EXT_FS_NONE) {
      *missing = 1;
      return EXT_OK;
    }
  }
  return ext_fs_open(store->root, fs);
}
static char *under(const char *dir, const char *path) { return join(dir, path); }

/* Copies the files of a tree artifact, creating its directories and keeping the
 * owner-execute bit of a file that has it, within the store's bounds. */
typedef struct {
  const ext_store_limits *limits;
  uint32_t files;
  uint64_t total;
} tree_copy;
static int by_name(const void *a, const void *b) {
  return strcmp(((const ext_fs_entry *)a)->name, ((const ext_fs_entry *)b)->name);
}
static ext_status copy_tree(ext_fs *src, ext_fs *dst, const char *from,
                            const char *to, tree_copy *state, unsigned depth) {
  if (depth > STORE_TREE_DEPTH)
    return EXT_INVALID;
  ext_status s = ext_fs_mkdirs(dst, to);
  ext_fs_entry *entries = NULL;
  size_t count = 0;
  if (!s)
    s = ext_fs_list(src, from, &entries, &count);
  if (!s && count > 1)
    qsort(entries, count, sizeof(*entries), by_name);
  for (size_t i = 0; i < count && !s; i++) {
    char *source = under(from, entries[i].name);
    char *target = under(to, entries[i].name);
    if (!source || !target)
      s = EXT_NOMEM;
    else if (entries[i].kind == EXT_FS_DIR)
      s = copy_tree(src, dst, source, target, state, depth + 1);
    else if (entries[i].kind != EXT_FS_FILE)
      s = EXT_INVALID; /* trees may hold only regular files */
    else {
      uint32_t mode = 0;
      uint8_t sum[32];
      uint64_t size = 0;
      if (++state->files > state->limits->max_tree_files)
        s = EXT_INVALID;
      if (!s)
        s = ext_fs_mode_bits(src, source, &mode);
      if (!s)
        s = ext_fs_copy_file(src, source, dst, target, (mode & 0100) != 0,
                             state->limits->max_artifact_bytes, sum, &size);
      if (!s) {
        state->total += size;
        if (state->total > state->limits->max_tree_bytes)
          s = EXT_INVALID;
      }
    }
    free(source);
    free(target);
  }
  ext_fs_list_free(entries, count);
  return s;
}
static int is_entrypoint(yyjson_val *m, yyjson_val *artifact) {
  yyjson_val *name = yyjson_obj_get(artifact, "name");
  size_t i, n;
  yyjson_val *e;
  yyjson_arr_foreach(yyjson_obj_get(m, "entrypoints"), i, n, e) {
    yyjson_val *target = yyjson_obj_get(e, "artifact");
    if (yyjson_is_str(name) && yyjson_is_str(target) &&
        yyjson_get_len(name) == yyjson_get_len(target) &&
        !memcmp(yyjson_get_str(name), yyjson_get_str(target),
                yyjson_get_len(name)))
      return 1;
  }
  return 0;
}
/* Copies the package at source (already verified) into a new staging directory
 * and verifies the copy. *staging is its name below the root. On failure the
 * staging directory is removed. */
static ext_status stage(const ext_store *store, ext_fs *fs, const char *source,
                        yyjson_val *m, const ext_buffer *manifest,
                        uint32_t flags, char **staging) {
  ext_fs *src = NULL;
  char *name = NULL;
  ext_status s = ext_fs_open(source, &src);
  if (!s)
    s = ext_fs_mkdtemp(fs, "", STORE_STAGING, &name);
  if (!s) {
    size_t i, n;
    yyjson_val *artifact;
    yyjson_arr_foreach(yyjson_obj_get(m, "artifacts"), i, n, artifact) {
      const char *path = yyjson_get_str(yyjson_obj_get(artifact, "path"));
      char *target = under(name, path);
      if (!target) {
        s = EXT_NOMEM;
        break;
      }
      if (is_tree(artifact)) {
        tree_copy state = {&store->limits, 0, 0};
        s = copy_tree(src, fs, path, target, &state, 0);
      } else {
        int executable = is_entrypoint(m, artifact);
        if (!executable && (flags & EXT_STORE_PRESERVE_EXECUTABLE)) {
          uint32_t mode = 0;
          s = ext_fs_mode_bits(src, path, &mode);
          executable = (mode & 0111) != 0;
        }
        uint8_t sum[32];
        uint64_t size = 0;
        if (!s)
          s = ext_fs_copy_file(src, path, fs, target, executable,
                               store->limits.max_artifact_bytes, sum, &size);
        if (!s) {
          char hex[65];
          ext_hash_hex(sum, hex);
          if (!yyjson_equals_str(yyjson_obj_get(artifact, "sha256"), hex))
            s = EXT_MISMATCH; /* changed during install */
        }
      }
      free(target);
      if (s)
        break;
    }
  }
  if (!s) {
    char *target = under(name, STORE_MANIFEST);
    s = target ? ext_fs_write_file(fs, target, manifest->data, manifest->len, 0)
               : EXT_NOMEM;
    free(target);
  }
  if (!s) {
    char *dir = under(store->root, name);
    ext_buffer copy = {0};
    yyjson_doc *doc = NULL;
    s = dir ? check_directory(dir, NULL, 0, &copy, &doc) : EXT_NOMEM;
    if (!s) {
      yyjson_doc_free(doc);
      ext_buffer_free(&copy);
    }
    free(dir);
  }
  ext_fs_close(src);
  if (s) {
    if (name)
      ext_fs_remove_all(fs, name);
    free(name);
    return s;
  }
  *staging = name;
  return EXT_OK;
}
/* Moves the installed package aside, puts the staged one in its place, and puts
 * the old one back if that fails. The old package is kept, in its backup
 * directory, when it could not be put back. */
static ext_status swap_in(ext_fs *fs, const char *staging, const char *dest) {
  char *backup = NULL, *old = NULL;
  ext_status s = ext_fs_mkdtemp(fs, "", STORE_BACKUP, &backup);
  if (!s) {
    old = under(backup, "old");
    if (!old)
      s = EXT_NOMEM;
  }
  int safe = 1;
  if (!s)
    s = ext_fs_rename(fs, dest, old);
  if (!s) {
    s = ext_fs_rename(fs, staging, dest);
    if (s)
      safe = !ext_fs_rename(fs, old, dest);
  }
  if (backup && safe)
    ext_fs_remove_all(fs, backup);
  free(old);
  free(backup);
  return s;
}
static int same_contracts(yyjson_val *a, yyjson_val *b) {
  yyjson_val *ca = yyjson_obj_get(yyjson_obj_get(a, "descriptor"), "contracts");
  yyjson_val *cb = yyjson_obj_get(yyjson_obj_get(b, "descriptor"), "contracts");
  size_t n = yyjson_arr_size(ca);
  unsigned char used[64] = {0};
  if (n != yyjson_arr_size(cb) || n > sizeof(used))
    return 0;
  size_t i, count, j, other;
  yyjson_val *x, *y;
  yyjson_arr_foreach(ca, i, count, x) {
    int found = 0;
    yyjson_arr_foreach(cb, j, other, y) {
      if (!used[j] && ext_same_ref(x, y)) {
        used[j] = 1;
        found = 1;
        break;
      }
    }
    if (!found)
      return 0;
  }
  return 1;
}
/* An upgrade keeps the id and the contracts and changes the revision. */
static ext_status check_upgrade(ext_fs *fs, const char *name, yyjson_val *fresh) {
  char *path = under(name, STORE_MANIFEST);
  if (!path)
    return EXT_NOMEM;
  ext_buffer bytes = {0};
  ext_status s = ext_fs_read_file(fs, path, STORE_MANIFEST_MAX, &bytes);
  free(path);
  yyjson_doc *doc = NULL;
  yyjson_val *old = NULL;
  if (!s) {
    doc = ext_parse(bytes.data, bytes.len);
    old = doc ? yyjson_doc_get_root(doc) : NULL;
    s = doc ? ext_manifest_valid(old) : EXT_INVALID;
  }
  if (!s) {
    const char *a = manifest_string(old, "descriptor", "identity", "id");
    const char *b = manifest_string(fresh, "descriptor", "identity", "id");
    if (!a || !b || strcmp(a, b) || !same_contracts(old, fresh))
      s = EXT_MISMATCH;
  }
  if (!s) {
    const char *a = manifest_string(old, "descriptor", "identity", "revision");
    const char *b = manifest_string(fresh, "descriptor", "identity", "revision");
    if (a && b && !strcmp(a, b))
      s = EXT_INVALID; /* an upgrade needs a new revision */
  }
  yyjson_doc_free(doc);
  ext_buffer_free(&bytes);
  return s;
}

enum { PUT_INSTALL, PUT_UPGRADE, PUT_REPLACE };
static ext_status put(ext_store *store, int mode, const char *source,
                      uint32_t flags, ext_buffer *out) {
  if (!store || !source || !*source || !out ||
      (flags & ~EXT_STORE_PRESERVE_EXECUTABLE))
    return EXT_INVALID;
  *out = (ext_buffer){0};
  pthread_mutex_lock(&store->mu);
  ext_buffer manifest = {0};
  yyjson_doc *doc = NULL;
  ext_fs *fs = NULL;
  char *name = NULL, *staging = NULL;
  int missing = 0, replacing = 0;
  ext_fs_kind kind = EXT_FS_NONE;
  ext_status s = check_directory(source, NULL, 1, &manifest, &doc);
  yyjson_val *m = doc ? yyjson_doc_get_root(doc) : NULL;
  if (!s) {
    name = dir_name(manifest_string(m, "descriptor", "identity", "id"));
    if (!name)
      s = EXT_NOMEM;
  }
  if (!s)
    s = open_root(store, mode != PUT_UPGRADE, &fs, &missing);
  if (!s && !missing)
    s = ext_fs_kind_of(fs, name, &kind);
  if (!s) {
    switch (mode) {
    case PUT_INSTALL:
      if (kind != EXT_FS_NONE)
        s = EXT_EXISTS;
      break;
    case PUT_UPGRADE:
      if (kind == EXT_FS_NONE)
        s = EXT_NOT_FOUND;
      else if (kind != EXT_FS_DIR)
        s = EXT_INVALID;
      else
        s = check_upgrade(fs, name, m);
      replacing = 1;
      break;
    default:
      if (kind == EXT_FS_DIR)
        replacing = 1;
      else if (kind != EXT_FS_NONE)
        s = EXT_INVALID;
    }
  }
  if (!s)
    s = stage(store, fs, source, m, &manifest, flags, &staging);
  if (!s)
    s = replacing ? swap_in(fs, staging, name) : ext_fs_rename(fs, staging, name);
  if (s && staging)
    ext_fs_remove_all(fs, staging);
  ext_fs_close(fs);
  yyjson_doc_free(doc);
  free(name);
  free(staging);
  pthread_mutex_unlock(&store->mu);
  if (s) {
    ext_buffer_free(&manifest);
    return s;
  }
  *out = manifest;
  return EXT_OK;
}
ext_status ext_store_install(ext_store *store, const char *source,
                             uint32_t flags, ext_buffer *manifest) {
  return put(store, PUT_INSTALL, source, flags, manifest);
}
ext_status ext_store_upgrade(ext_store *store, const char *source,
                             uint32_t flags, ext_buffer *manifest) {
  return put(store, PUT_UPGRADE, source, flags, manifest);
}
ext_status ext_store_replace(ext_store *store, const char *source,
                             uint32_t flags, ext_buffer *manifest) {
  return put(store, PUT_REPLACE, source, flags, manifest);
}
/* The directory of an installed id, which must be a directory. */
static ext_status installed_dir(ext_store *store, const char *id, ext_fs **fs,
                                char **name) {
  int missing = 0;
  *name = NULL;
  ext_status s = open_root(store, 0, fs, &missing);
  if (!s && missing)
    s = EXT_NOT_FOUND;
  if (!s) {
    *name = dir_name(id);
    if (!*name)
      s = EXT_NOMEM;
  }
  ext_fs_kind kind = EXT_FS_NONE;
  if (!s)
    s = ext_fs_kind_of(*fs, *name, &kind);
  if (!s && kind == EXT_FS_NONE)
    s = EXT_NOT_FOUND;
  else if (!s && kind != EXT_FS_DIR)
    s = EXT_INVALID;
  return s;
}
ext_status ext_store_remove(ext_store *store, const char *id) {
  if (!store || !valid_id(id))
    return EXT_INVALID;
  pthread_mutex_lock(&store->mu);
  ext_fs *fs = NULL;
  char *name = NULL, *path = NULL;
  ext_status s = installed_dir(store, id, &fs, &name);
  ext_buffer bytes = {0};
  if (!s) {
    path = under(name, STORE_MANIFEST);
    s = path ? ext_fs_read_file(fs, path, STORE_MANIFEST_MAX, &bytes) : EXT_NOMEM;
  }
  yyjson_doc *doc = NULL;
  if (!s) {
    doc = ext_parse(bytes.data, bytes.len);
    yyjson_val *m = doc ? yyjson_doc_get_root(doc) : NULL;
    s = doc ? ext_manifest_valid(m) : EXT_INVALID;
    if (!s) {
      const char *found = manifest_string(m, "descriptor", "identity", "id");
      if (!found || strcmp(found, id))
        s = EXT_MISMATCH;
    }
  }
  if (!s)
    s = ext_fs_remove_all(fs, name);
  yyjson_doc_free(doc);
  ext_buffer_free(&bytes);
  free(path);
  free(name);
  ext_fs_close(fs);
  pthread_mutex_unlock(&store->mu);
  return s;
}
ext_status ext_store_load(ext_store *store, const char *id,
                          ext_buffer *manifest) {
  if (!store || !valid_id(id) || !manifest)
    return EXT_INVALID;
  *manifest = (ext_buffer){0};
  pthread_mutex_lock(&store->mu);
  ext_fs *fs = NULL;
  char *name = NULL, *dir = NULL;
  ext_status s = installed_dir(store, id, &fs, &name);
  yyjson_doc *doc = NULL;
  ext_buffer bytes = {0};
  if (!s) {
    dir = under(store->root, name);
    s = dir ? check_directory(dir, id, 0, &bytes, &doc) : EXT_NOMEM;
  }
  if (!s) {
    yyjson_doc_free(doc);
    *manifest = bytes;
  }
  free(dir);
  free(name);
  ext_fs_close(fs);
  pthread_mutex_unlock(&store->mu);
  return s;
}
static int by_id(const void *a, const void *b) {
  return strcmp(*(char *const *)a, *(char *const *)b);
}
ext_status ext_store_ids(ext_store *store, ext_buffer *ids) {
  if (!store || !ids)
    return EXT_INVALID;
  *ids = (ext_buffer){0};
  pthread_mutex_lock(&store->mu);
  ext_fs *fs = NULL;
  int missing = 0;
  ext_fs_entry *entries = NULL;
  size_t count = 0;
  ext_status s = open_root(store, 0, &fs, &missing);
  if (!s && !missing)
    s = ext_fs_list(fs, "", &entries, &count);
  char **found = count ? calloc(count, sizeof(*found)) : NULL;
  size_t used = 0, bytes = 2;
  if (!s && count && !found)
    s = EXT_NOMEM;
  for (size_t i = 0; i < count && !s; i++) {
    const char *entry = entries[i].name;
    if (entry[0] == '.')
      continue; /* a staging directory */
    if (entries[i].kind != EXT_FS_DIR) {
      s = EXT_INVALID;
      break;
    }
    size_t m = strlen(entry);
    char *id = malloc(m + 1);
    if (!id) {
      s = EXT_NOMEM;
      break;
    }
    size_t n = 0;
    for (size_t j = 0; j < m; j++) {
      if (!strncmp(entry + j, "%2f", 3)) {
        id[n++] = '/';
        j += 2;
      } else
        id[n++] = entry[j];
    }
    id[n] = 0;
    if (!valid_id(id)) {
      free(id);
      s = EXT_INVALID;
      break;
    }
    found[used++] = id;
    bytes += n + 3;
  }
  if (!s && used > 1)
    qsort(found, used, sizeof(*found), by_id);
  uint8_t *json = NULL;
  size_t length = 0;
  if (!s) {
    json = malloc(bytes + 1);
    if (!json)
      s = EXT_NOMEM;
  }
  if (!s) {
    json[length++] = '[';
    for (size_t i = 0; i < used; i++) {
      size_t n = strlen(found[i]);
      if (i)
        json[length++] = ',';
      json[length++] = '"';
      memcpy(json + length, found[i], n);
      length += n;
      json[length++] = '"';
    }
    json[length++] = ']';
    *ids = (ext_buffer){json, length};
  }
  for (size_t i = 0; i < used; i++)
    free(found[i]);
  free(found);
  ext_fs_list_free(entries, count);
  ext_fs_close(fs);
  pthread_mutex_unlock(&store->mu);
  return s;
}
