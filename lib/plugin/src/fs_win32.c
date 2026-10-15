/* Windows filesystem access for package integrity. Paths are UTF-16 with the
 * \\?\ prefix so long paths work. A symbolic link or junction is a reparse
 * point; every component of every path is checked and refused. Unlike the
 * POSIX implementation this is path-based rather than handle-relative, so the
 * application must keep the root and its files from being modified while they
 * are read, which is the documented contract. */
#define WIN32_LEAN_AND_MEAN
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#endif
#include "fs.h"
#include "sha256.h"
#include <stdlib.h>
#include <string.h>
#include <windows.h>

struct ext_fs {
  wchar_t *root; /* absolute, \\?\ prefixed, no trailing separator */
};

static wchar_t *widen(const char *s) {
  int n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, s, -1, NULL, 0);
  if (n <= 0)
    return NULL;
  wchar_t *w = malloc((size_t)n * sizeof(*w));
  if (w && MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, s, -1, w, n) <= 0) {
    free(w);
    return NULL;
  }
  return w;
}
static char *narrow(const wchar_t *w) {
  int n = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, w, -1, NULL, 0,
                              NULL, NULL);
  if (n <= 0)
    return NULL;
  char *s = malloc((size_t)n);
  if (s && WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, w, -1, s, n, NULL,
                               NULL) <= 0) {
    free(s);
    return NULL;
  }
  return s;
}
/* A missing path and any other failure are both EXT_IO, as on POSIX. */
static ext_status win_error(DWORD error) {
  (void)error;
  return EXT_IO;
}
/* Attributes of path without following a link. */
static ext_status attributes(const wchar_t *path, DWORD *out) {
  DWORD a = GetFileAttributesW(path);
  if (a == INVALID_FILE_ATTRIBUTES)
    return win_error(GetLastError());
  *out = a;
  return EXT_OK;
}
/* Joins root and a slash-separated relative path, with backslashes. */
static wchar_t *join(const wchar_t *root, const char *relative) {
  wchar_t *rel = relative && *relative ? widen(relative) : wcsdup(L"");
  if (!rel)
    return NULL;
  size_t r = wcslen(root), n = wcslen(rel);
  wchar_t *path = malloc((r + n + 2) * sizeof(*path));
  if (!path) {
    free(rel);
    return NULL;
  }
  memcpy(path, root, r * sizeof(*path));
  size_t at = r;
  if (n)
    path[at++] = L'\\';
  for (size_t i = 0; i < n; i++)
    path[at++] = rel[i] == L'/' ? L'\\' : rel[i];
  path[at] = 0;
  free(rel);
  return path;
}
/* Checks that every component of relative, below root, is a plain directory
 * (all of them, or all but the last when leaf_is_file). */
static ext_status check_components(const wchar_t *root, const char *relative,
                                   int leaf_is_file) {
  char *copy = strdup(relative);
  if (!copy)
    return EXT_NOMEM;
  ext_status s = EXT_OK;
  char *end = copy;
  for (;;) {
    char *slash = strchr(end, '/');
    int last = !slash;
    if (slash)
      *slash = 0;
    wchar_t *path = join(root, copy);
    if (slash)
      *slash = '/';
    if (!path) {
      s = EXT_NOMEM;
      break;
    }
    DWORD a = 0;
    s = attributes(path, &a);
    free(path);
    if (s)
      break;
    if (a & FILE_ATTRIBUTE_REPARSE_POINT) {
      s = EXT_INVALID;
      break;
    }
    int directory = (a & FILE_ATTRIBUTE_DIRECTORY) != 0;
    if (last && leaf_is_file ? directory : !directory) {
      s = EXT_INVALID;
      break;
    }
    if (last)
      break;
    end = slash + 1;
  }
  free(copy);
  return s;
}
static ext_status hash_path(const wchar_t *path, uint8_t out[32]) {
  HANDLE h = CreateFileW(path, GENERIC_READ,
                         FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                         NULL, OPEN_EXISTING,
                         FILE_FLAG_OPEN_REPARSE_POINT | FILE_FLAG_SEQUENTIAL_SCAN,
                         NULL);
  if (h == INVALID_HANDLE_VALUE)
    return win_error(GetLastError());
  BY_HANDLE_FILE_INFORMATION info;
  ext_status s = EXT_OK;
  if (!GetFileInformationByHandle(h, &info))
    s = EXT_IO;
  else if (info.dwFileAttributes &
           (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY))
    s = EXT_INVALID;
  ext_hash hash;
  ext_hash_init(&hash);
  uint8_t buffer[65536];
  while (!s) {
    DWORD n = 0;
    if (!ReadFile(h, buffer, sizeof(buffer), &n, NULL))
      s = EXT_IO;
    else if (!n)
      break;
    else
      ext_hash_update(&hash, buffer, n);
  }
  CloseHandle(h);
  ext_hash_finish(&hash, out);
  return s;
}
static ext_status walk(const wchar_t *directory, const char *prefix,
                       ext_fs_visit visit, void *user, unsigned depth) {
  if (depth > 1024)
    return EXT_INVALID;
  size_t length = wcslen(directory);
  wchar_t *pattern = malloc((length + 3) * sizeof(*pattern));
  if (!pattern)
    return EXT_NOMEM;
  memcpy(pattern, directory, length * sizeof(*pattern));
  memcpy(pattern + length, L"\\*", 3 * sizeof(*pattern));
  WIN32_FIND_DATAW data;
  HANDLE find = FindFirstFileExW(pattern, FindExInfoBasic, &data,
                                 FindExSearchNameMatch, NULL,
                                 FIND_FIRST_EX_LARGE_FETCH);
  free(pattern);
  if (find == INVALID_HANDLE_VALUE)
    return win_error(GetLastError());
  ext_status s = EXT_OK;
  do {
    if (!wcscmp(data.cFileName, L".") || !wcscmp(data.cFileName, L".."))
      continue;
    char *name = narrow(data.cFileName);
    if (!name) {
      s = EXT_INVALID;
      break;
    }
    size_t base = strlen(prefix), n = strlen(name);
    char *path = malloc(base + n + 2);
    if (!path) {
      free(name);
      s = EXT_NOMEM;
      break;
    }
    memcpy(path, prefix, base);
    if (base)
      path[base++] = '/';
    memcpy(path + base, name, n + 1);
    free(name);
    wchar_t *child = malloc((length + wcslen(data.cFileName) + 2) * sizeof(*child));
    if (!child) {
      free(path);
      s = EXT_NOMEM;
      break;
    }
    memcpy(child, directory, length * sizeof(*child));
    child[length] = L'\\';
    wcscpy(child + length + 1, data.cFileName);
    if (data.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)
      s = EXT_INVALID;
    else if (data.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY)
      s = walk(child, path, visit, user, depth + 1);
    else if (strchr(path, '\r') || strchr(path, '\n'))
      s = EXT_INVALID;
    else {
      uint8_t sum[32];
      s = hash_path(child, sum);
      if (!s)
        s = visit(user, path, sum);
    }
    free(child);
    free(path);
  } while (!s && FindNextFileW(find, &data));
  if (!s && GetLastError() != ERROR_NO_MORE_FILES && GetLastError() != ERROR_SUCCESS)
    s = EXT_IO;
  FindClose(find);
  return s;
}

ext_status ext_fs_open(const char *root, ext_fs **out) {
  if (!root || !*root)
    return EXT_INVALID;
  wchar_t *wide = widen(root);
  if (!wide)
    return EXT_INVALID;
  DWORD n = GetFullPathNameW(wide, 0, NULL, NULL);
  wchar_t *full = n ? malloc((n + 8) * sizeof(*full)) : NULL;
  if (!full || !GetFullPathNameW(wide, n, full, NULL)) {
    free(wide);
    free(full);
    return EXT_IO;
  }
  free(wide);
  size_t length = wcslen(full);
  while (length > 3 && (full[length - 1] == L'\\' || full[length - 1] == L'/'))
    full[--length] = 0;
  wchar_t *prefixed;
  if (!wcsncmp(full, L"\\\\?\\", 4)) {
    prefixed = full;
  } else if (!wcsncmp(full, L"\\\\", 2)) {
    prefixed = malloc((length + 8) * sizeof(*prefixed));
    if (prefixed) {
      wcscpy(prefixed, L"\\\\?\\UNC\\");
      wcscpy(prefixed + 8, full + 2);
    }
    free(full);
  } else {
    prefixed = malloc((length + 5) * sizeof(*prefixed));
    if (prefixed) {
      wcscpy(prefixed, L"\\\\?\\");
      wcscpy(prefixed + 4, full);
    }
    free(full);
  }
  if (!prefixed)
    return EXT_NOMEM;
  DWORD a = 0;
  ext_status s = attributes(prefixed, &a);
  if (!s && ((a & FILE_ATTRIBUTE_REPARSE_POINT) || !(a & FILE_ATTRIBUTE_DIRECTORY)))
    s = EXT_INVALID;
  if (s) {
    free(prefixed);
    return s;
  }
  ext_fs *fs = malloc(sizeof(*fs));
  if (!fs) {
    free(prefixed);
    return EXT_NOMEM;
  }
  fs->root = prefixed;
  *out = fs;
  return EXT_OK;
}
void ext_fs_close(ext_fs *fs) {
  if (!fs)
    return;
  free(fs->root);
  free(fs);
}
ext_status ext_fs_hash_file(ext_fs *fs, const char *path, uint8_t out[32]) {
  ext_status s = check_components(fs->root, path, 1);
  if (s)
    return s;
  wchar_t *full = join(fs->root, path);
  if (!full)
    return EXT_NOMEM;
  s = hash_path(full, out);
  free(full);
  return s;
}
ext_status ext_fs_walk(ext_fs *fs, const char *path, ext_fs_visit visit,
                       void *user) {
  ext_status s = *path ? check_components(fs->root, path, 0) : EXT_OK;
  if (s)
    return s;
  wchar_t *full = join(fs->root, path);
  if (!full)
    return EXT_NOMEM;
  s = walk(full, "", visit, user, 0);
  free(full);
  return s;
}
