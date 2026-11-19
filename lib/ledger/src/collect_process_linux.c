/* Process identities on Linux and Android, from /proc, as res/system's
 * DiscoverProcesses reads them: the name, parent and start time from stat,
 * the executable from exe, the effective UID from status, and stat read again
 * to catch a PID reused during the reads. */
#if defined(__linux__)
#define _DEFAULT_SOURCE
#include "collect.h"
#include "recorder.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

/* Reads a whole /proc file of at most limit bytes; NULL with errno set otherwise. */
static char *read_proc(const char *path, size_t limit, size_t *len) {
  int fd = open(path, O_RDONLY | O_CLOEXEC);
  if (fd < 0) return NULL;
  size_t cap = 4096, n = 0;
  char *buf = malloc(cap + 1);
  while (buf) {
    ssize_t got = read(fd, buf + n, cap - n);
    if (got < 0) {
      if (errno == EINTR) continue;
      free(buf);
      buf = NULL;
      break;
    }
    if (got == 0) break;
    n += (size_t)got;
    if (n > limit) {
      free(buf);
      buf = NULL;
      errno = EFBIG;
      break;
    }
    if (n == cap) {
      char *grown = realloc(buf, cap * 2 + 1);
      if (!grown) {
        free(buf);
        buf = NULL;
        break;
      }
      buf = grown;
      cap *= 2;
    }
  }
  int saved = errno;
  close(fd);
  errno = saved;
  if (buf) {
    buf[n] = 0;
    *len = n;
  }
  return buf;
}

/* The fields after the name's final ')' in a stat record; *name gets the name. */
static int stat_fields(const char *text, char **name, char fields[24][32], int *count) {
  const char *open = strchr(text, '('), *close = strrchr(text, ')');
  if (!open || !close || close <= open) return -1;
  if (name) {
    size_t n = (size_t)(close - open - 1);
    if (!(*name = malloc(n + 1))) return -1;
    memcpy(*name, open + 1, n);
    (*name)[n] = 0;
  }
  *count = 0;
  for (const char *p = close + 1; *p && *count < 24;) {
    while (*p == ' ' || *p == '\n' || *p == '\t') p++;
    if (!*p) break;
    size_t k = 0;
    while (*p && *p != ' ' && *p != '\n' && *p != '\t') {
      if (k < 31) fields[*count][k++] = *p;
      p++;
    }
    fields[*count][k] = 0;
    (*count)++;
  }
  return 0;
}

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

/* One process; 1 when it went away before it could be read (it is then left out). */
static int read_process(int pid, lj_value *processes) {
  char path[64], fields[24][32];
  int count;
  size_t len;
  snprintf(path, sizeof path, "/proc/%d/stat", pid);
  lj_value *p = rec_obj(), *coverage = rec_obj();
  int bad = set(p, "pid", rec_num(pid)) | set(p, "parent_pid", rec_num(0)) | set(p, "name", rec_str(""));
  char *text = read_proc(path, 65536, &len), *name = NULL;
  if (!text) {
    int err = errno;
    lj_free(p);
    lj_free(coverage);
    if (err == ENOENT || err == ESRCH) return 1;
    lj_value *q = rec_obj(), *c = rec_obj();
    char what[96];
    snprintf(what, sizeof what, "open %s", path);
    bad = set(q, "pid", rec_num(pid)) | set(q, "parent_pid", rec_num(0)) | set(q, "name", rec_str("")) | set(c, "identity", ll_status_errno(err, what)) |
          set(q, "coverage", c) | lj_array_push(processes, q);
    return bad ? -1 : 0;
  }
  const char *invalid = NULL;
  if (stat_fields(text, &name, fields, &count)) invalid = "invalid process stat record";
  else if (count < 22) invalid = "short process stat record";
  free(text);
  char start[32] = "";
  if (!invalid) {
    snprintf(start, sizeof start, "%s", fields[19]);
    bad |= set(p, "name", rec_str(name)) | set(p, "parent_pid", rec_num(atof(fields[1]))) | set(p, "start_id", rec_str(start)) |
           set(coverage, "identity", ll_status("complete", NULL, NULL));
    char exe[4096];
    snprintf(path, sizeof path, "/proc/%d/exe", pid);
    ssize_t n = readlink(path, exe, sizeof exe - 1);
    if (n >= 0) {
      exe[n] = 0;
      bad |= set(p, "executable", rec_str(exe)) | set(coverage, "executable", ll_status("complete", NULL, NULL));
    } else {
      char what[96];
      snprintf(what, sizeof what, "readlink %s", path);
      bad |= set(coverage, "executable", ll_status_errno(errno, what));
    }
    snprintf(path, sizeof path, "/proc/%d/status", pid);
    char *status = read_proc(path, 1 << 20, &len), owner[32] = "";
    if (status) {
      for (char *line = status; line && *line;) {
        char *next = strchr(line, '\n');
        if (next) *next++ = 0;
        unsigned real, effective;
        if (strncmp(line, "Uid:", 4) == 0 && sscanf(line + 4, "%u %u", &real, &effective) == 2) snprintf(owner, sizeof owner, "%u", effective);
        line = next;
      }
      free(status);
      if (*owner) bad |= set(p, "owner", rec_str(owner)) | set(coverage, "owner", ll_status("complete", NULL, NULL));
      else bad |= set(coverage, "owner", ll_status("unavailable", "no_device", "effective UID missing from process status"));
    } else {
      char what[96];
      snprintf(what, sizeof what, "open %s", path);
      bad |= set(coverage, "owner", ll_status_errno(errno, what));
    }
    /* The fields must not belong to a PID reused during the reads. */
    snprintf(path, sizeof path, "/proc/%d/stat", pid);
    char *again = read_proc(path, 65536, &len), recheck[24][32];
    int c2;
    if (!again) {
      int err = errno;
      free(name);
      lj_free(p);
      lj_free(coverage);
      return err == ENOENT || err == ESRCH ? 1 : 0;
    }
    if (stat_fields(again, NULL, recheck, &c2) || c2 < 20 || strcmp(recheck[19], start) != 0) invalid = "process identity changed during discovery";
    free(again);
  }
  free(name);
  if (invalid) { /* a record that could not be read: a fresh one says so, as res/system reports it */
    lj_free(p);
    p = rec_obj();
    bad = set(p, "pid", rec_num(pid)) | set(p, "parent_pid", rec_num(0)) | set(p, "name", rec_str(""));
    lj_free(coverage);
    coverage = rec_obj();
    bad |= set(coverage, "identity", ll_status("unavailable", "no_device", invalid));
  }
  bad |= set(p, "coverage", coverage) | lj_array_push(processes, p);
  return bad ? -1 : 0;
}

int ll_processes(const ext_ledger_context *ctx, int64_t limit, lj_value *processes, lj_value **enumeration) {
  *enumeration = NULL;
  DIR *dir = opendir("/proc");
  if (!dir) return EXT_LEDGER_IO;
  int rc = 0, limited = 0;
  struct dirent *e;
  while (!rc && (e = readdir(dir)) != NULL) {
    if ((rc = ll_ctx_check(ctx))) break;
    char *end;
    long pid = strtol(e->d_name, &end, 10);
    if (*end || pid <= 0) continue;
    if ((int64_t)processes->u.array.len >= limit) {
      limited = 1;
      break;
    }
    if (read_process((int)pid, processes) < 0) rc = EXT_LEDGER_IO;
  }
  closedir(dir);
  if (rc) return rc;
  *enumeration = limited ? ll_status("partial", NULL, "record limit reached") : ll_status("complete", NULL, NULL);
  return *enumeration ? 0 : EXT_LEDGER_IO;
}

int ll_process_host_id(char out[25]) {
  size_t len;
  char *boot = read_proc("/proc/sys/kernel/random/boot_id", 4096, &len);
  if (!boot) return EXT_LEDGER_UNAVAILABLE;
  char pidns[256], netns[256];
  ssize_t a = readlink("/proc/self/ns/pid", pidns, sizeof pidns - 1), b = readlink("/proc/self/ns/net", netns, sizeof netns - 1);
  char *env = ll_host_environment();
  if (a < 0 || b < 0 || !env) {
    free(boot);
    free(env);
    return EXT_LEDGER_UNAVAILABLE;
  }
  pidns[a] = 0;
  netns[b] = 0;
  lj_buf s = {0};
  lj_buf_puts(&s, env);
  lj_buf_put(&s, boot, len);
  lj_buf_puts(&s, pidns);
  lj_buf_puts(&s, netns);
  free(boot);
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
