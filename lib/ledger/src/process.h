/* Child processes for the supervisor: starting one in its own process group
 * (POSIX) or job object (Windows), collecting what it writes, waiting for it,
 * and ending the whole tree. Also threads, random identifiers and the identity
 * of a running process, which recovery checks before it ends one. */
#ifndef EXT_LEDGER_PROCESS_H
#define EXT_LEDGER_PROCESS_H
#include "ledger_internal.h"

typedef struct ll_thread ll_thread;
int ll_thread_start(ll_thread **out, void (*fn)(void *), void *arg); /* 0 on success */
void ll_thread_join(ll_thread *t);

/* 2*bytes hex digits and a NUL from the OS's random source; 0 on success. */
int ll_random_hex(char *out, size_t bytes);

/* A copy of this process's environment, as NAME=value strings; NULL-terminated. */
char **ll_environ_copy(size_t *n);
/* Whether two environment names are the same (case-insensitive on Windows). */
int ll_env_same(const char *a, size_t alen, const char *b, size_t blen);

/* The file a command names, as exec.LookPath finds it; EXT_LEDGER_NOT_FOUND when none. */
int ll_look_path(const char *command, char **path);

typedef void (*ll_proc_sink)(void *arg, const char *data, size_t n);
typedef struct {
  const char *path;        /* from ll_look_path */
  const char *const *argv; /* argv[0] is the command; NULL-terminated */
  const char *dir;         /* NULL or "": the current directory */
  const char *const *env;  /* NAME=value, NULL-terminated */
  ll_proc_sink sink;       /* NULL: output is discarded */
  void *sink_arg;
} ll_proc_spec;

typedef struct ll_proc ll_proc;
/* EXT_LEDGER_UNSUPPORTED where an application cannot start processes (iOS). */
int ll_proc_spawn(const ll_proc_spec *spec, ll_proc **out, int64_t *pid);
/* 0 once it exited, with its code (-1 when a signal ended it); EXT_LEDGER_TIMEOUT before. */
int ll_proc_wait(ll_proc *p, int64_t timeout_ms, int *code);
/* Ends the process tree: politely, or by force. */
void ll_proc_terminate(ll_proc *p, int force);
/* Reaps the process (it must have exited) and stops collecting output. */
void ll_proc_close(ll_proc *p);

int ll_proc_alive(int64_t pid);
/* A digest of the pid and the process's start, as hex; 0 on success. */
int ll_proc_identity(int64_t pid, char out[65]);
/* Ends an orphan's tree after checking it is still the process recorded. */
int ll_proc_terminate_orphan(int64_t pid, const char *identity);
#endif
