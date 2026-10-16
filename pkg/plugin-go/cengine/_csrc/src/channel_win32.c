/* Windows channel: the child runs under CreateProcess with three anonymous
 * pipes. Anonymous pipes cannot be polled, so one thread moves the child's
 * stdout into a bounded inbound queue, another drains a bounded outbound queue
 * into its stdin, and wait/read/write operate on the queues. The child joins a
 * Job Object set to kill on close, so killing the channel ends the whole
 * process tree and releases every pipe end. Only the three standard handles are
 * inherited. Built with MinGW-w64 (winpthreads). */
#define WIN32_LEAN_AND_MEAN
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#endif
#include "channel.h"
#include <errno.h>
#include "sync.h"
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <windows.h>

#define QUEUE_HIGH (256u * 1024u)
#define CHUNK (64u * 1024u)

struct ext_channel {
  HANDLE process, job;
  HANDLE in_write, out_read, err_read;
  pthread_mutex_t mu; /* guards everything below */
  pthread_cond_t cv;
  uint8_t *in;
  size_t in_len, in_off, in_cap;
  int in_eof;
  uint8_t *out;
  size_t out_len, out_cap;
  int out_error, closing;
  size_t stderr_limit, stderr_len;
  uint8_t *stderr_data;
  pthread_t reader, writer, stderr_thread;
  int reader_started, writer_started, stderr_started;
};

int ext_path_is_absolute(const char *path) {
  if (!path)
    return 0;
  if ((path[0] == '\\' && path[1] == '\\') || (path[0] == '/' && path[1] == '/'))
    return 1;
  return ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) &&
         path[1] == ':' && (path[2] == '\\' || path[2] == '/');
}

static void deadline_after(int timeout_ms, struct timespec *t) {
  clock_gettime(CLOCK_REALTIME, t);
  t->tv_sec += timeout_ms / 1000;
  t->tv_nsec += (long)(timeout_ms % 1000) * 1000000L;
  if (t->tv_nsec >= 1000000000L) {
    t->tv_sec++;
    t->tv_nsec -= 1000000000L;
  }
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

/* Appends one argument quoted so CommandLineToArgvW and the C runtime parse it
 * back unchanged: wrap in quotes when it has spaces, tabs or quotes, double the
 * backslashes that precede a quote, and escape the quotes. */
static int append_argument(wchar_t **buffer, size_t *length, size_t *capacity,
                           const wchar_t *arg) {
  size_t need = *length + wcslen(arg) * 2 + 4;
  if (need > *capacity) {
    size_t next = need * 2;
    wchar_t *grown = realloc(*buffer, next * sizeof(**buffer));
    if (!grown)
      return 0;
    *buffer = grown;
    *capacity = next;
  }
  wchar_t *out = *buffer + *length;
  if (*length)
    *out++ = L' ';
  int quote = !*arg || wcspbrk(arg, L" \t\n\v\"");
  if (quote)
    *out++ = L'"';
  for (const wchar_t *p = arg;; p++) {
    size_t backslashes = 0;
    while (*p == L'\\') {
      p++;
      backslashes++;
    }
    if (!*p) {
      for (size_t i = 0; i < backslashes * (quote ? 2 : 1); i++)
        *out++ = L'\\';
      break;
    }
    if (*p == L'"') {
      for (size_t i = 0; i < backslashes * 2 + 1; i++)
        *out++ = L'\\';
      *out++ = L'"';
    } else {
      for (size_t i = 0; i < backslashes; i++)
        *out++ = L'\\';
      *out++ = *p;
    }
  }
  if (quote)
    *out++ = L'"';
  *out = 0;
  *length = (size_t)(out - *buffer);
  return 1;
}

static int compare_env(const void *a, const void *b) {
  return _wcsicmp(*(wchar_t *const *)a, *(wchar_t *const *)b);
}

/* A Unicode environment block: NAME=value entries sorted case-insensitively,
 * each NUL-terminated, ending with an extra NUL. */
static wchar_t *environment_block(char *const *environment) {
  size_t count = 0, total = 1;
  while (environment && environment[count])
    count++;
  wchar_t **entries = calloc(count + 1, sizeof(*entries));
  if (!entries)
    return NULL;
  for (size_t i = 0; i < count; i++) {
    if (!(entries[i] = widen(environment[i]))) {
      for (size_t j = 0; j < i; j++)
        free(entries[j]);
      free(entries);
      return NULL;
    }
    total += wcslen(entries[i]) + 1;
  }
  qsort(entries, count, sizeof(*entries), compare_env);
  wchar_t *block = calloc(total + 1, sizeof(*block));
  if (block) {
    wchar_t *at = block;
    for (size_t i = 0; i < count; i++) {
      size_t n = wcslen(entries[i]) + 1;
      memcpy(at, entries[i], n * sizeof(*at));
      at += n;
    }
    *at = 0;
  }
  for (size_t i = 0; i < count; i++)
    free(entries[i]);
  free(entries);
  return block;
}

static void *reader_main(void *arg) {
  ext_channel *c = arg;
  uint8_t chunk[CHUNK];
  for (;;) {
    DWORD n = 0;
    if (!ReadFile(c->out_read, chunk, sizeof(chunk), &n, NULL) || !n)
      break;
    pthread_mutex_lock(&c->mu);
    while (c->in_len - c->in_off >= QUEUE_HIGH && !c->closing)
      pthread_cond_wait(&c->cv, &c->mu);
    if (c->closing) {
      pthread_mutex_unlock(&c->mu);
      break;
    }
    if (c->in_off == c->in_len)
      c->in_off = c->in_len = 0;
    if (c->in_len + n > c->in_cap) {
      size_t next = c->in_cap ? c->in_cap : 4096;
      while (next < c->in_len + n)
        next *= 2;
      uint8_t *grown = realloc(c->in, next);
      if (!grown) {
        c->in_eof = 1;
        pthread_cond_broadcast(&c->cv);
        pthread_mutex_unlock(&c->mu);
        return NULL;
      }
      c->in = grown;
      c->in_cap = next;
    }
    memcpy(c->in + c->in_len, chunk, n);
    c->in_len += n;
    pthread_cond_broadcast(&c->cv);
    pthread_mutex_unlock(&c->mu);
  }
  pthread_mutex_lock(&c->mu);
  c->in_eof = 1;
  pthread_cond_broadcast(&c->cv);
  pthread_mutex_unlock(&c->mu);
  return NULL;
}

static void *writer_main(void *arg) {
  ext_channel *c = arg;
  uint8_t chunk[CHUNK];
  for (;;) {
    pthread_mutex_lock(&c->mu);
    while (!c->out_len && !c->closing)
      pthread_cond_wait(&c->cv, &c->mu);
    if (c->closing) {
      pthread_mutex_unlock(&c->mu);
      return NULL;
    }
    size_t n = c->out_len < sizeof(chunk) ? c->out_len : sizeof(chunk);
    memcpy(chunk, c->out, n);
    pthread_mutex_unlock(&c->mu);
    size_t done = 0;
    int failed = 0;
    while (done < n && !failed) {
      DWORD written = 0;
      if (!WriteFile(c->in_write, chunk + done, (DWORD)(n - done), &written,
                     NULL) ||
          !written)
        failed = 1;
      else
        done += written;
    }
    pthread_mutex_lock(&c->mu);
    if (failed) {
      c->out_error = 1;
    } else {
      memmove(c->out, c->out + n, c->out_len - n);
      c->out_len -= n;
    }
    pthread_cond_broadcast(&c->cv);
    pthread_mutex_unlock(&c->mu);
    if (failed)
      return NULL;
  }
}

/* Retains the first stderr_limit bytes and discards the rest. */
static void *stderr_main(void *arg) {
  ext_channel *c = arg;
  uint8_t chunk[1024];
  for (;;) {
    DWORD n = 0;
    if (!ReadFile(c->err_read, chunk, sizeof(chunk), &n, NULL) || !n)
      break;
    pthread_mutex_lock(&c->mu);
    size_t room = c->stderr_limit - c->stderr_len;
    size_t keep = n < room ? n : room;
    memcpy(c->stderr_data + c->stderr_len, chunk, keep);
    c->stderr_len += keep;
    pthread_mutex_unlock(&c->mu);
  }
  return NULL;
}

/* Creates a pipe whose child end is inheritable and whose parent end is not. */
static int make_pipe(HANDLE *parent, HANDLE *child, int parent_reads) {
  SECURITY_ATTRIBUTES sa = {sizeof(sa), NULL, TRUE};
  HANDLE read_end, write_end;
  if (!CreatePipe(&read_end, &write_end, &sa, 0))
    return 0;
  *parent = parent_reads ? read_end : write_end;
  *child = parent_reads ? write_end : read_end;
  if (!SetHandleInformation(*parent, HANDLE_FLAG_INHERIT, 0)) {
    CloseHandle(read_end);
    CloseHandle(write_end);
    return 0;
  }
  return 1;
}

static void close_handle(HANDLE *h) {
  if (*h && *h != INVALID_HANDLE_VALUE)
    CloseHandle(*h);
  *h = NULL;
}

ext_status ext_channel_open(const ext_launch *launch, ext_channel **out) {
  wchar_t *path = widen(launch->path), *directory = NULL, *command = NULL,
          *block = NULL;
  size_t command_length = 0, command_capacity = 0;
  ext_status status = EXT_IO;
  ext_channel *c = NULL;
  HANDLE in_child = NULL, out_child = NULL, err_child = NULL;
  HANDLE inherit[3];
  LPPROC_THREAD_ATTRIBUTE_LIST attributes = NULL;
  PROCESS_INFORMATION pi = {0};
  if (!path) {
    status = EXT_INVALID;
    goto done;
  }
  DWORD attribute = GetFileAttributesW(path);
  if (attribute == INVALID_FILE_ATTRIBUTES) {
    goto done;
  }
  if (attribute & FILE_ATTRIBUTE_DIRECTORY) {
    status = EXT_INVALID;
    goto done;
  }
  /* The launch path must name the exact executable, extension included. Only
   * .exe and .com start directly; batch files go through cmd.exe and are not
   * supported. */
  const wchar_t *dot = wcsrchr(path, L'.');
  if (!dot || (_wcsicmp(dot, L".exe") && _wcsicmp(dot, L".com"))) {
    status = EXT_INVALID;
    goto done;
  }
  if (launch->directory && !(directory = widen(launch->directory))) {
    status = EXT_INVALID;
    goto done;
  }
  for (char *const *a = launch->arguments; a && *a; a++) {
    wchar_t *w = widen(*a);
    if (!w) {
      status = EXT_INVALID;
      goto done;
    }
    int ok = append_argument(&command, &command_length, &command_capacity, w);
    free(w);
    if (!ok) {
      status = EXT_NOMEM;
      goto done;
    }
  }
  if (!command) {
    status = EXT_INVALID;
    goto done;
  }
  if (!(block = environment_block(launch->environment))) {
    status = EXT_NOMEM;
    goto done;
  }
  c = calloc(1, sizeof(*c));
  if (!c) {
    status = EXT_NOMEM;
    goto done;
  }
  if (pthread_mutex_init(&c->mu, NULL) || pthread_cond_init(&c->cv, NULL)) {
    free(c);
    c = NULL;
    goto done;
  }
  if (launch->capture_stderr) {
    c->stderr_limit = launch->stderr_limit;
    c->stderr_data = malloc(c->stderr_limit ? c->stderr_limit : 1);
    if (!c->stderr_data) {
      status = EXT_NOMEM;
      goto fail;
    }
  }
  if (!make_pipe(&c->in_write, &in_child, 0) ||
      !make_pipe(&c->out_read, &out_child, 1))
    goto fail;
  if (launch->capture_stderr) {
    if (!make_pipe(&c->err_read, &err_child, 1))
      goto fail;
  } else {
    /* Like the POSIX channel, inherit the host's stderr. */
    HANDLE own = GetStdHandle(STD_ERROR_HANDLE);
    if (own && own != INVALID_HANDLE_VALUE &&
        DuplicateHandle(GetCurrentProcess(), own, GetCurrentProcess(),
                        &err_child, 0, TRUE, DUPLICATE_SAME_ACCESS)) {
    } else {
      SECURITY_ATTRIBUTES sa = {sizeof(sa), NULL, TRUE};
      err_child = CreateFileW(L"NUL", GENERIC_WRITE, FILE_SHARE_WRITE, &sa,
                              OPEN_EXISTING, 0, NULL);
      if (err_child == INVALID_HANDLE_VALUE) {
        err_child = NULL;
        goto fail;
      }
    }
  }
  c->job = CreateJobObjectW(NULL, NULL);
  if (!c->job)
    goto fail;
  JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits = {0};
  limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
  if (!SetInformationJobObject(c->job, JobObjectExtendedLimitInformation,
                               &limits, sizeof(limits)))
    goto fail;
  inherit[0] = in_child;
  inherit[1] = out_child;
  inherit[2] = err_child;
  SIZE_T attribute_size = 0;
  InitializeProcThreadAttributeList(NULL, 1, 0, &attribute_size);
  attributes = malloc(attribute_size);
  if (!attributes ||
      !InitializeProcThreadAttributeList(attributes, 1, 0, &attribute_size) ||
      !UpdateProcThreadAttribute(attributes, 0,
                                 PROC_THREAD_ATTRIBUTE_HANDLE_LIST, inherit,
                                 sizeof(inherit), NULL, NULL))
    goto fail;
  STARTUPINFOEXW startup = {0};
  startup.StartupInfo.cb = sizeof(startup);
  startup.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
  startup.StartupInfo.hStdInput = in_child;
  startup.StartupInfo.hStdOutput = out_child;
  startup.StartupInfo.hStdError = err_child;
  startup.lpAttributeList = attributes;
  /* Created suspended so it joins the job before running a single instruction. */
  if (!CreateProcessW(path, command, NULL, NULL, TRUE,
                      CREATE_SUSPENDED | CREATE_NO_WINDOW |
                          CREATE_UNICODE_ENVIRONMENT |
                          EXTENDED_STARTUPINFO_PRESENT,
                      block, directory, &startup.StartupInfo, &pi))
    goto fail;
  c->process = pi.hProcess;
  if (!AssignProcessToJobObject(c->job, pi.hProcess)) {
    TerminateProcess(pi.hProcess, 1);
    CloseHandle(pi.hThread);
    goto fail;
  }
  ResumeThread(pi.hThread);
  CloseHandle(pi.hThread);
  close_handle(&in_child);
  close_handle(&out_child);
  close_handle(&err_child);
  if (pthread_create(&c->reader, NULL, reader_main, c))
    goto fail;
  c->reader_started = 1;
  if (pthread_create(&c->writer, NULL, writer_main, c))
    goto fail;
  c->writer_started = 1;
  if (launch->capture_stderr) {
    if (pthread_create(&c->stderr_thread, NULL, stderr_main, c))
      goto fail;
    c->stderr_started = 1;
  }
  *out = c;
  status = EXT_OK;
  goto done;
fail:
  if (c) {
    close_handle(&in_child);
    close_handle(&out_child);
    close_handle(&err_child);
    ext_channel_destroy(c);
    c = NULL;
  }
done:
  if (attributes) {
    DeleteProcThreadAttributeList(attributes);
    free(attributes);
  }
  if (status != EXT_OK && c == NULL) {
    close_handle(&in_child);
    close_handle(&out_child);
    close_handle(&err_child);
  }
  free(path);
  free(directory);
  free(command);
  free(block);
  return status;
}

ext_status ext_channel_wait(ext_channel *c, int write, int timeout_ms) {
  struct timespec until;
  deadline_after(timeout_ms < 0 ? 0 : timeout_ms, &until);
  ext_status result = EXT_OK;
  pthread_mutex_lock(&c->mu);
  for (;;) {
    int ready = write ? (c->out_len < QUEUE_HIGH || c->out_error || c->closing)
                      : (c->in_off < c->in_len || c->in_eof || c->closing);
    if (ready)
      break;
    if (pthread_cond_timedwait(&c->cv, &c->mu, &until)) {
      result = EXT_TIMEOUT;
      break;
    }
  }
  pthread_mutex_unlock(&c->mu);
  return result;
}

ptrdiff_t ext_channel_read(ext_channel *c, uint8_t *buffer, size_t length) {
  pthread_mutex_lock(&c->mu);
  ptrdiff_t result;
  if (c->in_off == c->in_len) {
    result = (c->in_eof || c->closing) ? -1 : 0;
  } else {
    size_t available = c->in_len - c->in_off;
    size_t n = available < length ? available : length;
    memcpy(buffer, c->in + c->in_off, n);
    c->in_off += n;
    result = (ptrdiff_t)n;
    pthread_cond_broadcast(&c->cv);
  }
  pthread_mutex_unlock(&c->mu);
  return result;
}

ptrdiff_t ext_channel_write(ext_channel *c, const uint8_t *buffer,
                            size_t length) {
  pthread_mutex_lock(&c->mu);
  ptrdiff_t result;
  if (c->closing || c->out_error) {
    result = -1;
  } else if (c->out_len >= QUEUE_HIGH) {
    result = 0;
  } else {
    size_t room = QUEUE_HIGH - c->out_len;
    size_t n = length < room ? length : room;
    if (c->out_len + n > c->out_cap) {
      size_t next = c->out_cap ? c->out_cap : 4096;
      while (next < c->out_len + n)
        next *= 2;
      uint8_t *grown = realloc(c->out, next);
      if (!grown) {
        pthread_mutex_unlock(&c->mu);
        return -1;
      }
      c->out = grown;
      c->out_cap = next;
    }
    memcpy(c->out + c->out_len, buffer, n);
    c->out_len += n;
    result = (ptrdiff_t)n;
    pthread_cond_broadcast(&c->cv);
  }
  pthread_mutex_unlock(&c->mu);
  return result;
}

void ext_channel_kill(ext_channel *c) {
  if (c->job)
    TerminateJobObject(c->job, 1);
  pthread_mutex_lock(&c->mu);
  c->closing = 1;
  pthread_cond_broadcast(&c->cv);
  pthread_mutex_unlock(&c->mu);
}

void ext_channel_destroy(ext_channel *c) {
  if (!c)
    return;
  ext_channel_kill(c);
  if (c->process)
    WaitForSingleObject(c->process, INFINITE);
  /* The child and its tree are gone, so every pipe end the threads block on
   * is broken. Closing our own ends releases the rest. */
  if (c->reader_started)
    pthread_join(c->reader, NULL);
  if (c->writer_started)
    pthread_join(c->writer, NULL);
  if (c->stderr_started)
    pthread_join(c->stderr_thread, NULL);
  close_handle(&c->in_write);
  close_handle(&c->out_read);
  close_handle(&c->err_read);
  close_handle(&c->process);
  close_handle(&c->job);
  free(c->in);
  free(c->out);
  free(c->stderr_data);
  pthread_cond_destroy(&c->cv);
  pthread_mutex_destroy(&c->mu);
  free(c);
}

size_t ext_channel_stderr(ext_channel *c, uint8_t *buffer, size_t capacity) {
  pthread_mutex_lock(&c->mu);
  size_t n = c->stderr_len;
  if (n && buffer)
    memcpy(buffer, c->stderr_data, n < capacity ? n : capacity);
  pthread_mutex_unlock(&c->mu);
  return n;
}
