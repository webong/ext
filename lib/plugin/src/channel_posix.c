#define _GNU_SOURCE
#include "channel.h"
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <spawn.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#if defined(__APPLE__) ||                                                      \
    (defined(__GLIBC__) &&                                                     \
     (__GLIBC__ > 2 || (__GLIBC__ == 2 && __GLIBC_MINOR__ >= 29)))
#define EXT_HAVE_SPAWN_CHDIR 1
#endif

struct ext_channel {
  pid_t pid;
  int fd, stderr_fd;
  pthread_mutex_t mu; /* guards the retained stderr */
  int stderr_started, stderr_stop;
  size_t stderr_limit, stderr_len;
  uint8_t *stderr_data;
  pthread_t stderr_thread;
};

/* Retains the first stderr_limit bytes of the child's stderr and discards the
 * rest, until the pipe closes or the channel is destroyed. */
static void *stderr_reader(void *arg) {
  ext_channel *c = arg;
  uint8_t chunk[1024];
  for (;;) {
    pthread_mutex_lock(&c->mu);
    int stop = c->stderr_stop;
    pthread_mutex_unlock(&c->mu);
    struct pollfd p = {c->stderr_fd, POLLIN, 0};
    int ready = poll(&p, 1, 100);
    if (ready < 0 && errno == EINTR)
      continue;
    if (ready < 0)
      break;
    if (!ready) {
      if (stop)
        break;
      continue;
    }
    ssize_t n = read(c->stderr_fd, chunk, sizeof(chunk));
    if (n < 0 && (errno == EINTR || errno == EAGAIN))
      continue;
    if (n <= 0)
      break;
    pthread_mutex_lock(&c->mu);
    size_t room = c->stderr_limit - c->stderr_len;
    size_t keep = (size_t)n < room ? (size_t)n : room;
    memcpy(c->stderr_data + c->stderr_len, chunk, keep);
    c->stderr_len += keep;
    pthread_mutex_unlock(&c->mu);
  }
  return NULL;
}

/* Keeps a descriptor above stderr, even when the embedding app closed its
 * standard descriptors, and marks it close-on-exec. Returns -1 on failure. */
static int keep_high(int fd) {
  if (fd <= 2 && fd >= 0) {
    int next = fcntl(fd, F_DUPFD_CLOEXEC, 3);
    close(fd);
    fd = next;
  }
  if (fd < 0 || fcntl(fd, F_SETFD, FD_CLOEXEC) < 0) {
    if (fd >= 0)
      close(fd);
    return -1;
  }
  return fd;
}

ext_status ext_channel_open(const ext_launch *launch, ext_channel **out) {
  struct stat st;
  if (stat(launch->path, &st))
    return EXT_IO;
  if (!S_ISREG(st.st_mode))
    return EXT_INVALID;
#ifndef EXT_HAVE_SPAWN_CHDIR
  if (launch->directory)
    return EXT_UNSUPPORTED;
#endif
  ext_channel *c = calloc(1, sizeof(*c));
  if (!c)
    return EXT_NOMEM;
  c->fd = c->stderr_fd = -1;
  if (pthread_mutex_init(&c->mu, NULL)) {
    free(c);
    return EXT_IO;
  }
  if (launch->capture_stderr) {
    c->stderr_limit = launch->stderr_limit;
    c->stderr_data = malloc(c->stderr_limit ? c->stderr_limit : 1);
    if (!c->stderr_data) {
      pthread_mutex_destroy(&c->mu);
      free(c);
      return EXT_NOMEM;
    }
  }
  int fds[2], errfds[2] = {-1, -1};
  if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds))
    goto fail;
  if ((fds[0] = keep_high(fds[0])) < 0 || (fds[1] = keep_high(fds[1])) < 0) {
    if (fds[0] >= 0)
      close(fds[0]);
    if (fds[1] >= 0)
      close(fds[1]);
    goto fail;
  }
#ifdef SO_NOSIGPIPE
  int yes = 1;
  if (setsockopt(fds[0], SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof(yes))) {
    close(fds[0]);
    close(fds[1]);
    goto fail;
  }
#endif
  if (launch->capture_stderr) {
    int bad = pipe(errfds);
    if (!bad && ((errfds[0] = keep_high(errfds[0])) < 0 ||
                 (errfds[1] = keep_high(errfds[1])) < 0))
      bad = 1;
    if (bad) {
      for (int i = 0; i < 2; i++)
        if (errfds[i] >= 0)
          close(errfds[i]);
      close(fds[0]);
      close(fds[1]);
      goto fail;
    }
  }
  posix_spawn_file_actions_t actions;
  int err = posix_spawn_file_actions_init(&actions);
  if (err) {
    close(fds[0]);
    close(fds[1]);
    for (int i = 0; i < 2; i++)
      if (errfds[i] >= 0)
        close(errfds[i]);
    goto fail;
  }
  if (!(err = posix_spawn_file_actions_adddup2(&actions, fds[1], STDIN_FILENO)))
    err = posix_spawn_file_actions_adddup2(&actions, fds[1], STDOUT_FILENO);
  if (!err)
    err = posix_spawn_file_actions_addclose(&actions, fds[0]);
  if (!err)
    err = posix_spawn_file_actions_addclose(&actions, fds[1]);
  if (!err && launch->capture_stderr) {
    err = posix_spawn_file_actions_adddup2(&actions, errfds[1], STDERR_FILENO);
    if (!err)
      err = posix_spawn_file_actions_addclose(&actions, errfds[0]);
    if (!err)
      err = posix_spawn_file_actions_addclose(&actions, errfds[1]);
  }
#ifdef EXT_HAVE_SPAWN_CHDIR
  if (!err && launch->directory)
    err = posix_spawn_file_actions_addchdir_np(&actions, launch->directory);
#endif
  if (!err)
    err = posix_spawn(&c->pid, launch->path, &actions, NULL,
                      launch->arguments, launch->environment);
  posix_spawn_file_actions_destroy(&actions);
  close(fds[1]);
  if (errfds[1] >= 0)
    close(errfds[1]);
  if (err) {
    close(fds[0]);
    if (errfds[0] >= 0)
      close(errfds[0]);
    goto fail;
  }
  c->fd = fds[0];
  if (launch->capture_stderr) {
    c->stderr_fd = errfds[0];
    if (pthread_create(&c->stderr_thread, NULL, stderr_reader, c)) {
      ext_channel_destroy(c);
      return EXT_IO;
    }
    c->stderr_started = 1;
  }
  if (fcntl(c->fd, F_SETFL, fcntl(c->fd, F_GETFL) | O_NONBLOCK) < 0) {
    ext_channel_destroy(c);
    return EXT_IO;
  }
  *out = c;
  return EXT_OK;
fail:
  free(c->stderr_data);
  pthread_mutex_destroy(&c->mu);
  free(c);
  return EXT_IO;
}

ext_status ext_channel_wait(ext_channel *c, int write, int timeout_ms) {
  short events = write ? POLLOUT : POLLIN;
  struct pollfd p = {c->fd, events, 0};
  int r = poll(&p, 1, timeout_ms);
  if (r < 0 && errno == EINTR)
    return EXT_TIMEOUT;
  if (r < 0)
    return EXT_IO;
  if (!r)
    return EXT_TIMEOUT;
  return (p.revents & events) ? EXT_OK : EXT_IO;
}

ptrdiff_t ext_channel_read(ext_channel *c, uint8_t *buffer, size_t length) {
  ssize_t r = recv(c->fd, buffer, length, 0);
  if (r < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK))
    return 0;
  return r <= 0 ? -1 : (ptrdiff_t)r;
}

ptrdiff_t ext_channel_write(ext_channel *c, const uint8_t *buffer,
                            size_t length) {
#ifdef MSG_NOSIGNAL
  ssize_t r = send(c->fd, buffer, length, MSG_NOSIGNAL);
#else
  ssize_t r = send(c->fd, buffer, length, 0);
#endif
  if (r < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK))
    return 0;
  return r <= 0 ? -1 : (ptrdiff_t)r;
}

void ext_channel_kill(ext_channel *c) {
  if (c->fd >= 0)
    shutdown(c->fd, SHUT_RDWR);
  if (c->pid > 0)
    kill(c->pid, SIGKILL);
}

void ext_channel_destroy(ext_channel *c) {
  if (!c)
    return;
  ext_channel_kill(c);
  if (c->pid > 0) {
    int status;
    while (waitpid(c->pid, &status, 0) < 0 && errno == EINTR) {
    }
  }
  if (c->fd >= 0)
    close(c->fd);
  if (c->stderr_started) {
    pthread_mutex_lock(&c->mu);
    c->stderr_stop = 1;
    pthread_mutex_unlock(&c->mu);
    pthread_join(c->stderr_thread, NULL);
  }
  if (c->stderr_fd >= 0)
    close(c->stderr_fd);
  free(c->stderr_data);
  pthread_mutex_destroy(&c->mu);
  free(c);
}

size_t ext_channel_stderr(ext_channel *c, uint8_t *buffer, size_t capacity) {
  pthread_mutex_lock(&c->mu);
  size_t n = c->stderr_len;
  if (n && buffer)
    memcpy(buffer, c->stderr_data, n < capacity ? n : capacity);
  pthread_mutex_unlock(&c->mu);
  return n;
}
