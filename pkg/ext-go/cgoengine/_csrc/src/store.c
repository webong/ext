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
 * other file. With content clear only the manifest and its id are checked and
 * no artifact is read or hashed. A package being installed may not list the
 * manifest's own name as an artifact. On success the manifest bytes and
 * document are the caller's. */
static ext_status check_directory(const char *dir, const char *want_id,
                                  int source, int content, ext_buffer *manifest,
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
  if (!s && content)
    s = ext_package_verify(bytes.data, bytes.len, dir);
  if (!s && content) {
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
  ext_status s = check_directory(dir, id, 0, 1, &manifest, &doc);
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
    s = dir ? check_directory(dir, NULL, 0, 1, &copy, &doc) : EXT_NOMEM;
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
  ext_status s = check_directory(source, NULL, 1, 1, &manifest, &doc);
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
/* Reads one installed package. The caller holds the store's lock. With content
 * set every artifact is verified; without it only the manifest is read and
 * checked against the directory name. */
static ext_status read_package(ext_store *store, const char *id, int content,
                               ext_buffer *manifest) {
  *manifest = (ext_buffer){0};
  ext_fs *fs = NULL;
  char *name = NULL, *dir = NULL;
  ext_status s = installed_dir(store, id, &fs, &name);
  yyjson_doc *doc = NULL;
  ext_buffer bytes = {0};
  if (!s) {
    dir = under(store->root, name);
    s = dir ? check_directory(dir, id, 0, content, &bytes, &doc) : EXT_NOMEM;
  }
  if (!s) {
    yyjson_doc_free(doc);
    *manifest = bytes;
  }
  free(dir);
  free(name);
  ext_fs_close(fs);
  return s;
}
ext_status ext_store_load(ext_store *store, const char *id,
                          ext_buffer *manifest) {
  if (!store || !valid_id(id) || !manifest)
    return EXT_INVALID;
  pthread_mutex_lock(&store->mu);
  ext_status s = read_package(store, id, 1, manifest);
  pthread_mutex_unlock(&store->mu);
  return s;
}

/* One name found directly below a root. id is set when the name is a package
 * directory; err says why it is not. */
typedef struct {
  char *name;
  char *id;
  ext_status err;
} scanned;
static void scanned_free(scanned *e, size_t n) {
  for (size_t i = 0; i < n; i++) {
    free(e[i].name);
    free(e[i].id);
  }
  free(e);
}
static int by_scanned(const void *a, const void *b) {
  return strcmp(((const scanned *)a)->name, ((const scanned *)b)->name);
}
/* The package id a directory name stands for: "%2f" is '/'. */
static char *decode_id(const char *name) {
  size_t m = strlen(name), n = 0;
  char *id = malloc(m + 1);
  if (!id)
    return NULL;
  for (size_t j = 0; j < m; j++) {
    if (!strncmp(name + j, "%2f", 3)) {
      id[n++] = '/';
      j += 2;
    } else
      id[n++] = name[j];
  }
  id[n] = 0;
  return id;
}
/* Lists the names below the root, sorted by name, without reading any package.
 * A missing root has none and a staging directory (a leading ".") is skipped.
 * The caller holds the store's lock. */
static ext_status scan_locked(ext_store *store, scanned **out, size_t *count) {
  *out = NULL;
  *count = 0;
  ext_fs *fs = NULL;
  int missing = 0;
  ext_fs_entry *entries = NULL;
  size_t n = 0, used = 0;
  ext_status s = open_root(store, 0, &fs, &missing);
  if (!s && !missing)
    s = ext_fs_list(fs, "", &entries, &n);
  scanned *found = n ? calloc(n, sizeof(*found)) : NULL;
  if (!s && n && !found)
    s = EXT_NOMEM;
  for (size_t i = 0; i < n && !s; i++) {
    if (entries[i].name[0] == '.')
      continue;
    scanned *e = &found[used];
    e->name = strdup(entries[i].name);
    if (!e->name) {
      s = EXT_NOMEM;
      break;
    }
    used++;
    if (entries[i].kind != EXT_FS_DIR) {
      e->err = EXT_INVALID;
      continue;
    }
    e->id = decode_id(e->name);
    if (!e->id)
      s = EXT_NOMEM;
    else if (!valid_id(e->id)) {
      free(e->id);
      e->id = NULL;
      e->err = EXT_INVALID;
    }
  }
  ext_fs_list_free(entries, n);
  ext_fs_close(fs);
  if (s) {
    scanned_free(found, used);
    return s;
  }
  if (used > 1)
    qsort(found, used, sizeof(*found), by_scanned);
  *out = found;
  *count = used;
  return EXT_OK;
}

static int by_id(const void *a, const void *b) {
  return strcmp(*(char *const *)a, *(char *const *)b);
}
ext_status ext_store_ids(ext_store *store, ext_buffer *ids) {
  if (!store || !ids)
    return EXT_INVALID;
  *ids = (ext_buffer){0};
  pthread_mutex_lock(&store->mu);
  scanned *entries = NULL;
  size_t count = 0;
  ext_status s = scan_locked(store, &entries, &count);
  char **found = count ? calloc(count, sizeof(*found)) : NULL;
  size_t used = 0, bytes = 2;
  if (!s && count && !found)
    s = EXT_NOMEM;
  for (size_t i = 0; i < count && !s; i++) {
    if (entries[i].err) {
      s = entries[i].err;
      break;
    }
    found[used++] = entries[i].id;
    bytes += strlen(entries[i].id) + 3;
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
  free(found);
  scanned_free(entries, count);
  pthread_mutex_unlock(&store->mu);
  return s;
}

/* A path made lexically clean the way Go's filepath.Clean does it: repeated
 * separators, "." and "x/.." collapse, and the result is "." for nothing. On
 * Windows both separators count and a drive prefix is kept. */
#ifdef _WIN32
#define PATH_SEP '\\'
static int is_sep(char c) { return c == '/' || c == '\\'; }
#else
#define PATH_SEP '/'
static int is_sep(char c) { return c == '/'; }
#endif
static char *clean_path(const char *path) {
  size_t n = strlen(path), w = 0, r = 0, volume = 0;
  char *out = malloc(n + 4);
  if (!out)
    return NULL;
#ifdef _WIN32
  if (n >= 2 && path[1] == ':' &&
      ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')))
    volume = 2;
#endif
  memcpy(out, path, volume);
  w = r = volume;
  int rooted = r < n && is_sep(path[r]);
  size_t dotdot = w;
  if (rooted) {
    out[w++] = PATH_SEP;
    r++;
    dotdot = w;
  }
  while (r < n) {
    if (is_sep(path[r]))
      r++;
    else if (path[r] == '.' && (r + 1 == n || is_sep(path[r + 1])))
      r++;
    else if (path[r] == '.' && path[r + 1] == '.' &&
             (r + 2 == n || is_sep(path[r + 2]))) {
      r += 2;
      if (w > dotdot) {
        w--;
        while (w > dotdot && !is_sep(out[w]))
          w--;
      } else if (!rooted) {
        if (w > volume)
          out[w++] = PATH_SEP;
        out[w++] = '.';
        out[w++] = '.';
        dotdot = w;
      }
    } else {
      if ((rooted && w != volume + 1) || (!rooted && w != volume))
        out[w++] = PATH_SEP;
      while (r < n && !is_sep(path[r]))
        out[w++] = path[r++];
    }
  }
  if (w == 0)
    out[w++] = '.';
  out[w] = 0;
  return out;
}
/* The cleaned path of an entry below a root. */
static char *entry_path(const char *root, const char *name) {
  char *joined = join(root, name);
  if (!joined)
    return NULL;
  char *clean = clean_path(joined);
  free(joined);
  return clean;
}

/* A set of ids, for the ids a higher layer has already claimed. */
typedef struct {
  char **v;
  size_t n, cap;
} idset;
static int idset_has(const idset *set, const char *id) {
  for (size_t i = 0; i < set->n; i++)
    if (!strcmp(set->v[i], id))
      return 1;
  return 0;
}
static ext_status idset_add(idset *set, const char *id) {
  if (set->n == set->cap) {
    size_t cap = set->cap ? set->cap * 2 : 16;
    if (cap > SIZE_MAX / sizeof(*set->v))
      return EXT_NOMEM;
    char **v = realloc(set->v, cap * sizeof(*v));
    if (!v)
      return EXT_NOMEM;
    set->v = v;
    set->cap = cap;
  }
  char *copy = strdup(id);
  if (!copy)
    return EXT_NOMEM;
  set->v[set->n++] = copy;
  return EXT_OK;
}
static void idset_free(idset *set) {
  for (size_t i = 0; i < set->n; i++)
    free(set->v[i]);
  free(set->v);
  *set = (idset){0};
}

/* One line of a report: a package (status EXT_OK, with its manifest when it
 * was read) or an entry that failed. */
typedef struct {
  char *id;
  char *dir;
  ext_status status;
  size_t layer;
  size_t seq;
  ext_buffer manifest;
} row;
typedef struct {
  row *v;
  size_t n, cap;
} rows;
/* Takes the row's strings and manifest, also when it fails. */
static ext_status rows_add(rows *r, char *id, char *dir, ext_status status,
                           size_t layer, ext_buffer manifest) {
  if (r->n == r->cap) {
    size_t cap = r->cap ? r->cap * 2 : 16;
    row *v = cap > SIZE_MAX / sizeof(*v) ? NULL : realloc(r->v, cap * sizeof(*v));
    if (!v) {
      free(id);
      free(dir);
      ext_buffer_free(&manifest);
      return EXT_NOMEM;
    }
    r->v = v;
    r->cap = cap;
  }
  r->v[r->n] = (row){id, dir, status, layer, r->n, manifest};
  r->n++;
  return EXT_OK;
}
static void rows_free(rows *r) {
  for (size_t i = 0; i < r->n; i++) {
    free(r->v[i].id);
    free(r->v[i].dir);
    ext_buffer_free(&r->v[i].manifest);
  }
  free(r->v);
  *r = (rows){0};
}
/* By id, then layer, then the order the rows were found in. */
static int row_cmp(const void *a, const void *b) {
  const row *x = a, *y = b;
  int c = strcmp(x->id, y->id);
  if (c)
    return c;
  if (x->layer != y->layer)
    return x->layer < y->layer ? -1 : 1;
  return x->seq < y->seq ? -1 : x->seq > y->seq;
}

typedef enum { SHAPE_PACKAGES, SHAPE_CHECKS, SHAPE_UNVERIFIED } report_shape;
static int add_row(yyjson_mut_doc *d, yyjson_mut_val *list, const row *r,
                   report_shape shape) {
  yyjson_mut_val *o = yyjson_mut_obj(d);
  if (!o || !yyjson_mut_obj_add_strcpy(d, o, "id", r->id) ||
      !yyjson_mut_obj_add_strcpy(d, o, "directory", r->dir) ||
      !yyjson_mut_obj_add_uint(d, o, "layer", r->layer))
    return 0;
  if (shape != SHAPE_CHECKS && r->status == EXT_OK) {
    yyjson_mut_val *m =
        yyjson_mut_rawncpy(d, (const char *)r->manifest.data, r->manifest.len);
    if (!m || !yyjson_mut_obj_add_val(d, o, "manifest", m))
      return 0;
  } else if (!yyjson_mut_obj_add_int(d, o, "status", (int64_t)r->status))
    return 0;
  return yyjson_mut_arr_add_val(list, o);
}
/* Sorts the rows and writes them as the JSON a report has. */
static ext_status emit(rows *r, report_shape shape, ext_buffer *out) {
  *out = (ext_buffer){0};
  if (r->n > 1)
    qsort(r->v, r->n, sizeof(*r->v), row_cmp);
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  if (!d)
    return EXT_NOMEM;
  yyjson_mut_val *packages = yyjson_mut_arr(d), *problems = NULL, *root = packages;
  int ok = packages != NULL;
  if (ok && shape == SHAPE_UNVERIFIED) {
    problems = yyjson_mut_arr(d);
    root = yyjson_mut_obj(d);
    ok = problems && root && yyjson_mut_obj_add_val(d, root, "packages", packages) &&
         yyjson_mut_obj_add_val(d, root, "problems", problems);
  }
  for (size_t i = 0; ok && i < r->n; i++) {
    int failed = shape == SHAPE_UNVERIFIED && r->v[i].status != EXT_OK;
    ok = add_row(d, failed ? problems : packages, &r->v[i], shape);
  }
  if (ok) {
    yyjson_mut_doc_set_root(d, root);
    size_t len = 0;
    char *text = yyjson_mut_write(d, YYJSON_WRITE_ALLOW_INVALID_UNICODE, &len);
    if (text)
      *out = (ext_buffer){(uint8_t *)text, len};
  }
  yyjson_mut_doc_free(d);
  return out->data ? EXT_OK : EXT_NOMEM;
}

typedef enum { COLLECT_VERIFY, COLLECT_UNVERIFIED } collect_mode;
/* Checks every entry below each store's root, the highest-priority store first.
 * An id that a higher store holds is skipped in the lower ones, so a package is
 * checked where it would be served from. An entry that is not a package
 * directory is reported in the store it sits in. A failed package does not stop
 * the others; the status is only for a root that cannot be read. */
static ext_status collect(ext_store *const *stores, size_t count,
                          collect_mode mode, rows *out) {
  idset claimed = {0};
  ext_status s = EXT_OK;
  for (size_t layer = 0; layer < count && !s; layer++) {
    ext_store *store = stores[layer];
    scanned *entries = NULL;
    size_t n = 0;
    pthread_mutex_lock(&store->mu);
    s = scan_locked(store, &entries, &n);
    for (size_t i = 0; i < n && !s; i++) {
      scanned *e = &entries[i];
      if (!e->err && idset_has(&claimed, e->id))
        continue;
      char *id = strdup(e->id ? e->id : e->name);
      char *dir = entry_path(store->root, e->name);
      if (!id || !dir) {
        free(id);
        free(dir);
        s = EXT_NOMEM;
        break;
      }
      ext_buffer manifest = {0};
      ext_status st = e->err;
      if (!st)
        st = read_package(store, e->id, mode == COLLECT_VERIFY, &manifest);
      if (mode == COLLECT_VERIFY)
        ext_buffer_free(&manifest);
      s = rows_add(out, id, dir, st, layer, manifest);
    }
    pthread_mutex_unlock(&store->mu);
    for (size_t i = 0; i < n && !s; i++)
      if (entries[i].id)
        s = idset_add(&claimed, entries[i].id);
    scanned_free(entries, n);
  }
  idset_free(&claimed);
  return s;
}

typedef struct {
  char *id;
  size_t layer;
} winner;
static int by_winner(const void *a, const void *b) {
  return strcmp(((const winner *)a)->id, ((const winner *)b)->id);
}
/* Verifies and returns one package per id, each from the highest store that
 * holds it. It fails on the first entry that is not a package directory and on
 * the first package that does not verify. */
static ext_status collect_list(ext_store *const *stores, size_t count,
                               rows *out) {
  idset claimed = {0};
  winner *won = NULL;
  size_t used = 0, cap = 0;
  ext_status s = EXT_OK;
  for (size_t layer = 0; layer < count && !s; layer++) {
    ext_store *store = stores[layer];
    scanned *entries = NULL;
    size_t n = 0;
    pthread_mutex_lock(&store->mu);
    s = scan_locked(store, &entries, &n);
    pthread_mutex_unlock(&store->mu);
    for (size_t i = 0; i < n && !s; i++) {
      if (entries[i].err) {
        s = entries[i].err;
        break;
      }
      if (idset_has(&claimed, entries[i].id))
        continue;
      if (used == cap) {
        size_t bigger = cap ? cap * 2 : 16;
        winner *v = bigger > SIZE_MAX / sizeof(*v) ? NULL : realloc(won, bigger * sizeof(*v));
        if (!v) {
          s = EXT_NOMEM;
          break;
        }
        won = v;
        cap = bigger;
      }
      won[used].id = strdup(entries[i].id);
      won[used].layer = layer;
      if (!won[used].id) {
        s = EXT_NOMEM;
        break;
      }
      used++;
    }
    for (size_t i = 0; i < n && !s; i++)
      if (!idset_has(&claimed, entries[i].id))
        s = idset_add(&claimed, entries[i].id);
    scanned_free(entries, n);
  }
  idset_free(&claimed);
  if (!s && used > 1)
    qsort(won, used, sizeof(*won), by_winner);
  for (size_t i = 0; i < used && !s; i++) {
    ext_store *store = stores[won[i].layer];
    ext_buffer manifest = {0};
    pthread_mutex_lock(&store->mu);
    s = read_package(store, won[i].id, 1, &manifest);
    pthread_mutex_unlock(&store->mu);
    if (s)
      break;
    char *dir = package_dir(store->root, won[i].id);
    char *clean = dir ? clean_path(dir) : NULL;
    free(dir);
    if (!clean) {
      ext_buffer_free(&manifest);
      s = EXT_NOMEM;
      break;
    }
    char *id = won[i].id;
    won[i].id = NULL;
    s = rows_add(out, id, clean, EXT_OK, won[i].layer, manifest);
  }
  for (size_t i = 0; i < used; i++)
    free(won[i].id);
  free(won);
  return s;
}

static ext_status report(ext_store *const *stores, size_t count, int kind,
                         ext_buffer *out) {
  rows r = {0};
  ext_status s;
  report_shape shape = SHAPE_PACKAGES;
  switch (kind) {
  case 0:
    s = collect_list(stores, count, &r);
    break;
  case 1:
    s = collect(stores, count, COLLECT_VERIFY, &r);
    shape = SHAPE_CHECKS;
    break;
  default:
    s = collect(stores, count, COLLECT_UNVERIFIED, &r);
    shape = SHAPE_UNVERIFIED;
  }
  if (!s)
    s = emit(&r, shape, out);
  rows_free(&r);
  return s;
}
ext_status ext_store_list(ext_store *store, ext_buffer *packages) {
  if (!store || !packages)
    return EXT_INVALID;
  *packages = (ext_buffer){0};
  return report(&store, 1, 0, packages);
}
ext_status ext_store_verify(ext_store *store, ext_buffer *checks) {
  if (!store || !checks)
    return EXT_INVALID;
  *checks = (ext_buffer){0};
  return report(&store, 1, 1, checks);
}
ext_status ext_store_list_unverified(ext_store *store, ext_buffer *listing) {
  if (!store || !listing)
    return EXT_INVALID;
  *listing = (ext_buffer){0};
  return report(&store, 1, 2, listing);
}

/* A read-only view over several stores, the first holding an id serving it. */
struct ext_layered {
  size_t count;
  ext_store **stores;
};
ext_status ext_layered_open(const char *const *roots, size_t count,
                            const ext_store_limits *limits,
                            ext_layered **out) {
  if (!roots || !count || !out || count > SIZE_MAX / sizeof(char *))
    return EXT_INVALID;
  char **clean = calloc(count, sizeof(*clean));
  ext_layered *layered = calloc(1, sizeof(*layered));
  ext_status s = clean && layered ? EXT_OK : EXT_NOMEM;
  for (size_t i = 0; i < count && !s; i++) {
    if (!roots[i] || !*roots[i]) {
      s = EXT_INVALID;
      break;
    }
    clean[i] = clean_path(roots[i]);
    if (!clean[i]) {
      s = EXT_NOMEM;
      break;
    }
    for (size_t j = 0; j < i; j++)
      if (!strcmp(clean[i], clean[j]))
        s = EXT_INVALID;
  }
  if (!s) {
    layered->stores = calloc(count, sizeof(*layered->stores));
    if (!layered->stores)
      s = EXT_NOMEM;
  }
  for (size_t i = 0; i < count && !s; i++) {
    s = ext_store_open(roots[i], limits, &layered->stores[i]);
    if (!s)
      layered->count++;
  }
  for (size_t i = 0; i < count; i++)
    free(clean ? clean[i] : NULL);
  free(clean);
  if (s) {
    ext_layered_close(layered);
    return s;
  }
  *out = layered;
  return EXT_OK;
}
void ext_layered_close(ext_layered *layered) {
  if (!layered)
    return;
  for (size_t i = 0; i < layered->count; i++)
    ext_store_close(layered->stores[i]);
  free(layered->stores);
  free(layered);
}
ext_status ext_layered_load(ext_layered *layered, const char *id,
                            ext_buffer *manifest, size_t *layer) {
  if (!layered || !valid_id(id) || !manifest)
    return EXT_INVALID;
  *manifest = (ext_buffer){0};
  for (size_t i = 0; i < layered->count; i++) {
    ext_buffer bytes = {0};
    ext_status s = ext_store_load(layered->stores[i], id, &bytes);
    if (s == EXT_NOT_FOUND)
      continue;
    if (!s) {
      *manifest = bytes;
      if (layer)
        *layer = i;
    }
    return s;
  }
  return EXT_NOT_FOUND;
}
ext_status ext_layered_list(ext_layered *layered, ext_buffer *packages) {
  if (!layered || !packages)
    return EXT_INVALID;
  *packages = (ext_buffer){0};
  return report(layered->stores, layered->count, 0, packages);
}
ext_status ext_layered_verify(ext_layered *layered, ext_buffer *checks) {
  if (!layered || !checks)
    return EXT_INVALID;
  *checks = (ext_buffer){0};
  return report(layered->stores, layered->count, 1, checks);
}
ext_status ext_layered_list_unverified(ext_layered *layered,
                                       ext_buffer *listing) {
  if (!layered || !listing)
    return EXT_INVALID;
  *listing = (ext_buffer){0};
  return report(layered->stores, layered->count, 2, listing);
}
