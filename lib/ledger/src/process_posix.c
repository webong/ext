/* Child processes on POSIX systems. A child leads its own process group, so
 * ending it ends what it started too; its stdout and stderr share one pipe that
 * a thread drains into the sink. iOS forbids an application to start processes. */
#ifndef _WIN32
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#elif !defined(__FreeBSD__) && !defined(__NetBSD__) && !defined(__OpenBSD__) && !defined(__DragonFly__)
#define _DEFAULT_SOURCE
#endif
#include "process.h"
#include "collect.h"

#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <TargetConditionals.h>
#endif

extern char **environ;

struct ll_thread {
  pthread_t t;
  void (*fn)(void *);
  void *arg;
};

static void *thread_main(void *p) {
  ll_thread *t = p;
  t->fn(t->arg);
  return NULL;
}

int ll_thread_start(ll_thread **out, void (*fn)(void *), void *arg) {
  ll_thread *t = malloc(sizeof *t);
  if (!t) return EXT_LEDGER_IO;
  t->fn = fn;
  t->arg = arg;
  if (pthread_create(&t->t, NULL, thread_main, t)) {
    free(t);
    return EXT_LEDGER_IO;
  }
  *out = t;
  return 0;
}

void ll_thread_join(ll_thread *t) {
  if (!t) return;
  pthread_join(t->t, NULL);
  free(t);
}

int ll_random_hex(char *out, size_t bytes) {
  unsigned char buf[64];
  if (bytes > sizeof buf) return EXT_LEDGER_INVALID;
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__) || defined(__DragonFly__)
  arc4random_buf(buf, bytes);
#else
  int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
  if (fd < 0) return EXT_LEDGER_IO;
  size_t got = 0;
  while (got < bytes) {
    ssize_t n = read(fd, buf + got, bytes - got);
    if (n < 0 && errno == EINTR) continue;
    if (n <= 0) {
      close(fd);
      return EXT_LEDGER_IO;
    }
    got += (size_t)n;
  }
  close(fd);
#endif
  static const char hex[] = "0123456789abcdef";
  for (size_t i = 0; i < bytes; i++) {
    out[2 * i] = hex[buf[i] >> 4];
    out[2 * i + 1] = hex[buf[i] & 15];
  }
  out[2 * bytes] = 0;
  return 0;
}

char **ll_environ_copy(size_t *n) {
  size_t count = 0;
  while (environ && environ[count]) count++;
  char **out = calloc(count + 1, sizeof *out);
  if (!out) return NULL;
  for (size_t i = 0; i < count; i++) {
    if (!(out[i] = strdup(environ[i]))) {
      for (size_t j = 0; j < i; j++) free(out[j]);
      free(out);
      return NULL;
    }
  }
  *n = count;
  return out;
}

int ll_env_same(const char *a, size_t alen, const char *b, size_t blen) { return alen == blen && memcmp(a, b, alen) == 0; }

static int executable_file(const char *path) {
  struct stat st;
  return stat(path, &st) == 0 && !S_ISDIR(st.st_mode) && (st.st_mode & 0111);
}

int ll_look_path(const char *command, char **path) {
  *path = NULL;
  if (!command || !*command) return EXT_LEDGER_NOT_FOUND;
  if (strchr(command, '/')) {
    if (!executable_file(command)) return EXT_LEDGER_NOT_FOUND;
    return (*path = strdup(command)) ? 0 : EXT_LEDGER_IO;
  }
  const char *list = getenv("PATH");
  if (!list) return EXT_LEDGER_NOT_FOUND;
  for (const char *p = list;;) {
    const char *end = strchr(p, ':');
    size_t n = end ? (size_t)(end - p) : strlen(p);
    /* exec.LookPath refuses a relative entry, which would resolve against the working directory. */
    if (n && p[0] == '/') {
      char *candidate = malloc(n + strlen(command) + 2);
      if (!candidate) return EXT_LEDGER_IO;
      memcpy(candidate, p, n);
      candidate[n] = '/';
      strcpy(candidate + n + 1, command);
      if (executable_file(candidate)) {
        *path = candidate;
        return 0;
      }
      free(candidate);
    }
    if (!end) break;
    p = end + 1;
  }
  return EXT_LEDGER_NOT_FOUND;
}

struct ll_proc {
  pid_t pid;
  int reaped, code, out;
  ll_thread *reader;
  volatile int stop;
  ll_proc_sink sink;
  void *sink_arg;
};

static void drain(void *arg) {
  ll_proc *p = arg;
  char buf[4096];
  for (;;) {
    struct pollfd fd = {p->out, POLLIN, 0};
    int r = poll(&fd, 1, 50);
    if (r < 0 && errno == EINTR) continue;
    if (r < 0) break;
    if (r == 0) {
      if (ll_flag_load(&p->stop)) break;
      continue;
    }
    ssize_t n = read(p->out, buf, sizeof buf);
    if (n < 0 && (errno == EINTR || errno == EAGAIN)) continue;
    if (n <= 0) break;
    p->sink(p->sink_arg, buf, (size_t)n);
  }
}

static void set_cloexec(int fd) { fcntl(fd, F_SETFD, fcntl(fd, F_GETFD) | FD_CLOEXEC); }

int ll_proc_spawn(const ll_proc_spec *spec, ll_proc **out, int64_t *pid) {
  *out = NULL;
  *pid = 0;
#if defined(__APPLE__) && TARGET_OS_IPHONE
  (void)spec;
  (void)drain;
  (void)set_cloexec;
  return EXT_LEDGER_UNSUPPORTED;
#else
  int status[2], output[2] = {-1, -1};
  if (pipe(status)) return EXT_LEDGER_IO;
  set_cloexec(status[0]);
  set_cloexec(status[1]);
  if (spec->sink) {
    if (pipe(output)) {
      close(status[0]);
      close(status[1]);
      return EXT_LEDGER_IO;
    }
    set_cloexec(output[0]);
  } else if ((output[1] = open("/dev/null", O_WRONLY)) < 0) {
    close(status[0]);
    close(status[1]);
    return EXT_LEDGER_IO;
  }
  int null_in = open("/dev/null", O_RDONLY);
  long max_fd = sysconf(_SC_OPEN_MAX);
  if (max_fd < 0 || max_fd > 65536) max_fd = 65536;
  pid_t child = fork();
  if (child == 0) {
    /* Only async-signal-safe calls from here to exec. */
    setpgid(0, 0);
    int err = 0;
    if (null_in < 0 || dup2(null_in, 0) < 0 || dup2(output[1], 1) < 0 || dup2(output[1], 2) < 0) err = errno ? errno : EIO;
    if (!err && spec->dir && *spec->dir && chdir(spec->dir)) err = errno;
    if (!err) {
      for (long fd = 3; fd < max_fd; fd++)
        if (fd != status[1]) close((int)fd);
      sigset_t none;
      sigemptyset(&none);
      sigprocmask(SIG_SETMASK, &none, NULL);
      execve(spec->path, (char *const *)spec->argv, (char *const *)spec->env);
      err = errno;
    }
    ssize_t ignored = write(status[1], &err, sizeof err);
    (void)ignored;
    _exit(127);
  }
  close(status[1]);
  if (null_in >= 0) close(null_in);
  close(output[1]);
  if (child < 0) {
    close(status[0]);
    if (output[0] >= 0) close(output[0]);
    return EXT_LEDGER_IO;
  }
  setpgid(child, child); /* either side may win the race */
  int err = 0;
  ssize_t n;
  while ((n = read(status[0], &err, sizeof err)) < 0 && errno == EINTR) {
  }
  close(status[0]);
  if (n > 0) {
    int st;
    while (waitpid(child, &st, 0) < 0 && errno == EINTR) {
    }
    if (output[0] >= 0) close(output[0]);
    return err == ENOENT ? EXT_LEDGER_NOT_FOUND : err == EACCES || err == EPERM ? EXT_LEDGER_DENIED : EXT_LEDGER_IO;
  }
  ll_proc *p = calloc(1, sizeof *p);
  if (!p) {
    kill(-child, SIGKILL);
    waitpid(child, NULL, 0);
    if (output[0] >= 0) close(output[0]);
    return EXT_LEDGER_IO;
  }
  p->pid = child;
  p->out = output[0];
  p->sink = spec->sink;
  p->sink_arg = spec->sink_arg;
  if (p->out >= 0 && ll_thread_start(&p->reader, drain, p)) {
    close(p->out);
    p->out = -1;
  }
  *out = p;
  *pid = child;
  return 0;
#endif
}

int ll_proc_wait(ll_proc *p, int64_t timeout_ms, int *code) {
  int64_t deadline = ll_now_unix_ms() + (timeout_ms > 0 ? timeout_ms : 0);
  while (!p->reaped) {
    int st;
    pid_t r = waitpid(p->pid, &st, WNOHANG);
    if (r == p->pid) {
      p->reaped = 1;
      p->code = WIFEXITED(st) ? WEXITSTATUS(st) : -1;
      break;
    }
    if (r < 0 && errno != EINTR) {
      p->reaped = 1;
      p->code = -1;
      break;
    }
    if (ll_now_unix_ms() >= deadline) return EXT_LEDGER_TIMEOUT;
    ll_sleep_ms(5);
  }
  *code = p->code;
  return 0;
}

void ll_proc_terminate(ll_proc *p, int force) {
  if (p->pid > 0) kill(-p->pid, force ? SIGKILL : SIGTERM);
}

void ll_proc_close(ll_proc *p) {
  if (!p) return;
  if (!p->reaped) {
    int code;
    ll_proc_terminate(p, 1);
    ll_proc_wait(p, 60000, &code);
  }
  ll_flag_store(&p->stop, 1); /* a descendant may still hold the pipe open */
  ll_thread_join(p->reader);
  if (p->out >= 0) close(p->out);
  free(p);
}

int ll_proc_alive(int64_t pid) {
  if (pid <= 0 || pid > 0x7fffffff) return 0;
  return kill((pid_t)pid, 0) == 0 || errno == EPERM;
}

int ll_proc_identity(int64_t pid, char out[65]) {
  out[0] = 0;
  char start[96], text[128];
  int rc = ll_process_start(pid, start, sizeof start);
  if (rc) return rc;
  int n = snprintf(text, sizeof text, "%lld:%s", (long long)pid, start);
  ll_sha256_hex(text, (size_t)n, out);
  return 0;
}

int ll_proc_terminate_orphan(int64_t pid, const char *identity) {
  if (!ll_proc_alive(pid)) return 0;
  char current[65];
  if (ll_proc_identity(pid, current) || strcmp(current, identity) != 0) return EXT_LEDGER_CHANGED; /* orphan process identity changed */
  if (getpgid((pid_t)pid) != (pid_t)pid) return EXT_LEDGER_DENIED; /* outside its isolated process group */
  kill(-(pid_t)pid, SIGTERM);
  int64_t deadline = ll_now_unix_ms() + 5000;
  while (ll_proc_alive(pid) && ll_now_unix_ms() < deadline) ll_sleep_ms(25);
  kill(-(pid_t)pid, SIGKILL);
  return 0;
}
#endif
