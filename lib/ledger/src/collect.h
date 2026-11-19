/* The collectors: what the recorder reads from the machine itself, through
 * each kernel's own interface. Their results take the JSON forms of
 * res/system's types, so the recorder records them as it records the same
 * observations given from outside. */
#ifndef EXT_LEDGER_COLLECT_H
#define EXT_LEDGER_COLLECT_H

#include "ledger_internal.h"

/* ---- the machine (collect_posix.c, collect_win32.c) ---- */
const char *ll_goos(void);   /* the OS as Go spells it: linux, darwin, ios, android, windows, freebsd, ... */
const char *ll_goarch(void); /* the architecture as Go spells it: amd64, arm64, 386, arm, ... */
char *ll_os_hostname(void);  /* "" when it cannot be read */
int64_t ll_os_ppid(void);
char *ll_os_getcwd(void);
int64_t ll_os_uid(void);     /* -1 where there is none (Windows) */
char *ll_os_home(void);      /* NULL when unknown */
char *ll_os_getenv(const char *name); /* NULL when unset */
int ll_os_exists(const char *path);
/* Extra identity of the host environment: on Linux "\0<mnt ns>\0<user ns>", elsewhere empty.
 * It holds NULs, so its length is returned in *len. */
char *ll_os_namespaces(size_t *len);
/* The recorder environment of this machine, as JSON (ext_ledger_machine_environment). */
char *ll_machine_environment(void);

/* The digest res/system calls the host environment (identity.HostEnvironment). */
char *ll_host_environment(void);

/* ---- statuses ---- */
/* A CollectionStatus: state, reason (may be NULL) and detail (may be NULL). */
lj_value *ll_status(const char *state, const char *reason, const char *detail);
/* The status of a failed OS call: exited for a gone process, permission-denied, or unavailable.
 * Its detail is "<what>: <error>", or the error alone when what is NULL. */
lj_value *ll_status_errno(int err, const char *what);

/* ---- processes (collect_process_<os>.c) ---- */
/* The host identity of process records: the host environment and this boot. */
int ll_process_host_id(char out[25]);
/* Appends one ProcessInfo object per process to processes (identity only), and sets *enumeration. */
int ll_processes(const ext_ledger_context *ctx, int64_t limit, lj_value *processes, lj_value **enumeration);

#endif
