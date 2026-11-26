/* Host inventory on POSIX systems: the shells /etc/shells registers, the
 * executable check, and the mount table (Linux's mountinfo with statvfs, the
 * BSD getfsstat on macOS). */
#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#else
#define _DEFAULT_SOURCE
#endif

#include "collect.h"
#include "recorder.h"

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <sys/mount.h>
#include <sys/param.h>
#elif defined(__linux__)
#include <sys/statvfs.h>
#include <sys/vfs.h>
#endif

char ll_path_list_separator(void) { return ':'; }
char *ll_path_join(const char *dir, const char *name) { return rec_join(dir, name); }
char *ll_path_clean(const char *path) { return rec_clean(path); }
const char *ll_executable_suffix(void) { return ""; }

int ll_is_executable(const char *path) {
  struct stat st;
  return stat(path, &st) == 0 && S_ISREG(st.st_mode) && (st.st_mode & 0111) != 0 && access(path, X_OK) == 0;
}

int ll_host_access(const char *path, int directory_writable) {
  if (directory_writable) {
    struct stat st;
    if (stat(path, &st)) return errno == EACCES ? EXT_LEDGER_DENIED : errno == ENOENT ? EXT_LEDGER_NOT_FOUND : EXT_LEDGER_IO;
    if (!S_ISDIR(st.st_mode)) return EXT_LEDGER_INVALID;
  }
  return access(path, directory_writable ? W_OK | X_OK : X_OK) == 0 ? 0 :
         errno == EACCES ? EXT_LEDGER_DENIED : errno == ENOENT ? EXT_LEDGER_NOT_FOUND : EXT_LEDGER_IO;
}

char *ll_resolve_path(const char *path) {
  char buf[PATH_MAX];
  if (!realpath(path, buf)) return NULL;
  size_t n = strlen(buf);
  char *out = malloc(n + 1);
  if (out) memcpy(out, buf, n + 1);
  return out;
}

int ll_platform_shell_paths(const ext_ledger_context *ctx, ll_shell_path **out, size_t *n) {
  *out = NULL;
  *n = 0;
  FILE *f = fopen("/etc/shells", "r");
  if (!f) return errno == ENOENT ? 0 : EXT_LEDGER_IO;
  char line[4096];
  size_t cap = 0;
  int rc = 0;
  while (!rc && fgets(line, sizeof line, f)) {
    if ((rc = ll_ctx_check(ctx))) break;
    char *hash = strchr(line, '#');
    if (hash) *hash = 0;
    char *start = line, *end = line + strlen(line);
    while (*start == ' ' || *start == '\t' || *start == '\r' || *start == '\n') start++;
    while (end > start && (end[-1] == ' ' || end[-1] == '\t' || end[-1] == '\r' || end[-1] == '\n')) end--;
    if (end == start) continue;
    if (*n == cap) {
      cap = cap ? cap * 2 : 16;
      ll_shell_path *g = realloc(*out, cap * sizeof **out);
      if (!g) {
        rc = EXT_LEDGER_IO;
        break;
      }
      *out = g;
    }
    char *path = malloc((size_t)(end - start) + 1);
    if (!path) {
      rc = EXT_LEDGER_IO;
      break;
    }
    memcpy(path, start, (size_t)(end - start));
    path[end - start] = 0;
    (*out)[*n].path = path;
    (*out)[(*n)++].source = "registered";
  }
  fclose(f);
  return rc;
}

static lj_value *filesystem(const char *mount, const char *source, const char *type, int read_only, lj_value *space, const char *detail) {
  lj_value *fs = rec_obj();
  int bad = rec_set(fs, "mount_point", rec_str(mount)) | rec_set(fs, "source", rec_str(source)) | rec_set(fs, "type", rec_str(type)) |
            rec_set(fs, "read_only", rec_bool(read_only));
  if (space) bad |= rec_set(fs, "space", space);
  if (detail && *detail) bad |= rec_set(fs, "detail_error", rec_str(detail));
  if (bad) {
    lj_free(fs);
    return NULL;
  }
  return fs;
}

#if defined(__linux__)
/* A mountinfo field with its octal escapes (\040 for a space) decoded; NULL when an escape is invalid. */
static char *unescape(const char *v) {
  size_t n = strlen(v);
  char *out = malloc(n + 1), *w = out;
  if (!out) return NULL;
  for (size_t i = 0; i < n; i++) {
    if (v[i] != '\\') {
      *w++ = v[i];
      continue;
    }
    if (i + 3 >= n) { /* an escape needs three octal digits */
      free(out);
      return NULL;
    }
    int c = 0;
    for (int k = 1; k <= 3; k++) {
      if (v[i + k] < '0' || v[i + k] > '7') {
        free(out);
        return NULL;
      }
      c = c * 8 + (v[i + k] - '0');
    }
    if (c == 0 || c > 255) {
      free(out);
      return NULL;
    }
    *w++ = (char)c;
    i += 3;
  }
  *w = 0;
  return out;
}

static int split(char *s, char **fields, int max) {
  int n = 0;
  for (char *tok = strtok(s, " \t\n"); tok && n < max; tok = strtok(NULL, " \t\n")) fields[n++] = tok;
  return n;
}

int ll_platform_filesystems(const ext_ledger_context *ctx, lj_value *filesystems) {
  FILE *f = fopen("/proc/self/mountinfo", "r");
  if (!f) return EXT_LEDGER_UNAVAILABLE;
  static char line[1 << 20];
  int rc = 0;
  while (!rc && fgets(line, sizeof line, f)) {
    if ((rc = ll_ctx_check(ctx))) break;
    char *dash = strstr(line, " - ");
    if (!dash) {
      rc = EXT_LEDGER_IO; /* invalid Linux mountinfo record */
      break;
    }
    *dash = 0;
    char *before[64], *after[16];
    int nb = split(line, before, 64), na = split(dash + 3, after, 16);
    if (nb < 6 || na < 3) {
      rc = EXT_LEDGER_IO;
      break;
    }
    char *point = unescape(before[4]), *source = unescape(after[1]);
    if (!point || !source) {
      free(point);
      free(source);
      rc = EXT_LEDGER_IO;
      break;
    }
    int read_only = 0;
    char *opts[2] = {before[5], after[2]};
    for (int k = 0; k < 2; k++) {
      char copy[4096];
      snprintf(copy, sizeof copy, "%s", opts[k]);
      for (char *o = strtok(copy, ","); o; o = strtok(NULL, ","))
        if (strcmp(o, "ro") == 0) read_only = 1;
    }
    struct statvfs st;
    lj_value *space = NULL;
    char detail[256] = "";
    if (statvfs(point, &st) != 0) {
      const char *msg = strerror(errno);
      snprintf(detail, sizeof detail, "space unavailable: %s", msg);
      size_t at = strlen("space unavailable: ");
      if (detail[at] >= 'A' && detail[at] <= 'Z') detail[at] = (char)(detail[at] + 32);
    } else {
      uint64_t size = st.f_frsize ? st.f_frsize : st.f_bsize;
      if (size) space = ll_filesystem_space(st.f_blocks, st.f_bfree, st.f_bavail, size);
    }
    lj_value *fs = filesystem(point, source, after[0], read_only, space, detail);
    free(point);
    free(source);
    if (!fs || lj_array_push(filesystems, fs)) rc = EXT_LEDGER_IO;
  }
  fclose(f);
  return rc;
}

int ll_platform_filesystem_at(const ext_ledger_context *ctx, const char *path, lj_value **out) {
  *out = NULL;
  int fd = open(path, 0x200000 /* O_PATH */ | O_CLOEXEC);
  if (fd < 0) return errno == EACCES ? EXT_LEDGER_DENIED : errno == ENOENT ? EXT_LEDGER_NOT_FOUND : EXT_LEDGER_IO;
  char fdinfo[64];
  snprintf(fdinfo, sizeof fdinfo, "/proc/self/fdinfo/%d", fd);
  FILE *info = fopen(fdinfo, "r");
  if (!info) { close(fd); return EXT_LEDGER_IO; }
  char line[512], mount_id[32] = "";
  while (fgets(line, sizeof line, info))
    if (sscanf(line, "mnt_id: %31s", mount_id) == 1) break;
  fclose(info);
  if (!*mount_id) { close(fd); return EXT_LEDGER_UNAVAILABLE; }
  FILE *mounts = fopen("/proc/self/mountinfo", "r");
  if (!mounts) { close(fd); return EXT_LEDGER_UNAVAILABLE; }
  char *entry = malloc(1 << 20);
  if (!entry) { fclose(mounts); close(fd); return EXT_LEDGER_IO; }
  int rc = EXT_LEDGER_NOT_FOUND;
  while (fgets(entry, 1 << 20, mounts)) {
    int check = ll_ctx_check(ctx);
    if (check) { rc = check; break; }
    char *space = strchr(entry, ' ');
    if (!space || (size_t)(space - entry) != strlen(mount_id) || memcmp(entry, mount_id, strlen(mount_id))) continue;
    char *dash = strstr(entry, " - ");
    if (!dash) { rc = EXT_LEDGER_IO; break; }
    *dash = 0;
    char *before[64], *after[16];
    int nb = split(entry, before, 64), na = split(dash + 3, after, 16);
    if (nb < 6 || na < 3) { rc = EXT_LEDGER_IO; break; }
    char *point = unescape(before[4]), *source = unescape(after[1]);
    if (!point || !source) { free(point); free(source); rc = EXT_LEDGER_IO; break; }
    int read_only = 0;
    for (char *option = strtok(before[5], ","); option; option = strtok(NULL, ","))
      if (!strcmp(option, "ro")) read_only = 1;
    for (char *option = strtok(after[2], ","); option; option = strtok(NULL, ","))
      if (!strcmp(option, "ro")) read_only = 1;
    struct statfs st;
    lj_value *capacity = NULL;
    if (fstatfs(fd, &st)) { free(point); free(source); rc = EXT_LEDGER_IO; break; }
    uint64_t size = st.f_frsize > 0 ? (uint64_t)st.f_frsize : (uint64_t)st.f_bsize;
    if (size) capacity = ll_filesystem_space(st.f_blocks, st.f_bfree, st.f_bavail, size);
    *out = filesystem(point, source, after[0], read_only, capacity, NULL);
    free(point); free(source);
    rc = *out ? 0 : EXT_LEDGER_IO;
    break;
  }
  free(entry); fclose(mounts); close(fd);
  return rc;
}
#elif defined(__APPLE__)
int ll_platform_filesystems(const ext_ledger_context *ctx, lj_value *filesystems) {
  for (int attempt = 0; attempt < 3; attempt++) {
    int rc = ll_ctx_check(ctx);
    if (rc) return rc;
    int count = getfsstat(NULL, 0, MNT_NOWAIT);
    if (count < 0) return EXT_LEDGER_UNAVAILABLE;
    int room = count + 16; /* mounts may appear between the count and the read */
    struct statfs *buf = calloc((size_t)room, sizeof *buf);
    if (!buf) return EXT_LEDGER_IO;
    count = getfsstat(buf, (int)(room * (int)sizeof *buf), MNT_NOWAIT);
    if (count < 0) {
      free(buf);
      return EXT_LEDGER_UNAVAILABLE;
    }
    if (count >= room) {
      free(buf);
      continue;
    }
    for (int i = 0; !rc && i < count; i++) {
      lj_value *fs = filesystem(buf[i].f_mntonname, buf[i].f_mntfromname, buf[i].f_fstypename, (buf[i].f_flags & MNT_RDONLY) != 0,
                                ll_filesystem_space(buf[i].f_blocks, buf[i].f_bfree, buf[i].f_bavail, buf[i].f_bsize), NULL);
      if (!fs || lj_array_push(filesystems, fs)) rc = EXT_LEDGER_IO;
    }
    free(buf);
    return rc;
  }
  return EXT_LEDGER_BUSY; /* mounted filesystems changed during discovery */
}

int ll_platform_filesystem_at(const ext_ledger_context *ctx, const char *path, lj_value **out) {
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  struct statfs st;
  if (statfs(path, &st)) return errno == EACCES ? EXT_LEDGER_DENIED : errno == ENOENT ? EXT_LEDGER_NOT_FOUND : EXT_LEDGER_IO;
  *out = filesystem(st.f_mntonname, st.f_mntfromname, st.f_fstypename, (st.f_flags & MNT_RDONLY) != 0,
                    ll_filesystem_space(st.f_blocks, st.f_bfree, st.f_bavail, st.f_bsize), NULL);
  return *out ? 0 : EXT_LEDGER_IO;
}
#else
int ll_platform_filesystems(const ext_ledger_context *ctx, lj_value *filesystems) {
  (void)ctx;
  (void)filesystems;
  return EXT_LEDGER_UNSUPPORTED;
}
int ll_platform_filesystem_at(const ext_ledger_context *ctx, const char *path, lj_value **out) {
  (void)ctx; (void)path; *out = NULL; return EXT_LEDGER_UNSUPPORTED;
}
#endif
#endif
