#define _POSIX_C_SOURCE 200809L
#define _DARWIN_C_SOURCE
#include "fs.h"
#include "platform.h"
#include "sha256.h"
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

/* The write side of the filesystem layer for POSIX. Like the read side it works
 * from descriptors and opens every component with O_NOFOLLOW, so a link swapped
 * in below the root is refused, not followed. */

#define REMOVE_DEPTH_MAX 1024u

/* A portable relative path: no leading, trailing or doubled slash, and no "."
 * or ".." component. */
static int valid_path(const char *path) {
  if (!path || !*path)
    return 0;
  const char *part = path;
  for (;;) {
    const char *end = strchr(part, '/');
    size_t n = end ? (size_t)(end - part) : strlen(part);
    if (!n || (n == 1 && part[0] == '.') ||
        (n == 2 && part[0] == '.' && part[1] == '.'))
      return 0;
    if (!end)
      return 1;
    part = end + 1;
  }
}
/* Splits a valid path into its parent (empty for a direct child) and leaf. The
 * result is one allocation. */
static ext_status split(const char *path, char **storage, const char **parent,
                        const char **leaf) {
  if (!valid_path(path))
    return EXT_INVALID;
  char *copy = strdup(path);
  if (!copy)
    return EXT_NOMEM;
  char *slash = strrchr(copy, '/');
  if (slash) {
    *slash = 0;
    *parent = copy;
    *leaf = slash + 1;
  } else {
    *parent = copy + strlen(copy);
    *leaf = copy;
  }
  *storage = copy;
  return EXT_OK;
}
/* Opens the plain directory at path below root, creating the missing ones when
 * create is set. The caller closes the descriptor. */
static ext_status open_dir(int root, const char *path, int create, int *out) {
  int fd = dup(root);
  if (fd < 0)
    return EXT_IO;
  if (!*path) {
    *out = fd;
    return EXT_OK;
  }
  if (!valid_path(path)) {
    close(fd);
    return EXT_INVALID;
  }
  char *parts = strdup(path);
  if (!parts) {
    close(fd);
    return EXT_NOMEM;
  }
  ext_status s = EXT_OK;
  char *part = parts;
  while (part && *part) {
    char *slash = strchr(part, '/');
    if (slash)
      *slash = 0;
    if (create) {
      if (!mkdirat(fd, part, 0700)) {
        /* Owner access whatever the umask said, or the directory could not be
         * opened below. */
        if (fchmodat(fd, part, 0700, 0)) {
          s = EXT_IO;
          break;
        }
      } else if (errno != EEXIST) {
        s = EXT_IO;
        break;
      }
    }
    struct stat st;
    if (fstatat(fd, part, &st, AT_SYMLINK_NOFOLLOW)) {
      s = errno == ENOENT ? EXT_NOT_FOUND : EXT_IO;
      break;
    }
    if (!S_ISDIR(st.st_mode)) {
      s = EXT_INVALID;
      break;
    }
    int next = openat(fd, part, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (next < 0) {
      s = errno == ENOENT ? EXT_NOT_FOUND : EXT_IO;
      break;
    }
    close(fd);
    fd = next;
    part = slash ? slash + 1 : NULL;
  }
  free(parts);
  if (s) {
    close(fd);
    return s;
  }
  *out = fd;
  return EXT_OK;
}
static ext_fs_kind kind_of_mode(mode_t mode) {
  if (S_ISDIR(mode))
    return EXT_FS_DIR;
  if (S_ISREG(mode))
    return EXT_FS_FILE;
  return EXT_FS_OTHER;
}

ext_status ext_fs_kind_of(ext_fs *fs, const char *path, ext_fs_kind *out) {
  if (!fs || !out)
    return EXT_INVALID;
  char *storage = NULL;
  const char *parent, *leaf;
  ext_status s = split(path, &storage, &parent, &leaf);
  if (s)
    return s;
  int dir = -1;
  s = open_dir(ext_fs_fd(fs), parent, 0, &dir);
  if (s == EXT_NOT_FOUND) {
    *out = EXT_FS_NONE;
    s = EXT_OK;
  } else if (!s) {
    struct stat st;
    if (fstatat(dir, leaf, &st, AT_SYMLINK_NOFOLLOW)) {
      if (errno == ENOENT) {
        *out = EXT_FS_NONE;
      } else
        s = EXT_IO;
    } else
      *out = kind_of_mode(st.st_mode);
    close(dir);
  }
  free(storage);
  return s;
}
void ext_fs_list_free(ext_fs_entry *entries, size_t count) {
  for (size_t i = 0; i < count; i++)
    free(entries[i].name);
  free(entries);
}
ext_status ext_fs_list(ext_fs *fs, const char *path, ext_fs_entry **out,
                       size_t *count) {
  if (!fs || !path || !out || !count)
    return EXT_INVALID;
  *out = NULL;
  *count = 0;
  int dir = -1;
  ext_status s = open_dir(ext_fs_fd(fs), path, 0, &dir);
  if (s)
    return s;
  DIR *handle = fdopendir(dir);
  if (!handle) {
    close(dir);
    return EXT_IO;
  }
  ext_fs_entry *entries = NULL;
  size_t used = 0, cap = 0;
  for (;;) {
    errno = 0;
    struct dirent *e = readdir(handle);
    if (!e) {
      if (errno)
        s = EXT_IO;
      break;
    }
    if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, ".."))
      continue;
    if (used == cap) {
      size_t next = cap ? cap * 2 : 16;
      ext_fs_entry *grown = realloc(entries, next * sizeof(*entries));
      if (!grown) {
        s = EXT_NOMEM;
        break;
      }
      entries = grown;
      cap = next;
    }
    struct stat st;
    if (fstatat(dirfd(handle), e->d_name, &st, AT_SYMLINK_NOFOLLOW)) {
      if (errno == ENOENT) /* removed since readdir */
        continue;
      s = EXT_IO;
      break;
    }
    char *name = strdup(e->d_name);
    if (!name) {
      s = EXT_NOMEM;
      break;
    }
    entries[used].name = name;
    entries[used].kind = kind_of_mode(st.st_mode);
    used++;
  }
  if (closedir(handle) && !s)
    s = EXT_IO;
  if (s) {
    ext_fs_list_free(entries, used);
    return s;
  }
  *out = entries;
  *count = used;
  return EXT_OK;
}
ext_status ext_fs_mkdirs(ext_fs *fs, const char *path) {
  if (!fs || !valid_path(path))
    return EXT_INVALID;
  int dir = -1;
  ext_status s = open_dir(ext_fs_fd(fs), path, 1, &dir);
  if (!s)
    close(dir);
  return s;
}
ext_status ext_fs_mkdtemp(ext_fs *fs, const char *parent, const char *prefix,
                          char **name) {
  if (!fs || !parent || !prefix || !name || strchr(prefix, '/') ||
      strlen(prefix) > 64)
    return EXT_INVALID;
  int dir = -1;
  ext_status s = open_dir(ext_fs_fd(fs), parent, 1, &dir);
  if (s)
    return s;
  char candidate[64 + 1 + 16 + 1];
  for (int attempt = 0; attempt < 100; attempt++) {
    uint8_t random[8];
    s = ext_random_bytes(random, sizeof(random));
    if (s)
      break;
    size_t n = strlen(prefix);
    memcpy(candidate, prefix, n);
    for (size_t i = 0; i < sizeof(random); i++) {
      candidate[n++] = "0123456789abcdef"[random[i] >> 4];
      candidate[n++] = "0123456789abcdef"[random[i] & 15];
    }
    candidate[n] = 0;
    if (!mkdirat(dir, candidate, 0700)) {
      s = fchmodat(dir, candidate, 0700, 0) ? EXT_IO : EXT_OK;
      if (!s) {
        *name = strdup(candidate);
        s = *name ? EXT_OK : EXT_NOMEM;
      }
      close(dir);
      return s;
    }
    if (errno != EEXIST) {
      s = EXT_IO;
      break;
    }
    s = EXT_IO;
  }
  close(dir);
  return s;
}

static ext_status write_all(int fd, const uint8_t *data, size_t len) {
  while (len) {
    ssize_t n = write(fd, data, len);
    if (n > 0) {
      data += n;
      len -= (size_t)n;
    } else if (n < 0 && errno == EINTR)
      continue;
    else
      return EXT_IO;
  }
  return EXT_OK;
}
/* Creates the file leaf below dir, which must not exist. */
static ext_status create_at(int dir, const char *leaf, int executable,
                            int *out) {
  mode_t mode = executable ? 0700 : 0600;
  int fd = openat(dir, leaf,
                  O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, mode);
  if (fd < 0)
    return errno == EEXIST ? EXT_INVALID : EXT_IO;
  /* Keep the owner bits whatever the umask said. */
  if (fchmod(fd, mode)) {
    close(fd);
    return EXT_IO;
  }
  *out = fd;
  return EXT_OK;
}
ext_status ext_fs_copy_file(ext_fs *src, const char *src_path, ext_fs *dst,
                            const char *dst_path, int executable,
                            uint64_t limit, uint8_t sum[32], uint64_t *size) {
  if (!src || !dst || !sum || !size || limit > (uint64_t)INT64_MAX - 1)
    return EXT_INVALID;
  char *from_storage = NULL, *to_storage = NULL;
  const char *from_parent, *from_leaf, *to_parent, *to_leaf;
  ext_status s = split(src_path, &from_storage, &from_parent, &from_leaf);
  if (!s)
    s = split(dst_path, &to_storage, &to_parent, &to_leaf);
  int from_dir = -1, to_dir = -1, in = -1, out = -1;
  struct stat before, after;
  if (!s)
    s = open_dir(ext_fs_fd(src), from_parent, 0, &from_dir);
  if (!s && fstatat(from_dir, from_leaf, &before, AT_SYMLINK_NOFOLLOW))
    s = errno == ENOENT ? EXT_NOT_FOUND : EXT_IO;
  if (!s && !S_ISREG(before.st_mode))
    s = EXT_INVALID;
  if (!s) {
    in = openat(from_dir, from_leaf,
                O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
    if (in < 0)
      s = errno == ELOOP ? EXT_INVALID : EXT_IO;
  }
  if (!s && (fstat(in, &after) || !S_ISREG(after.st_mode) ||
             after.st_dev != before.st_dev || after.st_ino != before.st_ino))
    s = EXT_INVALID; /* not the file that was checked */
  if (!s)
    s = open_dir(ext_fs_fd(dst), to_parent, 1, &to_dir);
  if (!s)
    s = create_at(to_dir, to_leaf, executable, &out);
  ext_hash h;
  ext_hash_init(&h);
  uint64_t total = 0;
  uint8_t buffer[65536];
  while (!s) {
    size_t want = sizeof(buffer);
    if ((uint64_t)want > limit + 1 - total)
      want = (size_t)(limit + 1 - total);
    if (!want)
      break;
    ssize_t n = read(in, buffer, want);
    if (n > 0) {
      total += (uint64_t)n;
      if (total > limit) {
        s = EXT_INVALID; /* longer than the limit */
        break;
      }
      ext_hash_update(&h, buffer, (size_t)n);
      s = write_all(out, buffer, (size_t)n);
    } else if (!n)
      break;
    else if (errno != EINTR)
      s = EXT_IO;
  }
  if (out >= 0 && close(out) && !s)
    s = EXT_IO;
  if (in >= 0)
    close(in);
  if (from_dir >= 0)
    close(from_dir);
  if (to_dir >= 0)
    close(to_dir);
  free(from_storage);
  free(to_storage);
  if (!s) {
    ext_hash_finish(&h, sum);
    *size = total;
  }
  return s;
}
ext_status ext_fs_write_file(ext_fs *fs, const char *path, const uint8_t *data,
                             size_t len, int executable) {
  if (!fs || (!data && len))
    return EXT_INVALID;
  char *storage = NULL;
  const char *parent, *leaf;
  ext_status s = split(path, &storage, &parent, &leaf);
  if (s)
    return s;
  int dir = -1, fd = -1;
  s = open_dir(ext_fs_fd(fs), parent, 1, &dir);
  if (!s)
    s = create_at(dir, leaf, executable, &fd);
  if (!s)
    s = write_all(fd, data, len);
  if (fd >= 0 && close(fd) && !s)
    s = EXT_IO;
  if (dir >= 0)
    close(dir);
  free(storage);
  return s;
}
ext_status ext_fs_rename(ext_fs *fs, const char *from, const char *to) {
  if (!fs)
    return EXT_INVALID;
  char *from_storage = NULL, *to_storage = NULL;
  const char *from_parent, *from_leaf, *to_parent, *to_leaf;
  ext_status s = split(from, &from_storage, &from_parent, &from_leaf);
  if (!s)
    s = split(to, &to_storage, &to_parent, &to_leaf);
  int from_dir = -1, to_dir = -1;
  if (!s)
    s = open_dir(ext_fs_fd(fs), from_parent, 0, &from_dir);
  if (!s)
    s = open_dir(ext_fs_fd(fs), to_parent, 0, &to_dir);
  if (!s && renameat(from_dir, from_leaf, to_dir, to_leaf))
    s = EXT_IO;
  if (from_dir >= 0)
    close(from_dir);
  if (to_dir >= 0)
    close(to_dir);
  free(from_storage);
  free(to_storage);
  return s;
}

static ext_status remove_at(int dir, const char *name, unsigned depth) {
  if (depth > REMOVE_DEPTH_MAX)
    return EXT_INVALID;
  struct stat st;
  if (fstatat(dir, name, &st, AT_SYMLINK_NOFOLLOW))
    return errno == ENOENT ? EXT_OK : EXT_IO;
  if (!S_ISDIR(st.st_mode))
    return unlinkat(dir, name, 0) && errno != ENOENT ? EXT_IO : EXT_OK;
  int child = openat(dir, name, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
  if (child < 0)
    return errno == ENOENT ? EXT_OK : EXT_IO;
  /* Read every name first: removing entries while reading a directory may
   * make a filesystem skip others. */
  int copy = dup(child);
  DIR *handle = copy < 0 ? NULL : fdopendir(copy);
  if (!handle) {
    if (copy >= 0)
      close(copy);
    close(child);
    return EXT_IO;
  }
  char **names = NULL;
  size_t used = 0, cap = 0;
  ext_status s = EXT_OK;
  for (;;) {
    errno = 0;
    struct dirent *e = readdir(handle);
    if (!e) {
      if (errno)
        s = EXT_IO;
      break;
    }
    if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, ".."))
      continue;
    if (used == cap) {
      size_t next = cap ? cap * 2 : 16;
      char **grown = realloc(names, next * sizeof(*names));
      if (!grown) {
        s = EXT_NOMEM;
        break;
      }
      names = grown;
      cap = next;
    }
    names[used] = strdup(e->d_name);
    if (!names[used]) {
      s = EXT_NOMEM;
      break;
    }
    used++;
  }
  closedir(handle);
  for (size_t i = 0; i < used && !s; i++)
    s = remove_at(child, names[i], depth + 1);
  for (size_t i = 0; i < used; i++)
    free(names[i]);
  free(names);
  close(child);
  if (!s && unlinkat(dir, name, AT_REMOVEDIR) && errno != ENOENT)
    s = EXT_IO;
  return s;
}
ext_status ext_fs_remove_all(ext_fs *fs, const char *path) {
  if (!fs)
    return EXT_INVALID;
  char *storage = NULL;
  const char *parent, *leaf;
  ext_status s = split(path, &storage, &parent, &leaf);
  if (s)
    return s;
  int dir = -1;
  s = open_dir(ext_fs_fd(fs), parent, 0, &dir);
  if (s == EXT_NOT_FOUND)
    s = EXT_OK; /* nothing to remove below a parent that is not there */
  else if (!s) {
    s = remove_at(dir, leaf, 0);
    close(dir);
  }
  free(storage);
  return s;
}
ext_status ext_fs_make_root(const char *path) {
  if (!path || !*path)
    return EXT_INVALID;
  char *copy = strdup(path);
  if (!copy)
    return EXT_NOMEM;
  size_t n = strlen(copy);
  while (n > 1 && copy[n - 1] == '/')
    copy[--n] = 0;
  ext_status s = EXT_OK;
  for (size_t i = 1; i <= n && !s; i++) {
    if (copy[i] != '/' && copy[i] != 0)
      continue;
    char saved = copy[i];
    copy[i] = 0;
    if (mkdir(copy, 0700) && errno != EEXIST)
      s = EXT_IO;
    copy[i] = saved;
  }
  free(copy);
  return s;
}
