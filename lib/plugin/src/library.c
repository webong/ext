/* The shared-library backend: runs a guest built against the C ABI in
 * ext_plugin.h inside the host process. It matches the Go loader in
 * pkg/ext-go/cgoengine/cshared: one image per canonical path, never unloaded;
 * ABI version 1; a {"deadline":...} handshake; the ABI's 24 MiB response
 * capacity with no retry; serialized calls; and a session that ends on any
 * transport failure. */
#define _GNU_SOURCE
#define _DARWIN_C_SOURCE
#include "channel.h"
#include "ext_host.h"
#include "ext_plugin.h"
#include "platform.h"
#include "sync.h"
#include "wire.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#else
#include <dlfcn.h>
#include <limits.h>
#endif

#define MAX_PATH_BYTES 4096u

typedef uint32_t(EXT_PLUGIN_CALL *version_fn)(void);
typedef uint64_t(EXT_PLUGIN_CALL *open_fn)(void);
typedef uint32_t(EXT_PLUGIN_CALL *call_fn)(uint64_t, uint32_t, const uint8_t *,
                                           uint32_t, uint8_t *, uint32_t,
                                           uint32_t *);
typedef void(EXT_PLUGIN_CALL *close_fn)(uint64_t);

/* A loaded image. Images are kept for the life of the process, so pointers to
 * them stay valid without reference counts. */
typedef struct image {
  struct image *next;
  char *path; /* canonical */
  ext_status status; /* EXT_OK, or why the image cannot serve */
  open_fn open;
  call_fn call;
  close_fn close;
} image;

static pthread_mutex_t images_mu = PTHREAD_MUTEX_INITIALIZER;
static image *images;

enum { NEW, CONNECTED, DEAD };

typedef struct {
  pthread_mutex_t mu;
  char *path;
  image *library;
  uint64_t handle;
  int state;
  int busy;
} backend;

static int valid_utf8(const unsigned char *s) {
  while (*s) {
    unsigned c = *s++;
    int extra = c < 0x80 ? 0 : (c & 0xE0) == 0xC0 ? 1 : (c & 0xF0) == 0xE0 ? 2
                : (c & 0xF8) == 0xF0 ? 3 : -1;
    if (extra < 0 || (extra == 1 && c < 0xC2) || (c == 0xF4 && (*s & 0xF0) != 0x80) ||
        c > 0xF4)
      return 0;
    unsigned first = *s;
    if (extra == 2 && ((c == 0xE0 && first < 0xA0) || (c == 0xED && first > 0x9F)))
      return 0;
    if (extra == 3 && c == 0xF0 && first < 0x90)
      return 0;
    for (int i = 0; i < extra; i++)
      if ((*s++ & 0xC0) != 0x80)
        return 0;
  }
  return 1;
}

static int64_t mono_ms(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (int64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}

#ifdef _WIN32
static char *canonical(const char *path) { return strdup(path); }
static ext_status load(image *i) {
  int count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, i->path, -1, NULL, 0);
  if (!count)
    return EXT_INVALID;
  wchar_t *wide = malloc((size_t)count * sizeof(wchar_t));
  if (!wide)
    return EXT_NOMEM;
  MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, i->path, -1, wide, count);
  /* Do not search the working directory for transitive DLL dependencies. */
  HMODULE h = LoadLibraryExW(wide, NULL,
                             LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_DEFAULT_DIRS);
  free(wide);
  if (!h)
    return EXT_UNSUPPORTED;
  version_fn version = (version_fn)(void (*)(void))GetProcAddress(h, "ext_plugin_abi_version");
  i->open = (open_fn)(void (*)(void))GetProcAddress(h, "ext_plugin_open");
  i->call = (call_fn)(void (*)(void))GetProcAddress(h, "ext_plugin_call");
  i->close = (close_fn)(void (*)(void))GetProcAddress(h, "ext_plugin_close");
  if (!version || !i->open || !i->call || !i->close)
    return EXT_UNSUPPORTED;
  return version() == EXT_PLUGIN_ABI_VERSION ? EXT_OK : EXT_UNSUPPORTED;
}
#else
static char *canonical(const char *path) {
  char *resolved = realpath(path, NULL);
  return resolved;
}
static void *symbol(void *h, const char *name) {
  void *p = dlsym(h, name);
  return p;
}
static ext_status load(image *i) {
  void *h = dlopen(i->path, RTLD_NOW | RTLD_LOCAL);
  if (!h)
    return EXT_UNSUPPORTED;
  /* Never dlclose, even on ABI failure: native initialization already ran. */
  version_fn version;
  void *p = symbol(h, "ext_plugin_abi_version");
  memcpy(&version, &p, sizeof(p));
  p = symbol(h, "ext_plugin_open");
  memcpy(&i->open, &p, sizeof(p));
  p = symbol(h, "ext_plugin_call");
  memcpy(&i->call, &p, sizeof(p));
  p = symbol(h, "ext_plugin_close");
  memcpy(&i->close, &p, sizeof(p));
  if (!version || !i->open || !i->call || !i->close)
    return EXT_UNSUPPORTED;
  return version() == EXT_PLUGIN_ABI_VERSION ? EXT_OK : EXT_UNSUPPORTED;
}
#endif

/* Finds or loads the image for path. Loading runs the library's initializers,
 * so it happens at most once per canonical path, under the lock. */
static ext_status image_for(const char *path, image **out) {
  char *name = canonical(path);
  if (!name)
    return EXT_NOT_FOUND;
  pthread_mutex_lock(&images_mu);
  image *i = images;
  while (i && strcmp(i->path, name))
    i = i->next;
  if (i) {
    free(name);
  } else {
    i = calloc(1, sizeof(*i));
    if (!i) {
      pthread_mutex_unlock(&images_mu);
      free(name);
      return EXT_NOMEM;
    }
    i->path = name;
    i->status = load(i);
    i->next = images;
    images = i;
  }
  ext_status s = i->status;
  pthread_mutex_unlock(&images_mu);
  *out = i;
  return s;
}

/* Admits one call; calls are serialized, and none starts once the backend is
 * closed or a call has failed. */
static ext_status begin(backend *b, int want) {
  pthread_mutex_lock(&b->mu);
  ext_status s = b->state == DEAD ? EXT_CLOSED
                 : b->state != want ? EXT_INVALID
                 : b->busy ? EXT_INVALID
                           : EXT_OK;
  if (!s)
    b->busy = 1;
  pthread_mutex_unlock(&b->mu);
  return s;
}
static void end(backend *b, ext_status s, int next) {
  pthread_mutex_lock(&b->mu);
  b->busy = 0;
  if (b->state != DEAD)
    b->state = s ? DEAD : next;
  pthread_mutex_unlock(&b->mu);
}

/* One call into the library. The response buffer is the ABI's full capacity,
 * allocated per call as the Go loader does. */
static ext_status round_trip(backend *b, uint32_t op, const uint8_t *request,
                             size_t len, const ext_call_options *o, ext_emit emit,
                             void *context) {
  if (len > EXT_PLUGIN_MAX_FRAME_BYTES)
    return EXT_INVALID;
  if (ext_cancel_is_signaled(o->cancel))
    return EXT_CANCELED;
  uint8_t *response = malloc(EXT_PLUGIN_MAX_FRAME_BYTES);
  if (!response)
    return EXT_NOMEM;
  int64_t started = mono_ms();
  uint32_t size = 0;
  uint32_t status = b->library->call(b->handle, op, request, (uint32_t)len, response,
                                     EXT_PLUGIN_MAX_FRAME_BYTES, &size);
  ext_status s = EXT_OK;
  /* Native code cannot be interrupted, so a call that outlived its deadline or
   * its cancellation is reported as such and ends the session, as in Go. */
  if (ext_cancel_is_signaled(o->cancel))
    s = EXT_CANCELED;
  else if (mono_ms() - started >= (int64_t)o->timeout_ms)
    s = EXT_TIMEOUT;
  else if (status != EXT_PLUGIN_OK || !size || size > EXT_PLUGIN_MAX_FRAME_BYTES)
    s = EXT_INVALID;
  if (!s)
    s = emit(context, response, size);
  free(response);
  return s;
}

static ext_status connect_cb(void *user, const ext_call_options *o, ext_emit emit,
                             void *context) {
  backend *b = user;
  if (!o || !o->timeout_ms)
    return EXT_TIMEOUT;
  ext_status s = begin(b, NEW);
  if (s)
    return s;
  s = image_for(b->path, &b->library);
  if (!s) {
    b->handle = b->library->open();
    if (!b->handle)
      s = EXT_DENIED;
  }
  if (!s) {
    char hello[64], stamp[40];
    int64_t wall = ext_wall_ms() + o->timeout_ms;
    time_t seconds = (time_t)(wall / 1000);
    struct tm t;
    ext_gmtime(&seconds, &t);
    strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%S", &t);
    int n = snprintf(hello, sizeof(hello), "{\"deadline\":\"%s.%03lldZ\"}", stamp,
                     (long long)(wall % 1000));
    s = round_trip(b, EXT_PLUGIN_HANDSHAKE, (const uint8_t *)hello, (size_t)n, o,
                   emit, context);
  }
  end(b, s, CONNECTED);
  return s;
}

static ext_status invoke_cb(void *user, const uint8_t *request, size_t len,
                            const ext_call_options *o, ext_emit emit,
                            void *context) {
  backend *b = user;
  if (!o || !o->timeout_ms)
    return EXT_TIMEOUT;
  ext_status s = begin(b, CONNECTED);
  if (s)
    return s;
  s = round_trip(b, EXT_PLUGIN_INVOKE, request, len, o, emit, context);
  end(b, s, CONNECTED);
  return s;
}

/* close runs concurrently with connect and invoke. Native code cannot be
 * interrupted, so it only refuses further calls; release closes the handle. */
static void close_cb(void *user) {
  backend *b = user;
  pthread_mutex_lock(&b->mu);
  b->state = DEAD;
  pthread_mutex_unlock(&b->mu);
}

static void release_cb(void *user) {
  backend *b = user;
  if (!b)
    return;
  /* Every host call has joined, so no call is running on the handle. */
  if (b->handle)
    b->library->close(b->handle);
  pthread_mutex_destroy(&b->mu);
  free(b->path);
  free(b);
}

ext_status ext_shared_library_backend_create(const ext_shared_library_options *options,
                                             ext_backend_extension_context *out) {
  if (!options || !out || options->struct_size != sizeof(*options) || !options->path)
    return EXT_INVALID;
  size_t n = strlen(options->path);
  if (!n || n > MAX_PATH_BYTES || !ext_path_is_absolute(options->path) ||
      !valid_utf8((const unsigned char *)options->path))
    return EXT_INVALID;
  backend *b = calloc(1, sizeof(*b));
  if (!b)
    return EXT_NOMEM;
  b->path = strdup(options->path);
  if (!b->path) {
    free(b);
    return EXT_NOMEM;
  }
  if (pthread_mutex_init(&b->mu, NULL)) {
    free(b->path);
    free(b);
    return EXT_IO;
  }
  *out = (ext_backend_extension_context){sizeof(*out), 0,        b,
                                         connect_cb,    invoke_cb, close_cb,
                                         release_cb};
  return EXT_OK;
}
