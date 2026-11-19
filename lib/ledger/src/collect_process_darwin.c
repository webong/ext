/* Process identities on macOS and iOS, through sysctl, as res/system's
 * DiscoverProcesses reads them: the process table, the executable path that
 * starts KERN_PROCARGS2 (arguments and environment are never read), the
 * application bundle the executable is in, and the record read again to catch
 * a PID reused during the reads. iOS refuses the process table to an app; the
 * collection then fails as unavailable. */
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#include "collect.h"
#include "recorder.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/sysctl.h>
#include <sys/types.h>

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

static int proc_record(int pid, struct kinfo_proc *out) {
  int mib[4] = {CTL_KERN, KERN_PROC, KERN_PROC_PID, pid};
  size_t size = sizeof *out;
  memset(out, 0, sizeof *out);
  if (sysctl(mib, 4, out, &size, NULL, 0) != 0) return errno;
  if (size == 0 || out->kp_proc.p_pid != pid) return ENOENT;
  return 0;
}

/* The executable path at the start of KERN_PROCARGS2, after argc. */
static int executable(int pid, char **out) {
  *out = NULL;
  int mib[3] = {CTL_KERN, KERN_PROCARGS2, pid}, argmax_mib[2] = {CTL_KERN, KERN_ARGMAX}, argmax = 0;
  size_t size = sizeof argmax;
  if (sysctl(argmax_mib, 2, &argmax, &size, NULL, 0) != 0 || argmax <= 0) argmax = 1 << 20;
  char *buf = malloc((size_t)argmax);
  if (!buf) return ENOMEM;
  size = (size_t)argmax;
  if (sysctl(mib, 3, buf, &size, NULL, 0) != 0) {
    int err = errno;
    free(buf);
    return err;
  }
  int rc = 0;
  if (size <= 4) rc = -1; /* executable path unavailable */
  else {
    char *end = memchr(buf + 4, 0, size - 4);
    if (!end) rc = -2; /* missing executable path terminator */
    else {
      size_t n = (size_t)(end - (buf + 4));
      if ((*out = malloc(n + 1))) {
        memcpy(*out, buf + 4, n);
        (*out)[n] = 0;
      } else rc = ENOMEM;
    }
  }
  free(buf);
  return rc;
}

/* The .app bundle an absolute executable path is in: the first component ending in .app. */
static int application(lj_value *p, const char *path) {
  if (path[0] != '/') return 0;
  const char *component = path;
  while (component) {
    const char *slash = strchr(component, '/');
    size_t n = slash ? (size_t)(slash - component) : strlen(component);
    if (n > 4 && strncasecmp(component + n - 4, ".app", 4) == 0) {
      size_t bundle_len = (size_t)(component - path) + n;
      lj_value *app = rec_obj();
      int bad = set(app, "name", rec_strn(component, n - 4)) | set(app, "path", rec_strn(path, bundle_len)) | set(p, "application", app);
      return bad;
    }
    component = slash ? slash + 1 : NULL;
  }
  return 0;
}

/* One process; 1 when it went away and is left out. */
static int read_process(int pid, lj_value *processes) {
  struct kinfo_proc record;
  int err = proc_record(pid, &record);
  lj_value *p = rec_obj(), *coverage = rec_obj();
  int bad = set(p, "pid", rec_num(pid));
  if (err) {
    lj_free(p);
    lj_free(coverage);
    if (err == ENOENT || err == ESRCH) return 1;
    p = rec_obj();
    coverage = rec_obj();
    bad = set(p, "pid", rec_num(pid)) | set(p, "parent_pid", rec_num(0)) | set(p, "name", rec_str("")) | set(coverage, "identity", ll_status_errno(err, NULL)) |
          set(p, "coverage", coverage) | lj_array_push(processes, p);
    return bad ? -1 : 0;
  }
  char name[MAXCOMLEN + 2], owner[24], start[64];
  memcpy(name, record.kp_proc.p_comm, sizeof record.kp_proc.p_comm < sizeof name ? sizeof record.kp_proc.p_comm : sizeof name);
  name[sizeof name - 1] = 0;
  name[strnlen(name, sizeof name - 1)] = 0;
  snprintf(owner, sizeof owner, "%u", (unsigned)record.kp_eproc.e_ucred.cr_uid);
  struct timeval started = record.kp_proc.p_starttime;
  bad |= set(p, "parent_pid", rec_num(record.kp_eproc.e_ppid)) | set(p, "name", rec_str(name)) | set(p, "owner", rec_str(owner));
  if (started.tv_sec != 0) {
    snprintf(start, sizeof start, "%lld:%d", (long long)started.tv_sec, (int)started.tv_usec);
    bad |= set(p, "start_id", rec_str(start)) | set(coverage, "identity", ll_status("complete", NULL, NULL));
  } else {
    bad |= set(coverage, "identity", ll_status("unsupported", "unsupported", "start time unavailable"));
  }
  bad |= set(coverage, "owner", ll_status("complete", NULL, NULL));
  char *exe;
  int e = executable(pid, &exe);
  if (e == 0) {
    bad |= set(p, "executable", rec_str(exe)) | set(coverage, "executable", ll_status("complete", NULL, NULL)) | application(p, exe);
    free(exe);
  } else if (e == -1) {
    bad |= set(coverage, "executable", ll_status("unavailable", "no_device", "executable path unavailable"));
  } else if (e == -2) {
    bad |= set(coverage, "executable", ll_status("unavailable", "no_device", "missing executable path terminator"));
  } else {
    bad |= set(coverage, "executable", ll_status_errno(e, NULL));
  }
  struct kinfo_proc after;
  err = proc_record(pid, &after);
  if (err == ENOENT || err == ESRCH) {
    lj_free(p);
    lj_free(coverage);
    return 1;
  }
  if (err || after.kp_proc.p_starttime.tv_sec != started.tv_sec || after.kp_proc.p_starttime.tv_usec != started.tv_usec) {
    lj_free(p);
    lj_free(coverage);
    p = rec_obj();
    coverage = rec_obj();
    bad = set(p, "pid", rec_num(pid)) | set(p, "parent_pid", rec_num(0)) | set(p, "name", rec_str("")) |
          set(coverage, "identity", err ? ll_status_errno(err, NULL) : ll_status("unavailable", "no_device", "process identity changed during discovery"));
  }
  bad |= set(p, "coverage", coverage) | lj_array_push(processes, p);
  return bad ? -1 : 0;
}

int ll_processes(const ext_ledger_context *ctx, int64_t limit, lj_value *processes, lj_value **enumeration) {
  *enumeration = NULL;
  int mib[4] = {CTL_KERN, KERN_PROC, KERN_PROC_ALL, 0};
  size_t size = 0;
  struct kinfo_proc *all = NULL;
  for (int attempt = 0; attempt < 8; attempt++) { /* the table can grow between the two calls */
    if (sysctl(mib, 3, NULL, &size, NULL, 0) != 0) return errno == EPERM ? EXT_LEDGER_DENIED : EXT_LEDGER_UNAVAILABLE;
    size += size / 8 + sizeof *all;
    free(all);
    if (!(all = malloc(size))) return EXT_LEDGER_IO;
    if (sysctl(mib, 3, all, &size, NULL, 0) == 0) break;
    if (errno != ENOMEM) {
      int err = errno;
      free(all);
      return err == EPERM ? EXT_LEDGER_DENIED : EXT_LEDGER_UNAVAILABLE;
    }
  }
  size_t n = size / sizeof *all;
  int rc = 0, limited = 0;
  for (size_t i = 0; !rc && i < n; i++) {
    if ((rc = ll_ctx_check(ctx))) break;
    int pid = all[i].kp_proc.p_pid;
    if (pid <= 0) continue;
    if ((int64_t)processes->u.array.len >= limit) {
      limited = 1;
      break;
    }
    if (read_process(pid, processes) < 0) rc = EXT_LEDGER_IO;
  }
  free(all);
  if (rc) return rc;
  *enumeration = limited ? ll_status("partial", NULL, "record limit reached") : ll_status("complete", NULL, NULL);
  return *enumeration ? 0 : EXT_LEDGER_IO;
}

int ll_process_host_id(char out[25]) {
  char boot[128];
  size_t size = sizeof boot;
  if (sysctlbyname("kern.bootsessionuuid", boot, &size, NULL, 0) != 0 || size == 0) return EXT_LEDGER_UNAVAILABLE;
  boot[size < sizeof boot ? size : sizeof boot - 1] = 0;
  size_t n = strlen(boot);
  if (!n) return EXT_LEDGER_UNAVAILABLE;
  char *env = ll_host_environment();
  if (!env) return EXT_LEDGER_IO;
  lj_buf s = {0};
  lj_buf_puts(&s, env);
  lj_buf_put(&s, boot, n);
  free(env);
  if (s.oom) {
    lj_buf_free(&s);
    return EXT_LEDGER_IO;
  }
  rec_digest(s.ptr, s.len, out);
  lj_buf_free(&s);
  return 0;
}
#endif
