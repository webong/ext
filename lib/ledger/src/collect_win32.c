/* The machine and its processes on Windows: Toolhelp
 * for the process list, the creation time as the start identity, the full image
 * name, and the owner's SID; the boot GUID for the host identity. */
#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <sddl.h>
#include <tlhelp32.h>

#include "collect.h"
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

const char *ll_goos(void) { return "windows"; }

const char *ll_goarch(void) {
#if defined(_M_X64) || defined(__x86_64__)
  return "amd64";
#elif defined(_M_ARM64) || defined(__aarch64__)
  return "arm64";
#elif defined(_M_IX86) || defined(__i386__)
  return "386";
#elif defined(_M_ARM) || defined(__arm__)
  return "arm";
#else
  return "unknown";
#endif
}

static char *utf8(const wchar_t *w, int len) {
  int n = WideCharToMultiByte(CP_UTF8, 0, w, len, NULL, 0, NULL, NULL);
  if (n <= 0 && len != 0) return NULL;
  char *s = malloc((size_t)n + 1);
  if (!s) return NULL;
  if (n) WideCharToMultiByte(CP_UTF8, 0, w, len, s, n, NULL, NULL);
  s[n] = 0;
  if (len < 0 && n > 0) s[n - 1] = 0;
  return s;
}

static wchar_t *wide(const char *s) {
  int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
  if (n <= 0) return NULL;
  wchar_t *w = malloc((size_t)n * sizeof *w);
  if (w) MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
  return w;
}

char *ll_os_hostname(void) {
  wchar_t buf[256];
  DWORD n = 256;
  if (!GetComputerNameExW(ComputerNamePhysicalDnsHostname, buf, &n)) return utf8(L"", -1);
  return utf8(buf, (int)n);
}

int64_t ll_os_ppid(void) {
  DWORD self = GetCurrentProcessId(), parent = 0;
  HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
  if (snap == INVALID_HANDLE_VALUE) return 0;
  PROCESSENTRY32W e;
  e.dwSize = sizeof e;
  for (BOOL ok = Process32FirstW(snap, &e); ok; ok = Process32NextW(snap, &e))
    if (e.th32ProcessID == self) {
      parent = e.th32ParentProcessID;
      break;
    }
  CloseHandle(snap);
  return (int64_t)parent;
}

char *ll_os_getcwd(void) {
  wchar_t buf[32768];
  DWORD n = GetCurrentDirectoryW(32768, buf);
  return n ? utf8(buf, (int)n) : NULL;
}

int64_t ll_os_uid(void) { return -1; } /* Go's os.Getuid on Windows */

char *ll_os_getenv(const char *name) {
  wchar_t *w = wide(name);
  if (!w) return NULL;
  wchar_t buf[32768];
  DWORD n = GetEnvironmentVariableW(w, buf, 32768);
  free(w);
  if (n == 0 || n >= 32768) return NULL;
  return utf8(buf, (int)n);
}

char *ll_os_home(void) {
  char *home = ll_os_getenv("USERPROFILE");
  if (home && !*home) {
    free(home);
    home = NULL;
  }
  return home;
}

int ll_os_exists(const char *path) {
  wchar_t *w = wide(path);
  if (!w) return 0;
  DWORD attrs = GetFileAttributesW(w);
  free(w);
  return attrs != INVALID_FILE_ATTRIBUTES;
}

char *ll_os_namespaces(size_t *len) {
  *len = 0;
  return NULL;
}

/* The status of a failed Windows call, with the system's message as its detail. */
static lj_value *win_status(DWORD err) {
  char msg[512] = "";
  DWORD n = FormatMessageA(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS, NULL, err, MAKELANGID(LANG_ENGLISH, SUBLANG_ENGLISH_US), msg,
                           sizeof msg, NULL);
  while (n > 0 && (msg[n - 1] == '\r' || msg[n - 1] == '\n')) msg[--n] = 0;
  if (!n) snprintf(msg, sizeof msg, "Winapi error #%lu", (unsigned long)err);
  if (err == ERROR_ACCESS_DENIED) return ll_status("permission-denied", "permission", msg);
  return ll_status("unavailable", "no_device", msg);
}

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

static const wchar_t *base_name(const wchar_t *path) {
  const wchar_t *b = path;
  for (const wchar_t *p = path; *p; p++)
    if (*p == L'\\' || *p == L'/') b = p + 1;
  return b;
}

static int read_process(const PROCESSENTRY32W *e, lj_value *processes) {
  lj_value *p = rec_obj(), *coverage = rec_obj();
  char *name = utf8(e->szExeFile, -1);
  int bad = set(p, "pid", rec_num(e->th32ProcessID)) | set(p, "parent_pid", rec_num(e->th32ParentProcessID)) | set(p, "name", rec_str(name ? name : ""));
  free(name);
  HANDLE h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, e->th32ProcessID);
  FILETIME creation, exit_time, kernel, user;
  if (!h || !GetProcessTimes(h, &creation, &exit_time, &kernel, &user)) {
    bad |= set(coverage, "identity", win_status(GetLastError()));
    if (h) CloseHandle(h);
    bad |= set(p, "coverage", coverage) | lj_array_push(processes, p);
    return bad ? -1 : 0;
  }
  char start[32];
  snprintf(start, sizeof start, "%llu", ((unsigned long long)creation.dwHighDateTime << 32) | creation.dwLowDateTime);
  bad |= set(p, "start_id", rec_str(start)) | set(coverage, "identity", ll_status("complete", NULL, NULL));
  static wchar_t image[32768];
  DWORD size = 32768;
  if (QueryFullProcessImageNameW(h, 0, image, &size)) {
    char *exe = utf8(image, (int)size), *base = utf8(base_name(image), -1);
    bad |= set(p, "executable", rec_str(exe ? exe : "")) | set(p, "name", rec_str(base ? base : "")) | set(coverage, "executable", ll_status("complete", NULL, NULL));
    free(exe);
    free(base);
  } else {
    bad |= set(coverage, "executable", win_status(GetLastError()));
  }
  HANDLE token;
  DWORD err = 0;
  if (OpenProcessToken(h, TOKEN_QUERY, &token)) {
    union {
      TOKEN_USER user;
      char bytes[256];
    } info;
    DWORD got;
    LPWSTR sid = NULL;
    if (GetTokenInformation(token, TokenUser, &info, sizeof info, &got) && ConvertSidToStringSidW(info.user.User.Sid, &sid)) {
      char *owner = utf8(sid, -1);
      bad |= set(p, "owner", rec_str(owner ? owner : "")) | set(coverage, "owner", ll_status("complete", NULL, NULL));
      free(owner);
      LocalFree(sid);
    } else {
      err = GetLastError();
    }
    CloseHandle(token);
  } else {
    err = GetLastError();
  }
  if (err) bad |= set(coverage, "owner", win_status(err));
  CloseHandle(h);
  bad |= set(p, "coverage", coverage) | lj_array_push(processes, p);
  return bad ? -1 : 0;
}

int ll_process_one(const ext_ledger_context *ctx, int64_t pid, lj_value *processes) {
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
  if (snap == INVALID_HANDLE_VALUE) return GetLastError() == ERROR_ACCESS_DENIED ? EXT_LEDGER_DENIED : EXT_LEDGER_UNAVAILABLE;
  PROCESSENTRY32W e;
  e.dwSize = sizeof e;
  rc = EXT_LEDGER_NOT_FOUND;
  for (BOOL ok = Process32FirstW(snap, &e); ok; ok = Process32NextW(snap, &e)) {
    if ((int64_t)e.th32ProcessID != pid) continue;
    rc = read_process(&e, processes) == 0 ? 0 : EXT_LEDGER_IO;
    break;
  }
  CloseHandle(snap);
  return rc;
}

int ll_process_start(int64_t pid, char *out, size_t n) {
  out[0] = 0;
  HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
  if (snap == INVALID_HANDLE_VALUE) return EXT_LEDGER_UNAVAILABLE;
  PROCESSENTRY32W e;
  e.dwSize = sizeof e;
  int rc = EXT_LEDGER_NOT_FOUND;
  for (BOOL ok = Process32FirstW(snap, &e); ok; ok = Process32NextW(snap, &e)) {
    if ((int64_t)e.th32ProcessID != pid) continue;
    lj_value *one = lj_new(LJ_ARRAY);
    if (one && read_process(&e, one) == 0 && one->u.array.len) {
      snprintf(out, n, "%s", rec_field(one->u.array.items[0], "start_id"));
      if (*out) rc = 0;
    }
    lj_free(one);
    break;
  }
  CloseHandle(snap);
  return rc;
}

int ll_processes(const ext_ledger_context *ctx, int64_t limit, lj_value *processes, lj_value **enumeration) {
  *enumeration = NULL;
  HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
  if (snap == INVALID_HANDLE_VALUE) return GetLastError() == ERROR_ACCESS_DENIED ? EXT_LEDGER_DENIED : EXT_LEDGER_UNAVAILABLE;
  PROCESSENTRY32W e;
  e.dwSize = sizeof e;
  int rc = 0, limited = 0;
  for (BOOL ok = Process32FirstW(snap, &e); ok && !rc; ok = Process32NextW(snap, &e)) {
    if ((rc = ll_ctx_check(ctx))) break;
    if (e.th32ProcessID == 0) continue;
    if ((int64_t)processes->u.array.len >= limit) {
      limited = 1;
      break;
    }
    if (read_process(&e, processes) < 0) rc = EXT_LEDGER_IO;
  }
  CloseHandle(snap);
  if (rc) return rc;
  *enumeration = limited ? ll_status("partial", NULL, "record limit reached") : ll_status("complete", NULL, NULL);
  return *enumeration ? 0 : EXT_LEDGER_IO;
}

typedef LONG(WINAPI *nt_query_fn)(ULONG, PVOID, ULONG, PULONG);

int ll_process_host_id(char out[25]) {
  HMODULE ntdll = GetModuleHandleW(L"ntdll.dll");
  nt_query_fn query = ntdll ? (nt_query_fn)(void *)GetProcAddress(ntdll, "NtQuerySystemInformation") : NULL;
  unsigned char info[32];
  ULONG size = 0;
  if (!query || query(90 /* SystemBootEnvironmentInformation */, info, sizeof info, &size) < 0) return EXT_LEDGER_UNAVAILABLE;
  char *env = ll_host_environment();
  if (!env) return EXT_LEDGER_IO;
  lj_buf s = {0};
  lj_buf_puts(&s, env);
  lj_buf_put(&s, (const char *)info, 16); /* the boot GUID */
  free(env);
  if (s.oom) {
    lj_buf_free(&s);
    return EXT_LEDGER_IO;
  }
  rec_digest(s.ptr, s.len, out);
  lj_buf_free(&s);
  return 0;
}
/* ---- host inventory ---- */

char ll_path_list_separator(void) { return ';'; }
const char *ll_executable_suffix(void) { return ".exe"; }

/* filepath.Clean on Windows: backslashes, no empty or "." elements, ".." resolved, the volume kept. */
char *ll_path_clean(const char *path) {
  size_t n = strlen(path);
  char *s = malloc(n + 2);
  if (!s) return NULL;
  for (size_t i = 0; i <= n; i++) s[i] = path[i] == '\\' ? '/' : path[i];
  size_t volume = 0;
  int unc = s[0] == '/' && s[1] == '/';
  if (((s[0] | 32) >= 'a' && (s[0] | 32) <= 'z') && s[1] == ':') volume = 2;
  else if (unc) { /* \\server\share */
    size_t k = 2;
    for (int part = 0; part < 2 && k < n; part++) {
      while (k < n && s[k] != '/') k++;
      if (part == 0 && k < n) k++;
    }
    volume = k;
  }
  char *rest = rec_clean(s + volume);
  if (!rest) {
    free(s);
    return NULL;
  }
  int rooted = volume < n && s[volume] == '/';
  size_t rl = strlen(rest);
  char *out = malloc(volume + rl + 3);
  if (out) {
    memcpy(out, s, volume);
    size_t w = volume;
    if (strcmp(rest, ".") == 0 && volume) {
      if (rooted) out[w++] = '/';
      else if (volume == 2) out[w++] = '.';
    } else {
      memcpy(out + w, rest, rl);
      w += rl;
    }
    out[w] = 0;
    for (char *p = out; *p; p++)
      if (*p == '/') *p = '\\';
  }
  free(rest);
  free(s);
  return out;
}

char *ll_path_join(const char *dir, const char *name) {
  size_t a = strlen(dir), b = strlen(name);
  char *j = malloc(a + b + 2);
  if (!j) return NULL;
  memcpy(j, dir, a);
  j[a] = '\\';
  memcpy(j + a + 1, name, b + 1);
  char *c = ll_path_clean(j);
  free(j);
  return c;
}

int ll_is_executable(const char *path) {
  wchar_t *w = wide(path);
  if (!w) return 0;
  DWORD attrs = GetFileAttributesW(w);
  free(w);
  size_t n = strlen(path);
  return attrs != INVALID_FILE_ATTRIBUTES && !(attrs & FILE_ATTRIBUTE_DIRECTORY) && n > 4 && _stricmp(path + n - 4, ".exe") == 0;
}

char *ll_resolve_path(const char *path) {
  wchar_t *w = wide(path);
  if (!w) return NULL;
  HANDLE h = CreateFileW(w, 0, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS, NULL);
  free(w);
  if (h == INVALID_HANDLE_VALUE) return NULL;
  wchar_t buf[32768];
  DWORD n = GetFinalPathNameByHandleW(h, buf, 32768, FILE_NAME_NORMALIZED);
  CloseHandle(h);
  if (n == 0 || n >= 32768) return NULL;
  const wchar_t *p = buf;
  if (wcsncmp(p, L"\\\\?\\UNC\\", 8) == 0) { /* \\?\UNC\server\share -> \\server\share */
    p += 6;
    buf[6] = L'\\';
  } else if (wcsncmp(p, L"\\\\?\\", 4) == 0) {
    p += 4;
  }
  return utf8(p, -1);
}

static void add_shell(ll_shell_path **out, size_t *n, size_t *cap, char *path, int *rc) {
  if (!path) {
    *rc = EXT_LEDGER_IO;
    return;
  }
  if (*n == *cap) {
    *cap = *cap ? *cap * 2 : 8;
    ll_shell_path *g = realloc(*out, *cap * sizeof **out);
    if (!g) {
      free(path);
      *rc = EXT_LEDGER_IO;
      return;
    }
    *out = g;
  }
  (*out)[*n].path = path;
  (*out)[(*n)++].source = "system";
}

int ll_platform_shell_paths(const ext_ledger_context *ctx, ll_shell_path **out, size_t *n) {
  *out = NULL;
  *n = 0;
  size_t cap = 0;
  int rc = 0;
  char *root = ll_os_getenv("SystemRoot");
  if (root && *root) {
    char *sys = ll_path_join(root, "System32");
    char *ps = sys ? ll_path_join(sys, "WindowsPowerShell\\v1.0\\powershell.exe") : NULL;
    add_shell(out, n, &cap, sys ? ll_path_join(sys, "cmd.exe") : NULL, &rc);
    add_shell(out, n, &cap, ps, &rc);
    free(sys);
  }
  free(root);
  static const char *const roots[] = {"ProgramFiles", "ProgramFiles(x86)"};
  for (int i = 0; !rc && i < 2; i++) {
    char *base = ll_os_getenv(roots[i]);
    if (!base || !(((base[0] | 32) >= 'a' && (base[0] | 32) <= 'z') && base[1] == ':')) {
      free(base);
      continue;
    }
    char *dir = ll_path_join(base, "PowerShell");
    free(base);
    if (!dir) {
      rc = EXT_LEDGER_IO;
      break;
    }
    char *pattern = ll_path_join(dir, "*");
    wchar_t *w = pattern ? wide(pattern) : NULL;
    free(pattern);
    WIN32_FIND_DATAW data;
    HANDLE h = w ? FindFirstFileW(w, &data) : INVALID_HANDLE_VALUE;
    free(w);
    if (h != INVALID_HANDLE_VALUE) {
      do {
        if ((rc = ll_ctx_check(ctx))) break;
        if (!(data.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) || !wcscmp(data.cFileName, L".") || !wcscmp(data.cFileName, L"..")) continue;
        char *version = utf8(data.cFileName, -1), *vdir = version ? ll_path_join(dir, version) : NULL;
        add_shell(out, n, &cap, vdir ? ll_path_join(vdir, "pwsh.exe") : NULL, &rc);
        free(version);
        free(vdir);
      } while (!rc && FindNextFileW(h, &data));
      FindClose(h);
    }
    free(dir);
  }
  return rc;
}

/* The strings of a double-NUL-terminated list. */
static int each_string(const wchar_t *list, int (*fn)(void *, const char *), void *arg) {
  for (const wchar_t *p = list; *p; p += wcslen(p) + 1) {
    char *s = utf8(p, -1);
    int rc = s ? fn(arg, s) : EXT_LEDGER_IO;
    free(s);
    if (rc) return rc;
  }
  return 0;
}

typedef struct {
  lj_value *mounts; /* path key -> {mount_point, source} */
  const char *volume;
} mounts_ctx;

static char *lower(const char *s) {
  char *k = ll_path_clean(s);
  for (char *p = k; p && *p; p++)
    if (*p >= 'A' && *p <= 'Z') *p = (char)(*p + 32);
  return k;
}

static int add_mount(void *arg, const char *path) {
  mounts_ctx *m = arg;
  char *key = lower(path);
  if (!key) return EXT_LEDGER_IO;
  int rc = 0;
  if (!m->volume && lj_get(m->mounts, key)) rc = 0; /* a drive letter a volume already names */
  else {
    lj_value *fs = rec_obj();
    rc = (rec_set(fs, "mount_point", rec_str(path)) | rec_set(fs, "source", rec_str(m->volume ? m->volume : path)) |
          lj_object_set(m->mounts, key, strlen(key), fs))
             ? EXT_LEDGER_IO
             : 0;
  }
  free(key);
  return rc;
}

static char *errtext(DWORD err) {
  char msg[512] = "";
  DWORD n = FormatMessageA(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS, NULL, err, MAKELANGID(LANG_ENGLISH, SUBLANG_ENGLISH_US), msg,
                           sizeof msg, NULL);
  while (n > 0 && (msg[n - 1] == '\r' || msg[n - 1] == '\n')) msg[--n] = 0;
  if (!n) snprintf(msg, sizeof msg, "Winapi error #%lu", (unsigned long)err);
  char *out = malloc(strlen(msg) + 1);
  if (out) strcpy(out, msg);
  return out;
}

int ll_platform_filesystems(const ext_ledger_context *ctx, lj_value *filesystems) {
  lj_value *mounts = rec_obj();
  if (!mounts) return EXT_LEDGER_IO;
  mounts_ctx m = {mounts, NULL};
  wchar_t volume[1024];
  HANDLE find = FindFirstVolumeW(volume, 1024);
  int rc = find == INVALID_HANDLE_VALUE ? EXT_LEDGER_UNAVAILABLE : 0;
  while (!rc) {
    if ((rc = ll_ctx_check(ctx))) break;
    char *name = utf8(volume, -1);
    static wchar_t paths[32768];
    DWORD required = 0;
    if (!name) rc = EXT_LEDGER_IO;
    else if (GetVolumePathNamesForVolumeNameW(volume, paths, 32768, &required)) {
      m.volume = name;
      rc = each_string(paths, add_mount, &m);
    }
    free(name);
    if (rc) break;
    if (!FindNextVolumeW(find, volume, 1024)) {
      if (GetLastError() != ERROR_NO_MORE_FILES) rc = EXT_LEDGER_UNAVAILABLE;
      break;
    }
  }
  if (find != INVALID_HANDLE_VALUE) FindVolumeClose(find);
  static wchar_t drives[32768];
  if (!rc) {
    DWORD count = GetLogicalDriveStringsW(32768, drives);
    if (count == 0 || count >= 32768) rc = EXT_LEDGER_UNAVAILABLE;
    else {
      m.volume = NULL;
      rc = each_string(drives, add_mount, &m);
    }
  }
  for (size_t i = 0; !rc && i < mounts->u.object.len; i++) {
    if ((rc = ll_ctx_check(ctx))) break;
    lj_value *fs = lj_clone(mounts->u.object.members[i].value);
    wchar_t *point = fs ? wide(rec_field(fs, "mount_point")) : NULL;
    if (!point) {
      lj_free(fs);
      rc = EXT_LEDGER_IO;
      break;
    }
    UINT type = GetDriveTypeW(point);
    static const char *const kinds[] = {"unknown", "unknown", "removable", "fixed", "network", "optical", "ramdisk"};
    int bad = rec_set(fs, "drive_kind", rec_str(type <= 6 ? kinds[type] : "unknown"));
    wchar_t label[261], kind[261];
    DWORD flags = 0;
    char detail[1024] = "";
    int present = 1;
    if (GetVolumeInformationW(point, label, 261, NULL, NULL, &flags, kind, 261)) {
      char *l = utf8(label, -1), *k = utf8(kind, -1);
      bad |= rec_set(fs, "type", rec_str(k ? k : "")) | rec_set(fs, "read_only", rec_bool((flags & 0x00080000) != 0));
      if (l && *l) bad |= rec_set(fs, "label", rec_str(l));
      free(l);
      free(k);
    } else {
      DWORD err = GetLastError();
      if (err == ERROR_NOT_READY && (type == 2 || type == 5)) present = 0; /* an empty removable or optical drive */
      char *t = errtext(err);
      snprintf(detail, sizeof detail, "volume details unavailable: %s", t ? t : "");
      free(t);
      bad |= rec_set(fs, "type", rec_str("")) | rec_set(fs, "read_only", rec_bool(0));
    }
    ULARGE_INTEGER available, total, free_bytes;
    if (present && GetDiskFreeSpaceExW(point, &available, &total, &free_bytes)) {
      lj_value *space = ll_filesystem_space(total.QuadPart, free_bytes.QuadPart, available.QuadPart, 1);
      if (space) bad |= rec_set(fs, "space", space);
    } else if (present) {
      char *t = errtext(GetLastError());
      size_t at = strlen(detail);
      snprintf(detail + at, sizeof detail - at, "%sspace unavailable: %s", at ? "; " : "", t ? t : "");
      free(t);
    }
    if (*detail) bad |= rec_set(fs, "detail_error", rec_str(detail));
    free(point);
    if (bad) rc = EXT_LEDGER_IO;
    else if (present && lj_array_push(filesystems, fs)) rc = EXT_LEDGER_IO;
    else if (!present) lj_free(fs);
  }
  lj_free(mounts);
  return rc;
}
#endif
