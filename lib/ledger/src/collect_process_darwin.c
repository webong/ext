/* Process identities on macOS and iOS, through sysctl: the process table, the executable path that
 * starts KERN_PROCARGS2 (arguments and environment are never read), the
 * application bundle the executable is in, and the record read again to catch
 * a PID reused during the reads. iOS refuses the process table to an app; the
 * collection then fails as unavailable. */
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#include "collect.h"
#include "recorder.h"

#include <TargetConditionals.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/sysctl.h>
#include <sys/types.h>
#if !TARGET_OS_IPHONE
#include <arpa/inet.h>
#include <libproc.h>
#include <mach/mach_time.h>
#include <netinet/in.h>
#endif

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

int ll_process_one(const ext_ledger_context *ctx, int64_t pid, lj_value *processes) {
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  int read = read_process((int)pid, processes);
  return read < 0 ? EXT_LEDGER_IO : read > 0 ? EXT_LEDGER_NOT_FOUND : 0;
}

int ll_process_start(int64_t pid, char *out, size_t n) {
  out[0] = 0;
  lj_value *one = lj_new(LJ_ARRAY);
  if (!one) return EXT_LEDGER_IO;
  int r = read_process((int)pid, one);
  const char *start = one->u.array.len ? rec_field(one->u.array.items[0], "start_id") : "";
  snprintf(out, n, "%s", start);
  lj_free(one);
  return r == 0 && *out ? 0 : EXT_LEDGER_NOT_FOUND;
}

int ll_processes(const ext_ledger_context *ctx, int64_t limit, lj_value *processes, lj_value **enumeration) {
  *enumeration = NULL;
  int mib[4] = {CTL_KERN, KERN_PROC, KERN_PROC_ALL, 0};
  size_t size = 0;
  struct kinfo_proc *all = NULL;
  for (int attempt = 0; attempt < 8; attempt++) { /* the table can grow between the two calls */
    if (sysctl(mib, 3, NULL, &size, NULL, 0) != 0) return errno == EPERM || errno == EACCES ? EXT_LEDGER_DENIED : EXT_LEDGER_UNAVAILABLE;
    size += size / 8 + sizeof *all;
    free(all);
    if (!(all = malloc(size))) return EXT_LEDGER_IO;
    if (sysctl(mib, 3, all, &size, NULL, 0) == 0) break;
    if (errno != ENOMEM) {
      int err = errno;
      free(all);
      return err == EPERM || err == EACCES ? EXT_LEDGER_DENIED : EXT_LEDGER_UNAVAILABLE;
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
  if (sysctlbyname("kern.bootsessionuuid", boot, &size, NULL, 0) != 0)
    return errno == EPERM || errno == EACCES ? EXT_LEDGER_DENIED : EXT_LEDGER_UNAVAILABLE;
  if (size == 0) return EXT_LEDGER_UNAVAILABLE;
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

#if !TARGET_OS_IPHONE
static const char *tcp_state(int state) {
  static const char *names[] = {"CLOSED", "LISTEN", "SYN_SENT", "SYN_RECEIVED", "ESTABLISHED", "CLOSE_WAIT",
                                "FIN_WAIT_1", "CLOSING", "LAST_ACK", "FIN_WAIT_2", "TIME_WAIT"};
  return state >= 0 && state < (int)(sizeof names / sizeof *names) ? names[state] : NULL;
}

static void socket_address(char out[96], const struct in_sockinfo *info, int local) {
  int ipv6 = info->insi_vflag & INI_IPV6;
  const void *ip = ipv6 ? (const void *)(local ? &info->insi_laddr.ina_6 : &info->insi_faddr.ina_6)
                        : (const void *)(local ? &info->insi_laddr.ina_46.i46a_addr4 : &info->insi_faddr.ina_46.i46a_addr4);
  char host[INET6_ADDRSTRLEN];
  if (!inet_ntop(ipv6 ? AF_INET6 : AF_INET, ip, host, sizeof host)) return;
  unsigned port = (unsigned)ntohs((uint16_t)(local ? info->insi_lport : info->insi_fport));
  if (ipv6) snprintf(out, 96, "[%s]:%u", host, port);
  else snprintf(out, 96, "%s:%u", host, port);
}

static int socket_resource(int pid, int fd, int64_t limit, lj_value *process) {
  char descriptor[24];
  snprintf(descriptor, sizeof descriptor, "%d", fd);
  struct socket_fdinfo info;
  if (proc_pidfdinfo(pid, fd, PROC_PIDFDSOCKETINFO, &info, sizeof info) != sizeof info) {
    int rc = ll_native_resource(process, limit, "socket", NULL, descriptor, NULL, NULL, NULL, NULL, NULL);
    return rc ? rc : ll_process_coverage(process, "socket", "partial", NULL, "some socket endpoints are unavailable");
  }
  char local[96] = "", remote[96] = "", endpoint[4097] = "";
  const char *protocol = NULL, *state = NULL;
  const struct socket_info *socket = &info.psi;
  if (socket->soi_kind == SOCKINFO_UN) {
    protocol = "unix";
    const struct sockaddr_un *address = &socket->soi_proto.pri_un.unsi_addr.ua_sun;
    size_t size = strnlen(address->sun_path, sizeof address->sun_path);
    if (size < sizeof endpoint) memcpy(endpoint, address->sun_path, size);
  } else if (socket->soi_kind == SOCKINFO_TCP || socket->soi_kind == SOCKINFO_IN) {
    const struct in_sockinfo *inet = socket->soi_kind == SOCKINFO_TCP ? &socket->soi_proto.pri_tcp.tcpsi_ini : &socket->soi_proto.pri_in;
    if (inet->insi_vflag & (INI_IPV4 | INI_IPV6)) {
      protocol = socket->soi_protocol == IPPROTO_TCP ? "tcp" : socket->soi_protocol == IPPROTO_UDP ? "udp" : NULL;
      socket_address(local, inet, 1);
      socket_address(remote, inet, 0);
      if (socket->soi_kind == SOCKINFO_TCP) state = tcp_state(socket->soi_proto.pri_tcp.tcpsi_state);
    }
  }
  int rc = ll_native_resource(process, limit, "socket", NULL, descriptor, protocol, local, remote, endpoint, state);
  if (!rc && !protocol) rc = ll_process_coverage(process, "socket", "partial", NULL, "some socket protocols or endpoints are unavailable");
  return rc;
}
#endif

int ll_process_resources(const ext_ledger_context *ctx, int64_t pid, int64_t limit, lj_value *process) {
#if TARGET_OS_IPHONE
  (void)ctx;
  (void)pid;
  (void)limit;
  (void)process;
  return EXT_LEDGER_UNSUPPORTED;
#else
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  struct proc_taskinfo task;
  if (proc_pidinfo((int)pid, PROC_PIDTASKINFO, 0, &task, sizeof task) == sizeof task) {
    lj_value *usage = rec_obj();
    mach_timebase_info_data_t timebase;
    int bad = rec_set(usage, "resident_bytes", rec_num((double)task.pti_resident_size)) |
              rec_set(usage, "virtual_bytes", rec_num((double)task.pti_virtual_size)) |
              rec_set(usage, "threads", rec_num((double)task.pti_threadnum));
    if (mach_timebase_info(&timebase) == KERN_SUCCESS && timebase.denom) {
      double seconds = ((double)task.pti_total_user + (double)task.pti_total_system) *
                       (double)timebase.numer / (double)timebase.denom / 1000000000.0;
      bad |= rec_set(usage, "cpu_seconds", rec_num(seconds));
    }
    if (bad || rec_set(process, "usage", usage)) return EXT_LEDGER_IO;
    rc = ll_process_coverage(process, "usage", "complete", NULL, NULL);
  } else rc = ll_process_coverage_errno(process, "usage", errno ? errno : ENOENT, "proc_pidinfo");
  if (rc) return rc;

  for (size_t i = 0; i < 3; i++) {
    static const char *kinds[] = {"file", "socket", "ipc"};
    if ((rc = ll_process_coverage(process, kinds[i], "complete", NULL, NULL))) return rc;
  }
  int bytes = proc_pidinfo((int)pid, PROC_PIDLISTFDS, 0, NULL, 0);
  if (bytes <= 0) {
    int err = errno ? errno : ENOENT;
    for (size_t i = 0; i < 3; i++) {
      static const char *kinds[] = {"file", "socket", "ipc"};
      if ((rc = ll_process_coverage_errno(process, kinds[i], err, "proc_pidinfo"))) return rc;
    }
  } else {
    int capacity = (int)(limit * 4 * (int64_t)sizeof(struct proc_fdinfo));
    if (bytes > capacity) bytes = capacity;
    struct proc_fdinfo *fds = malloc((size_t)bytes);
    if (!fds) return EXT_LEDGER_IO;
    int read = proc_pidinfo((int)pid, PROC_PIDLISTFDS, 0, fds, bytes);
    if (read < 0) read = 0;
    if (read == 0) {
      int err = errno ? errno : ENOENT;
      for (size_t i = 0; i < 3; i++) {
        static const char *kinds[] = {"file", "socket", "ipc"};
        rc |= ll_process_coverage_errno(process, kinds[i], err, "proc_pidinfo");
      }
    } else {
      for (int i = 0; i < read / (int)sizeof *fds && !rc; i++) {
        if ((rc = ll_ctx_check(ctx))) break;
        char fd[24];
        snprintf(fd, sizeof fd, "%d", fds[i].proc_fd);
        if (fds[i].proc_fdtype == PROX_FDTYPE_VNODE) {
          struct vnode_fdinfowithpath info;
          if (proc_pidfdinfo((int)pid, fds[i].proc_fd, PROC_PIDFDVNODEPATHINFO, &info, sizeof info) == sizeof info &&
              info.pvip.vip_path[0] == '/')
            rc = ll_native_resource(process, limit, "file", info.pvip.vip_path, fd, NULL, NULL, NULL, NULL, NULL);
          else rc = ll_process_coverage(process, "file", "partial", NULL, "some file paths are unavailable");
        } else if (fds[i].proc_fdtype == PROX_FDTYPE_SOCKET) {
          rc = socket_resource((int)pid, fds[i].proc_fd, limit, process);
        } else {
          char type[24];
          snprintf(type, sizeof type, "%u", fds[i].proc_fdtype);
          rc = ll_native_resource(process, limit, "ipc", NULL, fd, type, NULL, NULL, NULL, NULL);
          if (!rc) rc = ll_process_coverage(process, "ipc", "partial", NULL, "Mach port tables are not enumerated");
        }
      }
      if (!rc && read == capacity) {
        for (size_t i = 0; i < 3; i++) {
          static const char *kinds[] = {"file", "socket", "ipc"};
          rc |= ll_process_coverage(process, kinds[i], "partial", NULL, "record limit reached");
        }
      }
    }
    free(fds);
  }
  if (rc) return rc;

  rc = ll_process_coverage(process, "mapping", "complete", NULL, NULL);
  if (rc) return rc;
  uint64_t address = 0;
  for (int64_t seen = 0; seen < limit * 16; seen++) {
    if ((rc = ll_ctx_check(ctx))) return rc;
    struct proc_regionwithpathinfo region;
    int got = proc_pidinfo((int)pid, PROC_PIDREGIONPATHINFO, address, &region, sizeof region);
    if (got != sizeof region) {
      if (seen == 0 && errno) return ll_process_coverage_errno(process, "mapping", errno, "proc_pidinfo");
      return 0;
    }
    if (region.prp_vip.vip_path[0] == '/') {
      rc = ll_native_resource(process, limit, "mapping", region.prp_vip.vip_path, NULL, NULL, NULL, NULL, NULL, NULL);
      if (rc) return rc;
    }
    uint64_t next = region.prp_prinfo.pri_address + region.prp_prinfo.pri_size;
    if (next <= address) return ll_process_coverage(process, "mapping", "partial", NULL, "region enumeration did not advance");
    address = next;
  }
  return ll_process_coverage(process, "mapping", "partial", NULL, "record limit reached");
#endif
}
#endif
