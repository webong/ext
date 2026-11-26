/* Non-replacing publication of a caller-selected per-user service definition.
 * The platform manager is invoked by its adapter after this succeeds. */
#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#ifdef __APPLE__
#define _DARWIN_C_SOURCE
#endif
#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#else
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#endif

#include "collect.h"
#include "process.h"
#include "recorder.h"

#include <stdlib.h>
#include <string.h>

static const char *string(const lj_value *v, const char *key) {
  const lj_value *value = lj_get(v, key);
  return value && value->type == LJ_STRING && strlen(value->u.string.ptr) == value->u.string.len ? value->u.string.ptr : NULL;
}

static int absolute_directory(const char *path, const char *platform) {
  size_t n = strlen(path);
  if (n == 0 || n > 4096) return 0;
  if (strcmp(platform, "windows")) return path[0] == '/';
  if (n >= 2 && path[0] == '\\' && path[1] == '\\') return 1;
  return n >= 3 && ((path[0] | 32) >= 'a' && (path[0] | 32) <= 'z') &&
         path[1] == ':' && (path[2] == '\\' || path[2] == '/');
}

static int definition(const lj_value *in, const char **directory, const char **file, const char **content) {
  const lj_value *def = lj_get(in, "definition");
  *directory = string(in, "directory");
  *file = string(def, "file_name");
  *content = string(def, "content");
  const char *platform = string(def, "platform"), *digest = string(def, "digest");
  if (!*directory || !*file || !*content || !platform || !digest ||
      strcmp(platform, ll_goos()) || !absolute_directory(*directory, platform) ||
      !**file || strlen(*file) > 256 || !strcmp(*file, ".") || !strcmp(*file, "..") ||
      !**content || strlen(*content) > (1 << 20) || strlen(digest) != 64) return EXT_LEDGER_INVALID;
  for (const char *p = *file; *p; p++) if (*p == '/' || *p == '\\') return EXT_LEDGER_INVALID;
  char actual[65]; ll_sha256_hex(*content, strlen(*content), actual);
  return strcmp(actual, digest) ? EXT_LEDGER_INVALID : 0;
}

static int output(const lj_value *value, char **out, size_t *out_len) {
  lj_buf b = {0};
  lj_write(&b, value);
  *out = lj_buf_take(&b, out_len);
  return *out ? 0 : EXT_LEDGER_IO;
}

static int result(const char *dir, const char *file, int installed, int matches, int created, int existing,
                  const char *digest, char **out, size_t *out_len) {
  char *path = ll_path_join(dir, file);
  if (!path) return EXT_LEDGER_IO;
  lj_value *r = rec_obj();
  int bad = rec_set(r, "path", rec_str(path)) | rec_set(r, "installed", rec_bool(installed)) |
            rec_set(r, "matches", rec_bool(matches)) | rec_set(r, "created", rec_bool(created)) |
            rec_set(r, "existing", rec_bool(existing));
  if (digest && *digest) bad |= rec_set(r, "digest", rec_str(digest));
  free(path);
  int rc = bad ? EXT_LEDGER_IO : output(r, out, out_len);
  lj_free(r);
  return rc;
}

#ifndef _WIN32
static int open_directory(const char *path, int *out) {
  struct stat before, after;
  if (lstat(path, &before) || !S_ISDIR(before.st_mode) || (before.st_mode & 0022)) return EXT_LEDGER_DENIED;
  int fd = open(path, O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
  if (fd < 0) return EXT_LEDGER_DENIED;
  if (fstat(fd, &after) || before.st_dev != after.st_dev || before.st_ino != after.st_ino) {
    close(fd);
    return EXT_LEDGER_CHANGED;
  }
  *out = fd;
  return 0;
}

static int inspect_file(int root, const char *name, const char *expected, int *installed, int *matches, char digest[65]) {
  *installed = *matches = 0;
  digest[0] = 0;
  struct stat before, after;
  if (fstatat(root, name, &before, AT_SYMLINK_NOFOLLOW)) return errno == ENOENT ? 0 : EXT_LEDGER_IO;
  *installed = 1;
  if (!S_ISREG(before.st_mode) || (before.st_mode & 0022)) return EXT_LEDGER_DENIED;
  int fd = openat(root, name, O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
  if (fd < 0) return EXT_LEDGER_DENIED;
  int rc = 0;
  if (fstat(fd, &after) || before.st_dev != after.st_dev || before.st_ino != after.st_ino) rc = EXT_LEDGER_CHANGED;
  if (!rc && (after.st_size < 0 || after.st_size > (1 << 20))) rc = EXT_LEDGER_TOO_LARGE;
  size_t size = rc ? 0 : (size_t)after.st_size, used = 0;
  char *bytes = rc ? NULL : malloc(size + 1);
  if (!rc && !bytes) rc = EXT_LEDGER_IO;
  while (!rc && used < size) {
    ssize_t n = read(fd, bytes + used, size - used);
    if (n < 0 && errno == EINTR) continue;
    if (n <= 0) { rc = EXT_LEDGER_CHANGED; break; }
    used += (size_t)n;
  }
  if (!rc) {
    char extra;
    if (read(fd, &extra, 1) != 0) rc = EXT_LEDGER_CHANGED;
  }
  if (!rc) {
    bytes[size] = 0;
    ll_sha256_hex(bytes, size, digest);
    *matches = size == strlen(expected) && !memcmp(bytes, expected, size);
  }
  free(bytes);
  close(fd);
  return rc;
}

static int publish(const ext_ledger_context *ctx, int root, const char *dir, const char *file, const char *content,
                   char **out, size_t *out_len) {
  int installed, matches;
  char digest[65];
  int rc = inspect_file(root, file, content, &installed, &matches, digest);
  if (rc) return rc;
  if (installed) return matches ? result(dir, file, 1, 1, 0, 1, digest, out, out_len) : EXT_LEDGER_EXISTS;
  char nonce[33], temporary[48] = ".ext-service-";
  if (ll_random_hex(nonce, 16)) return EXT_LEDGER_IO;
  strcat(temporary, nonce);
  int fd = openat(root, temporary, O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC | O_NOFOLLOW, 0600);
  if (fd < 0) return EXT_LEDGER_IO;
  size_t size = strlen(content), used = 0;
  while (!rc && used < size) {
    if ((rc = ll_ctx_check(ctx))) break;
    ssize_t n = write(fd, content + used, size - used);
    if (n < 0 && errno == EINTR) continue;
    if (n <= 0) { rc = EXT_LEDGER_IO; break; }
    used += (size_t)n;
  }
  if (!rc && fsync(fd)) rc = EXT_LEDGER_IO;
  if (close(fd) && !rc) rc = EXT_LEDGER_IO;
  if (!rc && linkat(root, temporary, root, file, 0)) rc = errno == EEXIST ? EXT_LEDGER_EXISTS : EXT_LEDGER_IO;
  unlinkat(root, temporary, 0);
  if (rc == EXT_LEDGER_EXISTS) {
    int present, equal; char current[65];
    if (!inspect_file(root, file, content, &present, &equal, current) && present && equal)
      return result(dir, file, 1, 1, 0, 1, current, out, out_len);
  }
  if (rc) return rc;
  if (fsync(root)) return EXT_LEDGER_IO;
  ll_sha256_hex(content, size, digest);
  return result(dir, file, 1, 1, 1, 0, digest, out, out_len);
}
#else
static wchar_t *wide_path(const char *path) {
  int n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, NULL, 0);
  if (n <= 0) return NULL;
  wchar_t *wide = malloc((size_t)n * sizeof *wide);
  if (!wide || !MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wide, n)) {
    free(wide);
    return NULL;
  }
  return wide;
}

static wchar_t *child_path(const char *dir, const char *file) {
  char *path = ll_path_join(dir, file);
  if (!path) return NULL;
  wchar_t *wide = wide_path(path);
  free(path);
  return wide;
}

static int secure_directory(const char *dir) {
  wchar_t *wide = wide_path(dir);
  if (!wide) return EXT_LEDGER_INVALID;
  HANDLE handle = CreateFileW(wide, FILE_READ_ATTRIBUTES,
      FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING,
      FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT, NULL);
  free(wide);
  if (handle == INVALID_HANDLE_VALUE) return EXT_LEDGER_DENIED;
  BY_HANDLE_FILE_INFORMATION info;
  int okay = GetFileInformationByHandle(handle, &info) &&
      (info.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) &&
      !(info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT);
  CloseHandle(handle);
  return okay ? 0 : EXT_LEDGER_DENIED;
}

static int win_inspect(const wchar_t *path, const char *expected, int *installed, int *matches, char digest[65]) {
  *installed = *matches = 0;
  digest[0] = 0;
  HANDLE handle = CreateFileW(path, GENERIC_READ,
      FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING,
      FILE_FLAG_OPEN_REPARSE_POINT, NULL);
  if (handle == INVALID_HANDLE_VALUE) {
    DWORD error = GetLastError();
    return error == ERROR_FILE_NOT_FOUND || error == ERROR_PATH_NOT_FOUND ? 0 : EXT_LEDGER_DENIED;
  }
  *installed = 1;
  BY_HANDLE_FILE_INFORMATION info;
  LARGE_INTEGER size;
  int rc = 0;
  if (!GetFileInformationByHandle(handle, &info) || !GetFileSizeEx(handle, &size)) rc = EXT_LEDGER_IO;
  else if (info.dwFileAttributes & (FILE_ATTRIBUTE_DIRECTORY | FILE_ATTRIBUTE_REPARSE_POINT)) rc = EXT_LEDGER_DENIED;
  else if (size.QuadPart < 0 || size.QuadPart > (1 << 20)) rc = EXT_LEDGER_TOO_LARGE;
  size_t length = rc ? 0 : (size_t)size.QuadPart;
  char *bytes = rc ? NULL : malloc(length + 1);
  if (!rc && !bytes) rc = EXT_LEDGER_IO;
  if (!rc) {
    DWORD read = 0;
    if (!ReadFile(handle, bytes, (DWORD)length, &read, NULL) || read != length) rc = EXT_LEDGER_CHANGED;
    else {
      char extra;
      if (!ReadFile(handle, &extra, 1, &read, NULL) || read != 0) rc = EXT_LEDGER_CHANGED;
    }
    if (!rc) {
      bytes[length] = 0;
      ll_sha256_hex(bytes, length, digest);
      *matches = length == strlen(expected) && !memcmp(bytes, expected, length);
    }
  }
  free(bytes);
  CloseHandle(handle);
  return rc;
}

static int win_publish(const ext_ledger_context *ctx, const char *dir, const char *file,
                       const char *content, wchar_t *target, char **out, size_t *out_len) {
  int installed, matches;
  char digest[65];
  int rc = win_inspect(target, content, &installed, &matches, digest);
  if (rc) return rc;
  if (installed) return matches ? result(dir, file, 1, 1, 0, 1, digest, out, out_len) : EXT_LEDGER_EXISTS;
  char nonce[33], temporary[48] = ".ext-service-";
  if (ll_random_hex(nonce, 16)) return EXT_LEDGER_IO;
  strcat(temporary, nonce);
  wchar_t *stage = child_path(dir, temporary);
  if (!stage) return EXT_LEDGER_IO;
  HANDLE handle = CreateFileW(stage, GENERIC_WRITE, 0, NULL, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, NULL);
  if (handle == INVALID_HANDLE_VALUE) { free(stage); return EXT_LEDGER_IO; }
  size_t length = strlen(content);
  DWORD written = 0;
  if ((rc = ll_ctx_check(ctx)) == 0 &&
      (!WriteFile(handle, content, (DWORD)length, &written, NULL) || written != length || !FlushFileBuffers(handle)))
    rc = EXT_LEDGER_IO;
  CloseHandle(handle);
  if (!rc && !CreateHardLinkW(target, stage, NULL))
    rc = GetLastError() == ERROR_ALREADY_EXISTS || GetLastError() == ERROR_FILE_EXISTS ? EXT_LEDGER_EXISTS : EXT_LEDGER_IO;
  DeleteFileW(stage);
  free(stage);
  if (rc == EXT_LEDGER_EXISTS) {
    int present, equal; char current[65];
    if (!win_inspect(target, content, &present, &equal, current) && present && equal)
      return result(dir, file, 1, 1, 0, 1, current, out, out_len);
  }
  if (rc) return rc;
  ll_sha256_hex(content, length, digest);
  return result(dir, file, 1, 1, 1, 0, digest, out, out_len);
}
#endif

int ll_service_file(const ext_ledger_context *ctx, const char *operation, const lj_value *in, char **out, size_t *out_len) {
  const char *dir, *file, *content;
  int rc = definition(in, &dir, &file, &content);
  if (rc) return rc;
  if ((rc = ll_ctx_check(ctx))) return rc;
#ifdef _WIN32
  if ((rc = secure_directory(dir))) return rc;
  wchar_t *target = child_path(dir, file);
  if (!target) return EXT_LEDGER_IO;
  if (!strcmp(operation, "service_publish")) rc = win_publish(ctx, dir, file, content, target, out, out_len);
  else {
    int installed, matches; char digest[65];
    rc = win_inspect(target, content, &installed, &matches, digest);
    if (!rc && !strcmp(operation, "service_remove") && installed) {
      if (!matches) rc = EXT_LEDGER_CHANGED;
      else if (!DeleteFileW(target)) rc = EXT_LEDGER_IO;
      else installed = matches = 0;
    }
    if (!rc) rc = result(dir, file, installed, matches, 0, 0, digest, out, out_len);
  }
  free(target);
  return rc;
#else
  int root;
  if ((rc = open_directory(dir, &root))) return rc;
  if (!strcmp(operation, "service_publish")) {
    rc = publish(ctx, root, dir, file, content, out, out_len);
  } else {
    int installed, matches; char digest[65];
    rc = inspect_file(root, file, content, &installed, &matches, digest);
    if (!rc && !strcmp(operation, "service_remove") && installed) {
      if (!matches) rc = EXT_LEDGER_CHANGED;
      else if (unlinkat(root, file, 0)) rc = EXT_LEDGER_IO;
      else if (fsync(root)) rc = EXT_LEDGER_IO;
      else installed = matches = 0;
    }
    if (!rc) rc = result(dir, file, installed, matches, 0, 0, digest, out, out_len);
  }
  close(root);
  return rc;
#endif
}
