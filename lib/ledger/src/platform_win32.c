/* The platform layer on Windows. Paths are UTF-8 and converted to UTF-16.
 * Files are opened with FILE_SHARE_DELETE, so a writer can rename a new log
 * over one that readers hold open, as POSIX allows. */
#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>

#include "ledger_internal.h"

#include <stdlib.h>
#include <string.h>

struct ll_mutex {
  SRWLOCK lock;
};
struct ll_cond {
  CONDITION_VARIABLE cv;
};

ll_mutex *ll_mutex_new(void) {
  ll_mutex *m = malloc(sizeof *m);
  if (m) InitializeSRWLock(&m->lock);
  return m;
}
void ll_mutex_free(ll_mutex *m) { free(m); }
void ll_mutex_lock(ll_mutex *m) { AcquireSRWLockExclusive(&m->lock); }
void ll_mutex_unlock(ll_mutex *m) { ReleaseSRWLockExclusive(&m->lock); }

ll_cond *ll_cond_new(void) {
  ll_cond *c = malloc(sizeof *c);
  if (c) InitializeConditionVariable(&c->cv);
  return c;
}
void ll_cond_free(ll_cond *c) { free(c); }
void ll_cond_wait(ll_cond *c, ll_mutex *m, int64_t timeout_ms) {
  SleepConditionVariableSRW(&c->cv, &m->lock, timeout_ms < 0 ? INFINITE : (DWORD)(timeout_ms > 0x7FFFFFFF ? 0x7FFFFFFF : timeout_ms), 0);
}
void ll_cond_broadcast(ll_cond *c) { WakeAllConditionVariable(&c->cv); }

int64_t ll_now_unix_ms(void) {
  FILETIME ft;
  GetSystemTimeAsFileTime(&ft);
  uint64_t t = ((uint64_t)ft.dwHighDateTime << 32) | ft.dwLowDateTime; /* 100 ns since 1601 */
  return (int64_t)(t / 10000) - 11644473600000LL;
}

void ll_sleep_ms(int64_t ms) { Sleep((DWORD)ms); }

static wchar_t *wide(const char *path) {
  int n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, NULL, 0);
  if (n <= 0) return NULL;
  wchar_t *w = malloc((size_t)n * sizeof *w);
  if (w && MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, w, n) <= 0) {
    free(w);
    return NULL;
  }
  return w;
}

struct ll_file {
  HANDLE h;
};

static int not_found(DWORD e) { return e == ERROR_FILE_NOT_FOUND || e == ERROR_PATH_NOT_FOUND; }

int ll_file_open(const char *path, int flags, ll_file **out) {
  wchar_t *w = wide(path);
  if (!w) return EXT_LEDGER_IO;
  DWORD access = GENERIC_READ | ((flags & LL_WRITE) ? GENERIC_WRITE : 0);
  DWORD disposition = OPEN_EXISTING;
  if ((flags & LL_CREATE) && (flags & LL_TRUNC)) disposition = CREATE_ALWAYS;
  else if (flags & LL_CREATE) disposition = OPEN_ALWAYS;
  else if (flags & LL_TRUNC) disposition = TRUNCATE_EXISTING;
  HANDLE h = CreateFileW(w, access, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, disposition, FILE_ATTRIBUTE_NORMAL, NULL);
  DWORD err = GetLastError();
  free(w);
  if (h == INVALID_HANDLE_VALUE) return not_found(err) ? EXT_LEDGER_NOT_FOUND : EXT_LEDGER_IO;
  ll_file *f = malloc(sizeof *f);
  if (!f) {
    CloseHandle(h);
    return EXT_LEDGER_IO;
  }
  f->h = h;
  *out = f;
  return 0;
}

int ll_file_read_at(ll_file *f, uint64_t off, void *buf, size_t n, size_t *got) {
  size_t done = 0;
  while (done < n) {
    OVERLAPPED o;
    memset(&o, 0, sizeof o);
    uint64_t at = off + done;
    o.Offset = (DWORD)at;
    o.OffsetHigh = (DWORD)(at >> 32);
    DWORD chunk = (DWORD)((n - done) > 0x40000000 ? 0x40000000 : (n - done)), read = 0;
    if (!ReadFile(f->h, (char *)buf + done, chunk, &read, &o)) {
      if (GetLastError() == ERROR_HANDLE_EOF) break;
      return EXT_LEDGER_IO;
    }
    if (read == 0) break;
    done += read;
  }
  *got = done;
  return 0;
}

int ll_file_write_at(ll_file *f, uint64_t off, const void *buf, size_t n) {
  size_t done = 0;
  while (done < n) {
    OVERLAPPED o;
    memset(&o, 0, sizeof o);
    uint64_t at = off + done;
    o.Offset = (DWORD)at;
    o.OffsetHigh = (DWORD)(at >> 32);
    DWORD chunk = (DWORD)((n - done) > 0x40000000 ? 0x40000000 : (n - done)), wrote = 0;
    if (!WriteFile(f->h, (const char *)buf + done, chunk, &wrote, &o)) return EXT_LEDGER_IO;
    done += wrote;
  }
  return 0;
}

int ll_file_size(ll_file *f, uint64_t *size) {
  LARGE_INTEGER li;
  if (!GetFileSizeEx(f->h, &li)) return EXT_LEDGER_IO;
  *size = (uint64_t)li.QuadPart;
  return 0;
}

int ll_file_truncate(ll_file *f, uint64_t size) {
  FILE_END_OF_FILE_INFO info;
  info.EndOfFile.QuadPart = (LONGLONG)size;
  return SetFileInformationByHandle(f->h, FileEndOfFileInfo, &info, sizeof info) ? 0 : EXT_LEDGER_IO;
}

int ll_file_sync(ll_file *f) { return FlushFileBuffers(f->h) ? 0 : EXT_LEDGER_IO; }

int ll_file_identity(ll_file *f, ll_fid *id) {
  BY_HANDLE_FILE_INFORMATION info;
  if (!GetFileInformationByHandle(f->h, &info)) return EXT_LEDGER_IO;
  id->a = info.dwVolumeSerialNumber;
  id->b = ((uint64_t)info.nFileIndexHigh << 32) | info.nFileIndexLow;
  return 0;
}

int ll_file_try_lock(ll_file *f) {
  OVERLAPPED o;
  memset(&o, 0, sizeof o);
  if (LockFileEx(f->h, LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)) return 0;
  return GetLastError() == ERROR_LOCK_VIOLATION ? EXT_LEDGER_BUSY : EXT_LEDGER_IO;
}

void ll_file_unlock(ll_file *f) {
  OVERLAPPED o;
  memset(&o, 0, sizeof o);
  UnlockFileEx(f->h, 0, 1, 0, &o);
}

void ll_file_close(ll_file *f) {
  if (!f) return;
  CloseHandle(f->h);
  free(f);
}

int ll_path_identity(const char *path, ll_fid *id) {
  ll_file *f;
  int rc = ll_file_open(path, LL_READ, &f);
  if (rc) return rc;
  rc = ll_file_identity(f, id);
  ll_file_close(f);
  return rc;
}

int ll_path_kind(const char *path) {
  wchar_t *w = wide(path);
  if (!w) return -1;
  DWORD attrs = GetFileAttributesW(w);
  DWORD err = GetLastError();
  free(w);
  if (attrs == INVALID_FILE_ATTRIBUTES) return not_found(err) ? 0 : -1;
  return (attrs & FILE_ATTRIBUTE_DIRECTORY) ? 1 : 2;
}

int ll_mkdir(const char *path) {
  wchar_t *w = wide(path);
  if (!w) return EXT_LEDGER_IO;
  int rc = 0;
  if (!CreateDirectoryW(w, NULL)) {
    DWORD err = GetLastError();
    if (err == ERROR_PATH_NOT_FOUND) {
      size_t n = strlen(path);
      char *copy = malloc(n + 1);
      if (!copy) rc = EXT_LEDGER_IO;
      else {
        memcpy(copy, path, n + 1);
        char *cut = NULL;
        for (char *p = copy; *p; p++)
          if (*p == '/' || *p == '\\') cut = p;
        rc = EXT_LEDGER_IO;
        if (cut && cut != copy) {
          *cut = 0;
          rc = ll_mkdir(copy);
        }
        free(copy);
        if (!rc && !CreateDirectoryW(w, NULL) && GetLastError() != ERROR_ALREADY_EXISTS) rc = EXT_LEDGER_IO;
      }
    } else if (err != ERROR_ALREADY_EXISTS) {
      rc = EXT_LEDGER_IO;
    }
  }
  free(w);
  return rc;
}

int ll_list_dir(const char *dir, int (*each)(void *arg, const char *name), void *arg) {
  size_t n = strlen(dir);
  char *pattern = malloc(n + 3);
  if (!pattern) return EXT_LEDGER_IO;
  memcpy(pattern, dir, n);
  memcpy(pattern + n, "\\*", 3);
  wchar_t *w = wide(pattern);
  free(pattern);
  if (!w) return EXT_LEDGER_IO;
  WIN32_FIND_DATAW data;
  HANDLE h = FindFirstFileW(w, &data);
  free(w);
  if (h == INVALID_HANDLE_VALUE) return EXT_LEDGER_IO;
  int rc = 0;
  do {
    if (wcscmp(data.cFileName, L".") == 0 || wcscmp(data.cFileName, L"..") == 0) continue;
    char name[MAX_PATH * 4];
    if (WideCharToMultiByte(CP_UTF8, 0, data.cFileName, -1, name, sizeof name, NULL, NULL) <= 0) {
      rc = EXT_LEDGER_IO;
      break;
    }
    rc = each(arg, name);
  } while (!rc && FindNextFileW(h, &data));
  FindClose(h);
  return rc;
}

int ll_rename(const char *from, const char *to) {
  wchar_t *a = wide(from), *b = wide(to);
  int rc = EXT_LEDGER_IO;
  if (a && b) {
    /* A reader that opened the file without FILE_SHARE_DELETE blocks the
     * rename for as long as it holds it; wait a little for it. */
    for (int attempt = 0; attempt < 100; attempt++) {
      if (MoveFileExW(a, b, MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH)) {
        rc = 0;
        break;
      }
      DWORD err = GetLastError();
      if (err != ERROR_ACCESS_DENIED && err != ERROR_SHARING_VIOLATION) break;
      Sleep(10);
    }
  }
  free(a);
  free(b);
  return rc;
}

int ll_remove(const char *path) {
  wchar_t *w = wide(path);
  if (!w) return EXT_LEDGER_IO;
  int rc = (DeleteFileW(w) || not_found(GetLastError())) ? 0 : EXT_LEDGER_IO;
  free(w);
  return rc;
}

int ll_sync_dir(const char *dir) {
  (void)dir; /* NTFS commits a rename made with MOVEFILE_WRITE_THROUGH */
  return 0;
}
#endif
