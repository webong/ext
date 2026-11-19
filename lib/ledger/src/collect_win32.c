/* The machine and its processes on Windows, as res/system reads them: Toolhelp
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
#endif
