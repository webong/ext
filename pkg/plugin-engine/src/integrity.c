#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE
#include "services.h"
#include "sha256.h"
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

/* The application must protect reviewed files against writes. Descriptor-
 * relative opens avoid traversing symlink substitutions, but are not a sandbox.
 */
static ctx_status root_open(const char *root, int *fd) {
  if (!root || !*root)
    return CTX_INVALID;
  char *path = strdup(root);
  if (!path)
    return CTX_NOMEM;
  size_t n = strlen(path);
  while (n > 1 && path[n - 1] == '/')
    path[--n] = 0;
  struct stat st;
  ctx_status s = CTX_OK;
  if (lstat(path, &st))
    s = CTX_IO;
  else if (!S_ISDIR(st.st_mode))
    s = CTX_INVALID;
  if (!s) {
    *fd = open(path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (*fd < 0)
      s = CTX_IO;
  }
  free(path);
  return s;
}
static ctx_status file_hash(int dir, const char *name, uint8_t out[32]) {
  struct stat st;
  if (fstatat(dir, name, &st, AT_SYMLINK_NOFOLLOW))
    return CTX_IO;
  if (!S_ISREG(st.st_mode))
    return CTX_INVALID;
  int fd = openat(dir, name, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
  if (fd < 0)
    return errno == ELOOP ? CTX_INVALID : CTX_IO;
  ctx_status s = CTX_OK;
  if (fstat(fd, &st))
    s = CTX_IO;
  else if (!S_ISREG(st.st_mode))
    s = CTX_INVALID;
  ctx_hash h;
  ctx_hash_init(&h);
  uint8_t buffer[65536];
  while (!s) {
    ssize_t n = read(fd, buffer, sizeof(buffer));
    if (n > 0)
      ctx_hash_update(&h, buffer, (size_t)n);
    else if (!n)
      break;
    else if (errno != EINTR)
      s = CTX_IO;
  }
  if (close(fd) && !s)
    s = CTX_IO;
  ctx_hash_finish(&h, out);
  return s;
}
static ctx_status artifact_hash(int root, const char *path, uint8_t out[32]) {
  char *parts = strdup(path);
  if (!parts)
    return CTX_NOMEM;
  int fd = dup(root);
  if (fd < 0) {
    free(parts);
    return CTX_IO;
  }
  ctx_status s = CTX_OK;
  char *part = parts, *slash;
  while ((slash = strchr(part, '/')) != NULL) {
    *slash = 0;
    struct stat st;
    if (fstatat(fd, part, &st, AT_SYMLINK_NOFOLLOW)) {
      s = CTX_IO;
      break;
    }
    if (!S_ISDIR(st.st_mode)) {
      s = CTX_INVALID;
      break;
    }
    int next =
        openat(fd, part, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (next < 0) {
      s = CTX_IO;
      break;
    }
    close(fd);
    fd = next;
    part = slash + 1;
  }
  if (!s)
    s = file_hash(fd, part, out);
  close(fd);
  free(parts);
  return s;
}
ctx_status ctx_package_verify(const uint8_t *manifest, size_t len,
                              const char *root) {
  yyjson_doc *doc = ctx_parse(manifest, len);
  if (!doc)
    return CTX_INVALID;
  yyjson_val *m = yyjson_doc_get_root(doc);
  ctx_status s = ctx_manifest_valid(m);
  int fd = -1;
  if (!s)
    s = root_open(root, &fd);
  if (!s) {
    size_t i, n;
    yyjson_val *a;
    yyjson_arr_foreach(yyjson_obj_get(m, "artifacts"), i, n, a) {
      uint8_t sum[32];
      char hex[65];
      s = artifact_hash(fd, yyjson_get_str(yyjson_obj_get(a, "path")), sum);
      if (s)
        break;
      ctx_hash_hex(sum, hex);
      if (!yyjson_equals_str(yyjson_obj_get(a, "sha256"), hex)) {
        s = CTX_MISMATCH;
        break;
      }
    }
  }
  if (fd >= 0)
    close(fd);
  yyjson_doc_free(doc);
  return s;
}
typedef struct {
  char *path;
  uint8_t sum[32];
} file;
typedef struct {
  file *files;
  size_t count, capacity;
} listing;
static ctx_status walk(int fd, const char *prefix, listing *list,
                       unsigned depth) {
  if (depth > 1024)
    return CTX_INVALID;
  int copy = dup(fd);
  if (copy < 0)
    return CTX_IO;
  DIR *dir = fdopendir(copy);
  if (!dir) {
    close(copy);
    return CTX_IO;
  }
  ctx_status s = CTX_OK;
  struct dirent *entry;
  for (;;) {
    errno = 0;
    entry = readdir(dir);
    if (!entry) {
      if (errno)
        s = CTX_IO;
      break;
    }
    if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, ".."))
      continue;
    size_t base = strlen(prefix), name = strlen(entry->d_name);
    if (base > SIZE_MAX - name - 2) {
      s = CTX_NOMEM;
      break;
    }
    char *path = malloc(base + name + 2);
    if (!path) {
      s = CTX_NOMEM;
      break;
    }
    memcpy(path, prefix, base);
    if (base)
      path[base++] = '/';
    memcpy(path + base, entry->d_name, name + 1);
    struct stat st;
    if (fstatat(fd, entry->d_name, &st, AT_SYMLINK_NOFOLLOW))
      s = CTX_IO;
    else if (S_ISDIR(st.st_mode)) {
      int child = openat(fd, entry->d_name,
                         O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
      if (child < 0)
        s = CTX_IO;
      else {
        s = walk(child, path, list, depth + 1);
        close(child);
      }
    } else if (!S_ISREG(st.st_mode) || strchr(path, '\r') || strchr(path, '\n'))
      s = CTX_INVALID;
    else {
      if (list->count == list->capacity) {
        size_t capacity = list->capacity ? list->capacity * 2 : 64;
        file *files =
            capacity > list->capacity && capacity <= SIZE_MAX / sizeof(file)
                ? realloc(list->files, capacity * sizeof(file))
                : NULL;
        if (!files)
          s = CTX_NOMEM;
        else {
          list->files = files;
          list->capacity = capacity;
        }
      }
      if (!s) {
        file *f = &list->files[list->count];
        s = file_hash(fd, entry->d_name, f->sum);
        if (!s) {
          f->path = path;
          path = NULL;
          list->count++;
        }
      }
    }
    free(path);
    if (s)
      break;
  }
  if (closedir(dir) && !s)
    s = CTX_IO;
  return s;
}
static int compare(const void *a, const void *b) {
  return strcmp(((const file *)a)->path, ((const file *)b)->path);
}
ctx_status ctx_directory_digest(const char *root, uint8_t out[32]) {
  if (!out)
    return CTX_INVALID;
  int fd = -1;
  ctx_status s = root_open(root, &fd);
  listing list = {0};
  if (!s) {
    s = walk(fd, "", &list, 0);
    close(fd);
  }
  if (!s) {
    if (list.count > 1)
      qsort(list.files, list.count, sizeof(file), compare);
    ctx_hash hash;
    ctx_hash_init(&hash);
    for (size_t i = 0; i < list.count; i++) {
      char hex[65];
      ctx_hash_hex(list.files[i].sum, hex);
      ctx_hash_update(&hash, (const uint8_t *)"./", 2);
      ctx_hash_update(&hash, (const uint8_t *)list.files[i].path,
                      strlen(list.files[i].path));
      ctx_hash_update(&hash, (const uint8_t *)" ", 1);
      ctx_hash_update(&hash, (const uint8_t *)hex, 64);
      ctx_hash_update(&hash, (const uint8_t *)"\n", 1);
    }
    ctx_hash_finish(&hash, out);
  }
  for (size_t i = 0; i < list.count; i++)
    free(list.files[i].path);
  free(list.files);
  return s;
}
