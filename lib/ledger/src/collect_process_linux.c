/* Process identities on Linux and Android, from /proc: the name, parent and start time from stat,
 * the executable from exe, the effective UID from status, and stat read again
 * to catch a PID reused during the reads. */
#if defined(__linux__)
#define _DEFAULT_SOURCE
#include "collect.h"
#include "recorder.h"

#include <dirent.h>
#include <arpa/inet.h>
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
  if (invalid) { /* retain a record with unavailable identity when the native read is incomplete */
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

static const char *socket_state(const char *hex) {
  static const char *states[] = {"", "ESTABLISHED", "SYN_SENT", "SYN_RECV", "FIN_WAIT1", "FIN_WAIT2", "TIME_WAIT",
                                 "CLOSE", "CLOSE_WAIT", "LAST_ACK", "LISTEN", "CLOSING"};
  char *end;
  long state = strtol(hex, &end, 16);
  return *end || state < 1 || state >= (long)(sizeof states / sizeof *states) ? hex : states[state];
}

static int socket_address(const char *encoded, char out[96]) {
  const char *colon = strchr(encoded, ':');
  if (!colon || (colon - encoded != 8 && colon - encoded != 32) || strlen(colon + 1) != 4) return -1;
  unsigned char ip[16] = {0};
  for (size_t i = 0; i < (size_t)(colon - encoded); i += 8) {
    char chunk[9];
    memcpy(chunk, encoded + i, 8);
    chunk[8] = 0;
    char *end;
    unsigned long word = strtoul(chunk, &end, 16);
    if (*end) return -1;
    uint32_t native = (uint32_t)word;
    memcpy(ip + i / 2, &native, 4);
  }
  char *end;
  unsigned long port = strtoul(colon + 1, &end, 16);
  if (*end || port > 65535) return -1;
  char host[INET6_ADDRSTRLEN];
  int ipv6 = colon - encoded == 32;
  if (!inet_ntop(ipv6 ? AF_INET6 : AF_INET, ip, host, sizeof host)) return -1;
  if (ipv6) snprintf(out, 96, "[%s]:%lu", host, port);
  else snprintf(out, 96, "%s:%lu", host, port);
  return 0;
}

static int resolve_sockets(const ext_ledger_context *ctx, int64_t pid, lj_value *process) {
  lj_value *resources = lj_get(process, "resources");
  if (!resources || resources->type != LJ_ARRAY) return 0;
  int pending = 0, rc = 0;
  for (size_t i = 0; i < resources->u.array.len; i++)
    if (!strcmp(rec_field(resources->u.array.items[i], "kind"), "socket")) pending++;
  if (!pending) return 0;
  static const char *tables[] = {"tcp", "tcp6", "udp", "udp6", "unix"};
  for (size_t t = 0; t < sizeof tables / sizeof *tables && !rc; t++) {
    if ((rc = ll_ctx_check(ctx))) break;
    char path[96];
    snprintf(path, sizeof path, "/proc/%lld/net/%s", (long long)pid, tables[t]);
    size_t len;
    char *data = read_proc(path, 8 << 20, &len);
    if (!data) {
      rc = ll_process_coverage(process, "socket", "partial", NULL, "some socket tables are unavailable");
      continue;
    }
    char *line = strchr(data, '\n');
    if (line) line++;
    while (line && *line && !rc) {
      if ((rc = ll_ctx_check(ctx))) break;
      char *next = strchr(line, '\n');
      if (next) *next++ = 0;
      char *fields[16] = {0}, *save = NULL;
      int count = 0;
      for (char *part = strtok_r(line, " \t", &save); part && count < 16; part = strtok_r(NULL, " \t", &save)) fields[count++] = part;
      if (count >= (t == 4 ? 7 : 10)) {
        const char *inode = fields[t == 4 ? 6 : 9];
        char raw[80];
        snprintf(raw, sizeof raw, "socket:[%s]", inode);
        for (size_t i = 0; i < resources->u.array.len && !rc; i++) {
          lj_value *resource = resources->u.array.items[i];
          if (strcmp(rec_field(resource, "kind"), "socket") || strcmp(rec_field(resource, "endpoint"), raw)) continue;
          char local[96] = "", remote[96] = "";
          if (t != 4 && (socket_address(fields[1], local) || socket_address(fields[2], remote))) {
            rc = ll_process_coverage(process, "socket", "partial", NULL, "invalid socket address");
            continue;
          }
          int bad = rec_set(resource, "protocol", rec_str(tables[t]));
          if (t == 4) {
            bad |= rec_set(resource, "state", rec_str(fields[5]));
            bad |= rec_set(resource, "endpoint", rec_str(count > 7 ? fields[7] : ""));
          } else {
            bad |= rec_set(resource, "local_address", rec_str(local));
            bad |= rec_set(resource, "remote_address", rec_str(remote));
            if (t < 2) bad |= rec_set(resource, "state", rec_str(socket_state(fields[3])));
            bad |= rec_set(resource, "endpoint", rec_str(""));
          }
          if (bad) rc = EXT_LEDGER_IO;
          pending--;
        }
      }
      line = next;
    }
    free(data);
  }
  if (rc) return rc;
  if (pending) return ll_process_coverage(process, "socket", "partial", NULL, "some socket protocols or endpoints could not be resolved");
  return 0;
}

int ll_process_resources(const ext_ledger_context *ctx, int64_t pid, int64_t limit, lj_value *process) {
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  char path[96], fields[24][32];
  size_t n = 0;
  int count = 0;
  snprintf(path, sizeof path, "/proc/%lld/stat", (long long)pid);
  char *stat = read_proc(path, 65536, &n);
  if (!stat) rc = ll_process_coverage_errno(process, "usage", errno ? errno : ENOMEM, path);
  else if (stat_fields(stat, NULL, fields, &count) || count < 22)
    rc = ll_process_coverage(process, "usage", "unavailable", "no_device", "invalid process stat record");
  else {
    unsigned long long threads = strtoull(fields[17], NULL, 10), virtual_bytes = strtoull(fields[20], NULL, 10);
    unsigned long long resident_pages = strtoull(fields[21], NULL, 10);
    unsigned long long user_ticks = strtoull(fields[11], NULL, 10), system_ticks = strtoull(fields[12], NULL, 10);
    long page = sysconf(_SC_PAGESIZE), hz = sysconf(_SC_CLK_TCK);
    lj_value *usage = rec_obj();
    int bad = rec_set(usage, "threads", rec_num((double)threads)) |
              rec_set(usage, "virtual_bytes", rec_num((double)virtual_bytes));
    if (page > 0) bad |= rec_set(usage, "resident_bytes", rec_num((double)resident_pages * (double)page));
    if (hz > 0) bad |= rec_set(usage, "cpu_seconds", rec_num((double)(user_ticks + system_ticks) / (double)hz));
    if (bad || rec_set(process, "usage", usage)) rc = EXT_LEDGER_IO;
    else rc = ll_process_coverage(process, "usage", page > 0 && hz > 0 ? "complete" : "partial", NULL,
                                  page > 0 && hz > 0 ? NULL : "CPU clock frequency or page size unavailable");
  }
  free(stat);
  if (rc) return rc;

  for (size_t i = 0; i < 3; i++) {
    static const char *kinds[] = {"file", "socket", "ipc"};
    if ((rc = ll_process_coverage(process, kinds[i], "complete", NULL, NULL))) return rc;
  }
  snprintf(path, sizeof path, "/proc/%lld/fd", (long long)pid);
  DIR *fds = opendir(path);
  if (!fds) {
    int err = errno;
    for (size_t i = 0; i < 3; i++) {
      static const char *kinds[] = {"file", "socket", "ipc"};
      if ((rc = ll_process_coverage_errno(process, kinds[i], err, path))) return rc;
    }
  } else {
    int64_t seen = 0;
    struct dirent *entry;
    while ((entry = readdir(fds)) != NULL) {
      if ((rc = ll_ctx_check(ctx))) break;
      if (entry->d_name[0] < '0' || entry->d_name[0] > '9') continue;
      if (++seen > limit * 4) {
        for (size_t i = 0; i < 3; i++) {
          static const char *kinds[] = {"file", "socket", "ipc"};
          rc |= ll_process_coverage(process, kinds[i], "partial", NULL, "record limit reached");
        }
        break;
      }
      char fdpath[320], target[4098];
      snprintf(fdpath, sizeof fdpath, "/proc/%lld/fd/%s", (long long)pid, entry->d_name);
      ssize_t length = readlink(fdpath, target, sizeof target - 1);
      if (length < 0) {
        for (size_t i = 0; i < 3; i++) {
          static const char *kinds[] = {"file", "socket", "ipc"};
          rc |= ll_process_coverage(process, kinds[i], "partial", NULL, "some descriptors changed or were inaccessible");
        }
        continue;
      }
      target[length] = 0;
      if (length > 4096) {
        rc = ll_process_coverage(process, "file", "partial", NULL, "resource exceeds graph string limit");
        continue;
      }
      if (!strncmp(target, "socket:[", 8)) {
        rc = ll_native_resource(process, limit, "socket", NULL, entry->d_name, NULL, NULL, NULL, target, NULL);
      } else if (target[0] == '/') rc = ll_native_resource(process, limit, "file", target, entry->d_name, NULL, NULL, NULL, NULL, NULL);
      else rc = ll_native_resource(process, limit, "ipc", NULL, entry->d_name, NULL, NULL, NULL, target, NULL);
      if (rc) break;
    }
    closedir(fds);
  }
  if (rc) return rc;
  if ((rc = resolve_sockets(ctx, pid, process))) return rc;

  if ((rc = ll_process_coverage(process, "mapping", "complete", NULL, NULL))) return rc;
  snprintf(path, sizeof path, "/proc/%lld/maps", (long long)pid);
  char *maps = read_proc(path, 8 << 20, &n);
  if (!maps) return ll_process_coverage_errno(process, "mapping", errno ? errno : ENOMEM, path);
  char *line = maps;
  for (char *next; line && *line; line = next) {
    if ((rc = ll_ctx_check(ctx))) break;
    next = strchr(line, '\n');
    if (next) *next++ = 0;
    char *rest = line;
    for (int field = 0; field < 5; field++) {
      while (*rest == ' ' || *rest == '\t') rest++;
      while (*rest && *rest != ' ' && *rest != '\t') rest++;
    }
    while (*rest == ' ' || *rest == '\t') rest++;
    if (*rest == '/') rc = ll_native_resource(process, limit, "mapping", rest, NULL, NULL, NULL, NULL, NULL, NULL);
  }
  free(maps);
  return rc;
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
