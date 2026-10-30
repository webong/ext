#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE
#include "fs.h"
#include "sha256.h"
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

/* The status of a failed file operation: a name that is not there is
 * EXT_MISSING, anything else EXT_IO. Call it before anything else can change
 * errno. */
static ext_status failed(void) {
  return errno == ENOENT ? EXT_MISSING : EXT_IO;
}

struct ext_fs {
  int fd;
};

/* Descriptor-relative opens avoid traversing symlink substitutions. */
static ext_status root_open(const char *root, int *fd) {
  if (!root || !*root)
    return EXT_INVALID;
  char *path = strdup(root);
  if (!path)
    return EXT_NOMEM;
  size_t n = strlen(path);
  while (n > 1 && path[n - 1] == '/')
    path[--n] = 0;
  struct stat st;
  ext_status s = EXT_OK;
  if (lstat(path, &st))
    s = failed();
  else if (!S_ISDIR(st.st_mode))
    s = EXT_INVALID;
  if (!s) {
    *fd = open(path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (*fd < 0)
      s = failed();
  }
  free(path);
  return s;
}
static ext_status file_hash(int dir, const char *name, uint8_t out[32]) {
  struct stat st;
  if (fstatat(dir, name, &st, AT_SYMLINK_NOFOLLOW))
    return failed();
  if (!S_ISREG(st.st_mode))
    return EXT_INVALID;
  int fd = openat(dir, name, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
  if (fd < 0)
    return errno == ELOOP ? EXT_INVALID : failed();
  ext_status s = EXT_OK;
  if (fstat(fd, &st))
    s = EXT_IO;
  else if (!S_ISREG(st.st_mode))
    s = EXT_INVALID;
  ext_hash h;
  ext_hash_init(&h);
  uint8_t buffer[65536];
  while (!s) {
    ssize_t n = read(fd, buffer, sizeof(buffer));
    if (n > 0)
      ext_hash_update(&h, buffer, (size_t)n);
    else if (!n)
      break;
    else if (errno != EINTR)
      s = EXT_IO;
  }
  if (close(fd) && !s)
    s = EXT_IO;
  ext_hash_finish(&h, out);
  return s;
}
/* Descends the slash-separated components of path from root, each a plain
 * directory, and returns a descriptor for the last one (a copy of root for an
 * empty path). With want_file set, the last component is left for the caller
 * and returned through *leaf. The caller closes the descriptor. */
static ext_status descend(int root, const char *path, int want_file, int *out,
                          char **leaf_out, char **storage) {
  char *parts = strdup(path);
  if (!parts)
    return EXT_NOMEM;
  int fd = dup(root);
  if (fd < 0) {
    free(parts);
    return EXT_IO;
  }
  ext_status s = EXT_OK;
  char *part = parts;
  while (*part) {
    char *slash = strchr(part, '/');
    if (!slash && want_file)
      break;
    if (slash)
      *slash = 0;
    struct stat st;
    if (fstatat(fd, part, &st, AT_SYMLINK_NOFOLLOW)) {
      s = failed();
      break;
    }
    if (!S_ISDIR(st.st_mode)) {
      s = EXT_INVALID;
      break;
    }
    int next =
        openat(fd, part, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (next < 0) {
      s = failed();
      break;
    }
    close(fd);
    fd = next;
    if (!slash) {
      part += strlen(part);
      break;
    }
    part = slash + 1;
  }
  if (s) {
    close(fd);
    free(parts);
    return s;
  }
  *out = fd;
  if (leaf_out)
    *leaf_out = part;
  *storage = parts;
  return EXT_OK;
}
static ext_status walk(int fd, const char *prefix, ext_fs_visit visit,
                       void *user, unsigned depth) {
  if (depth > 1024)
    return EXT_INVALID;
  int copy = dup(fd);
  if (copy < 0)
    return EXT_IO;
  DIR *dir = fdopendir(copy);
  if (!dir) {
    close(copy);
    return EXT_IO;
  }
  /* The copy shares its read position with fd, which an earlier walk of the
   * same directory may have left at the end. */
  rewinddir(dir);
  ext_status s = EXT_OK;
  struct dirent *entry;
  for (;;) {
    errno = 0;
    entry = readdir(dir);
    if (!entry) {
      if (errno)
        s = EXT_IO;
      break;
    }
    if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, ".."))
      continue;
    size_t base = strlen(prefix), name = strlen(entry->d_name);
    if (base > SIZE_MAX - name - 2) {
      s = EXT_NOMEM;
      break;
    }
    char *path = malloc(base + name + 2);
    if (!path) {
      s = EXT_NOMEM;
      break;
    }
    memcpy(path, prefix, base);
    if (base)
      path[base++] = '/';
    memcpy(path + base, entry->d_name, name + 1);
    struct stat st;
    if (fstatat(fd, entry->d_name, &st, AT_SYMLINK_NOFOLLOW))
      s = failed();
    else if (S_ISDIR(st.st_mode)) {
      int child = openat(fd, entry->d_name,
                         O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
      if (child < 0)
        s = failed();
      else {
        s = walk(child, path, visit, user, depth + 1);
        close(child);
      }
    } else if (!S_ISREG(st.st_mode) || strchr(path, '\r') || strchr(path, '\n'))
      s = EXT_INVALID;
    else {
      uint8_t sum[32];
      s = file_hash(fd, entry->d_name, sum);
      if (!s)
        s = visit(user, path, sum);
    }
    free(path);
    if (s)
      break;
  }
  if (closedir(dir) && !s)
    s = EXT_IO;
  return s;
}

ext_status ext_fs_open(const char *root, ext_fs **out) {
  int fd = -1;
  ext_status s = root_open(root, &fd);
  if (s)
    return s;
  ext_fs *fs = malloc(sizeof(*fs));
  if (!fs) {
    close(fd);
    return EXT_NOMEM;
  }
  fs->fd = fd;
  *out = fs;
  return EXT_OK;
}
void ext_fs_close(ext_fs *fs) {
  if (!fs)
    return;
  close(fs->fd);
  free(fs);
}
int ext_fs_fd(ext_fs *fs) { return fs->fd; }
ext_status ext_fs_hash_file(ext_fs *fs, const char *path, uint8_t out[32]) {
  int dir = -1;
  char *leaf = NULL, *storage = NULL;
  ext_status s = descend(fs->fd, path, 1, &dir, &leaf, &storage);
  if (s)
    return s;
  s = file_hash(dir, leaf, out);
  close(dir);
  free(storage);
  return s;
}
ext_status ext_fs_read_file(ext_fs *fs, const char *path, size_t limit,
                            ext_buffer *out) {
  if (!fs || !path || !*path || !out || !limit)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  int dir = -1;
  char *leaf = NULL, *storage = NULL;
  ext_status s = descend(fs->fd, path, 1, &dir, &leaf, &storage);
  if (s)
    return s;
  int fd = openat(dir, leaf, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
  if (fd < 0)
    s = errno == ELOOP ? EXT_INVALID : failed();
  struct stat st;
  if (!s && (fstat(fd, &st) || !S_ISREG(st.st_mode)))
    s = EXT_INVALID;
  if (!s && ((uint64_t)st.st_size > limit || st.st_size < 0))
    s = EXT_INVALID;
  uint8_t *data = NULL;
  if (!s) {
    data = malloc((size_t)st.st_size ? (size_t)st.st_size : 1);
    if (!data)
      s = EXT_NOMEM;
  }
  size_t used = 0;
  while (!s && used < (size_t)st.st_size) {
    ssize_t n = read(fd, data + used, (size_t)st.st_size - used);
    if (n > 0)
      used += (size_t)n;
    else if (!n)
      s = EXT_IO;
    else if (errno != EINTR)
      s = EXT_IO;
  }
  if (!s) {
    uint8_t extra;
    ssize_t n = read(fd, &extra, 1);
    if (n != 0)
      s = n > 0 ? EXT_INVALID : EXT_IO;
  }
  if (fd >= 0 && close(fd) && !s)
    s = EXT_IO;
  close(dir);
  free(storage);
  if (s)
    free(data);
  else
    *out = (ext_buffer){data, used};
  return s;
}
ext_status ext_fs_walk(ext_fs *fs, const char *path, ext_fs_visit visit,
                       void *user) {
  int dir = -1;
  char *storage = NULL;
  ext_status s = descend(fs->fd, path, 0, &dir, NULL, &storage);
  if (s)
    return s;
  s = walk(dir, "", visit, user, 0);
  close(dir);
  free(storage);
  return s;
}
