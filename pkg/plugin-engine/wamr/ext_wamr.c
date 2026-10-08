#define _POSIX_C_SOURCE 200809L
#include "ext_wamr.h"
#include "wasm_export.h"
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

/* Constants restated from pkg/plugin-cshared/ext_plugin.h and the reactor ABI. */
#define ABI_VERSION 1u
#define OP_HANDSHAKE 1u
#define OP_INVOKE 2u
#define STATUS_OK 0u
#define STATUS_INVALID 1u
#define STATUS_CLOSED 2u
#define MAX_FRAME (24u * 1024u * 1024u)
#define MAX_MODULE (64u * 1024u * 1024u)
#define DEFAULT_PAGES 4096u
#define DEFAULT_STACK (1024u * 1024u)
#define DEFAULT_DIAGNOSTIC 65536u
#define WASI_SUCCESS 0u
#define WASI_BADF 8u
#define WASI_INVAL 28u
#define WASI_NOSYS 52u
#define SLICE_MS 5u

enum { NEW, CONNECTED, DEAD };

typedef struct backend {
  uint8_t *bytes;
  size_t len;
  ext_wamr_options opt;

  pthread_mutex_t mu;
  pthread_cond_t wake; /* wakes the watchdog */
  int state;           /* guarded by mu */
  int busy;            /* a call into the guest is running; guarded by mu */
  int tripped;         /* the watchdog or close stopped the guest; guarded by mu */
  int watchdog_up, watchdog_quit;
  pthread_t watchdog;

  /* The call being timed, guarded by mu. */
  int armed;
  uint64_t deadline_ns;
  const ext_cancel *cancel;
  int cancelled; /* the watchdog stopped it because of cancel, not time */

  wasm_module_t module;
  wasm_module_inst_t inst;
  wasm_exec_env_t env;
  wasm_function_inst_t f_call, f_alloc, f_free, f_close;
  uint64_t handle;
  uint32_t resp_ptr, len_ptr;
  atomic_int stop; /* read by guest natives that sleep */
  uint32_t diag_left;
} backend;

static uint64_t now_ns(clockid_t clock) {
  struct timespec t;
  clock_gettime(clock, &t);
  return (uint64_t)t.tv_sec * 1000000000ull + (uint64_t)t.tv_nsec;
}

/* ---- the runtime, started once per process ------------------------------ */

static pthread_once_t runtime_once = PTHREAD_ONCE_INIT;
static int runtime_ready;

static uint32_t range(wasm_module_inst_t inst, uint32_t off, uint32_t len,
                      void **out) {
  if (len && !wasm_runtime_validate_app_addr(inst, off, len)) return 0;
  if (!len && !wasm_runtime_validate_app_addr(inst, off, 1)) return 0;
  *out = wasm_runtime_addr_app_to_native(inst, off);
  return *out != NULL;
}

static backend *owner(wasm_exec_env_t env) {
  return wasm_runtime_get_custom_data(wasm_runtime_get_module_inst(env));
}

/* Exactly the imports the ABI lists. Anything else fails to link. */

static uint32_t wasi_environ_sizes_get(wasm_exec_env_t env, uint32_t count,
                                       uint32_t size) {
  wasm_module_inst_t inst = wasm_runtime_get_module_inst(env);
  uint32_t *c, *s;
  if (!range(inst, count, 4, (void **)&c) || !range(inst, size, 4, (void **)&s))
    return WASI_INVAL;
  memset(c, 0, 4);
  memset(s, 0, 4);
  return WASI_SUCCESS;
}

static uint32_t wasi_environ_get(wasm_exec_env_t env, uint32_t a, uint32_t b) {
  (void)env;
  (void)a;
  (void)b;
  return WASI_SUCCESS; /* the environment is empty */
}

static uint32_t wasi_clock_time_get(wasm_exec_env_t env, uint32_t id,
                                    uint64_t precision, uint32_t out) {
  (void)precision;
  wasm_module_inst_t inst = wasm_runtime_get_module_inst(env);
  uint64_t *p;
  if (id > 1) return WASI_INVAL;
  if (!range(inst, out, 8, (void **)&p)) return WASI_INVAL;
  uint64_t t = now_ns(id == 0 ? CLOCK_REALTIME : CLOCK_MONOTONIC);
  memcpy(p, &t, 8);
  return WASI_SUCCESS;
}

static uint32_t wasi_random_get(wasm_exec_env_t env, uint32_t buf, uint32_t len) {
  wasm_module_inst_t inst = wasm_runtime_get_module_inst(env);
  uint8_t *p;
  if (!len) return WASI_SUCCESS;
  if (!range(inst, buf, len, (void **)&p)) return WASI_INVAL;
  int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
  if (fd < 0) return WASI_NOSYS;
  size_t done = 0;
  while (done < len) {
    ssize_t n = read(fd, p + done, len - done);
    if (n <= 0) {
      if (n < 0 && errno == EINTR) continue;
      close(fd);
      return WASI_NOSYS;
    }
    done += (size_t)n;
  }
  close(fd);
  return WASI_SUCCESS;
}

static uint32_t wasi_fd_write(wasm_exec_env_t env, uint32_t fd, uint32_t iovs,
                              uint32_t count, uint32_t written) {
  backend *b = owner(env);
  wasm_module_inst_t inst = wasm_runtime_get_module_inst(env);
  uint32_t *out;
  if (fd != 1 && fd != 2) return WASI_BADF;
  if (count > 1024 || !range(inst, written, 4, (void **)&out)) return WASI_INVAL;
  uint64_t total = 0;
  for (uint32_t i = 0; i < count; i++) {
    uint32_t *iov;
    uint8_t *data;
    if (!range(inst, iovs + i * 8u, 8, (void **)&iov)) return WASI_INVAL;
    uint32_t ptr, n;
    memcpy(&ptr, iov, 4);
    memcpy(&n, iov + 1, 4);
    if (n && !range(inst, ptr, n, (void **)&data)) return WASI_INVAL;
    total += n;
    if (n && b->opt.diagnostic && b->diag_left) {
      uint32_t keep = n < b->diag_left ? n : b->diag_left;
      b->diag_left -= keep;
      b->opt.diagnostic(b->opt.diagnostic_user, data, keep);
    }
  }
  uint32_t t = total > 0xffffffffu ? 0xffffffffu : (uint32_t)total;
  memcpy(out, &t, 4);
  return WASI_SUCCESS;
}

/* Clock subscriptions only: sleep until the earliest, in slices so a deadline
 * or close interrupts it. */
static uint32_t wasi_poll_oneoff(wasm_exec_env_t env, uint32_t in, uint32_t out,
                                 uint32_t n, uint32_t nevents) {
  backend *b = owner(env);
  wasm_module_inst_t inst = wasm_runtime_get_module_inst(env);
  uint32_t *ne;
  if (n == 0 || n > 64 || !range(inst, nevents, 4, (void **)&ne)) return WASI_INVAL;
  uint8_t *subs, *events;
  if (!range(inst, in, n * 48u, (void **)&subs) ||
      !range(inst, out, n * 32u, (void **)&events))
    return WASI_INVAL;
  uint64_t wake[64];
  uint64_t earliest = UINT64_MAX;
  for (uint32_t i = 0; i < n; i++) {
    const uint8_t *s = subs + i * 48u;
    if (s[8] != 0) return WASI_NOSYS; /* not a clock subscription */
    uint32_t id;
    uint64_t timeout;
    uint16_t flags;
    memcpy(&id, s + 16, 4);
    memcpy(&timeout, s + 24, 8);
    memcpy(&flags, s + 40, 2);
    if (id > 1) return WASI_INVAL;
    clockid_t clock = id == 0 ? CLOCK_REALTIME : CLOCK_MONOTONIC;
    uint64_t at = (flags & 1) ? timeout : now_ns(clock) + timeout;
    /* Normalize to the monotonic clock for waiting. */
    uint64_t left = at > now_ns(clock) ? at - now_ns(clock) : 0;
    wake[i] = now_ns(CLOCK_MONOTONIC) + left;
    if (wake[i] < earliest) earliest = wake[i];
  }
  while (now_ns(CLOCK_MONOTONIC) < earliest) {
    if (atomic_load(&b->stop)) return WASI_SUCCESS; /* the call is being stopped */
    uint64_t left = earliest - now_ns(CLOCK_MONOTONIC);
    uint64_t slice = SLICE_MS * 1000000ull;
    struct timespec t = {0, (long)(left < slice ? left : slice)};
    nanosleep(&t, NULL);
  }
  uint32_t fired = 0;
  uint64_t now = now_ns(CLOCK_MONOTONIC);
  for (uint32_t i = 0; i < n; i++) {
    if (wake[i] > now) continue;
    uint8_t *e = events + fired * 32u;
    memset(e, 0, 32);
    memcpy(e, subs + i * 48u, 8);
    e[10] = 0; /* clock */
    fired++;
  }
  memcpy(ne, &fired, 4);
  return WASI_SUCCESS;
}

static void wasi_proc_exit(wasm_exec_env_t env, uint32_t code) {
  (void)code;
  wasm_runtime_set_exception(wasm_runtime_get_module_inst(env), "proc_exit");
}

static NativeSymbol wasi_symbols[] = {
    {"environ_sizes_get", wasi_environ_sizes_get, "(ii)i", NULL},
    {"environ_get", wasi_environ_get, "(ii)i", NULL},
    {"clock_time_get", wasi_clock_time_get, "(iIi)i", NULL},
    {"random_get", wasi_random_get, "(ii)i", NULL},
    {"fd_write", wasi_fd_write, "(iiii)i", NULL},
    {"poll_oneoff", wasi_poll_oneoff, "(iiii)i", NULL},
    {"proc_exit", wasi_proc_exit, "(i)", NULL},
};

static void start_runtime(void) {
  if (!wasm_runtime_init()) return;
  if (!wasm_runtime_register_natives("wasi_snapshot_preview1", wasi_symbols,
                                     sizeof(wasi_symbols) / sizeof(*wasi_symbols))) {
    wasm_runtime_destroy();
    return;
  }
  runtime_ready = 1;
}

/* ---- timing: a watchdog that sleeps unless a call is being timed --------- */

static void arm(backend *b, uint32_t timeout_ms, const ext_cancel *cancel) {
  pthread_mutex_lock(&b->mu);
  b->armed = 1;
  b->cancelled = 0;
  b->cancel = cancel;
  b->deadline_ns = now_ns(CLOCK_MONOTONIC) + (uint64_t)timeout_ms * 1000000ull;
  pthread_cond_signal(&b->wake);
  pthread_mutex_unlock(&b->mu);
}

static void disarm(backend *b) {
  pthread_mutex_lock(&b->mu);
  b->armed = 0;
  b->cancel = NULL;
  pthread_mutex_unlock(&b->mu);
}

/* Stops the running guest. Callers hold mu and know inst is still alive. */
static void stop_guest_locked(backend *b) {
  b->tripped = 1;
  atomic_store(&b->stop, 1);
  if (b->inst) wasm_runtime_terminate(b->inst);
}

static void *watch(void *arg) {
  backend *b = arg;
  pthread_mutex_lock(&b->mu);
  while (!b->watchdog_quit) {
    if (!b->armed) {
      pthread_cond_wait(&b->wake, &b->mu);
      continue;
    }
    uint64_t now = now_ns(CLOCK_MONOTONIC);
    if (b->cancel && ext_cancel_is_signaled(b->cancel)) {
      b->cancelled = 1;
      b->armed = 0;
      stop_guest_locked(b);
      continue;
    }
    if (now >= b->deadline_ns) {
      b->armed = 0;
      stop_guest_locked(b);
      continue;
    }
    /* Wake for the deadline, or soon to poll a cancel flag. */
    uint64_t wait = b->deadline_ns - now;
    if (b->cancel && wait > SLICE_MS * 1000000ull) wait = SLICE_MS * 1000000ull;
    struct timespec t;
    clock_gettime(CLOCK_REALTIME, &t);
    uint64_t abs = (uint64_t)t.tv_sec * 1000000000ull + (uint64_t)t.tv_nsec + wait;
    struct timespec until = {(time_t)(abs / 1000000000ull), (long)(abs % 1000000000ull)};
    pthread_cond_timedwait(&b->wake, &b->mu, &until);
  }
  pthread_mutex_unlock(&b->mu);
  return NULL;
}

/* ---- calling the guest --------------------------------------------------- */

static ext_status classify_failure(backend *b) {
  /* A guest that did not return normally ends the instance. */
  pthread_mutex_lock(&b->mu);
  int cancelled = b->cancelled, tripped = b->tripped;
  b->state = DEAD;
  pthread_mutex_unlock(&b->mu);
  if (cancelled) return EXT_CANCELED;
  if (tripped) return EXT_TIMEOUT;
  const char *why = b->inst ? wasm_runtime_get_exception(b->inst) : NULL;
  if (why && strstr(why, "instruction limit")) return EXT_CAPACITY;
  return EXT_CLOSED;
}

static ext_status call(backend *b, wasm_function_inst_t fn, uint32_t cells,
                       uint32_t *argv) {
  if (b->opt.instruction_limit) {
    uint64_t limit = b->opt.instruction_limit;
    wasm_runtime_set_instruction_count_limit(b->env, limit > 0x7fffffffu ? 0x7fffffff : (int)limit);
  }
  if (!wasm_runtime_call_wasm(b->env, fn, cells, argv)) return classify_failure(b);
  return EXT_OK;
}

static ext_status guest_alloc(backend *b, uint32_t size, uint32_t *ptr) {
  uint32_t argv[1] = {size};
  ext_status s = call(b, b->f_alloc, 1, argv);
  if (s != EXT_OK) return s;
  if (argv[0] == 0) return EXT_NOMEM;
  *ptr = argv[0];
  return EXT_OK;
}

/* One ext_plugin_call. On EXT_OK the response is at resp_ptr with *n bytes. */
static ext_status round_trip(backend *b, uint32_t op, const uint8_t *req, size_t len,
                             uint32_t *n) {
  if (!len || len > MAX_FRAME) return EXT_INVALID;
  uint32_t ptr = 0;
  ext_status s = guest_alloc(b, (uint32_t)len, &ptr);
  if (s != EXT_OK) return s;
  void *native;
  if (!range(b->inst, ptr, (uint32_t)len, &native)) {
    pthread_mutex_lock(&b->mu);
    b->state = DEAD;
    pthread_mutex_unlock(&b->mu);
    return EXT_INVALID;
  }
  memcpy(native, req, len);
  uint32_t argv[8] = {(uint32_t)(b->handle & 0xffffffffu), (uint32_t)(b->handle >> 32),
                      op, ptr, (uint32_t)len, b->resp_ptr, MAX_FRAME, b->len_ptr};
  s = call(b, b->f_call, 8, argv);
  if (s != EXT_OK) return s;
  uint32_t status = argv[0];
  uint32_t release[2] = {ptr, (uint32_t)len};
  s = call(b, b->f_free, 2, release);
  if (s != EXT_OK) return s;
  switch (status) {
  case STATUS_OK: {
    uint32_t *cell;
    if (!range(b->inst, b->len_ptr, 4, (void **)&cell)) goto invalid;
    memcpy(n, cell, 4);
    if (*n > MAX_FRAME) goto invalid;
    return EXT_OK;
  }
  case STATUS_INVALID:
    return EXT_INVALID;
  case STATUS_CLOSED:
  default:
    pthread_mutex_lock(&b->mu);
    b->state = DEAD;
    pthread_mutex_unlock(&b->mu);
    return EXT_CLOSED;
  }
invalid:
  pthread_mutex_lock(&b->mu);
  b->state = DEAD;
  pthread_mutex_unlock(&b->mu);
  return EXT_INVALID;
}

static ext_status emit_response(backend *b, uint32_t n, ext_emit emit, void *context) {
  uint8_t *data;
  if (!range(b->inst, b->resp_ptr, n, (void **)&data)) return EXT_INVALID;
  return emit(context, data, n); /* emit copies before we touch the guest again */
}

/* ---- the extension interface -------------------------------------------- */

static void format_deadline(uint32_t timeout_ms, char out[40]) {
  struct timespec t;
  clock_gettime(CLOCK_REALTIME, &t);
  uint64_t ms = (uint64_t)t.tv_sec * 1000 + (uint64_t)t.tv_nsec / 1000000 + timeout_ms;
  time_t secs = (time_t)(ms / 1000);
  struct tm tm;
  gmtime_r(&secs, &tm);
  snprintf(out, 40, "%04d-%02d-%02dT%02d:%02d:%02d.%03uZ", tm.tm_year + 1900,
           tm.tm_mon + 1, tm.tm_mday, tm.tm_hour, tm.tm_min, tm.tm_sec,
           (unsigned)(ms % 1000));
}

static void teardown(backend *b) {
  /* The caller has exclusive access: no call is running and none will start. */
  if (b->env) wasm_runtime_destroy_exec_env(b->env);
  if (b->inst) wasm_runtime_deinstantiate(b->inst);
  if (b->module) wasm_runtime_unload(b->module);
  b->env = NULL;
  b->inst = NULL;
  b->module = NULL;
}

static ext_status begin(backend *b) {
  pthread_mutex_lock(&b->mu);
  ext_status s = EXT_OK;
  if (b->state == DEAD) s = EXT_CLOSED;
  else if (b->busy) s = EXT_CAPACITY; /* the engine serializes; this is a guard */
  else {
    b->busy = 1;
    b->tripped = 0;
    atomic_store(&b->stop, 0);
  }
  pthread_mutex_unlock(&b->mu);
  return s;
}

/* Load and link failures are reported to the diagnostic callback so a host can
 * say why a module was refused. */
static void report(backend *b, const char *stage, const char *why) {
  if (!b->opt.diagnostic) return;
  char line[256];
  int n = snprintf(line, sizeof(line), "ext_wamr %s: %s", stage, why);
  if (n > 0) b->opt.diagnostic(b->opt.diagnostic_user, (const uint8_t *)line, (size_t)(n < 256 ? n : 255));
}

static ext_status do_connect(backend *b, const ext_call_options *o, ext_emit emit,
                             void *context) {
  if (!runtime_ready) return EXT_UNSUPPORTED;
  char error[128] = {0};
  b->module = wasm_runtime_load(b->bytes, (uint32_t)b->len, error, sizeof(error));
  if (!b->module) { /* malformed, or imports outside the ABI set */
    report(b, "load", error);
    return EXT_UNSUPPORTED;
  }
  /* WAMR links an unknown import lazily and only warns, so a module could load
   * and trap later. The ABI says it must not load, so every import is checked:
   * a function the host provides, nothing else. */
  int32_t imports = wasm_runtime_get_import_count(b->module);
  for (int32_t i = 0; i < imports; i++) {
    wasm_import_t import;
    wasm_runtime_get_import_type(b->module, i, &import);
    if (import.kind != WASM_IMPORT_EXPORT_KIND_FUNC || !import.linked) {
      char why[200];
      snprintf(why, sizeof(why), "the module imports %s.%s, which the host does not provide",
               import.module_name ? import.module_name : "?", import.name ? import.name : "?");
      report(b, "reject", why);
      return EXT_UNSUPPORTED;
    }
  }
  struct InstantiationArgs2 *args = NULL;
  if (!wasm_runtime_instantiation_args_create(&args)) return EXT_NOMEM;
  wasm_runtime_instantiation_args_set_default_stack_size(args, b->opt.stack_size);
  wasm_runtime_instantiation_args_set_host_managed_heap_size(args, 0);
  wasm_runtime_instantiation_args_set_max_memory_pages(args, b->opt.memory_limit_pages);
  b->inst = wasm_runtime_instantiate_ex2(b->module, args, error, sizeof(error));
  wasm_runtime_instantiation_args_destroy(args);
  if (!b->inst) {
    report(b, "instantiate", error);
    return EXT_UNSUPPORTED;
  }
  wasm_runtime_set_custom_data(b->inst, b);
  if (wasm_runtime_lookup_function(b->inst, "_start")) {
    report(b, "reject", "the module exports _start: a command, not a reactor");
    return EXT_UNSUPPORTED;
  }
  wasm_function_inst_t version = wasm_runtime_lookup_function(b->inst, "ext_plugin_abi_version");
  wasm_function_inst_t open_fn = wasm_runtime_lookup_function(b->inst, "ext_plugin_open");
  b->f_call = wasm_runtime_lookup_function(b->inst, "ext_plugin_call");
  b->f_close = wasm_runtime_lookup_function(b->inst, "ext_plugin_close");
  b->f_alloc = wasm_runtime_lookup_function(b->inst, "ext_plugin_alloc");
  b->f_free = wasm_runtime_lookup_function(b->inst, "ext_plugin_free");
  if (!version || !open_fn || !b->f_call || !b->f_close || !b->f_alloc || !b->f_free) {
    report(b, "reject", "the module does not export every ext_plugin_* function");
    return EXT_UNSUPPORTED;
  }
  b->env = wasm_runtime_create_exec_env(b->inst, b->opt.stack_size);
  if (!b->env) return EXT_NOMEM;

  arm(b, o->timeout_ms, o->cancel);
  ext_status s = EXT_OK;
  wasm_function_inst_t init = wasm_runtime_lookup_function(b->inst, "_initialize");
  if (init && (s = call(b, init, 0, NULL)) != EXT_OK) return s;
  uint32_t v[1];
  if ((s = call(b, version, 0, v)) != EXT_OK) return s;
  if (v[0] != ABI_VERSION) return EXT_MISMATCH;
  uint32_t h[2];
  if ((s = call(b, open_fn, 0, h)) != EXT_OK) return s;
  b->handle = (uint64_t)h[0] | ((uint64_t)h[1] << 32);
  if (!b->handle) return EXT_CLOSED;
  /* One response buffer of the maximum frame, reused by every call. */
  if ((s = guest_alloc(b, MAX_FRAME, &b->resp_ptr)) != EXT_OK) return s;
  if ((s = guest_alloc(b, 4, &b->len_ptr)) != EXT_OK) return s;

  char deadline[40], request[80];
  format_deadline(o->timeout_ms, deadline);
  int n = snprintf(request, sizeof(request), "{\"deadline\":\"%s\"}", deadline);
  uint32_t out = 0;
  if ((s = round_trip(b, OP_HANDSHAKE, (const uint8_t *)request, (size_t)n, &out)) != EXT_OK)
    return s;
  return emit_response(b, out, emit, context);
}

static ext_status connect_cb(void *user, const ext_call_options *o, ext_emit emit,
                             void *context) {
  backend *b = user;
  if (!o || !o->timeout_ms) return EXT_TIMEOUT;
  pthread_mutex_lock(&b->mu);
  int fresh = b->state == NEW;
  pthread_mutex_unlock(&b->mu);
  if (!fresh) return EXT_SEQUENCE;
  ext_status s = begin(b);
  if (s != EXT_OK) return s;
  /* The watchdog exists for the life of the backend, asleep unless a call runs. */
  if (!b->watchdog_up) {
    if (pthread_create(&b->watchdog, NULL, watch, b)) {
      pthread_mutex_lock(&b->mu);
      b->busy = 0;
      pthread_mutex_unlock(&b->mu);
      return EXT_NOMEM;
    }
    b->watchdog_up = 1;
  }
  s = do_connect(b, o, emit, context);
  disarm(b);
  pthread_mutex_lock(&b->mu);
  b->busy = 0;
  b->state = s == EXT_OK ? CONNECTED : DEAD;
  pthread_mutex_unlock(&b->mu);
  return s;
}

static ext_status invoke_cb(void *user, const uint8_t *request, size_t len,
                            const ext_call_options *o, ext_emit emit, void *context) {
  backend *b = user;
  if (!o || !o->timeout_ms) return EXT_TIMEOUT;
  pthread_mutex_lock(&b->mu);
  int ready = b->state == CONNECTED;
  int dead = b->state == DEAD;
  pthread_mutex_unlock(&b->mu);
  if (dead) return EXT_CLOSED;
  if (!ready) return EXT_SEQUENCE;
  ext_status s = begin(b);
  if (s != EXT_OK) return s;
  arm(b, o->timeout_ms, o->cancel);
  uint32_t n = 0;
  s = round_trip(b, OP_INVOKE, request, len, &n);
  if (s == EXT_OK) s = emit_response(b, n, emit, context);
  disarm(b);
  pthread_mutex_lock(&b->mu);
  b->busy = 0;
  pthread_mutex_unlock(&b->mu);
  return s;
}

/* close runs concurrently with connect and invoke and must interrupt them. It
 * never frees what a running call is using; release does that. */
static void close_cb(void *user) {
  backend *b = user;
  pthread_mutex_lock(&b->mu);
  if (b->state != DEAD) b->state = DEAD;
  if (b->busy) stop_guest_locked(b);
  pthread_mutex_unlock(&b->mu);
}

static void release_cb(void *user) {
  backend *b = user;
  if (!b) return;
  /* All host calls have joined, so nothing is running. */
  if (b->watchdog_up) {
    pthread_mutex_lock(&b->mu);
    b->watchdog_quit = 1;
    pthread_cond_signal(&b->wake);
    pthread_mutex_unlock(&b->mu);
    pthread_join(b->watchdog, NULL);
  }
  teardown(b);
  pthread_cond_destroy(&b->wake);
  pthread_mutex_destroy(&b->mu);
  free(b->bytes);
  free(b);
}

ext_status ext_wamr_backend_create(const uint8_t *module, size_t module_len,
                                   const ext_wamr_options *options,
                                   ext_backend_extension_context *out) {
  if (!module || !module_len || module_len > MAX_MODULE || !out) return EXT_INVALID;
  if (options && options->struct_size < sizeof(ext_wamr_options)) return EXT_INVALID;
  pthread_once(&runtime_once, start_runtime);
  if (!runtime_ready) return EXT_UNSUPPORTED;
  backend *b = calloc(1, sizeof(*b));
  if (!b) return EXT_NOMEM;
  b->bytes = malloc(module_len);
  if (!b->bytes) {
    free(b);
    return EXT_NOMEM;
  }
  memcpy(b->bytes, module, module_len);
  b->len = module_len;
  if (options) b->opt = *options;
  if (!b->opt.memory_limit_pages) b->opt.memory_limit_pages = DEFAULT_PAGES;
  if (b->opt.memory_limit_pages > 65536u) {
    free(b->bytes);
    free(b);
    return EXT_INVALID;
  }
  if (!b->opt.stack_size) b->opt.stack_size = DEFAULT_STACK;
  if (!b->opt.max_diagnostic_bytes) b->opt.max_diagnostic_bytes = DEFAULT_DIAGNOSTIC;
  b->diag_left = b->opt.max_diagnostic_bytes;
  pthread_mutex_init(&b->mu, NULL);
  pthread_cond_init(&b->wake, NULL);
  atomic_init(&b->stop, 0);
  *out = (ext_backend_extension_context){sizeof(*out), 0, b, connect_cb,
                                         invoke_cb, close_cb, release_cb};
  return EXT_OK;
}
