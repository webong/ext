/* The collectors' shared part: the host environment, statuses, the snapshot a
 * process collection returns, and the recorder's scans. */
#include "collect.h"
#include "recorder.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

lj_value *ll_status(const char *state, const char *reason, const char *detail) {
  lj_value *s = rec_obj();
  if (rec_set(s, "state", rec_str(state)) | (reason && *reason ? rec_set(s, "reason", rec_str(reason)) : 0) |
      (detail && *detail ? rec_set(s, "detail", rec_str(detail)) : 0)) {
    lj_free(s);
    return NULL;
  }
  return s;
}

lj_value *ll_status_errno(int err, const char *what) {
  char detail[512];
  const char *msg = strerror(err);
  if (what) snprintf(detail, sizeof detail, "%s: %s", what, msg ? msg : "error");
  else snprintf(detail, sizeof detail, "%s", msg ? msg : "error");
  size_t at = what ? strlen(what) + 2 : 0; /* keep the native diagnostic's first letter lower case */
  if (at < sizeof detail && detail[at] >= 'A' && detail[at] <= 'Z') detail[at] = (char)(detail[at] + 32);
#ifdef ESRCH
  if (err == ESRCH) return ll_status("exited", NULL, detail);
#endif
  if (err == EACCES || err == EPERM) return ll_status("permission-denied", "permission", detail);
  return ll_status("unavailable", "no_device", detail);
}

int ll_process_coverage(lj_value *process, const char *kind, const char *state, const char *reason, const char *detail) {
  lj_value *coverage = lj_get(process, "coverage");
  return coverage && coverage->type == LJ_OBJECT && !rec_set(coverage, kind, ll_status(state, reason, detail)) ? 0 : EXT_LEDGER_IO;
}

int ll_process_coverage_errno(lj_value *process, const char *kind, int err, const char *what) {
  lj_value *coverage = lj_get(process, "coverage");
  return coverage && coverage->type == LJ_OBJECT && !rec_set(coverage, kind, ll_status_errno(err, what)) ? 0 : EXT_LEDGER_IO;
}

int ll_process_resource(lj_value *process, lj_value *resource, int64_t limit) {
  if (!resource) return EXT_LEDGER_IO;
  const char *name = rec_field(resource, "kind");
  char kind[16];
  if (strlen(name) >= sizeof kind) {
    lj_free(resource);
    return EXT_LEDGER_INVALID;
  }
  snprintf(kind, sizeof kind, "%s", name);
  lj_value *coverage = lj_get(process, "coverage");
  if (!*kind || !coverage || coverage->type != LJ_OBJECT) {
    lj_free(resource);
    return EXT_LEDGER_IO;
  }
  for (size_t i = 0; i < resource->u.object.len; i++) {
    const lj_value *field = resource->u.object.members[i].value;
    if (field->type == LJ_STRING && field->u.string.len > 4096) {
      lj_free(resource);
      return ll_process_coverage(process, kind, "partial", NULL, "resource exceeds graph string limit");
    }
  }
  lj_value *items = lj_get(process, "resources");
  if (!items) {
    items = lj_new(LJ_ARRAY);
    if (!items || rec_set(process, "resources", items)) {
      lj_free(resource);
      return EXT_LEDGER_IO;
    }
  }
  int64_t count = 0;
  for (size_t i = 0; i < items->u.array.len; i++) {
    const lj_value *old = items->u.array.items[i];
    if (strcmp(kind, rec_field(old, "kind"))) continue;
    count++;
    if (!strcmp(rec_field(old, "path"), rec_field(resource, "path")) &&
        !strcmp(rec_field(old, "descriptor"), rec_field(resource, "descriptor")) &&
        !strcmp(rec_field(old, "endpoint"), rec_field(resource, "endpoint")) &&
        !strcmp(rec_field(old, "protocol"), rec_field(resource, "protocol")) &&
        !strcmp(rec_field(old, "local_address"), rec_field(resource, "local_address")) &&
        !strcmp(rec_field(old, "remote_address"), rec_field(resource, "remote_address")) &&
        !strcmp(rec_field(old, "state"), rec_field(resource, "state"))) {
      lj_free(resource);
      return 0;
    }
  }
  if (count >= limit) {
    lj_free(resource);
    return ll_process_coverage(process, kind, "partial", NULL, "record limit reached");
  }
  return lj_array_push(items, resource) ? EXT_LEDGER_IO : 0;
}

int ll_native_resource(lj_value *process, int64_t limit, const char *kind, const char *path, const char *descriptor,
                       const char *protocol, const char *local, const char *remote, const char *endpoint, const char *state) {
  lj_value *resource = rec_obj();
  if (!resource) return EXT_LEDGER_IO;
  int bad = rec_set(resource, "kind", rec_str(kind));
  static const char *keys[] = {"path", "descriptor", "protocol", "local_address", "remote_address", "endpoint", "state"};
  const char *values[] = {path, descriptor, protocol, local, remote, endpoint, state};
  for (size_t i = 0; i < sizeof keys / sizeof *keys; i++)
    if (values[i] && *values[i]) bad |= rec_set(resource, keys[i], rec_str(values[i]));
  if (bad) {
    lj_free(resource);
    return EXT_LEDGER_IO;
  }
  return ll_process_resource(process, resource, limit);
}

char *ll_host_environment(void) {
  size_t ns_len = 0;
  char *host = ll_os_hostname(), *home = ll_os_home(), *ns = ll_os_namespaces(&ns_len);
  lj_buf b = {0};
  lj_buf_puts(&b, host && *host ? host : "hostname-unavailable");
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, ll_goos());
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, ll_goarch());
  lj_buf_putc(&b, 0);
  char uid[24];
  snprintf(uid, sizeof uid, "%lld", (long long)ll_os_uid());
  lj_buf_puts(&b, uid);
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, home ? home : "home-unavailable");
  if (ns) lj_buf_put(&b, ns, ns_len);
  free(host);
  free(home);
  char *d = malloc(25);
  if (d && !b.oom) rec_digest(b.ptr, b.len, d);
  else {
    free(d);
    d = NULL;
  }
  free(ns);
  lj_buf_free(&b);
  return d;
}

/* ---- collections ---- */

static int cmp_pid(const void *a, const void *b) {
  const lj_value *x = lj_get(*(lj_value *const *)a, "pid"), *y = lj_get(*(lj_value *const *)b, "pid");
  double p = x ? x->u.number : 0, q = y ? y->u.number : 0;
  return p < q ? -1 : p > q;
}

/* Put the same observation time on the enumeration and each coverage status. */
static int stamp(lj_value *enumeration, lj_value *processes, const char *at) {
  int bad = rec_set(enumeration, "observed_at", rec_str(at));
  for (size_t i = 0; i < processes->u.array.len; i++) {
    lj_value *coverage = lj_get(processes->u.array.items[i], "coverage");
    for (size_t k = 0; coverage && coverage->type == LJ_OBJECT && k < coverage->u.object.len; k++)
      bad |= rec_set(coverage->u.object.members[k].value, "observed_at", rec_str(at));
  }
  return bad;
}

static char *copy_string(const char *s) {
  size_t n = strlen(s) + 1;
  char *copy = malloc(n);
  if (copy) memcpy(copy, s, n);
  return copy;
}

static int collect_processes(const ext_ledger_context *ctx, int64_t limit, int64_t pid, int64_t resource_limit, lj_value **out) {
  char host[25];
  int rc = ll_process_host_id(host);
  if (rc) return rc;
  lj_buf at = {0};
  ll_time now = ll_time_now();
  ll_time_write(&at, &now);
  lj_value *processes = lj_new(LJ_ARRAY), *enumeration = NULL;
  if (!processes || at.oom) rc = EXT_LEDGER_IO;
  if (!rc && pid) {
    rc = ll_process_one(ctx, pid, processes);
    if (!rc && processes->u.array.len != 1) rc = EXT_LEDGER_NOT_FOUND;
    if (!rc) {
      lj_value *first = processes->u.array.items[0];
      char *start = copy_string(rec_field(first, "start_id")), *executable = copy_string(rec_field(first, "executable"));
      if (!start || !executable) rc = EXT_LEDGER_IO;
      else if (!*start) rc = EXT_LEDGER_UNAVAILABLE;
      if (!rc) rc = ll_process_resources(ctx, pid, resource_limit, first);
      if (!rc) {
        lj_value *recheck = lj_new(LJ_ARRAY);
        if (!recheck) rc = EXT_LEDGER_IO;
        else {
          rc = ll_process_one(ctx, pid, recheck);
          if (!rc && (recheck->u.array.len != 1 || strcmp(start, rec_field(recheck->u.array.items[0], "start_id")) ||
                      strcmp(executable, rec_field(recheck->u.array.items[0], "executable")))) rc = EXT_LEDGER_CHANGED;
          lj_free(recheck);
        }
      }
      free(start);
      free(executable);
    }
    if (!rc) enumeration = ll_status("complete", NULL, NULL);
    if (!rc && !enumeration) rc = EXT_LEDGER_IO;
  } else if (!rc) rc = ll_processes(ctx, limit, processes, &enumeration);
  if (!rc && processes->u.array.len > 1)
    qsort(processes->u.array.items, processes->u.array.len, sizeof *processes->u.array.items, cmp_pid);
  lj_value *snapshot = rc ? NULL : rec_obj();
  if (!rc) {
    char *stamp_at = at.ptr ? at.ptr + 1 : "";
    at.ptr[at.len - 1] = 0; /* unquote */
    rc = (stamp(enumeration, processes, stamp_at) | rec_set(snapshot, "host_id", rec_str(host)) | rec_set(snapshot, "scope", rec_str(pid ? "process" : "all")) |
          rec_set(snapshot, "observed_at", rec_str(stamp_at)) | rec_set(snapshot, "enumeration", enumeration) |
          rec_set(snapshot, "processes", processes))
             ? EXT_LEDGER_IO
             : 0;
    enumeration = processes = NULL;
  }
  lj_free(processes);
  lj_free(enumeration);
  lj_buf_free(&at);
  if (rc) {
    lj_free(snapshot);
    return rc;
  }
  *out = snapshot;
  return 0;
}

static int collect(const ext_ledger_context *ctx, const char *what, int64_t limit, int64_t pid, int64_t resource_limit, lj_value **out);

static int collect(const ext_ledger_context *ctx, const char *what, int64_t limit, int64_t pid, int64_t resource_limit, lj_value **out) {
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  if (strcmp(what, "processes") == 0) return collect_processes(ctx, limit, 0, 0, out);
  if (strcmp(what, "process") == 0) return collect_processes(ctx, limit, pid, resource_limit, out);
  if (strcmp(what, "shells") == 0 || strcmp(what, "filesystems") == 0) {
    lj_value *list;
    if (what[0] == 's') {
      char *path = ll_os_getenv("PATH");
      rc = ll_collect_shells(ctx, path, &list);
      free(path);
    } else {
      rc = ll_collect_filesystems(ctx, &list);
    }
    if (rc) return rc;
    *out = list;
    return 0;
  }
  if (strcmp(what, "resources") == 0) return ll_collect_resources(ctx, out);
  if (strcmp(what, "webviews") == 0) return ll_collect_webviews(ctx, out);
  if (strcmp(what, "surfaces") == 0) return ll_collect_surfaces(ctx, out);
  if (strcmp(what, "desktop") == 0) return ll_collect_desktop(ctx, out);
  if (strcmp(what, "accessibility") == 0) return ll_collect_accessibility(ctx, out);
  if (strcmp(what, "permissions") == 0) return ll_collect_permissions(ctx, out);
  if (strcmp(what, "host") == 0) { /* every host kind this build collects; one it cannot collect here is left out */
    lj_value *host = rec_obj();
    if (!host) return EXT_LEDGER_IO;
    static const char *const kinds[] = {"shells", "filesystems", "webviews", "resources", "desktop", "surfaces", "accessibility", "permissions"};
    for (size_t i = 0; i < sizeof kinds / sizeof *kinds; i++) {
      lj_value *v;
      rc = collect(ctx, kinds[i], limit, 0, 0, &v);
      if (rc == EXT_LEDGER_UNSUPPORTED) continue;
      if (rc || rec_set(host, kinds[i], v)) {
        lj_free(host);
        return rc ? rc : EXT_LEDGER_IO;
      }
    }
    *out = host;
    return 0;
  }
  return EXT_LEDGER_UNSUPPORTED;
}

int ll_collect_with_options(const ext_ledger_context *ctx, const char *what, int64_t limit, int64_t pid, int64_t resource_limit,
                            char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!what || !out || limit < 1 || limit > 65536 || (strcmp(what, "process") == 0 &&
      (pid < 1 || pid > 2147483647 || resource_limit < 1 || resource_limit > 65536))) return EXT_LEDGER_INVALID;
  lj_value *v;
  int rc = collect(ctx, what, limit, pid, resource_limit, &v);
  if (rc) return rc;
  lj_buf b = {0};
  lj_write(&b, v);
  lj_free(v);
  char *p = lj_buf_take(&b, out_len);
  if (!p) return EXT_LEDGER_IO;
  *out = p;
  return 0;
}

int ext_ledger_collect(const ext_ledger_context *ctx, const char *what, char **out, size_t *out_len) {
  return ll_collect_with_options(ctx, what, 4096, 0, 0, out, out_len);
}

int ext_ledger_collect_process(const ext_ledger_context *ctx, int64_t pid, int64_t resource_limit, char **out, size_t *out_len) {
  return ll_collect_with_options(ctx, "process", 4096, pid, resource_limit ? resource_limit : 1024, out, out_len);
}

int ext_ledger_recorder_scan(ext_ledger_recorder *r, const ext_ledger_context *ctx, const char *what) {
  if (!r || !what) return EXT_LEDGER_INVALID;
  lj_value *v;
  int rc = collect(ctx, what, 4096, 0, 0, &v);
  if (rc) return rc;
  if (strcmp(what, "processes") == 0) rc = rec_observe_processes(r, ctx, v);
  else if (strcmp(what, "host") == 0) rc = rec_observe_host(r, ctx, v);
  else rc = EXT_LEDGER_UNSUPPORTED; /* a single host kind is recorded through "host" */
  lj_free(v);
  return rc;
}

int ext_ledger_recorder_scan_process(ext_ledger_recorder *r, const ext_ledger_context *ctx, int64_t pid, int64_t resource_limit) {
  if (!r || pid < 1 || pid > 2147483647 || resource_limit < 1 || resource_limit > 65536) return EXT_LEDGER_INVALID;
  lj_value *v;
  int rc = collect(ctx, "process", 4096, pid, resource_limit, &v);
  if (rc) return rc;
  rc = rec_observe_processes(r, ctx, v);
  lj_free(v);
  return rc;
}

/* ---- the machine's environment ---- */

char *ll_machine_environment(void) {
  lj_value *env = rec_obj(), *vars = rec_obj();
  static const char *const names[] = {"SHELL", "ComSpec", "TERM_PROGRAM", "PATH", "LD_LIBRARY_PATH"};
  int bad = !env || !vars;
  for (size_t i = 0; i < sizeof names / sizeof *names; i++) {
    char *v = ll_os_getenv(names[i]);
    if (v) bad |= rec_set(vars, names[i], rec_str(v));
    free(v);
  }
  char *host = ll_os_hostname(), *cwd = ll_os_getcwd(), *hostenv = ll_host_environment();
  lj_buf now = {0};
  ll_time t = ll_time_now();
  ll_time_write(&now, &t);
  bad |= now.oom || !hostenv;
  if (!bad) {
    now.ptr[now.len - 1] = 0;
    bad |= rec_set(env, "now", rec_str(now.ptr + 1)) | rec_set(env, "hostname", rec_str(host ? host : "")) | rec_set(env, "os", rec_str(ll_goos())) |
           rec_set(env, "architecture", rec_str(ll_goarch())) | rec_set(env, "parent_pid", rec_num((double)ll_os_ppid())) |
           rec_set(env, "working_directory", rec_str(cwd ? cwd : "")) | rec_set(env, "variables", vars) |
           rec_set(env, "host_environment", rec_str(hostenv));
    vars = NULL;
  }
  free(host);
  free(cwd);
  free(hostenv);
  lj_buf_free(&now);
  lj_free(vars);
  char *out = NULL;
  if (!bad) {
    lj_buf b = {0};
    lj_write(&b, env);
    out = lj_buf_take(&b, NULL);
  }
  lj_free(env);
  return out;
}

int ext_ledger_collect_environment(char **out, size_t *out_len) {
  if (!out) return EXT_LEDGER_INVALID;
  *out = ll_machine_environment();
  if (!*out) return EXT_LEDGER_IO;
  if (out_len) *out_len = strlen(*out);
  return 0;
}
