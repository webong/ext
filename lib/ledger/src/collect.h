/* The ledger's machine collectors, through each kernel's native interface.
 * Their JSON observations are the recorder's input contract. */
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
/* The recorder environment of this machine, as JSON (ext_ledger_collect_environment). */
char *ll_machine_environment(void);

/* The stable digest identifying the observed host environment. */
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
/* Append one process identity, or return NOT_FOUND if it exited. */
int ll_process_one(const ext_ledger_context *ctx, int64_t pid, lj_value *processes);
/* Add bounded usage and resource evidence to one identity observation. */
int ll_process_resources(const ext_ledger_context *ctx, int64_t pid, int64_t limit, lj_value *process);
/* Resource fields obey the ledger's 4096-byte string bound and per-kind limit. */
int ll_process_resource(lj_value *process, lj_value *resource, int64_t limit);
int ll_native_resource(lj_value *process, int64_t limit, const char *kind, const char *path, const char *descriptor,
                       const char *protocol, const char *local, const char *remote, const char *endpoint, const char *state);
int ll_process_coverage(lj_value *process, const char *kind, const char *state, const char *reason, const char *detail);
int ll_process_coverage_errno(lj_value *process, const char *kind, int err, const char *what);
/* The start identity of one process; NOT_FOUND when it has none. */
int ll_process_start(int64_t pid, char *out, size_t n);

/* ---- host inventory (collect_host.c; collect_host_posix.c, collect_win32.c) ---- */
typedef struct {
  char *path;
  const char *source; /* registered, system, configured-default, path */
} ll_shell_path;
/* The platform's own shell locations (/etc/shells; the Windows system shells). */
int ll_platform_shell_paths(const ext_ledger_context *ctx, ll_shell_path **out, size_t *n);
int ll_is_executable(const char *path);
char *ll_resolve_path(const char *path); /* symlinks evaluated; NULL when it cannot be */
char ll_path_list_separator(void);
char *ll_path_join(const char *dir, const char *name);
char *ll_path_clean(const char *path);
const char *ll_executable_suffix(void); /* ".exe" on Windows */
/* Appends one FilesystemInfo per mounted filesystem (source not yet redacted). */
int ll_platform_filesystems(const ext_ledger_context *ctx, lj_value *filesystems);
int ll_platform_filesystem_at(const ext_ledger_context *ctx, const char *path, lj_value **out);
int ll_host_access(const char *path, int directory_writable);
lj_value *ll_filesystem_space(uint64_t blocks, uint64_t free_blocks, uint64_t available, uint64_t size);

int ll_collect_shells(const ext_ledger_context *ctx, const char *search_path, lj_value **out);
int ll_collect_programs(const ext_ledger_context *ctx, const lj_value *in, lj_value **out); /* collect_programs.c */
int ll_inspect_program(const ext_ledger_context *ctx, const lj_value *in, lj_value **out); /* collect_programs.c */
int ll_service_render(const ext_ledger_context *ctx, const lj_value *in, char **out, size_t *out_len); /* service_definition.c */
int ll_service_file(const ext_ledger_context *ctx, const char *operation, const lj_value *in, char **out, size_t *out_len); /* service_store.c */
int ll_collect_filesystems(const ext_ledger_context *ctx, lj_value **out);
int ll_collect_resources(const ext_ledger_context *ctx, lj_value **out); /* collect_resources.c */
int ll_collect_webviews(const ext_ledger_context *ctx, lj_value **out);  /* collect_webviews.c */
int ll_collect_surfaces(const ext_ledger_context *ctx, lj_value **out);  /* collect_surfaces.c */
int ll_collect_permissions(const ext_ledger_context *ctx, lj_value **out);
int ll_collect_desktop(const ext_ledger_context *ctx, lj_value **out); /* collect_desktop.c */
int ll_collect_accessibility(const ext_ledger_context *ctx, lj_value **out); /* collect_accessibility.c */

/* The described interface can bound process enumeration without changing the
 * default typed C API. The caller supplies a limit from 1 to 65536. */
int ll_collect_with_options(const ext_ledger_context *ctx, const char *what, int64_t limit, int64_t pid, int64_t resource_limit,
                            char **out, size_t *out_len);

#endif
