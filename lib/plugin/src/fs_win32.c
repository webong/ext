/* Windows filesystem access for package integrity. Paths are UTF-16 with the
 * \\?\ prefix so long paths work. A symbolic link or junction is a reparse
 * point and is refused wherever it is met.
 *
 * The read side (ext_fs_hash_file, ext_fs_read_file and ext_fs_walk) is
 * handle-relative, as the POSIX one is: each component is opened relative to
 * the handle of the directory above it, without following a link, and a
 * directory is listed through its own handle. A path is never resolved a second
 * time, so a directory swapped for a junction or symbolic link while a walk is
 * under way cannot redirect the steps that follow.
 *
 * The write side, and the source of ext_fs_copy_file, are still path-based: the
 * application must keep the root and its files from being modified while they
 * are used, which is the documented contract. As a second line of defense, a
 * file that copy opens is checked by its final path, as the system resolved it,
 * which must be the root's canonical path plus the lexical relative path. */
#define WIN32_LEAN_AND_MEAN
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#endif
#include "fs.h"
#include "platform.h"
#include "sha256.h"
#include <stdlib.h>
#include <string.h>
#include <windows.h>

struct ext_fs {
  wchar_t *root;      /* absolute, \\?\ prefixed, no trailing separator */
  wchar_t *canonical; /* the root's final path as the system resolves it */
};

/* A hashing context: where files are expected to resolve. */
typedef struct {
  const wchar_t *root;
  const wchar_t *canonical;
} where;

/* The final path of an open handle, or NULL. */
static wchar_t *final_path(HANDLE h) {
  DWORD n = GetFinalPathNameByHandleW(h, NULL, 0, FILE_NAME_NORMALIZED | VOLUME_NAME_DOS);
  if (!n)
    return NULL;
  wchar_t *path = malloc((n + 1) * sizeof(*path));
  if (!path)
    return NULL;
  DWORD m = GetFinalPathNameByHandleW(h, path, n + 1, FILE_NAME_NORMALIZED | VOLUME_NAME_DOS);
  if (!m || m > n) {
    free(path);
    return NULL;
  }
  return path;
}

/* Whether handle h is where the lexical path says: canonical root plus the part
 * of path below the lexical root, compared without regard to case. */
static int resolves_to(HANDLE h, const wchar_t *path, const where *w) {
  size_t root = wcslen(w->root);
  if (!w->canonical || wcsncmp(path, w->root, root))
    return 0;
  size_t base = wcslen(w->canonical), rest = wcslen(path + root);
  wchar_t *expected = malloc((base + rest + 1) * sizeof(*expected));
  wchar_t *actual = final_path(h);
  int same = 0;
  if (expected && actual) {
    memcpy(expected, w->canonical, base * sizeof(*expected));
    memcpy(expected + base, path + root, (rest + 1) * sizeof(*expected));
    same = CompareStringOrdinal(expected, -1, actual, -1, TRUE) == CSTR_EQUAL;
  }
  free(expected);
  free(actual);
  return same;
}

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
/* A name that is not there is EXT_MISSING, any other failure EXT_IO, as on POSIX. */
static ext_status win_error(DWORD error) {
  return error == ERROR_FILE_NOT_FOUND || error == ERROR_PATH_NOT_FOUND
             ? EXT_MISSING
             : EXT_IO;
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
/* ---- Read side: relative to open handles ----
 * NtCreateFile with a RootDirectory is what openat is on POSIX. The name given
 * is a single component, FILE_OPEN_REPARSE_POINT makes a link or junction open
 * as itself instead of being followed, and the attributes of the handle that
 * comes back then refuse it. The declarations are ntdll's, resolved at run time
 * so that nothing links against it. */
typedef struct {
  USHORT length, maximum;
  PWSTR buffer;
} nt_name;
typedef struct {
  ULONG length;
  HANDLE root;
  nt_name *name;
  ULONG attributes;
  PVOID descriptor, quality;
} nt_object;
typedef struct {
  union {
    LONG status;
    PVOID pointer;
  } u;
  ULONG_PTR information;
} nt_io;
typedef LONG(NTAPI *nt_create_file)(PHANDLE, ACCESS_MASK, nt_object *, nt_io *,
                                    PLARGE_INTEGER, ULONG, ULONG, ULONG, ULONG,
                                    PVOID, ULONG);
#define NT_FILE_OPEN 1u             /* FILE_OPEN: it must exist */
#define NT_SYNCHRONOUS 0x20u        /* FILE_SYNCHRONOUS_IO_NONALERT */
#define NT_OPEN_REPARSE_POINT 0x200000u
#define NT_CASE_INSENSITIVE 0x40u   /* OBJ_CASE_INSENSITIVE, as Win32 names are */
#define NT_MAX_COMPONENT 32000u     /* UTF-16 units; the limit of a name is 255 */

static nt_create_file nt_create(void) {
  static nt_create_file volatile resolved;
  nt_create_file fn = resolved;
  if (!fn) {
    HMODULE ntdll = GetModuleHandleW(L"ntdll.dll");
    fn = ntdll ? (nt_create_file)(void (*)(void))GetProcAddress(ntdll, "NtCreateFile")
               : NULL;
    resolved = fn;
  }
  return fn;
}
/* The status of a failed NtCreateFile as ours: a name that is not there is
 * EXT_MISSING, as on POSIX, and a name Windows will not take is EXT_INVALID. */
static ext_status nt_error(LONG status) {
  switch ((ULONG)status) {
  case 0xC000000Fu: /* STATUS_NO_SUCH_FILE */
  case 0xC0000034u: /* STATUS_OBJECT_NAME_NOT_FOUND */
  case 0xC000003Au: /* STATUS_OBJECT_PATH_NOT_FOUND */
    return EXT_MISSING;
  case 0xC0000033u: /* STATUS_OBJECT_NAME_INVALID */
  case 0xC0000039u: /* STATUS_OBJECT_PATH_INVALID */
  case 0xC000003Bu: /* STATUS_OBJECT_PATH_SYNTAX_BAD */
    return EXT_INVALID;
  default:
    return EXT_IO;
  }
}
/* Opens the one component name below dir, without following a link. The handle
 * can read the entry or, for a directory, list it. */
static ext_status open_child(HANDLE dir, const wchar_t *name, HANDLE *out) {
  nt_create_file create = nt_create();
  size_t n = wcslen(name);
  if (!create)
    return EXT_IO;
  if (!n || n > NT_MAX_COMPONENT)
    return EXT_INVALID;
  nt_name text = {(USHORT)(n * sizeof(wchar_t)), (USHORT)(n * sizeof(wchar_t)),
                  (PWSTR)name};
  nt_object object = {sizeof(object), dir, &text, NT_CASE_INSENSITIVE, NULL, NULL};
  nt_io io;
  memset(&io, 0, sizeof(io));
  HANDLE h = NULL;
  LONG status = create(&h, FILE_LIST_DIRECTORY | FILE_READ_ATTRIBUTES | SYNCHRONIZE,
                       &object, &io, NULL, FILE_ATTRIBUTE_NORMAL,
                       FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                       NT_FILE_OPEN, NT_OPEN_REPARSE_POINT | NT_SYNCHRONOUS, NULL, 0);
  if (status < 0)
    return nt_error(status);
  *out = h;
  return EXT_OK;
}
/* Whether the open handle is a directory. A reparse point (a symbolic link, a
 * junction, a mount point) is EXT_INVALID. */
static ext_status classify(HANDLE h, int *directory) {
  BY_HANDLE_FILE_INFORMATION info;
  if (!GetFileInformationByHandle(h, &info))
    return EXT_IO;
  if (info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)
    return EXT_INVALID;
  *directory = (info.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0;
  return EXT_OK;
}
/* Opens the root, which must be a plain directory. */
static ext_status open_root(const ext_fs *fs, HANDLE *out) {
  HANDLE h = CreateFileW(fs->root, FILE_LIST_DIRECTORY | FILE_READ_ATTRIBUTES,
                         FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                         NULL, OPEN_EXISTING,
                         FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT,
                         NULL);
  if (h == INVALID_HANDLE_VALUE)
    return win_error(GetLastError());
  int directory = 0;
  ext_status s = classify(h, &directory);
  if (!s && !directory)
    s = EXT_INVALID;
  if (s) {
    CloseHandle(h);
    return s;
  }
  *out = h;
  return EXT_OK;
}
/* One component of a portable relative path, as UTF-16. A backslash or a colon
 * would name another place on Windows (a parent, a stream), and a control
 * character is not part of a name. */
static ext_status component(const char *part, size_t n, wchar_t **out) {
  if (!n || (n == 1 && part[0] == '.') || (n == 2 && part[0] == '.' && part[1] == '.'))
    return EXT_INVALID;
  for (size_t i = 0; i < n; i++) {
    unsigned char c = (unsigned char)part[i];
    if (c == '\\' || c == ':' || c < 0x20)
      return EXT_INVALID;
  }
  char *copy = malloc(n + 1);
  if (!copy)
    return EXT_NOMEM;
  memcpy(copy, part, n);
  copy[n] = 0;
  wchar_t *wide = widen(copy);
  free(copy);
  if (!wide)
    return EXT_INVALID; /* not UTF-8 */
  *out = wide;
  return EXT_OK;
}
/* Opens the entry at the portable relative path below the root ("" is the root
 * itself) and says whether it is a directory. Every directory above it is a
 * plain one, opened from the one before; the caller closes the handle. A link
 * at any component, or a non-directory above the last, is EXT_INVALID. */
static ext_status open_below(const ext_fs *fs, const char *relative, HANDLE *out,
                             int *directory) {
  HANDLE current = INVALID_HANDLE_VALUE;
  ext_status s = open_root(fs, &current);
  *directory = 1;
  const char *part = relative;
  while (!s && *part) {
    const char *end = strchr(part, '/');
    size_t n = end ? (size_t)(end - part) : strlen(part);
    if (!*directory || (end && !end[1])) { /* a file with something below it, or a trailing slash */
      s = EXT_INVALID;
      break;
    }
    wchar_t *name = NULL;
    s = component(part, n, &name);
    if (s)
      break;
    HANDLE next = INVALID_HANDLE_VALUE;
    s = open_child(current, name, &next);
    free(name);
    if (s)
      break;
    CloseHandle(current);
    current = next;
    s = classify(current, directory);
    part = end ? end + 1 : part + n;
  }
  if (s) {
    if (current != INVALID_HANDLE_VALUE)
      CloseHandle(current);
    return s;
  }
  *out = current;
  return EXT_OK;
}
/* SHA-256 of the file behind an open handle. */
static ext_status hash_handle(HANDLE h, uint8_t out[32]) {
  ext_hash hash;
  ext_hash_init(&hash);
  uint8_t buffer[65536];
  ext_status s = EXT_OK;
  for (;;) {
    DWORD n = 0;
    if (!ReadFile(h, buffer, sizeof(buffer), &n, NULL)) {
      s = EXT_IO;
      break;
    }
    if (!n)
      break;
    ext_hash_update(&hash, buffer, n);
  }
  ext_hash_finish(&hash, out);
  return s;
}
/* The names in the directory behind dir, except "." and "..", read through the
 * handle and so from the directory the handle was opened on. */
static ext_status read_names(HANDLE dir, wchar_t ***out, size_t *count) {
  enum { BUFFER = 32768 };
  *out = NULL;
  *count = 0;
  uint8_t *buffer = malloc(BUFFER);
  if (!buffer)
    return EXT_NOMEM;
  wchar_t **names = NULL;
  size_t used = 0, cap = 0;
  ext_status s = EXT_OK;
  FILE_INFO_BY_HANDLE_CLASS what = FileFullDirectoryRestartInfo;
  while (!s) {
    if (!GetFileInformationByHandleEx(dir, what, buffer, BUFFER)) {
      if (GetLastError() != ERROR_NO_MORE_FILES)
        s = EXT_IO;
      break;
    }
    what = FileFullDirectoryInfo;
    FILE_FULL_DIR_INFO *entry = (FILE_FULL_DIR_INFO *)buffer;
    for (;;) {
      size_t n = entry->FileNameLength / sizeof(wchar_t);
      int dots = (n == 1 && entry->FileName[0] == L'.') ||
                 (n == 2 && entry->FileName[0] == L'.' && entry->FileName[1] == L'.');
      if (!dots) {
        if (used == cap) {
          size_t next = cap ? cap * 2 : 16;
          wchar_t **grown = realloc(names, next * sizeof(*names));
          if (!grown) {
            s = EXT_NOMEM;
            break;
          }
          names = grown;
          cap = next;
        }
        wchar_t *name = malloc((n + 1) * sizeof(*name));
        if (!name) {
          s = EXT_NOMEM;
          break;
        }
        memcpy(name, entry->FileName, n * sizeof(*name));
        name[n] = 0;
        names[used++] = name;
      }
      if (!entry->NextEntryOffset)
        break;
      entry = (FILE_FULL_DIR_INFO *)((uint8_t *)entry + entry->NextEntryOffset);
    }
  }
  free(buffer);
  if (s) {
    for (size_t i = 0; i < used; i++)
      free(names[i]);
    free(names);
    return s;
  }
  *out = names;
  *count = used;
  return EXT_OK;
}
/* Visits every regular file below the directory behind dir. Each entry is
 * opened from dir, so what is hashed or entered is what dir held, never what a
 * path would lead to now. */
static ext_status walk(HANDLE dir, const char *prefix, ext_fs_visit visit,
                       void *user, unsigned depth) {
  if (depth > 1024)
    return EXT_INVALID;
  wchar_t **names = NULL;
  size_t count = 0;
  ext_status s = read_names(dir, &names, &count);
  for (size_t i = 0; i < count && !s; i++) {
    char *name = narrow(names[i]);
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
    HANDLE child = INVALID_HANDLE_VALUE;
    int directory = 0;
    s = open_child(dir, names[i], &child);
    if (!s) {
      s = classify(child, &directory);
      if (s) {
        CloseHandle(child);
      } else if (directory) {
        s = walk(child, path, visit, user, depth + 1);
        CloseHandle(child);
      } else if (strchr(path, '\r') || strchr(path, '\n')) {
        CloseHandle(child);
        s = EXT_INVALID;
      } else {
        uint8_t sum[32];
        s = hash_handle(child, sum);
        CloseHandle(child);
        if (!s)
          s = visit(user, path, sum);
      }
    }
    free(path);
  }
  for (size_t i = 0; i < count; i++)
    free(names[i]);
  free(names);
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
  fs->canonical = NULL;
  HANDLE dir = CreateFileW(prefixed, FILE_READ_ATTRIBUTES,
                            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                            NULL, OPEN_EXISTING,
                            FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT,
                            NULL);
  if (dir == INVALID_HANDLE_VALUE) {
    DWORD error = GetLastError();
    free(fs->root);
    free(fs);
    return win_error(error);
  }
  fs->canonical = final_path(dir);
  CloseHandle(dir);
  if (!fs->canonical) {
    free(fs->root);
    free(fs);
    return EXT_IO;
  }
  *out = fs;
  return EXT_OK;
}
void ext_fs_close(ext_fs *fs) {
  if (!fs)
    return;
  free(fs->root);
  free(fs->canonical);
  free(fs);
}
ext_status ext_fs_hash_file(ext_fs *fs, const char *path, uint8_t out[32]) {
  if (!fs || !path)
    return EXT_INVALID;
  HANDLE h = INVALID_HANDLE_VALUE;
  int directory = 0;
  ext_status s = open_below(fs, path, &h, &directory);
  if (s)
    return s;
  s = directory ? EXT_INVALID : hash_handle(h, out);
  CloseHandle(h);
  return s;
}
ext_status ext_fs_read_file(ext_fs *fs, const char *path, size_t limit,
                            ext_buffer *out) {
  if (!fs || !path || !*path || !out || !limit)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  HANDLE h = INVALID_HANDLE_VALUE;
  int directory = 0;
  ext_status s = open_below(fs, path, &h, &directory);
  if (s)
    return s;
  LARGE_INTEGER size;
  size.QuadPart = 0;
  if (directory || !GetFileSizeEx(h, &size) || size.QuadPart < 0 ||
      (uint64_t)size.QuadPart > limit)
    s = EXT_INVALID;
  uint8_t *data = NULL;
  if (!s) {
    data = malloc(size.QuadPart ? (size_t)size.QuadPart : 1);
    if (!data)
      s = EXT_NOMEM;
  }
  size_t used = 0;
  while (!s && used < (size_t)size.QuadPart) {
    DWORD n = 0;
    DWORD want = (size_t)size.QuadPart - used > 65536 ? 65536 : (DWORD)((size_t)size.QuadPart - used);
    if (!ReadFile(h, data + used, want, &n, NULL) || !n)
      s = EXT_IO;
    else
      used += n;
  }
  if (!s) {
    uint8_t extra;
    DWORD n = 0;
    if (!ReadFile(h, &extra, 1, &n, NULL))
      s = EXT_IO;
    else if (n)
      s = EXT_INVALID;
  }
  CloseHandle(h);
  if (s)
    free(data);
  else
    *out = (ext_buffer){data, used};
  return s;
}
ext_status ext_fs_walk(ext_fs *fs, const char *path, ext_fs_visit visit,
                       void *user) {
  if (!fs || !path || !visit)
    return EXT_INVALID;
  HANDLE h = INVALID_HANDLE_VALUE;
  int directory = 0;
  ext_status s = open_below(fs, path, &h, &directory);
  if (s)
    return s;
  s = directory ? walk(h, "", visit, user, 0) : EXT_INVALID;
  CloseHandle(h);
  return s;
}

/* ---- Write side ----
 * Path-based like the read side: every directory below the root is checked to be
 * a plain one (not a reparse point) before it is used, and files are opened with
 * FILE_FLAG_OPEN_REPARSE_POINT and checked again by handle. Entries inherit the
 * access control of the directory they are made in, so protecting the store's
 * root is the application's job, and there is no execute bit. A path component
 * may not hold a backslash, a colon or a control character, which would name
 * another place on Windows. */
static int valid_rel(const char *path) {
  if (!path || !*path)
    return 0;
  const char *part = path;
  for (;;) {
    const char *end = strchr(part, '/');
    size_t n = end ? (size_t)(end - part) : strlen(part);
    if (!n || (n == 1 && part[0] == '.') ||
        (n == 2 && part[0] == '.' && part[1] == '.'))
      return 0;
    for (size_t i = 0; i < n; i++) {
      unsigned char c = (unsigned char)part[i];
      if (c == '\\' || c == ':' || c < 0x20)
        return 0;
    }
    if (!end)
      return 1;
    part = end + 1;
  }
}
static ext_status split_rel(const char *path, char **storage, const char **parent,
                            const char **leaf) {
  if (!valid_rel(path))
    return EXT_INVALID;
  char *copy = strdup(path);
  if (!copy)
    return EXT_NOMEM;
  char *slash = strrchr(copy, '/');
  if (slash) {
    *slash = 0;
    *parent = copy;
    *leaf = slash + 1;
  } else {
    *parent = copy + strlen(copy);
    *leaf = copy;
  }
  *storage = copy;
  return EXT_OK;
}
static int is_missing(DWORD error) {
  return error == ERROR_FILE_NOT_FOUND || error == ERROR_PATH_NOT_FOUND;
}
/* Checks every directory of relative below root is a plain one, creating the
 * missing ones when create is set. EXT_NOT_FOUND when one is missing and create
 * is not set, EXT_INVALID for a link or a file. */
static ext_status prefixes(const wchar_t *root, const char *relative, int create) {
  if (!*relative)
    return EXT_OK;
  char *copy = strdup(relative);
  if (!copy)
    return EXT_NOMEM;
  ext_status s = EXT_OK;
  char *end = copy;
  for (;;) {
    char *slash = strchr(end, '/');
    if (slash)
      *slash = 0;
    wchar_t *path = join(root, copy);
    if (slash)
      *slash = '/';
    if (!path) {
      s = EXT_NOMEM;
      break;
    }
    if (create && !CreateDirectoryW(path, NULL) &&
        GetLastError() != ERROR_ALREADY_EXISTS) {
      free(path);
      s = EXT_IO;
      break;
    }
    DWORD a = GetFileAttributesW(path);
    DWORD error = a == INVALID_FILE_ATTRIBUTES ? GetLastError() : 0;
    free(path);
    if (a == INVALID_FILE_ATTRIBUTES) {
      s = is_missing(error) ? EXT_NOT_FOUND : EXT_IO;
      break;
    }
    if ((a & FILE_ATTRIBUTE_REPARSE_POINT) || !(a & FILE_ATTRIBUTE_DIRECTORY)) {
      s = EXT_INVALID;
      break;
    }
    if (!slash)
      break;
    end = slash + 1;
  }
  free(copy);
  return s;
}
ext_status ext_fs_kind_of(ext_fs *fs, const char *path, ext_fs_kind *out) {
  if (!fs || !out)
    return EXT_INVALID;
  char *storage = NULL;
  const char *parent, *leaf;
  ext_status s = split_rel(path, &storage, &parent, &leaf);
  if (s)
    return s;
  s = prefixes(fs->root, parent, 0);
  if (s == EXT_NOT_FOUND) {
    *out = EXT_FS_NONE;
    s = EXT_OK;
  } else if (!s) {
    wchar_t *full = join(fs->root, path);
    if (!full)
      s = EXT_NOMEM;
    else {
      DWORD a = GetFileAttributesW(full);
      DWORD error = a == INVALID_FILE_ATTRIBUTES ? GetLastError() : 0;
      free(full);
      if (a == INVALID_FILE_ATTRIBUTES) {
        if (is_missing(error))
          *out = EXT_FS_NONE;
        else
          s = EXT_IO;
      } else if (a & FILE_ATTRIBUTE_REPARSE_POINT)
        *out = EXT_FS_OTHER;
      else if (a & FILE_ATTRIBUTE_DIRECTORY)
        *out = EXT_FS_DIR;
      else
        *out = EXT_FS_FILE;
    }
  }
  free(storage);
  return s;
}
void ext_fs_list_free(ext_fs_entry *entries, size_t count) {
  for (size_t i = 0; i < count; i++)
    free(entries[i].name);
  free(entries);
}
/* "directory\*", for FindFirstFileExW. */
static wchar_t *find_pattern(const wchar_t *directory) {
  size_t length = wcslen(directory);
  wchar_t *pattern = malloc((length + 3) * sizeof(*pattern));
  if (!pattern)
    return NULL;
  memcpy(pattern, directory, length * sizeof(*pattern));
  memcpy(pattern + length, L"\\*", 3 * sizeof(*pattern));
  return pattern;
}
ext_status ext_fs_list(ext_fs *fs, const char *path, ext_fs_entry **out,
                       size_t *count) {
  if (!fs || !path || !out || !count)
    return EXT_INVALID;
  *out = NULL;
  *count = 0;
  if (*path && !valid_rel(path))
    return EXT_INVALID;
  ext_status s = prefixes(fs->root, path, 0);
  if (s)
    return s;
  wchar_t *directory = join(fs->root, path);
  wchar_t *pattern = directory ? find_pattern(directory) : NULL;
  free(directory);
  if (!pattern)
    return EXT_NOMEM;
  WIN32_FIND_DATAW data;
  HANDLE find = FindFirstFileExW(pattern, FindExInfoBasic, &data,
                                 FindExSearchNameMatch, NULL,
                                 FIND_FIRST_EX_LARGE_FETCH);
  free(pattern);
  if (find == INVALID_HANDLE_VALUE)
    return EXT_IO;
  ext_fs_entry *entries = NULL;
  size_t used = 0, cap = 0;
  do {
    if (!wcscmp(data.cFileName, L".") || !wcscmp(data.cFileName, L".."))
      continue;
    if (used == cap) {
      size_t next = cap ? cap * 2 : 16;
      ext_fs_entry *grown = realloc(entries, next * sizeof(*entries));
      if (!grown) {
        s = EXT_NOMEM;
        break;
      }
      entries = grown;
      cap = next;
    }
    char *name = narrow(data.cFileName);
    if (!name) {
      s = EXT_INVALID;
      break;
    }
    entries[used].name = name;
    if (data.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)
      entries[used].kind = EXT_FS_OTHER;
    else if (data.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY)
      entries[used].kind = EXT_FS_DIR;
    else
      entries[used].kind = EXT_FS_FILE;
    used++;
  } while (FindNextFileW(find, &data));
  if (!s && GetLastError() != ERROR_NO_MORE_FILES)
    s = EXT_IO;
  FindClose(find);
  if (s) {
    ext_fs_list_free(entries, used);
    return s;
  }
  *out = entries;
  *count = used;
  return EXT_OK;
}
ext_status ext_fs_mkdirs(ext_fs *fs, const char *path) {
  if (!fs || !valid_rel(path))
    return EXT_INVALID;
  return prefixes(fs->root, path, 1);
}
ext_status ext_fs_mkdtemp(ext_fs *fs, const char *parent, const char *prefix,
                          char **name) {
  if (!fs || !parent || !prefix || !name || strpbrk(prefix, "/\\:") ||
      strlen(prefix) > 64 || (*parent && !valid_rel(parent)))
    return EXT_INVALID;
  ext_status s = prefixes(fs->root, parent, 1);
  if (s)
    return s;
  size_t parent_length = strlen(parent);
  char candidate[64 + 16 + 1];
  char *relative = malloc(parent_length + 1 + sizeof(candidate));
  if (!relative)
    return EXT_NOMEM;
  s = EXT_IO;
  for (int attempt = 0; attempt < 100; attempt++) {
    uint8_t random[8];
    s = ext_random_bytes(random, sizeof(random));
    if (s)
      break;
    size_t n = strlen(prefix);
    memcpy(candidate, prefix, n);
    for (size_t i = 0; i < sizeof(random); i++) {
      candidate[n++] = "0123456789abcdef"[random[i] >> 4];
      candidate[n++] = "0123456789abcdef"[random[i] & 15];
    }
    candidate[n] = 0;
    if (parent_length) {
      memcpy(relative, parent, parent_length);
      relative[parent_length] = '/';
      memcpy(relative + parent_length + 1, candidate, n + 1);
    } else
      memcpy(relative, candidate, n + 1);
    wchar_t *full = join(fs->root, relative);
    if (!full) {
      s = EXT_NOMEM;
      break;
    }
    BOOL made = CreateDirectoryW(full, NULL);
    DWORD error = made ? 0 : GetLastError();
    free(full);
    if (made) {
      *name = strdup(candidate);
      s = *name ? EXT_OK : EXT_NOMEM;
      break;
    }
    s = EXT_IO;
    if (error != ERROR_ALREADY_EXISTS)
      break;
  }
  free(relative);
  return s;
}
static ext_status write_all(HANDLE h, const uint8_t *data, size_t len) {
  while (len) {
    DWORD want = len > 65536 ? 65536 : (DWORD)len, n = 0;
    if (!WriteFile(h, data, want, &n, NULL) || !n)
      return EXT_IO;
    data += n;
    len -= n;
  }
  return EXT_OK;
}
/* Creates the file at full, which must not exist. */
static ext_status create_new(const wchar_t *full, HANDLE *out) {
  HANDLE h = CreateFileW(full, GENERIC_WRITE, 0, NULL, CREATE_NEW,
                         FILE_ATTRIBUTE_NORMAL | FILE_FLAG_SEQUENTIAL_SCAN, NULL);
  if (h == INVALID_HANDLE_VALUE) {
    DWORD error = GetLastError();
    return error == ERROR_FILE_EXISTS || error == ERROR_ALREADY_EXISTS ? EXT_INVALID
                                                                       : EXT_IO;
  }
  *out = h;
  return EXT_OK;
}
ext_status ext_fs_copy_file(ext_fs *src, const char *src_path, ext_fs *dst,
                            const char *dst_path, int executable, uint64_t limit,
                            uint8_t sum[32], uint64_t *size) {
  (void)executable;
  if (!src || !dst || !sum || !size || limit > (uint64_t)INT64_MAX - 1)
    return EXT_INVALID;
  char *from_storage = NULL, *to_storage = NULL;
  const char *from_parent, *from_leaf, *to_parent, *to_leaf;
  ext_status s = split_rel(src_path, &from_storage, &from_parent, &from_leaf);
  if (!s)
    s = split_rel(dst_path, &to_storage, &to_parent, &to_leaf);
  if (!s)
    s = prefixes(src->root, from_parent, 0);
  wchar_t *from = NULL, *to = NULL;
  HANDLE in = INVALID_HANDLE_VALUE, out = INVALID_HANDLE_VALUE;
  if (!s) {
    from = join(src->root, src_path);
    if (!from)
      s = EXT_NOMEM;
  }
  if (!s) {
    DWORD a = GetFileAttributesW(from);
    if (a == INVALID_FILE_ATTRIBUTES)
      s = is_missing(GetLastError()) ? EXT_NOT_FOUND : EXT_IO;
    else if (a & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY))
      s = EXT_INVALID;
  }
  if (!s) {
    in = CreateFileW(from, GENERIC_READ,
                     FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL,
                     OPEN_EXISTING,
                     FILE_FLAG_OPEN_REPARSE_POINT | FILE_FLAG_SEQUENTIAL_SCAN,
                     NULL);
    if (in == INVALID_HANDLE_VALUE)
      s = is_missing(GetLastError()) ? EXT_NOT_FOUND : EXT_IO;
  }
  if (!s) {
    BY_HANDLE_FILE_INFORMATION info;
    where w = {src->root, src->canonical};
    if (!GetFileInformationByHandle(in, &info) ||
        (info.dwFileAttributes &
         (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY)) ||
        !resolves_to(in, from, &w))
      s = EXT_INVALID; /* not the file that was checked */
  }
  if (!s)
    s = prefixes(dst->root, to_parent, 1);
  if (!s) {
    to = join(dst->root, dst_path);
    s = to ? create_new(to, &out) : EXT_NOMEM;
  }
  ext_hash h;
  ext_hash_init(&h);
  uint64_t total = 0;
  uint8_t buffer[65536];
  while (!s) {
    uint64_t room = limit + 1 - total;
    DWORD want = room < sizeof(buffer) ? (DWORD)room : (DWORD)sizeof(buffer), n = 0;
    if (!want)
      break;
    if (!ReadFile(in, buffer, want, &n, NULL)) {
      s = EXT_IO;
      break;
    }
    if (!n)
      break;
    total += n;
    if (total > limit) {
      s = EXT_INVALID; /* longer than the limit */
      break;
    }
    ext_hash_update(&h, buffer, n);
    s = write_all(out, buffer, n);
  }
  if (out != INVALID_HANDLE_VALUE && !CloseHandle(out) && !s)
    s = EXT_IO;
  if (in != INVALID_HANDLE_VALUE)
    CloseHandle(in);
  free(from);
  free(to);
  free(from_storage);
  free(to_storage);
  if (!s) {
    ext_hash_finish(&h, sum);
    *size = total;
  }
  return s;
}
ext_status ext_fs_write_file(ext_fs *fs, const char *path, const uint8_t *data,
                             size_t len, int executable) {
  (void)executable;
  if (!fs || (!data && len))
    return EXT_INVALID;
  char *storage = NULL;
  const char *parent, *leaf;
  ext_status s = split_rel(path, &storage, &parent, &leaf);
  if (s)
    return s;
  s = prefixes(fs->root, parent, 1);
  HANDLE h = INVALID_HANDLE_VALUE;
  wchar_t *full = NULL;
  if (!s) {
    full = join(fs->root, path);
    s = full ? create_new(full, &h) : EXT_NOMEM;
  }
  if (!s)
    s = write_all(h, data, len);
  if (h != INVALID_HANDLE_VALUE && !CloseHandle(h) && !s)
    s = EXT_IO;
  free(full);
  free(storage);
  return s;
}
ext_status ext_fs_rename(ext_fs *fs, const char *from, const char *to) {
  if (!fs)
    return EXT_INVALID;
  char *from_storage = NULL, *to_storage = NULL;
  const char *from_parent, *from_leaf, *to_parent, *to_leaf;
  ext_status s = split_rel(from, &from_storage, &from_parent, &from_leaf);
  if (!s)
    s = split_rel(to, &to_storage, &to_parent, &to_leaf);
  if (!s)
    s = prefixes(fs->root, from_parent, 0);
  if (!s)
    s = prefixes(fs->root, to_parent, 0);
  if (!s) {
    wchar_t *a = join(fs->root, from), *b = join(fs->root, to);
    if (!a || !b)
      s = EXT_NOMEM;
    else if (!MoveFileExW(a, b, 0))
      s = EXT_IO;
    free(a);
    free(b);
  }
  free(from_storage);
  free(to_storage);
  return s;
}
static ext_status remove_path(const wchar_t *path, unsigned depth) {
  if (depth > 1024)
    return EXT_INVALID;
  DWORD a = GetFileAttributesW(path);
  if (a == INVALID_FILE_ATTRIBUTES)
    return is_missing(GetLastError()) ? EXT_OK : EXT_IO;
  if (a & FILE_ATTRIBUTE_READONLY)
    SetFileAttributesW(path, a & ~(DWORD)FILE_ATTRIBUTE_READONLY);
  if (a & FILE_ATTRIBUTE_REPARSE_POINT) {
    /* a link: remove the link and not what it points at */
    BOOL removed = (a & FILE_ATTRIBUTE_DIRECTORY) ? RemoveDirectoryW(path)
                                                  : DeleteFileW(path);
    return removed ? EXT_OK : EXT_IO;
  }
  if (!(a & FILE_ATTRIBUTE_DIRECTORY))
    return DeleteFileW(path) ? EXT_OK : EXT_IO;
  wchar_t *pattern = find_pattern(path);
  if (!pattern)
    return EXT_NOMEM;
  WIN32_FIND_DATAW data;
  HANDLE find = FindFirstFileExW(pattern, FindExInfoBasic, &data,
                                 FindExSearchNameMatch, NULL,
                                 FIND_FIRST_EX_LARGE_FETCH);
  free(pattern);
  if (find == INVALID_HANDLE_VALUE)
    return EXT_IO;
  /* Read every name first, then remove. */
  wchar_t **names = NULL;
  size_t used = 0, cap = 0;
  ext_status s = EXT_OK;
  do {
    if (!wcscmp(data.cFileName, L".") || !wcscmp(data.cFileName, L".."))
      continue;
    if (used == cap) {
      size_t next = cap ? cap * 2 : 16;
      wchar_t **grown = realloc(names, next * sizeof(*names));
      if (!grown) {
        s = EXT_NOMEM;
        break;
      }
      names = grown;
      cap = next;
    }
    names[used] = wcsdup(data.cFileName);
    if (!names[used]) {
      s = EXT_NOMEM;
      break;
    }
    used++;
  } while (FindNextFileW(find, &data));
  if (!s && GetLastError() != ERROR_NO_MORE_FILES)
    s = EXT_IO;
  FindClose(find);
  size_t base = wcslen(path);
  for (size_t i = 0; i < used && !s; i++) {
    wchar_t *child = malloc((base + wcslen(names[i]) + 2) * sizeof(*child));
    if (!child) {
      s = EXT_NOMEM;
      break;
    }
    memcpy(child, path, base * sizeof(*child));
    child[base] = L'\\';
    wcscpy(child + base + 1, names[i]);
    s = remove_path(child, depth + 1);
    free(child);
  }
  for (size_t i = 0; i < used; i++)
    free(names[i]);
  free(names);
  if (!s && !RemoveDirectoryW(path))
    s = EXT_IO;
  return s;
}
ext_status ext_fs_remove_all(ext_fs *fs, const char *path) {
  if (!fs)
    return EXT_INVALID;
  char *storage = NULL;
  const char *parent, *leaf;
  ext_status s = split_rel(path, &storage, &parent, &leaf);
  if (s)
    return s;
  s = prefixes(fs->root, parent, 0);
  if (s == EXT_NOT_FOUND)
    s = EXT_OK; /* nothing to remove below a parent that is not there */
  else if (!s) {
    wchar_t *full = join(fs->root, path);
    if (!full)
      s = EXT_NOMEM;
    else {
      s = remove_path(full, 0);
      free(full);
    }
  }
  free(storage);
  return s;
}
ext_status ext_fs_make_root(const char *path) {
  if (!path || !*path)
    return EXT_INVALID;
  wchar_t *wide = widen(path);
  if (!wide)
    return EXT_INVALID;
  DWORD n = GetFullPathNameW(wide, 0, NULL, NULL);
  wchar_t *full = n ? malloc((n + 1) * sizeof(*full)) : NULL;
  if (!full || !GetFullPathNameW(wide, n, full, NULL)) {
    free(wide);
    free(full);
    return EXT_IO;
  }
  free(wide);
  size_t length = wcslen(full);
  while (length > 3 && (full[length - 1] == L'\\' || full[length - 1] == L'/'))
    full[--length] = 0;
  /* A prefix that cannot be made (a drive, a share) is passed over: a path
   * whose parent really is missing fails at the last component. */
  for (size_t i = 3; i < length; i++) {
    if (full[i] != L'\\' && full[i] != L'/')
      continue;
    wchar_t saved = full[i];
    full[i] = 0;
    CreateDirectoryW(full, NULL);
    full[i] = saved;
  }
  ext_status s = EXT_OK;
  if (!CreateDirectoryW(full, NULL) && GetLastError() != ERROR_ALREADY_EXISTS)
    s = EXT_IO;
  free(full);
  return s;
}
ext_status ext_fs_probe(const char *path, ext_fs_kind *out) {
  if (!path || !*path || !out)
    return EXT_INVALID;
  wchar_t *wide = widen(path);
  if (!wide)
    return EXT_INVALID;
  DWORD a = GetFileAttributesW(wide);
  DWORD error = a == INVALID_FILE_ATTRIBUTES ? GetLastError() : 0;
  free(wide);
  if (a == INVALID_FILE_ATTRIBUTES) {
    if (!is_missing(error))
      return EXT_IO;
    *out = EXT_FS_NONE;
  } else if (a & FILE_ATTRIBUTE_REPARSE_POINT)
    *out = EXT_FS_OTHER;
  else if (a & FILE_ATTRIBUTE_DIRECTORY)
    *out = EXT_FS_DIR;
  else
    *out = EXT_FS_FILE;
  return EXT_OK;
}
ext_status ext_fs_mode_bits(ext_fs *fs, const char *path, uint32_t *mode) {
  if (!fs || !mode)
    return EXT_INVALID;
  ext_fs_kind kind;
  ext_status s = ext_fs_kind_of(fs, path, &kind);
  if (s)
    return s;
  if (kind == EXT_FS_NONE)
    return EXT_NOT_FOUND;
  if (kind != EXT_FS_FILE)
    return EXT_INVALID;
  *mode = 0;
  return EXT_OK;
}
