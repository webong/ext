/* The platform layer on POSIX systems: Linux, the BSDs, macOS and iOS. */
#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#elif defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__) || defined(__DragonFly__)
#define __BSD_VISIBLE 1
#else
#define _DEFAULT_SOURCE
#endif

#include "ledger_internal.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

struct ll_mutex {
  pthread_mutex_t m;
};
struct ll_cond {
  pthread_cond_t c;
};

ll_mutex *ll_mutex_new(void) {
  ll_mutex *m = malloc(sizeof *m);
  if (m && pthread_mutex_init(&m->m, NULL)) {
    free(m);
    return NULL;
  }
  return m;
}
void ll_mutex_free(ll_mutex *m) {
  if (!m) return;
  pthread_mutex_destroy(&m->m);
  free(m);
}
void ll_mutex_lock(ll_mutex *m) { pthread_mutex_lock(&m->m); }
void ll_mutex_unlock(ll_mutex *m) { pthread_mutex_unlock(&m->m); }

ll_cond *ll_cond_new(void) {
  ll_cond *c = malloc(sizeof *c);
  if (c && pthread_cond_init(&c->c, NULL)) {
    free(c);
    return NULL;
  }
  return c;
}
void ll_cond_free(ll_cond *c) {
  if (!c) return;
  pthread_cond_destroy(&c->c);
  free(c);
}
void ll_cond_wait(ll_cond *c, ll_mutex *m, int64_t timeout_ms) {
  if (timeout_ms < 0) {
    pthread_cond_wait(&c->c, &m->m);
    return;
  }
  struct timespec ts;
  clock_gettime(CLOCK_REALTIME, &ts);
  ts.tv_sec += (time_t)(timeout_ms / 1000);
  ts.tv_nsec += (long)(timeout_ms % 1000) * 1000000L;
  if (ts.tv_nsec >= 1000000000L) {
    ts.tv_sec++;
    ts.tv_nsec -= 1000000000L;
  }
  pthread_cond_timedwait(&c->c, &m->m, &ts);
}
void ll_cond_broadcast(ll_cond *c) { pthread_cond_broadcast(&c->c); }

int64_t ll_now_unix_ms(void) {
  struct timespec ts;
  clock_gettime(CLOCK_REALTIME, &ts);
  return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

void ll_sleep_ms(int64_t ms) {
  struct timespec ts = {(time_t)(ms / 1000), (long)(ms % 1000) * 1000000L};
  while (nanosleep(&ts, &ts) && errno == EINTR) {
  }
}

struct ll_file {
  int fd;
};

int ll_file_open(const char *path, int flags, ll_file **out) {
  int mode = (flags & LL_WRITE) ? O_RDWR : O_RDONLY;
  if (flags & LL_CREATE) mode |= O_CREAT;
  if (flags & LL_TRUNC) mode |= O_TRUNC;
#ifdef O_CLOEXEC
  mode |= O_CLOEXEC;
#endif
  int fd;
  do fd = open(path, mode, 0600);
  while (fd < 0 && errno == EINTR);
  if (fd < 0) return errno == ENOENT ? EXT_LEDGER_NOT_FOUND : EXT_LEDGER_IO;
  ll_file *f = malloc(sizeof *f);
  if (!f) {
    close(fd);
    return EXT_LEDGER_IO;
  }
  f->fd = fd;
  *out = f;
  return 0;
}

int ll_file_read_at(ll_file *f, uint64_t off, void *buf, size_t n, size_t *got) {
  size_t done = 0;
  while (done < n) {
    ssize_t r = pread(f->fd, (char *)buf + done, n - done, (off_t)(off + done));
    if (r < 0) {
      if (errno == EINTR) continue;
      return EXT_LEDGER_IO;
    }
    if (r == 0) break;
    done += (size_t)r;
  }
  *got = done;
  return 0;
}

int ll_file_write_at(ll_file *f, uint64_t off, const void *buf, size_t n) {
  size_t done = 0;
  while (done < n) {
    ssize_t r = pwrite(f->fd, (const char *)buf + done, n - done, (off_t)(off + done));
    if (r < 0) {
      if (errno == EINTR) continue;
      return EXT_LEDGER_IO;
    }
    done += (size_t)r;
  }
  return 0;
}

int ll_file_size(ll_file *f, uint64_t *size) {
  struct stat st;
  if (fstat(f->fd, &st)) return EXT_LEDGER_IO;
  *size = (uint64_t)st.st_size;
  return 0;
}

int ll_file_truncate(ll_file *f, uint64_t size) { return ftruncate(f->fd, (off_t)size) ? EXT_LEDGER_IO : 0; }

int ll_file_sync(ll_file *f) {
#if defined(__APPLE__) && defined(F_FULLFSYNC)
  if (fcntl(f->fd, F_FULLFSYNC) == 0) return 0; /* fsync alone does not reach the platter on Darwin */
#endif
  return fsync(f->fd) ? EXT_LEDGER_IO : 0;
}

int ll_file_identity(ll_file *f, ll_fid *id) {
  struct stat st;
  if (fstat(f->fd, &st)) return EXT_LEDGER_IO;
  id->a = (uint64_t)st.st_dev;
  id->b = (uint64_t)st.st_ino;
  return 0;
}

int ll_file_try_lock(ll_file *f) {
  if (flock(f->fd, LOCK_EX | LOCK_NB) == 0) return 0;
  return (errno == EWOULDBLOCK || errno == EINTR) ? EXT_LEDGER_BUSY : EXT_LEDGER_IO;
}

void ll_file_unlock(ll_file *f) { flock(f->fd, LOCK_UN); }

void ll_file_close(ll_file *f) {
  if (!f) return;
  close(f->fd);
  free(f);
}

int ll_path_identity(const char *path, ll_fid *id) {
  struct stat st;
  if (stat(path, &st)) return errno == ENOENT ? EXT_LEDGER_NOT_FOUND : EXT_LEDGER_IO;
  id->a = (uint64_t)st.st_dev;
  id->b = (uint64_t)st.st_ino;
  return 0;
}

int ll_path_kind(const char *path) {
  struct stat st;
  if (stat(path, &st)) return errno == ENOENT ? 0 : -1;
  return S_ISDIR(st.st_mode) ? 1 : 2;
}

int ll_mkdir(const char *path) {
  if (mkdir(path, 0700) == 0 || errno == EEXIST) return 0;
  if (errno == ENOENT) { /* create the parents first */
    char *copy = strdup(path);
    if (!copy) return EXT_LEDGER_IO;
    char *slash = strrchr(copy, '/');
    int rc = EXT_LEDGER_IO;
    if (slash && slash != copy) {
      *slash = 0;
      rc = ll_mkdir(copy);
    }
    free(copy);
    if (rc) return rc;
    if (mkdir(path, 0700) == 0 || errno == EEXIST) return 0;
  }
  return EXT_LEDGER_IO;
}

int ll_list_dir(const char *dir, int (*each)(void *arg, const char *name), void *arg) {
  DIR *d = opendir(dir);
  if (!d) return EXT_LEDGER_IO;
  int rc = 0;
  struct dirent *e;
  while (!rc && (e = readdir(d)) != NULL) {
    if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) continue;
    rc = each(arg, e->d_name);
  }
  closedir(d);
  return rc;
}

int ll_rename(const char *from, const char *to) { return rename(from, to) ? EXT_LEDGER_IO : 0; }

int ll_remove(const char *path) { return (unlink(path) == 0 || errno == ENOENT) ? 0 : EXT_LEDGER_IO; }

int ll_sync_dir(const char *dir) {
  int fd = open(dir, O_RDONLY);
  if (fd < 0) return EXT_LEDGER_IO;
  int rc = fsync(fd) ? EXT_LEDGER_IO : 0;
  close(fd);
  return rc;
}
#endif
