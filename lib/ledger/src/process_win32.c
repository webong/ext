/* Child processes on Windows. A child starts suspended inside a job object that
 * ends every process in it when closed, so ending it ends what it started too;
 * its stdout and stderr share one pipe that a thread drains into the sink. */
#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <tlhelp32.h>

#include "process.h"
#include "collect.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct ll_thread {
  HANDLE h;
  void (*fn)(void *);
  void *arg;
};

static DWORD WINAPI thread_main(LPVOID p) {
  ll_thread *t = p;
  t->fn(t->arg);
  return 0;
}

int ll_thread_start(ll_thread **out, void (*fn)(void *), void *arg) {
  ll_thread *t = malloc(sizeof *t);
  if (!t) return EXT_LEDGER_IO;
  t->fn = fn;
  t->arg = arg;
  if (!(t->h = CreateThread(NULL, 0, thread_main, t, 0, NULL))) {
    free(t);
    return EXT_LEDGER_IO;
  }
  *out = t;
  return 0;
}

void ll_thread_join(ll_thread *t) {
  if (!t) return;
  WaitForSingleObject(t->h, INFINITE);
  CloseHandle(t->h);
  free(t);
}

/* RtlGenRandom, exported by advapi32 as SystemFunction036. */
BOOLEAN WINAPI SystemFunction036(PVOID buffer, ULONG length);

int ll_random_hex(char *out, size_t bytes) {
  unsigned char buf[64];
  if (bytes > sizeof buf) return EXT_LEDGER_INVALID;
  if (!SystemFunction036(buf, (ULONG)bytes)) return EXT_LEDGER_IO;
  static const char hex[] = "0123456789abcdef";
  for (size_t i = 0; i < bytes; i++) {
    out[2 * i] = hex[buf[i] >> 4];
    out[2 * i + 1] = hex[buf[i] & 15];
  }
  out[2 * bytes] = 0;
  return 0;
}

static wchar_t *wide(const char *s) {
  int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
  if (n <= 0) return NULL;
  wchar_t *w = malloc((size_t)n * sizeof *w);
  if (w && MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n) <= 0) {
    free(w);
    return NULL;
  }
  return w;
}

static char *narrow(const wchar_t *w, int len) {
  int n = WideCharToMultiByte(CP_UTF8, 0, w, len, NULL, 0, NULL, NULL);
  char *s = malloc((size_t)n + 1);
  if (!s) return NULL;
  WideCharToMultiByte(CP_UTF8, 0, w, len, s, n, NULL, NULL);
  s[n] = 0;
  return s;
}

char **ll_environ_copy(size_t *n) {
  wchar_t *block = GetEnvironmentStringsW();
  if (!block) return NULL;
  size_t count = 0;
  for (wchar_t *p = block; *p; p += wcslen(p) + 1)
    if (*p != L'=') count++; /* "=C:=C:\..." entries are the shell's per-drive directories */
  char **out = calloc(count + 1, sizeof *out);
  size_t i = 0;
  for (wchar_t *p = block; out && *p; p += wcslen(p) + 1) {
    if (*p == L'=') continue;
    if (!(out[i++] = narrow(p, (int)wcslen(p)))) {
      for (size_t j = 0; j + 1 < i; j++) free(out[j]);
      free(out);
      out = NULL;
    }
  }
  FreeEnvironmentStringsW(block);
  if (out) *n = count;
  return out;
}

int ll_env_same(const char *a, size_t alen, const char *b, size_t blen) {
  if (alen != blen) return 0;
  for (size_t i = 0; i < alen; i++) {
    char x = a[i], y = b[i];
    if (x >= 'a' && x <= 'z') x = (char)(x - 32);
    if (y >= 'a' && y <= 'z') y = (char)(y - 32);
    if (x != y) return 0;
  }
  return 1;
}

static int file_exists(const char *path) {
  wchar_t *w = wide(path);
  if (!w) return 0;
  DWORD a = GetFileAttributesW(w);
  free(w);
  return a != INVALID_FILE_ATTRIBUTES && !(a & FILE_ATTRIBUTE_DIRECTORY);
}

static int has_extension(const char *name) {
  const char *slash = strrchr(name, '\\'), *fwd = strrchr(name, '/');
  if (fwd > slash) slash = fwd;
  const char *dot = strrchr(name, '.');
  return dot && (!slash || dot > slash);
}

/* name as is when it has an extension, else with each PATHEXT extension, as exec.LookPath tries them. */
static int find_file(const char *name, char **path) {
  if (has_extension(name) && file_exists(name)) return (*path = _strdup(name)) ? 0 : EXT_LEDGER_IO;
  char exts[1024];
  DWORD n = GetEnvironmentVariableA("PATHEXT", exts, sizeof exts);
  if (!n || n >= sizeof exts) strcpy(exts, ".com;.exe;.bat;.cmd");
  for (char *e = exts; e && *e;) {
    char *end = strchr(e, ';');
    if (end) *end = 0;
    if (*e) {
      size_t len = strlen(name) + strlen(e) + 1;
      char *candidate = malloc(len);
      if (!candidate) return EXT_LEDGER_IO;
      snprintf(candidate, len, "%s%s", name, e);
      if (file_exists(candidate)) {
        *path = candidate;
        return 0;
      }
      free(candidate);
    }
    e = end ? end + 1 : NULL;
  }
  return EXT_LEDGER_NOT_FOUND;
}

int ll_look_path(const char *command, char **path) {
  *path = NULL;
  if (!command || !*command) return EXT_LEDGER_NOT_FOUND;
  if (strchr(command, '\\') || strchr(command, '/') || strchr(command, ':')) return find_file(command, path);
  char *list = ll_os_getenv("PATH");
  if (!list) return EXT_LEDGER_NOT_FOUND;
  int rc = EXT_LEDGER_NOT_FOUND;
  for (char *p = list; p && rc == EXT_LEDGER_NOT_FOUND;) {
    char *end = strchr(p, ';');
    if (end) *end = 0;
    if (*p == '"') {
      p++;
      size_t l = strlen(p);
      if (l && p[l - 1] == '"') p[l - 1] = 0;
    }
    /* Like exec.LookPath, never a relative entry. */
    int abs = ((p[0] | 32) >= 'a' && (p[0] | 32) <= 'z' && p[1] == ':') || (p[0] == '\\' && p[1] == '\\');
    if (abs) {
      char *candidate = ll_path_join(p, command);
      if (!candidate) rc = EXT_LEDGER_IO;
      else {
        rc = find_file(candidate, path);
        free(candidate);
      }
    }
    p = end ? end + 1 : NULL;
  }
  free(list);
  return rc;
}

/* Appends one argument quoted as CommandLineToArgvW reads it back (syscall.EscapeArg). */
static void quote_arg(lj_buf *b, const char *s) {
  if (*s && !strpbrk(s, " \t\n\v\"")) {
    lj_buf_puts(b, s);
    return;
  }
  lj_buf_putc(b, '"');
  size_t slashes = 0;
  for (const char *p = s; *p; p++) {
    if (*p == '\\') {
      slashes++;
    } else if (*p == '"') {
      for (size_t i = 0; i < slashes + 1; i++) lj_buf_putc(b, '\\'); /* double the run, escape the quote */
      slashes = 0;
    } else {
      slashes = 0;
    }
    lj_buf_putc(b, *p);
  }
  for (size_t i = 0; i < slashes; i++) lj_buf_putc(b, '\\');
  lj_buf_putc(b, '"');
}

struct ll_proc {
  HANDLE process, job, out;
  DWORD pid;
  int reaped, code;
  ll_thread *reader;
  volatile int stop;
  ll_proc_sink sink;
  void *sink_arg;
};

static void drain(void *arg) {
  ll_proc *p = arg;
  char buf[4096];
  for (;;) {
    DWORD avail = 0;
    if (!PeekNamedPipe(p->out, NULL, 0, NULL, &avail, NULL)) break; /* every writer closed it */
    if (!avail) {
      if (ll_flag_load(&p->stop)) break;
      Sleep(10);
      continue;
    }
    DWORD n = 0;
    if (!ReadFile(p->out, buf, avail < sizeof buf ? avail : sizeof buf, &n, NULL) || !n) break;
    p->sink(p->sink_arg, buf, n);
  }
}

int ll_proc_spawn(const ll_proc_spec *spec, ll_proc **out, int64_t *pid) {
  *out = NULL;
  *pid = 0;
  lj_buf cmd = {0}, env = {0};
  for (size_t i = 0; spec->argv[i]; i++) {
    if (i) lj_buf_putc(&cmd, ' ');
    quote_arg(&cmd, spec->argv[i]);
  }
  lj_buf_putc(&cmd, 0);
  /* The block: UTF-16 strings, each NUL-terminated, then one more NUL. */
  for (size_t i = 0; spec->env[i]; i++) lj_buf_put(&env, spec->env[i], strlen(spec->env[i]) + 1);
  lj_buf_putc(&env, 0);
  if (cmd.oom || env.oom) {
    lj_buf_free(&cmd);
    lj_buf_free(&env);
    return EXT_LEDGER_IO;
  }
  int wn = MultiByteToWideChar(CP_UTF8, 0, env.ptr, (int)env.len, NULL, 0);
  wchar_t *wenv = wn > 0 ? malloc((size_t)(wn + 1) * sizeof *wenv) : NULL;
  if (wenv) {
    MultiByteToWideChar(CP_UTF8, 0, env.ptr, (int)env.len, wenv, wn);
    wenv[wn] = 0;
  }
  wchar_t *wcmd = wide(cmd.ptr), *wpath = wide(spec->path), *wdir = spec->dir && *spec->dir ? wide(spec->dir) : NULL;
  lj_buf_free(&cmd);
  lj_buf_free(&env);
  int rc = 0;
  HANDLE read_end = NULL, write_end = NULL, nul_in = INVALID_HANDLE_VALUE, job = NULL;
  SECURITY_ATTRIBUTES sa = {sizeof sa, NULL, TRUE};
  if (!wenv || !wcmd || !wpath || (spec->dir && *spec->dir && !wdir)) rc = EXT_LEDGER_IO;
  if (!rc && spec->sink) {
    if (!CreatePipe(&read_end, &write_end, &sa, 0)) rc = EXT_LEDGER_IO;
    else SetHandleInformation(read_end, HANDLE_FLAG_INHERIT, 0);
  } else if (!rc && (write_end = CreateFileW(L"NUL", GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, NULL)) == INVALID_HANDLE_VALUE) {
    write_end = NULL;
    rc = EXT_LEDGER_IO;
  }
  if (!rc && (nul_in = CreateFileW(L"NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, NULL)) == INVALID_HANDLE_VALUE) rc = EXT_LEDGER_IO;
  if (!rc) {
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits;
    memset(&limits, 0, sizeof limits);
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if (!(job = CreateJobObjectW(NULL, NULL)) || !SetInformationJobObject(job, JobObjectExtendedLimitInformation, &limits, sizeof limits)) rc = EXT_LEDGER_IO;
  }
  PROCESS_INFORMATION pi;
  memset(&pi, 0, sizeof pi);
  if (!rc) {
    /* Only the three standard handles are inherited. */
    HANDLE inherit[3] = {nul_in, write_end, write_end};
    SIZE_T size = 0;
    InitializeProcThreadAttributeList(NULL, 1, 0, &size);
    LPPROC_THREAD_ATTRIBUTE_LIST attrs = malloc(size);
    STARTUPINFOEXW si;
    memset(&si, 0, sizeof si);
    si.StartupInfo.cb = sizeof si;
    si.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
    si.StartupInfo.hStdInput = nul_in;
    si.StartupInfo.hStdOutput = write_end;
    si.StartupInfo.hStdError = write_end;
    if (!attrs || !InitializeProcThreadAttributeList(attrs, 1, 0, &size) ||
        !UpdateProcThreadAttribute(attrs, 0, PROC_THREAD_ATTRIBUTE_HANDLE_LIST, inherit, sizeof(HANDLE) * 2, NULL, NULL)) {
      rc = EXT_LEDGER_IO;
    } else {
      si.lpAttributeList = attrs;
      DWORD flags = CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT | CREATE_NEW_PROCESS_GROUP | EXTENDED_STARTUPINFO_PRESENT | CREATE_NO_WINDOW;
      if (!CreateProcessW(wpath, wcmd, NULL, NULL, TRUE, flags, wenv, wdir, &si.StartupInfo, &pi)) {
        DWORD err = GetLastError();
        rc = err == ERROR_FILE_NOT_FOUND || err == ERROR_PATH_NOT_FOUND ? EXT_LEDGER_NOT_FOUND : err == ERROR_ACCESS_DENIED ? EXT_LEDGER_DENIED : EXT_LEDGER_IO;
      } else if (!AssignProcessToJobObject(job, pi.hProcess)) {
        TerminateProcess(pi.hProcess, 1);
        rc = EXT_LEDGER_IO;
      } else {
        ResumeThread(pi.hThread);
      }
      DeleteProcThreadAttributeList(attrs);
    }
    free(attrs);
  }
  free(wenv);
  free(wcmd);
  free(wpath);
  free(wdir);
  if (write_end) CloseHandle(write_end);
  if (nul_in != INVALID_HANDLE_VALUE) CloseHandle(nul_in);
  if (pi.hThread) CloseHandle(pi.hThread);
  ll_proc *p = rc ? NULL : calloc(1, sizeof *p);
  if (!rc && !p) {
    TerminateJobObject(job, 1);
    rc = EXT_LEDGER_IO;
  }
  if (rc) {
    if (pi.hProcess) {
      WaitForSingleObject(pi.hProcess, 5000);
      CloseHandle(pi.hProcess);
    }
    if (job) CloseHandle(job);
    if (read_end) CloseHandle(read_end);
    return rc;
  }
  p->process = pi.hProcess;
  p->pid = pi.dwProcessId;
  p->job = job;
  p->out = read_end;
  p->sink = spec->sink;
  p->sink_arg = spec->sink_arg;
  if (p->out && ll_thread_start(&p->reader, drain, p)) {
    CloseHandle(p->out);
    p->out = NULL;
  }
  *out = p;
  *pid = p->pid;
  return 0;
}

int ll_proc_wait(ll_proc *p, int64_t timeout_ms, int *code) {
  if (!p->reaped) {
    if (WaitForSingleObject(p->process, timeout_ms < 0 ? INFINITE : (DWORD)timeout_ms) != WAIT_OBJECT_0) return EXT_LEDGER_TIMEOUT;
    DWORD c = 1;
    GetExitCodeProcess(p->process, &c);
    p->code = (int)c;
    p->reaped = 1;
  }
  *code = p->code;
  return 0;
}

void ll_proc_terminate(ll_proc *p, int force) {
  (void)force; /* a console process tree has no polite signal from outside its console */
  if (p->job) TerminateJobObject(p->job, 1);
}

void ll_proc_close(ll_proc *p) {
  if (!p) return;
  if (!p->reaped) {
    int code;
    ll_proc_terminate(p, 1);
    ll_proc_wait(p, 60000, &code);
  }
  ll_flag_store(&p->stop, 1);
  ll_thread_join(p->reader);
  if (p->out) CloseHandle(p->out);
  CloseHandle(p->job);
  CloseHandle(p->process);
  free(p);
}

int ll_proc_alive(int64_t pid) {
  if (pid <= 0 || pid > 0xffffffffLL) return 0;
  HANDLE h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, (DWORD)pid);
  if (!h) return GetLastError() == ERROR_ACCESS_DENIED;
  DWORD code = 0;
  int alive = GetExitCodeProcess(h, &code) && code == STILL_ACTIVE;
  CloseHandle(h);
  return alive;
}

int ll_proc_identity(int64_t pid, char out[65]) {
  out[0] = 0;
  char start[96], text[128];
  int rc = ll_process_start(pid, start, sizeof start);
  if (rc) return rc;
  int n = snprintf(text, sizeof text, "%lld:%s", (long long)pid, start);
  ll_sha256_hex(text, (size_t)n, out);
  return 0;
}

/* Ends pid and its descendants, as taskkill /T /F does: children first. */
static void kill_tree(DWORD root) {
  HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
  if (snap == INVALID_HANDLE_VALUE) return;
  DWORD *tree = malloc(sizeof *tree * 1024);
  size_t n = 0, cap = 1024;
  if (tree) tree[n++] = root;
  for (size_t i = 0; tree && i < n; i++) {
    PROCESSENTRY32W e;
    e.dwSize = sizeof e;
    for (BOOL ok = Process32FirstW(snap, &e); ok; ok = Process32NextW(snap, &e)) {
      if (e.th32ParentProcessID != tree[i] || e.th32ProcessID == tree[i]) continue;
      int seen = 0;
      for (size_t j = 0; j < n && !seen; j++) seen = tree[j] == e.th32ProcessID;
      if (seen) continue;
      if (n == cap) {
        DWORD *grown = realloc(tree, sizeof *tree * cap * 2);
        if (!grown) break;
        tree = grown;
        cap *= 2;
      }
      tree[n++] = e.th32ProcessID;
    }
  }
  CloseHandle(snap);
  for (size_t i = n; tree && i-- > 0;) {
    HANDLE h = OpenProcess(PROCESS_TERMINATE, FALSE, tree[i]);
    if (h) {
      TerminateProcess(h, 1);
      CloseHandle(h);
    }
  }
  free(tree);
}

int ll_proc_terminate_orphan(int64_t pid, const char *identity) {
  if (!ll_proc_alive(pid)) return 0;
  char current[65];
  if (ll_proc_identity(pid, current) || strcmp(current, identity) != 0) return EXT_LEDGER_CHANGED;
  kill_tree((DWORD)pid);
  return ll_proc_alive(pid) ? EXT_LEDGER_IO : 0;
}
#endif
