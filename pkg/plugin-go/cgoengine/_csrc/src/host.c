#define _GNU_SOURCE
#include "channel.h"
#include "platform.h"
#include "progress.h"
#include "reverse.h"
#include "wire.h"
#include <errno.h>
#include <limits.h>
#include "sync.h"
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static int canceled(const ext_call_options *o) {
  return ext_cancel_is_signaled(o->cancel);
}
static int valid_call(const ext_call_options *o) {
  return o && o->struct_size == sizeof(*o) && o->timeout_ms;
}
typedef struct reverse_worker reverse_worker;
typedef struct pending_call pending_call;
struct ext_host {
  pthread_mutex_t mu;
  pthread_cond_t changed;
  /* One frame at a time on the channel. Taken before mu and never inside it. */
  pthread_mutex_t wmu;
  int active, running, started, closed;
  ext_host_state state;
  ext_host_hooks hooks;
  ext_host_services services;
  int has_services;
  ext_backend_extension extension;
  ext_backend_extension_context extension_context;
  ext_channel *channel;
  uint64_t next;
  char *path;
  char **arguments, **environment;
  char *directory;
  int capture;
  size_t stderr_limit;
  yyjson_doc *selection;
  uint8_t *descriptor;
  size_t descriptor_len;
  ext_policy verify, authorize;
  void *user;
  uint8_t *readbuf;
  size_t capacity;
  /* Bytes the handshake read beyond the hello response, for the reader. */
  size_t rb_start, rb_len;
  /* The descriptor lists reverse operations, so the plugin may speak when no
   * call is in flight: a reader thread owns the channel's input after the
   * handshake, and reverse calls run on worker threads. Everything below is
   * guarded by mu. */
  int threaded;
  int serving; /* the handshake matched; reverse calls may be served */
  pthread_t reader;
  int reader_started;
  pending_call *calls;   /* the calls in flight, newest first */
  int frames;            /* an extension backend that hands the engine its frames */
  ext_status reader_failure; /* why the reader stopped, when nothing closed us */
  unsigned reverses; /* reverse calls admitted and not yet finished */
  reverse_worker *workers;          /* running */
  reverse_worker *finished;         /* done, waiting to be joined */
};
static int64_t mono_ms(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (int64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}
static void free_strings(char **strings) {
  if (strings) {
    for (size_t i = 0; strings[i]; i++)
      free(strings[i]);
    free(strings);
  }
}
static ext_status copy_strings(const char *const *input, size_t count,
                               const char *prefix, int environment,
                               char ***out) {
  if (count > 256 || (count && !input))
    return EXT_INVALID;
  size_t offset = prefix ? 1 : 0;
  char **copy = calloc(count + offset + 1, sizeof(*copy));
  if (!copy)
    return EXT_NOMEM;
  if (prefix && !(copy[0] = strdup(prefix))) {
    free(copy);
    return EXT_NOMEM;
  }
  ext_status status = EXT_OK;
  for (size_t i = 0; i < count; i++) {
    if (!input[i] || strlen(input[i]) > (environment ? 8192 : 4096)) {
      status = EXT_INVALID;
      break;
    }
    if (environment) {
      const char *equal = strchr(input[i], '=');
      if (!equal || equal == input[i]) {
        status = EXT_INVALID;
        break;
      }
      size_t key = (size_t)(equal - input[i]);
      for (size_t j = 0; j < i; j++)
        if (!strncmp(input[i], input[j], key) && input[j][key] == '=') {
          status = EXT_INVALID;
          break;
        }
      if (status)
        break;
    }
    if (!(copy[offset + i] = strdup(input[i]))) {
      status = EXT_NOMEM;
      break;
    }
  }
  if (status)
    free_strings(copy);
  else
    *out = copy;
  return status;
}
static int names_entry(const char *entry, const char *name) {
  size_t n = strlen(name);
#ifdef _WIN32
  return !_strnicmp(entry, name, n) && entry[n] == '=';
#else
  return !strncmp(entry, name, n) && entry[n] == '=';
#endif
}
/* Prepends the base environment of a launched package to *environment (which
 * holds count caller entries): PATH, plus SystemRoot on Windows, each taken from
 * the host and skipped when the caller supplied that name. */
static ext_status add_base_environment(char ***environment, size_t count) {
  static const char *const names[] = {"PATH",
#ifdef _WIN32
                                      "SystemRoot",
#endif
  };
  const size_t known = sizeof(names) / sizeof(names[0]);
  char **grown = calloc(count + known + 1, sizeof(*grown));
  if (!grown)
    return EXT_NOMEM;
  size_t added = 0;
  for (size_t k = 0; k < known; k++) {
    int supplied = 0;
    for (size_t i = 0; i < count; i++)
      supplied |= names_entry((*environment)[i], names[k]);
    if (supplied)
      continue;
    const char *value = getenv(names[k]);
    size_t length = strlen(names[k]) + 1 + (value ? strlen(value) : 0);
    char *entry = malloc(length + 1);
    if (!entry) {
      free_strings(grown);
      return EXT_NOMEM;
    }
    snprintf(entry, length + 1, "%s=%s", names[k], value ? value : "");
    grown[added++] = entry;
  }
  for (size_t i = 0; i < count; i++)
    grown[added + i] = (*environment)[i];
  free(*environment);
  *environment = grown;
  return EXT_OK;
}
ext_status ext_host_create(const ext_host_options *o,
                           const ext_backend_options *b, ext_host **out) {
  if (!out)
    return EXT_INVALID;
  *out = NULL;
  if (!o || o->abi_version != EXT_HOST_ABI_VERSION ||
      o->struct_size != sizeof(*o) || !o->verify || !o->authorize || !b ||
      b->struct_size != sizeof(*b))
    return EXT_INVALID;
  const ext_jsonline_process_options *process = NULL;
  ext_jsonline_process_options compatible_process;
  const ext_jsonline_process_config *process_config = NULL;
  const ext_jsonline_process_launch *launch = NULL;
  const ext_backend_extension *extension = NULL;
  const ext_backend_extension_context *extension_context = NULL;
  if (b->kind == EXT_BACKEND_JSONLINE_PROCESS_CONFIG) {
    if (!b->config || b->config_size != sizeof(*process_config))
      return EXT_INVALID;
    process_config = b->config;
    if (process_config->struct_size != sizeof(*process_config))
      return EXT_INVALID;
    compatible_process.executable = process_config->executable;
    process = &compatible_process;
    if (!process->executable || !ext_path_is_absolute(process->executable) ||
        strlen(process->executable) > 4096)
      return EXT_INVALID;
  } else if (b->kind == EXT_BACKEND_JSONLINE_PROCESS_LAUNCH) {
    if (!b->config || b->config_size != sizeof(*launch))
      return EXT_INVALID;
    launch = b->config;
    if (launch->struct_size != sizeof(*launch) ||
        launch->stderr_bytes > (16u << 20) ||
        (launch->working_directory &&
         (!ext_path_is_absolute(launch->working_directory) ||
          strlen(launch->working_directory) > 4096)))
      return EXT_INVALID;
    compatible_process.executable = launch->executable;
    process = &compatible_process;
    if (!process->executable || !ext_path_is_absolute(process->executable) ||
        strlen(process->executable) > 4096)
      return EXT_INVALID;
  } else if (b->kind == EXT_BACKEND_JSONLINE_PROCESS) {
    if (!b->config || b->config_size != sizeof(*process))
      return EXT_INVALID;
    process = b->config;
    if (!process->executable || !ext_path_is_absolute(process->executable))
      return EXT_INVALID;
  } else if (b->kind == EXT_BACKEND_EXTENSION) {
    if (!b->config || b->config_size != sizeof(*extension))
      return EXT_INVALID;
    extension = b->config;
    if (extension->struct_size != sizeof(*extension) ||
        (extension->flags & ~(EXT_BACKEND_CONCURRENT | EXT_BACKEND_FRAMES)) ||
        !extension->connect ||
        !extension->invoke || !extension->close || !extension->release)
      return EXT_INVALID;
  } else if (b->kind == EXT_BACKEND_EXTENSION_CONTEXT) {
    if (!b->config || b->config_size != sizeof(*extension_context))
      return EXT_INVALID;
    extension_context = b->config;
    if (extension_context->struct_size != sizeof(*extension_context) ||
        (extension_context->flags &
         ~(EXT_BACKEND_CONCURRENT | EXT_BACKEND_FRAMES)) ||
        !extension_context->connect || !extension_context->invoke ||
        !extension_context->close || !extension_context->release)
      return EXT_INVALID;
  } else {
    return EXT_UNSUPPORTED;
  }
  yyjson_doc *d = ext_parse(o->descriptor, o->descriptor_len);
  if (!d || !ext_descriptor(yyjson_doc_get_root(d))) {
    yyjson_doc_free(d);
    return EXT_INVALID;
  }
  /* Fail closed rather than run a plugin without the frames it declared. A host
   * that owns the child's pipes carries them; an extension backend hands the
   * engine one response at a time unless it says it hands over its frames too. */
  int frames = (extension && (extension->flags & EXT_BACKEND_FRAMES)) ||
               (extension_context &&
                (extension_context->flags & EXT_BACKEND_FRAMES));
  if ((extension || extension_context) && !frames &&
      (ext_descriptor_declares_reverse(yyjson_doc_get_root(d)) ||
       ext_descriptor_declares_progress(yyjson_doc_get_root(d)))) {
    yyjson_doc_free(d);
    return EXT_UNSUPPORTED;
  }
  ext_host *h = calloc(1, sizeof(*h));
  if (!h) {
    yyjson_doc_free(d);
    return EXT_NOMEM;
  }
  h->path = process ? strdup(process->executable) : NULL;
  h->descriptor = malloc(o->descriptor_len);
  if ((process && !h->path) || !h->descriptor) {
    free(h->path);
    free(h->descriptor);
    free(h);
    yyjson_doc_free(d);
    return EXT_NOMEM;
  }
  if (pthread_mutex_init(&h->mu, NULL)) {
    free(h->path);
    free(h->descriptor);
    free(h);
    yyjson_doc_free(d);
    return EXT_IO;
  }
  if (pthread_mutex_init(&h->wmu, NULL)) {
    pthread_mutex_destroy(&h->mu);
    free(h->path);
    free(h->descriptor);
    free(h);
    yyjson_doc_free(d);
    return EXT_IO;
  }
  pthread_condattr_t attr;
  int cond_error = pthread_condattr_init(&attr);
  if (!cond_error) {
#if !defined(__APPLE__) && !defined(_WIN32)
    cond_error = pthread_condattr_setclock(&attr, CLOCK_MONOTONIC);
#endif
    if (!cond_error)
      cond_error = pthread_cond_init(&h->changed, &attr);
    pthread_condattr_destroy(&attr);
  }
  if (cond_error) {
    pthread_mutex_destroy(&h->wmu);
    pthread_mutex_destroy(&h->mu);
    free(h->path);
    free(h->descriptor);
    free(h);
    yyjson_doc_free(d);
    return EXT_IO;
  }
  memcpy(h->descriptor, o->descriptor, o->descriptor_len);
  h->descriptor_len = o->descriptor_len;
  h->selection = d;
  h->threaded = process && ext_descriptor_declares_reverse(yyjson_doc_get_root(d));
  h->frames = frames;
  h->verify = o->verify;
  h->authorize = o->authorize;
  h->user = o->user;
  if (process) {
    const char *const *arguments =
        launch ? launch->arguments : process_config ? process_config->arguments : NULL;
    size_t argument_count = launch ? launch->argument_count
                            : process_config ? process_config->argument_count : 0;
    const char *const *environment = launch ? launch->environment
                            : process_config ? process_config->environment : NULL;
    size_t environment_count = launch ? launch->environment_count
                            : process_config ? process_config->environment_count : 0;
    ext_status status =
        copy_strings(arguments, argument_count, h->path, 0, &h->arguments);
    if (!status)
      status = copy_strings(environment, environment_count, NULL, 1,
                            &h->environment);
    if (!status && launch) {
      h->capture = 1;
      h->stderr_limit = launch->stderr_bytes ? launch->stderr_bytes : 4096;
      h->directory = launch->working_directory
                         ? strdup(launch->working_directory)
                         : NULL;
      if (launch->working_directory && !h->directory)
        status = EXT_NOMEM;
      /* Scrubbed base environment taken from the host, unless the caller set
       * the same name. */
      if (!status)
        status = add_base_environment(&h->environment, environment_count);
    }
    if (status) {
      ext_host_destroy(h);
      return status;
    }
  }
  if (extension)
    h->extension = *extension;
  if (extension_context)
    h->extension_context = *extension_context;
  *out = h;
  return EXT_OK;
}
ext_status ext_host_set_hooks(ext_host *h, const ext_host_hooks *hooks) {
  if (!h || !hooks || hooks->struct_size != sizeof(*hooks))
    return EXT_INVALID;
  pthread_mutex_lock(&h->mu);
  ext_status s = (h->state == EXT_HOST_CREATED && !h->active && !h->started)
                     ? EXT_OK
                     : EXT_INVALID;
  if (!s)
    h->hooks = *hooks;
  pthread_mutex_unlock(&h->mu);
  return s;
}
ext_status ext_host_set_services(ext_host *h, const ext_host_services *s) {
  if (!h || !s || s->struct_size != sizeof(*s))
    return EXT_INVALID;
  if (!s->authorize || !s->handle)
    return EXT_DENIED;
  if (s->max_payload_bytes > EXT_HOST_MAX_FRAME ||
      s->max_in_flight > EXT_REVERSE_MAX_IN_FLIGHT)
    return EXT_INVALID;
  pthread_mutex_lock(&h->mu);
  ext_status status =
      (h->state == EXT_HOST_CREATED && !h->active && !h->started &&
       (h->frames ||
        (!h->extension.connect && !h->extension_context.connect)))
          ? EXT_OK
          : EXT_INVALID;
  if (!status) {
    h->services = *s;
    if (!h->services.timeout_ms)
      h->services.timeout_ms = EXT_REVERSE_DEFAULT_TIMEOUT_MS;
    if (!h->services.max_payload_bytes)
      h->services.max_payload_bytes = EXT_REVERSE_DEFAULT_PAYLOAD;
    if (!h->services.max_in_flight)
      h->services.max_in_flight = EXT_REVERSE_DEFAULT_IN_FLIGHT;
    h->has_services = 1;
  }
  pthread_mutex_unlock(&h->mu);
  return status;
}
static void observe_event(ext_host *h, const ext_call_options *options,
                          const char *stage, yyjson_val *request,
                          ext_status status, yyjson_val *response,
                          int64_t start) {
  if (!h->hooks.observe)
    return;
  static const char *codes[] = {
      "ok",        "invalid",           "denied",  "mismatch", "unsupported",
      "closed",    "deadline_exceeded", "failed",  "failed",   "draining",
      "not_found", "ambiguous",         "canceled"};
  const char *code =
      status >= 0 && status <= EXT_CANCELED ? codes[status] : "failed";
  yyjson_val *remote =
      yyjson_obj_get(yyjson_obj_get(response, "error"), "code");
  if (!status && ext_text(remote, 0, 0))
    code = yyjson_get_str(remote);
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  if (!doc)
    return;
  yyjson_mut_val *root = yyjson_mut_obj(doc);
  yyjson_mut_doc_set_root(doc, root);
  int ok = root && yyjson_mut_obj_add_str(doc, root, "stage", stage) &&
           yyjson_mut_obj_add_str(doc, root, "code", code) &&
           yyjson_mut_obj_add_sint(doc, root, "durationMilliseconds",
                                   mono_ms() - start);
  yyjson_val *identity =
      yyjson_obj_get(yyjson_doc_get_root(h->selection), "identity");
  yyjson_mut_val *copy = yyjson_val_mut_copy(doc, identity);
  ok = ok && copy && yyjson_mut_obj_add_val(doc, root, "identity", copy);
  const char *fields[] = {"contract", "operation", "id"};
  for (size_t i = 0; i < 3 && ok; i++) {
    yyjson_val *v = yyjson_obj_get(request, fields[i]);
    int valid = i == 0 ? (EXT_FIELDS(v, "name", "version") && ext_ref(v))
                       : ext_text(v, i == 2, 0);
    if (valid) {
      copy = yyjson_val_mut_copy(doc, v);
      ok = copy && yyjson_mut_obj_add_val(
                       doc, root, i == 2 ? "requestID" : fields[i], copy);
    }
  }
  size_t len = 0;
  char *data = ok ? yyjson_mut_write(doc, 0, &len) : NULL;
  if (data)
    h->hooks.observe(h->hooks.user, options, (const uint8_t *)data, len);
  free(data);
  yyjson_mut_doc_free(doc);
}
/* Signals the cancel of the running reverse calls: those of one call, or all of
 * them when call is NULL; the caller holds h->mu. */
static void cancel_workers(ext_host *h, const pending_call *call);
void ext_host_close(ext_host *h) {
  if (!h)
    return;
  int notify = 0;
  pthread_mutex_lock(&h->mu);
  if (!h->closed) {
    h->closed = 1;
    if (h->state != EXT_HOST_FAILED)
      h->state = EXT_HOST_CLOSED;
    notify = 1;
    if (h->channel)
      ext_channel_kill(h->channel);
    cancel_workers(h, NULL);
    pthread_cond_broadcast(&h->changed);
  }
  pthread_mutex_unlock(&h->mu);
  if (notify && h->extension.close)
    h->extension.close(h->extension.user);
  if (notify && h->extension_context.close)
    h->extension_context.close(h->extension_context.user);
}
static void stop_threads(ext_host *h);
void ext_host_destroy(ext_host *h) {
  if (!h)
    return;
  ext_host_close(h);
  stop_threads(h);
  ext_channel_destroy(h->channel);
  if (h->extension.release)
    h->extension.release(h->extension.user);
  if (h->extension_context.release)
    h->extension_context.release(h->extension_context.user);
  yyjson_doc_free(h->selection);
  free(h->descriptor);
  free(h->path);
  free(h->readbuf);
  free_strings(h->arguments);
  free_strings(h->environment);
  free(h->directory);
  pthread_cond_destroy(&h->changed);
  pthread_mutex_destroy(&h->wmu);
  pthread_mutex_destroy(&h->mu);
  free(h);
}
static void wait_changed(ext_host *h, int64_t deadline,
                         const ext_call_options *o) {
  int64_t remain = deadline - mono_ms();
  if (remain <= 0)
    return;
  if (o->cancel && remain > 10)
    remain = 10;
  struct timespec t;
#ifdef __APPLE__
  t.tv_sec = (time_t)(remain / 1000);
  t.tv_nsec = (long)(remain % 1000) * 1000000;
  pthread_cond_timedwait_relative_np(&h->changed, &h->mu, &t);
#elif defined(_WIN32)
  /* winpthreads condition variables run on the realtime clock. Callers loop and
   * recheck their deadline on the monotonic clock, so a clock step only moves
   * the wake-up. */
  clock_gettime(CLOCK_REALTIME, &t);
  int64_t nanoseconds = (int64_t)t.tv_nsec + (remain % 1000) * 1000000;
  t.tv_sec += (time_t)(remain / 1000 + nanoseconds / 1000000000);
  t.tv_nsec = (long)(nanoseconds % 1000000000);
  pthread_cond_timedwait(&h->changed, &h->mu, &t);
#else
  int64_t until = mono_ms() + remain;
  t.tv_sec = (time_t)(until / 1000);
  t.tv_nsec = (long)(until % 1000) * 1000000;
  pthread_cond_timedwait(&h->changed, &h->mu, &t);
#endif
}
static ext_status enter(ext_host *h, int64_t deadline, int starting,
                        const ext_call_options *o) {
  pthread_mutex_lock(&h->mu);
  ext_status status = canceled(o)                                 ? EXT_CANCELED
                      : h->closed                                 ? EXT_CLOSED
                      : h->state == EXT_HOST_DRAINING             ? EXT_DRAINING
                      : (!starting && h->state != EXT_HOST_READY) ? EXT_INVALID
                      : mono_ms() >= deadline                     ? EXT_TIMEOUT
                                                                  : EXT_OK;
  if (status) {
    pthread_mutex_unlock(&h->mu);
    return status;
  }
  /* Admission precedes serialized backend queueing. Drain must wait for every
   * admitted call, including callers waiting for the backend gate. */
  h->active++;
  uint32_t flags = h->extension.flags | h->extension_context.flags;
  while (h->running && !h->closed &&
         (starting || !(flags & EXT_BACKEND_CONCURRENT))) {
    if (canceled(o) || mono_ms() >= deadline)
      break;
    wait_changed(h, deadline, o);
  }
  status = canceled(o)             ? EXT_CANCELED
           : h->closed             ? EXT_CLOSED
           : mono_ms() >= deadline ? EXT_TIMEOUT
                                   : EXT_OK;
  if (status) {
    h->active--;
    pthread_cond_broadcast(&h->changed);
  } else
    h->running++;
  pthread_mutex_unlock(&h->mu);
  return status;
}
static void leave(ext_host *h) {
  pthread_mutex_lock(&h->mu);
  h->active--;
  h->running--;
  pthread_cond_broadcast(&h->changed);
  pthread_mutex_unlock(&h->mu);
}
static void fail(ext_host *h) {
  pthread_mutex_lock(&h->mu);
  if (!h->closed)
    h->state = EXT_HOST_FAILED;
  pthread_mutex_unlock(&h->mu);
  ext_host_close(h);
}
ext_host_state ext_host_get_state(ext_host *h) {
  if (!h)
    return EXT_HOST_CLOSED;
  pthread_mutex_lock(&h->mu);
  ext_host_state state = h->state;
  pthread_mutex_unlock(&h->mu);
  return state;
}
ext_status ext_host_drain_with_options(ext_host *h, const ext_call_options *o) {
  if (!h || !valid_call(o))
    return EXT_INVALID;
  if (canceled(o))
    return EXT_CANCELED;
  int64_t deadline = mono_ms() + o->timeout_ms;
  pthread_mutex_lock(&h->mu);
  if (h->closed) {
    pthread_mutex_unlock(&h->mu);
    return EXT_OK;
  }
  if (h->state != EXT_HOST_READY) {
    ext_status s = h->state == EXT_HOST_DRAINING ? EXT_DRAINING : EXT_INVALID;
    pthread_mutex_unlock(&h->mu);
    return s;
  }
  h->state = EXT_HOST_DRAINING;
  pthread_cond_broadcast(&h->changed);
  while (h->active && !h->closed) {
    if (canceled(o) || mono_ms() >= deadline) {
      h->state = EXT_HOST_READY;
      pthread_cond_broadcast(&h->changed);
      pthread_mutex_unlock(&h->mu);
      return canceled(o) ? EXT_CANCELED : EXT_TIMEOUT;
    }
    wait_changed(h, deadline, o);
  }
  if (!h->closed && canceled(o)) {
    h->state = EXT_HOST_READY;
    pthread_cond_broadcast(&h->changed);
    pthread_mutex_unlock(&h->mu);
    return EXT_CANCELED;
  }
  pthread_mutex_unlock(&h->mu);
  ext_host_close(h);
  return EXT_OK;
}
/* The result sink prevents foreign allocator ownership from crossing the ABI.
 */
typedef struct {
  ext_buffer *out;
  int emitted;
  ext_status status;
} result_sink;
static ext_status emit_result(void *context, const uint8_t *data, size_t len) {
  result_sink *sink = context;
  if (sink->emitted++ || !data || !len || len > EXT_HOST_MAX_FRAME) {
    sink->status = EXT_INVALID;
    return sink->status;
  }
  sink->out->data = malloc(len);
  if (!sink->out->data) {
    sink->status = EXT_NOMEM;
    return sink->status;
  }
  memcpy(sink->out->data, data, len);
  sink->out->len = len;
  return EXT_OK;
}
static ext_status extension_call(ext_host *h, const uint8_t *data, size_t len,
                                 int64_t deadline, const ext_call_options *o,
                                 ext_buffer *out) {
  int64_t remaining = deadline - mono_ms();
  if (remaining <= 0)
    return EXT_TIMEOUT;
  if (canceled(o))
    return EXT_CANCELED;
  result_sink sink = {out, 0, EXT_OK};
  ext_status s;
  if (h->extension_context.connect) {
    ext_call_options call = *o;
    call.timeout_ms = (uint32_t)remaining;
    s = data ? h->extension_context.invoke(h->extension_context.user, data, len,
                                           &call, emit_result, &sink)
             : h->extension_context.connect(h->extension_context.user, &call,
                                            emit_result, &sink);
  } else {
    s = data ? h->extension.invoke(h->extension.user, data, len,
                                   (uint32_t)remaining, emit_result, &sink)
             : h->extension.connect(h->extension.user, (uint32_t)remaining,
                                    emit_result, &sink);
  }
  /* A backend that hands over its frames reports the violation the engine found
   * in them, so the session fails with the same status. */
  if (s < EXT_OK || (s > EXT_CANCELED && !(h->frames && s == EXT_PROTOCOL)))
    s = EXT_INVALID;
  if (!s)
    s = sink.status ? sink.status : sink.emitted == 1 ? EXT_OK : EXT_INVALID;
  return s;
}
static int closed(ext_host *h) {
  pthread_mutex_lock(&h->mu);
  int result = h->closed;
  pthread_mutex_unlock(&h->mu);
  return result;
}
static ext_status ready(ext_host *h, int write, int64_t deadline,
                        const ext_call_options *o) {
  for (;;) {
    if (canceled(o))
      return EXT_CANCELED;
    if (closed(h))
      return EXT_CLOSED;
    int64_t ms = deadline - mono_ms();
    if (ms <= 0)
      return EXT_TIMEOUT;
    if (o->cancel && ms > 10)
      ms = 10;
    if (ms > INT_MAX)
      ms = INT_MAX;
    ext_status s = ext_channel_wait(h->channel, write, (int)ms);
    if (s == EXT_TIMEOUT)
      continue;
    if (s == EXT_OK)
      return EXT_OK;
    return closed(h) ? EXT_CLOSED : EXT_IO;
  }
}
static ext_status write_bytes(ext_host *h, const uint8_t *p, size_t n,
                              int64_t deadline, const ext_call_options *o) {
  while (n) {
    ext_status s = ready(h, 1, deadline, o);
    if (s)
      return s;
    ptrdiff_t r = ext_channel_write(h->channel, p, n);
    if (!r)
      continue;
    if (r < 0)
      return EXT_IO;
    p += r;
    n -= (size_t)r;
  }
  return EXT_OK;
}
/* The call the frames being read belong to. A progress frame is legal only
 * while a call that declared progress is in flight, and names it as its parent;
 * anything else is a protocol violation. */
typedef struct {
  ext_host *host;
  const ext_call_options *options;
  int64_t deadline; /* of the call, on the monotonic clock */
  const char *id;
  int declared;     /* the request carried a progress rate */
  int forward;      /* a call, not the handshake */
  ext_progress_sink *sink;
} frame_call;
/* A line the strict parser refused. Go reads a line in two steps: it peeks at the
 * "frame" tag with a lenient decoder, and decodes the whole frame strictly only
 * afterwards. So a line that is not even JSON, or not an object, is a violation
 * of the framing; one that is an object with a tag is a malformed frame, also a
 * violation; and an untagged object the strict step refuses (a duplicate key, an
 * unknown field, bad UTF-8) is an invalid response. */
static ext_status unparsable(const uint8_t *line, size_t len) {
  yyjson_doc *loose = yyjson_read(
      (const char *)line, len,
      YYJSON_READ_NUMBER_AS_RAW | YYJSON_READ_ALLOW_INVALID_UNICODE);
  yyjson_val *root = loose ? yyjson_doc_get_root(loose) : NULL;
  yyjson_val *tag = yyjson_is_obj(root) ? yyjson_obj_get(root, "frame") : NULL;
  ext_status s = !yyjson_is_obj(root)                              ? EXT_PROTOCOL
                 : tag && yyjson_is_str(tag) && yyjson_get_len(tag) ? EXT_PROTOCOL
                                                                    : EXT_INVALID;
  yyjson_doc_free(loose);
  return s;
}
/* One frame at a time on the channel: a frame is written whole, under wmu, by
 * whichever thread has one to send (a caller, a worker, the reader). */
static ext_status write_frame(ext_host *h, const uint8_t *p, size_t n,
                              int64_t deadline, const ext_call_options *o) {
  pthread_mutex_lock(&h->wmu);
  ext_status s = write_bytes(h, p, n, deadline, o);
  if (!s)
    s = write_bytes(h, (const uint8_t *)"\n", 1, deadline, o);
  pthread_mutex_unlock(&h->wmu);
  return s;
}

/* ---- reverse calls ---- */

/* What a reverse handler emitted. */
typedef struct {
  uint32_t kind;
  uint8_t *data;
  size_t len;
  int emitted;
} reverse_sink;
static ext_status emit_reverse(void *context, uint32_t kind,
                               const uint8_t *data, size_t len) {
  reverse_sink *sink = context;
  if (sink->emitted++ || kind > EXT_HOST_REVERSE_PUBLIC_ERROR ||
      (len && !data) || len > EXT_HOST_MAX_FRAME)
    return EXT_INVALID;
  if (len) {
    sink->data = malloc(len);
    if (!sink->data)
      return EXT_NOMEM;
    memcpy(sink->data, data, len);
  }
  sink->kind = kind;
  sink->len = len;
  return EXT_OK;
}
/* The Request the hooks receive: the plugin's frame as the host's own record of
 * the call, with the plugin's identity, the surface the descriptor declared for
 * the operation, and the call it belongs to as parent. */
static char *reverse_request_json(ext_host *h, const ext_reverse_request *r,
                                  yyjson_val *declared, size_t *len) {
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  if (!doc)
    return NULL;
  yyjson_mut_val *root = yyjson_mut_obj(doc);
  yyjson_mut_doc_set_root(doc, root);
  yyjson_val *surface = yyjson_obj_get(declared, "surface");
  if (surface && (!yyjson_is_str(surface) || !yyjson_get_len(surface)))
    surface = NULL;
  const struct {
    const char *key;
    yyjson_val *value;
  } fields[] = {
      {"id", r->id},
      {"plugin", yyjson_obj_get(yyjson_doc_get_root(h->selection), "identity")},
      {"contract", r->contract},
      {"operation", r->operation},
      {"surface", surface},
      {"deadline", r->deadline},
      {"payload", r->payload},
      {"parent", r->parent}};
  int ok = root && yyjson_mut_obj_add_str(doc, root, "apiVersion",
                                          "ext.plugin/v1");
  for (size_t i = 0; ok && i < sizeof(fields) / sizeof(*fields); i++) {
    if (!fields[i].value)
      continue;
    yyjson_mut_val *copy = yyjson_val_mut_copy(doc, fields[i].value);
    ok = copy && yyjson_mut_obj_add_val(doc, root, fields[i].key, copy);
  }
  char *json = ok ? yyjson_mut_write(doc, 0, len) : NULL;
  yyjson_mut_doc_free(doc);
  return json;
}
static int64_t earliest(int64_t a, int64_t b) { return a < b ? a : b; }
/* The size of a payload as it would be written compactly. */
static size_t payload_size(const ext_reverse_request *r) {
  size_t n = 0;
  if (r->payload) {
    char *text = yyjson_val_write(r->payload, 0, &n);
    if (!text)
      return SIZE_MAX;
    free(text);
  }
  return n;
}

/* Decides what becomes of a validated reverse request; the caller holds h->mu.
 * live is the id of the call in flight, or NULL. On EXT_OK, *refusal is the code
 * to answer with, or NULL when the request is admitted and counted, and
 * *declared is the descriptor's entry for the operation. The checks and their
 * order are Session.admitReverse and the JSON-line client's: with no services
 * every request is unavailable whatever it names; otherwise a parent that is not
 * the call in flight fails the session, then undeclared, unavailable (the
 * handshake has not matched), too_large, expired, busy. */
static ext_status admit_reverse(ext_host *h, const ext_reverse_request *r,
                                size_t payload_len, const char *live,
                                const char **refusal, yyjson_val **declared) {
  *refusal = NULL;
  *declared = NULL;
  if (!h->has_services) {
    *refusal = EXT_REVERSE_UNAVAILABLE;
    return EXT_OK;
  }
  if (r->parent && (!live || !yyjson_equals_str(r->parent, live)))
    return EXT_PROTOCOL;
  *declared = ext_lookup_in(
      yyjson_obj_get(yyjson_doc_get_root(h->selection), "reverse"), r->contract,
      r->operation);
  if (!*declared)
    *refusal = EXT_REVERSE_UNDECLARED;
  else if (!h->serving)
    *refusal = EXT_REVERSE_UNAVAILABLE;
  else if (payload_len > h->services.max_payload_bytes)
    *refusal = EXT_REVERSE_TOO_LARGE;
  else if (r->deadline_ms - ext_wall_ms() <= 0)
    *refusal = EXT_REVERSE_EXPIRED;
  else if (h->reverses >= h->services.max_in_flight)
    *refusal = EXT_REVERSE_BUSY;
  else
    h->reverses++;
  return EXT_OK;
}
/* Answers a request with a refusal. */
static ext_status refuse_reverse(ext_host *h, const ext_reverse_request *r,
                                 const char *code, int64_t deadline,
                                 const ext_call_options *o) {
  size_t len = 0;
  char *line = ext_reverse_refusal(yyjson_get_str(r->id), yyjson_get_len(r->id),
                                   code, &len);
  if (!line)
    return EXT_NOMEM;
  ext_status s = write_frame(h, (const uint8_t *)line, len, deadline, o);
  free(line);
  return s;
}

/* A reverse call that has been admitted and is running its hooks. */
struct reverse_worker {
  reverse_worker *next;
  ext_host *host;
  pthread_t thread;
  ext_cancel *cancel; /* the hooks' cancel signal */
  char *id;           /* the plugin's id for the request */
  size_t id_len;
  char *json; /* the Request the hooks receive */
  size_t json_len;
  int64_t plugin_deadline_ms; /* wall clock */
  int call_scoped;
  pending_call *call;    /* the call it belongs to, when call scoped */
  int threaded;          /* it runs on a thread of its own; else the backend's */
  void *value;           /* the options' value for the hooks */
  int64_t call_deadline; /* monotonic; call scoped */
};
static void cancel_workers(ext_host *h, const pending_call *call) {
  for (reverse_worker *w = h->workers; w; w = w->next)
    if (!call || w->call == call)
      ext_cancel_signal(w->cancel);
}
static void destroy_worker(reverse_worker *w) {
  if (w) {
    ext_cancel_destroy(w->cancel);
    free(w->id);
    free(w->json);
    free(w);
  }
}
/* Removes w from the running list; the caller holds h->mu. */
static void unlink_worker(ext_host *h, reverse_worker *w) {
  for (reverse_worker **at = &h->workers; *at; at = &(*at)->next)
    if (*at == w) {
      *at = w->next;
      return;
    }
}
/* Joins the workers that have finished. */
static void reap_workers(ext_host *h) {
  pthread_mutex_lock(&h->mu);
  reverse_worker *list = h->finished;
  h->finished = NULL;
  pthread_mutex_unlock(&h->mu);
  while (list) {
    reverse_worker *w = list;
    list = w->next;
    pthread_join(w->thread, NULL);
    destroy_worker(w);
  }
}
/* A worker for an admitted request; the caller holds h->mu, and the request is
 * already counted. It is on the running list before the lock is released, so a
 * call that ends, or a host that closes, cancels it. call is the call in flight
 * for a call-scoped request. */
static reverse_worker *new_worker(ext_host *h, const ext_reverse_request *r,
                                  const pending_call *call);

/* The call in flight, as the reader thread sees it. Its fields are guarded by
 * h->mu; the caller waits on h->changed until done. */
struct pending_call {
  const char *id;
  int declared; /* the request carried a progress rate */
  ext_progress_sink *sink;
  const ext_call_options *options;
  int64_t deadline; /* monotonic */
  int done;
  uint8_t *response;
  size_t response_len;
  yyjson_doc *doc;
  pending_call *next;
  unsigned reverses; /* its reverse requests that have not finished */
  unsigned users;    /* threads handing it frames (they keep it alive) */
  int busy;          /* one of them is passing progress to the sink */
};
/* The calls in flight are on a list under h->mu: one for a process the engine
 * reads, any number on a concurrent backend. */
static pending_call *find_call(ext_host *h, const char *id, size_t len) {
  for (pending_call *c = h->calls; c; c = c->next)
    if (strlen(c->id) == len && !memcmp(c->id, id, len))
      return c;
  return NULL;
}
static void link_call(ext_host *h, pending_call *c) {
  c->next = h->calls;
  h->calls = c;
}
static void unlink_call(ext_host *h, pending_call *c) {
  for (pending_call **at = &h->calls; *at; at = &(*at)->next)
    if (*at == c) {
      *at = c->next;
      return;
    }
}

/* Takes a call off the list and waits for everything that holds it: the threads
 * handing it frames, and the hooks of its reverse requests, which are told to
 * stop. The caller holds h->mu. */
static void end_call(ext_host *h, pending_call *call) {
  unlink_call(h, call);
  cancel_workers(h, call);
  while (call->users || call->reverses)
    pthread_cond_wait(&h->changed, &h->mu);
}
/* Runs the hooks of an admitted request and returns the reverse_response line
 * for the plugin, or NULL when memory ran out. *until is the reverse deadline on
 * the monotonic clock. The line is the engine's whole contribution to the
 * answer; writing it is the caller's. */
static char *reverse_line(reverse_worker *w, size_t *len, int64_t *until) {
  ext_host *h = w->host;
  const ext_host_services *services = &h->services;
  ext_call_options options = {sizeof(options), 1, w->cancel, w->value};
  reverse_sink sink = {0};
  char *line = NULL;
  /* The reverse deadline is the earliest of the plugin's, the call's (call
   * scoped) and the services' own bound. */
  int64_t now = mono_ms();
  int64_t left = earliest(w->plugin_deadline_ms - ext_wall_ms(),
                          (int64_t)services->timeout_ms);
  if (w->call_scoped)
    left = earliest(left, w->call_deadline - now);
  int64_t reverse_deadline = now + left;
  *until = reverse_deadline;
  options.timeout_ms = left > 0 ? (uint32_t)earliest(left, UINT32_MAX) : 1;
  if (services->authorize(services->user, &options, (const uint8_t *)w->json,
                          w->json_len)) {
    line = ext_reverse_refusal(w->id, w->id_len, EXT_REVERSE_DENIED, len);
  } else if (canceled(&options) || mono_ms() >= reverse_deadline) {
    line = ext_reverse_refusal(w->id, w->id_len, EXT_REVERSE_EXPIRED, len);
  } else {
    options.timeout_ms =
        (uint32_t)earliest(reverse_deadline - mono_ms(), UINT32_MAX);
    ext_status s =
        services->handle(services->user, &options, (const uint8_t *)w->json,
                         w->json_len, emit_reverse, &sink);
    if (s || sink.emitted != 1) {
      line = ext_reverse_failure(w->id, w->id_len, len);
    } else if (sink.kind != EXT_HOST_REVERSE_PUBLIC_ERROR &&
               (canceled(&options) || mono_ms() >= reverse_deadline)) {
      /* A result that outlived its deadline is not one the plugin may use. */
      line = ext_reverse_failure(w->id, w->id_len, len);
    } else {
      line = ext_reverse_answer(w->id, w->id_len, sink.kind, sink.data,
                                sink.len, services->max_payload_bytes, len);
    }
  }
  free(sink.data);
  return line;
}
/* Ends an admitted request: it no longer counts, and its call no longer waits for
 * it. A request on a thread of its own is left for the reader to join; one that
 * ran on the backend's thread is freed here. */
static void finish_worker(reverse_worker *w) {
  ext_host *h = w->host;
  pthread_mutex_lock(&h->mu);
  unlink_worker(h, w);
  if (w->threaded) {
    w->next = h->finished;
    h->finished = w;
  }
  h->reverses--;
  if (w->call)
    w->call->reverses--;
  pthread_cond_broadcast(&h->changed);
  pthread_mutex_unlock(&h->mu);
  if (!w->threaded)
    destroy_worker(w);
}
static void *worker_main(void *arg) {
  reverse_worker *w = arg;
  ext_host *h = w->host;
  size_t len = 0;
  int64_t reverse_deadline;
  char *line = reverse_line(w, &len, &reverse_deadline);
  if (line) {
    /* A frame is written whole or the session is ended: cancellation must not
     * cut one short, so the write ignores the signal and is bounded by the call
     * (or, for a session-scoped request, a grace past its deadline). */
    ext_call_options write_options = {sizeof(write_options), 1, NULL, NULL};
    int64_t until_write =
        w->call_scoped ? w->call_deadline : reverse_deadline + 30000;
    ext_status s = write_frame(h, (const uint8_t *)line, len, until_write,
                               &write_options);
    if (s && s != EXT_CLOSED)
      fail(h);
  }
  free(line);
  finish_worker(w);
  return NULL;
}
/* Routes a reverse_request when no thread can run the hooks: during the
 * handshake and for a plugin that declares no reverse operations. Nothing can be
 * admitted there (an operation is never declared, or the handshake has not
 * matched), so the answer is a refusal. */
static ext_status serve_reverse(const frame_call *call, yyjson_val *frame) {
  ext_host *h = call->host;
  ext_reverse_request request;
  ext_status s = ext_reverse_request_frame(frame, &request);
  if (s)
    return s;
  size_t payload_len = payload_size(&request);
  const char *refusal;
  yyjson_val *declared;
  pthread_mutex_lock(&h->mu);
  s = admit_reverse(h, &request, payload_len, call->forward ? call->id : NULL,
                    &refusal, &declared);
  if (!s && !refusal) {
    /* Unreachable: only a plugin with declared reverse operations is admitted,
     * and its reader thread serves it. Do not run it here. */
    h->reverses--;
    refusal = EXT_REVERSE_UNAVAILABLE;
  }
  pthread_mutex_unlock(&h->mu);
  if (s)
    return s;
  return refuse_reverse(h, &request, refusal, call->deadline, call->options);
}

static reverse_worker *new_worker(ext_host *h, const ext_reverse_request *r,
                                  const pending_call *call) {
  reverse_worker *w = calloc(1, sizeof(*w));
  if (!w)
    return NULL;
  w->host = h;
  w->id_len = yyjson_get_len(r->id);
  w->id = malloc(w->id_len + 1);
  if (!w->id || ext_cancel_create(&w->cancel)) {
    destroy_worker(w);
    return NULL;
  }
  memcpy(w->id, yyjson_get_str(r->id), w->id_len);
  w->id[w->id_len] = 0;
  w->plugin_deadline_ms = r->deadline_ms;
  if (r->parent) {
    w->call_scoped = 1;
    w->call = (pending_call *)call;
    w->call->reverses++;
    w->value = call->options->value;
    w->call_deadline = call->deadline;
  } else {
    w->value = h->services.session_value;
  }
  w->next = h->workers;
  h->workers = w;
  return w;
}

/* Routes one line the plugin sent, when this thread is the one waiting for the
 * response. A line without a "frame" tag is the response; *response says so, and
 * the caller validates it. */
static ext_status route_frame(yyjson_doc *doc, const frame_call *call,
                              int *response) {
  yyjson_val *root = yyjson_doc_get_root(doc), *tag;
  *response = 0;
  /* Only an object can be a frame or a response. A JSON null decodes in Go to
   * an empty response, whose id never names the call in flight: a violation. */
  if (!yyjson_is_obj(root))
    return EXT_PROTOCOL;
  tag = yyjson_obj_get(root, "frame");
  if (!tag || yyjson_is_null(tag) || yyjson_equals_str(tag, "")) {
    *response = 1;
    return EXT_OK;
  }
  if (!yyjson_is_str(tag))
    return EXT_PROTOCOL;
  if (yyjson_equals_str(tag, "progress")) {
    ext_progress_event event;
    yyjson_val *parent;
    ext_status s = ext_progress_frame(root, &event, &parent);
    if (s)
      return s;
    if (!call || !call->declared || !yyjson_equals_str(parent, call->id))
      return EXT_PROTOCOL;
    return ext_progress_sink_deliver(call->sink, &event);
  }
  if (yyjson_equals_str(tag, "reverse_request"))
    return serve_reverse(call, root);
  return EXT_PROTOCOL;
}
/* Writes one request line and reads lines until the response. Progress frames
 * that arrive first are validated, limited and passed on through call. The
 * response must be the last thing the plugin has sent, unless keep_tail: the
 * handshake of a plugin with a reader thread, which takes over what follows. On
 * success *parsed is the response, parsed, and out holds its bytes. This is the
 * path of a plugin with no reader thread, and of the handshake. */
static ext_status exchange(ext_host *h, const uint8_t *p, size_t n,
                           int64_t deadline, const ext_call_options *o,
                           const frame_call *call, int keep_tail,
                           ext_buffer *out, yyjson_doc **parsed) {
  *parsed = NULL;
  ext_status s = write_frame(h, p, n, deadline, o);
  if (s)
    return s;
  /* Bytes [start, len) are unprocessed; those before searched hold no newline. */
  size_t start = 0, searched = 0, len = 0;
  for (;;) {
    uint8_t *newline =
        len > searched ? memchr(h->readbuf + searched, '\n', len - searched)
                       : NULL;
    if (newline) {
      size_t end = (size_t)(newline - h->readbuf), frame = end - start;
      if (frame > EXT_HOST_MAX_FRAME)
        return EXT_INVALID;
      yyjson_doc *doc = ext_parse(h->readbuf + start, frame);
      int response = 0;
      s = doc ? route_frame(doc, call, &response)
              : unparsable(h->readbuf + start, frame);
      if (!s && response) {
        /* A response that names another call is a violation, as in the Go
         * client; one that is malformed or has the wrong version is left to the
         * caller's validation. */
        yyjson_val *root = yyjson_doc_get_root(doc);
        if (ext_response(root, call->id) == EXT_MISMATCH &&
            !yyjson_equals_str(yyjson_obj_get(root, "id"), call->id))
          s = EXT_PROTOCOL;
        else if (end + 1 != len && !keep_tail)
          s = EXT_INVALID;
      }
      if (!s && !response) {
        /* A plugin that floods frames must not outlast the call. */
        if (canceled(o))
          s = EXT_CANCELED;
        else if (closed(h))
          s = EXT_CLOSED;
        else if (mono_ms() >= deadline)
          s = EXT_TIMEOUT;
      }
      if (s) {
        yyjson_doc_free(doc);
        return s;
      }
      if (response) {
        out->data = malloc(frame ? frame : 1);
        if (!out->data) {
          yyjson_doc_free(doc);
          return EXT_NOMEM;
        }
        memcpy(out->data, h->readbuf + start, frame);
        out->len = frame;
        *parsed = doc;
        h->rb_start = end + 1;
        h->rb_len = len;
        return EXT_OK;
      }
      yyjson_doc_free(doc);
      start = end + 1;
      searched = start;
      continue;
    }
    searched = len;
    if (start) {
      memmove(h->readbuf, h->readbuf + start, len - start);
      len -= start;
      searched = len;
      start = 0;
    }
    if (len == h->capacity) {
      size_t next = h->capacity ? h->capacity * 2 : 4096;
      if (next > EXT_HOST_MAX_FRAME + 1)
        next = EXT_HOST_MAX_FRAME + 1;
      if (next <= len)
        return EXT_INVALID;
      uint8_t *b = realloc(h->readbuf, next);
      if (!b)
        return EXT_NOMEM;
      h->readbuf = b;
      h->capacity = next;
    }
    s = ready(h, 0, deadline, o);
    if (s)
      return s;
    ptrdiff_t r =
        ext_channel_read(h->channel, h->readbuf + len, h->capacity - len);
    if (!r)
      continue;
    if (r < 0)
      return EXT_IO;
    len += (size_t)r;
  }
}

/* ---- the reader thread ---- */

/* The response to the call it names. Takes the parsed line on success. */
static ext_status reader_response(ext_host *h, yyjson_doc *doc,
                                  const uint8_t *line, size_t len) {
  yyjson_val *root = yyjson_doc_get_root(doc), *id = yyjson_obj_get(root, "id");
  ext_status s = EXT_OK;
  pthread_mutex_lock(&h->mu);
  pending_call *call =
      yyjson_is_str(id) ? find_call(h, yyjson_get_str(id), yyjson_get_len(id))
                        : NULL;
  if (!call || call->done)
    s = EXT_PROTOCOL; /* a response to no call in flight */
  else if (!(call->response = malloc(len ? len : 1)))
    s = EXT_NOMEM;
  else {
    memcpy(call->response, line, len);
    call->response_len = len;
    call->doc = doc;
    call->done = 1;
    pthread_cond_broadcast(&h->changed);
  }
  pthread_mutex_unlock(&h->mu);
  return s;
}
/* A progress event of a call in flight, passed to its sink outside the lock. The
 * call does not end while a thread is handing it frames, and events reach the sink
 * one at a time. */
static ext_status progress_frame(ext_host *h, yyjson_val *frame,
                                 int *delivered) {
  ext_progress_event event;
  yyjson_val *parent;
  if (delivered)
    *delivered = 0;
  ext_status s = ext_progress_frame(frame, &event, &parent);
  if (s)
    return s;
  pthread_mutex_lock(&h->mu);
  pending_call *call =
      find_call(h, yyjson_get_str(parent), yyjson_get_len(parent));
  if (!call || !call->declared) {
    pthread_mutex_unlock(&h->mu);
    return EXT_PROTOCOL;
  }
  call->users++;
  while (call->busy)
    pthread_cond_wait(&h->changed, &h->mu);
  call->busy = 1;
  pthread_mutex_unlock(&h->mu);
  s = ext_progress_sink_report(call->sink, &event, delivered);
  pthread_mutex_lock(&h->mu);
  call->busy = 0;
  call->users--;
  pthread_cond_broadcast(&h->changed);
  pthread_mutex_unlock(&h->mu);
  return s;
}
/* Validates and admits one reverse request. On EXT_OK either *refusal is the line
 * that answers it, or *worker is the admitted request with the Request its hooks
 * receive built. EXT_PROTOCOL for a request that breaks the framing rules.
 * write_deadline, when not NULL, is how long an answer may take to write. */
static ext_status reverse_admit(ext_host *h, yyjson_val *frame,
                                reverse_worker **worker, char **refusal,
                                size_t *refusal_len, int64_t *write_deadline) {
  ext_reverse_request request;
  *worker = NULL;
  *refusal = NULL;
  ext_status s = ext_reverse_request_frame(frame, &request);
  if (s)
    return s;
  size_t payload_len = payload_size(&request);
  const char *code;
  yyjson_val *declared;
  reverse_worker *w = NULL;
  if (write_deadline)
    *write_deadline = mono_ms() + 30000;
  pthread_mutex_lock(&h->mu);
  pending_call *call = request.parent
                           ? find_call(h, yyjson_get_str(request.parent),
                                       yyjson_get_len(request.parent))
                           : NULL;
  s = admit_reverse(h, &request, payload_len, call ? call->id : NULL, &code,
                    &declared);
  if (!s && write_deadline && call)
    *write_deadline = call->deadline;
  if (!s && !code && !(w = new_worker(h, &request, call))) {
    h->reverses--;
    code = EXT_REVERSE_BUSY;
  }
  pthread_mutex_unlock(&h->mu);
  if (s)
    return s;
  if (!code) {
    w->json = reverse_request_json(h, &request, declared, &w->json_len);
    if (w->json)
      *worker = w;
    else {
      pthread_mutex_lock(&h->mu);
      unlink_worker(h, w);
      h->reverses--;
      if (w->call)
        w->call->reverses--;
      pthread_cond_broadcast(&h->changed);
      pthread_mutex_unlock(&h->mu);
      destroy_worker(w);
      code = EXT_REVERSE_BUSY;
    }
  }
  if (code) {
    *refusal = ext_reverse_refusal(yyjson_get_str(request.id),
                                   yyjson_get_len(request.id), code,
                                   refusal_len);
    if (!*refusal)
      return EXT_NOMEM;
  }
  return EXT_OK;
}
/* A reverse request from the reader: refused here, or handed to a thread. */
static ext_status reader_reverse(ext_host *h, yyjson_val *frame) {
  reverse_worker *w;
  char *refusal;
  size_t len = 0;
  int64_t write_deadline;
  ext_call_options plain = {sizeof(plain), 1, NULL, NULL};
  ext_status s = reverse_admit(h, frame, &w, &refusal, &len, &write_deadline);
  if (s)
    return s;
  if (refusal) {
    s = write_frame(h, (const uint8_t *)refusal, len, write_deadline, &plain);
    free(refusal);
    return s;
  }
  w->threaded = 1;
  if (pthread_create(&w->thread, NULL, worker_main, w)) {
    w->threaded = 0;
    pthread_mutex_lock(&h->mu);
    unlink_worker(h, w);
    h->reverses--;
    if (w->call)
      w->call->reverses--;
    pthread_cond_broadcast(&h->changed);
    pthread_mutex_unlock(&h->mu);
    /* The plugin's id is still in the worker: answer busy before freeing it. */
    char *line = ext_reverse_refusal(w->id, w->id_len, EXT_REVERSE_BUSY, &len);
    destroy_worker(w);
    if (!line)
      return EXT_NOMEM;
    s = write_frame(h, (const uint8_t *)line, len, write_deadline, &plain);
    free(line);
    return s;
  }
  return EXT_OK;
}
static ext_status reader_line(ext_host *h, const uint8_t *line, size_t len) {
  yyjson_doc *doc = ext_parse(line, len);
  if (!doc)
    return unparsable(line, len);
  yyjson_val *root = yyjson_doc_get_root(doc), *tag;
  ext_status s;
  if (!yyjson_is_obj(root)) {
    s = EXT_PROTOCOL;
  } else if (!(tag = yyjson_obj_get(root, "frame")) || yyjson_is_null(tag) ||
             yyjson_equals_str(tag, "")) {
    s = reader_response(h, doc, line, len);
    if (!s)
      return EXT_OK; /* the call owns the parsed response now */
  } else if (!yyjson_is_str(tag)) {
    s = EXT_PROTOCOL;
  } else if (yyjson_equals_str(tag, "progress")) {
    s = progress_frame(h, root, NULL);
  } else if (yyjson_equals_str(tag, "reverse_request")) {
    s = reader_reverse(h, root);
  } else {
    s = EXT_PROTOCOL;
  }
  yyjson_doc_free(doc);
  return s;
}
/* Reads the plugin's frames for as long as the host lives: the response and the
 * progress of the call in flight, and reverse requests at any time. It owns the
 * channel's input and h->readbuf from the end of the handshake. A violation or a
 * lost connection fails the session; the call in flight reports why. */
static void *reader_main(void *arg) {
  ext_host *h = arg;
  size_t start = h->rb_start, len = h->rb_len, searched = start;
  ext_status failure = EXT_OK;
  for (;;) {
    reap_workers(h);
    uint8_t *newline =
        len > searched ? memchr(h->readbuf + searched, '\n', len - searched)
                       : NULL;
    if (newline) {
      size_t end = (size_t)(newline - h->readbuf), frame = end - start;
      if (frame > EXT_HOST_MAX_FRAME) {
        failure = EXT_INVALID;
        break;
      }
      if ((failure = reader_line(h, h->readbuf + start, frame)))
        break;
      start = end + 1;
      searched = start;
      continue;
    }
    searched = len;
    if (start) {
      memmove(h->readbuf, h->readbuf + start, len - start);
      len -= start;
      searched = len;
      start = 0;
    }
    if (len == h->capacity) {
      size_t next = h->capacity ? h->capacity * 2 : 4096;
      if (next > EXT_HOST_MAX_FRAME + 1)
        next = EXT_HOST_MAX_FRAME + 1;
      if (next <= len) {
        failure = EXT_INVALID;
        break;
      }
      uint8_t *grown = realloc(h->readbuf, next);
      if (!grown) {
        failure = EXT_NOMEM;
        break;
      }
      h->readbuf = grown;
      h->capacity = next;
    }
    if (closed(h)) {
      failure = EXT_CLOSED;
      break;
    }
    ext_status waited = ext_channel_wait(h->channel, 0, 100);
    if (waited == EXT_TIMEOUT)
      continue;
    ptrdiff_t r = waited == EXT_OK ? ext_channel_read(h->channel,
                                                      h->readbuf + len,
                                                      h->capacity - len)
                                   : -1;
    if (!r)
      continue;
    if (r < 0) {
      failure = closed(h) ? EXT_CLOSED : EXT_IO;
      break;
    }
    len += (size_t)r;
  }
  /* Fail the session, as the Go client does when it fails its connection: the
   * call in flight, if any, reports why, and a later call finds the host closed. */
  pthread_mutex_lock(&h->mu);
  int ended = failure != EXT_CLOSED && !h->closed;
  if (ended && h->calls && !h->reader_failure)
    h->reader_failure = failure;
  pthread_cond_broadcast(&h->changed);
  pthread_mutex_unlock(&h->mu);
  if (ended)
    fail(h);
  return NULL;
}
/* Joins the threads a host started; the host is closed. */
static void stop_threads(ext_host *h) {
  if (h->reader_started) {
    pthread_join(h->reader, NULL);
    h->reader_started = 0;
  }
  pthread_mutex_lock(&h->mu);
  while (h->workers)
    pthread_cond_wait(&h->changed, &h->mu);
  pthread_mutex_unlock(&h->mu);
  reap_workers(h);
}

/* Sends one request and waits for the reader thread to bring the response. The
 * call does not return until the hooks of its call-scoped reverse requests have,
 * so the options they borrow stay valid; they are cancelled the moment the call
 * ends, however it ends. */
static ext_status exchange_threaded(ext_host *h, const uint8_t *p, size_t n,
                                    int64_t deadline,
                                    const ext_call_options *o,
                                    const frame_call *call, ext_buffer *out,
                                    yyjson_doc **parsed) {
  pending_call pending = {0};
  pending.id = call->id;
  pending.declared = call->declared;
  pending.sink = call->sink;
  pending.options = o;
  pending.deadline = deadline;
  *parsed = NULL;
  pthread_mutex_lock(&h->mu);
  ext_status s = h->reader_failure ? h->reader_failure
                 : h->closed       ? EXT_CLOSED
                                   : EXT_OK;
  if (!s)
    link_call(h, &pending);
  pthread_mutex_unlock(&h->mu);
  if (s)
    return s;
  s = write_frame(h, p, n, deadline, o);
  pthread_mutex_lock(&h->mu);
  /* A write that fails because the reader stopped the plugin reports why the
   * reader stopped: it records the cause before it kills the channel. */
  if (s == EXT_IO && h->reader_failure)
    s = h->reader_failure;
  while (!s && !pending.done && !h->closed && !h->reader_failure) {
    if (canceled(o))
      s = EXT_CANCELED;
    else if (mono_ms() >= deadline)
      s = EXT_TIMEOUT;
    else
      wait_changed(h, deadline, o);
  }
  if (!s && !pending.done)
    s = h->reader_failure ? h->reader_failure : EXT_CLOSED;
  end_call(h, &pending);
  pthread_mutex_unlock(&h->mu);
  reap_workers(h);
  if (!s) {
    out->data = pending.response;
    out->len = pending.response_len;
    *parsed = pending.doc;
  } else {
    free(pending.response);
    yyjson_doc_free(pending.doc);
  }
  return s;
}

ext_status ext_host_process_stderr(ext_host *h, uint8_t *buffer,
                                   size_t capacity, size_t *length) {
  if (!h || !length || (capacity && !buffer))
    return EXT_INVALID;
  pthread_mutex_lock(&h->mu);
  ext_channel *channel = h->channel;
  pthread_mutex_unlock(&h->mu);
  *length = channel ? ext_channel_stderr(channel, buffer, capacity) : 0;
  return EXT_OK;
}
static ext_status spawn_child(ext_host *h) {
  ext_launch launch = {h->path,     h->arguments,    h->environment,
                       h->directory, h->capture,      h->stderr_limit};
  ext_channel *channel = NULL;
  pthread_mutex_lock(&h->mu);
  if (h->closed) {
    pthread_mutex_unlock(&h->mu);
    return EXT_CLOSED;
  }
  ext_status s = ext_channel_open(&launch, &channel);
  if (!s)
    h->channel = channel;
  pthread_mutex_unlock(&h->mu);
  return s;
}
ext_status ext_host_start_with_options(ext_host *h, const ext_call_options *o) {
  if (!h || !valid_call(o))
    return EXT_INVALID;
  uint32_t timeout = o->timeout_ms;
  int64_t deadline = mono_ms() + timeout;
  ext_status s = enter(h, deadline, 1, o);
  if (s)
    return s;
  if (h->started) {
    leave(h);
    return EXT_INVALID;
  }
  h->started = 1;
  int64_t stage_start = mono_ms();
  if (h->hooks.verify
          ? h->hooks.verify(h->hooks.user, o, h->descriptor, h->descriptor_len)
          : h->verify(h->user, h->descriptor, h->descriptor_len))
    s = EXT_DENIED;
  observe_event(h, o, "verify", NULL, s, NULL, stage_start);
  stage_start = mono_ms();
  if (!s && canceled(o))
    s = EXT_CANCELED;
  if (!s && mono_ms() >= deadline)
    s = EXT_TIMEOUT;
  if (!s && closed(h))
    s = EXT_CLOSED;
  int connecting = !s;
  if (!s && !(h->extension.connect || h->extension_context.connect))
    s = spawn_child(h);
  ext_buffer response = {0};
  yyjson_doc *doc = NULL;
  if (!s && (h->extension.connect || h->extension_context.connect))
    s = extension_call(h, NULL, 0, deadline, o, &response);
  if (connecting)
    observe_event(h, o, "connect", NULL, s, NULL, stage_start);
  int handshaking = !s;
  stage_start = mono_ms();
  if (!s && !(h->extension.connect || h->extension_context.connect)) {
    char hello[192], stamp[40];
    int64_t wall = ext_wall_ms() + (deadline - mono_ms());
    time_t seconds = (time_t)(wall / 1000);
    struct tm t;
    ext_gmtime(&seconds, &t);
    strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%S", &t);
    int n = snprintf(hello, sizeof(hello),
                     "{\"apiVersion\":\"ext.plugin/"
                     "v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\","
                     "\"deadline\":\"%s.%03lldZ\"}",
                     stamp, (long long)(wall % 1000));
    frame_call hello_call = {h, o, deadline, "hello", 0, 0, NULL};
    s = exchange(h, (uint8_t *)hello, (size_t)n, deadline, o, &hello_call,
                 h->threaded, &response, &doc);
  }
  if (!s) {
    if (!doc)
      doc = ext_parse(response.data, response.len);
    if (!doc)
      s = EXT_INVALID;
    else if (h->extension.connect || h->extension_context.connect) {
      if (!ext_match(yyjson_doc_get_root(h->selection),
                     yyjson_doc_get_root(doc)))
        s = EXT_MISMATCH;
    } else {
      s = ext_response(yyjson_doc_get_root(doc), "hello");
      if (!s && !ext_match(yyjson_doc_get_root(h->selection),
                           yyjson_obj_get(yyjson_doc_get_root(doc), "payload")))
        s = EXT_MISMATCH;
    }
  }
  if (!s && handshaking) {
    /* The handshake matched: reverse calls may be served. */
    pthread_mutex_lock(&h->mu);
    h->serving = 1;
    pthread_mutex_unlock(&h->mu);
  }
  if (!s && canceled(o))
    s = EXT_CANCELED;
  if (!s && mono_ms() >= deadline)
    s = EXT_TIMEOUT;
  if (!s && closed(h))
    s = EXT_CLOSED;
  if (handshaking)
    observe_event(h, o, "handshake", NULL, s, NULL, stage_start);
  yyjson_doc_free(doc);
  ext_buffer_free(&response);
  if (s) {
    fail(h);
  } else {
    pthread_mutex_lock(&h->mu);
    if (!h->closed)
      h->state = EXT_HOST_READY;
    else
      s = EXT_CLOSED;
    pthread_mutex_unlock(&h->mu);
  }
  /* A plugin that declares reverse operations gets a reader thread, which takes
   * over the channel's input once the host is ready: the plugin may speak when no
   * call is in flight. It starts last, so a plugin that leaves right after its
   * handshake fails the session afterwards, as it does in Go, and not the
   * start. */
  if (!s && h->threaded) {
    if (pthread_create(&h->reader, NULL, reader_main, h)) {
      s = EXT_IO;
      fail(h);
    } else {
      h->reader_started = 1;
    }
  }
  leave(h);
  return s;
}
static ext_status invoke_impl(ext_host *h, const uint8_t *input, size_t len,
                              const ext_call_options *o,
                              const ext_progress_options *progress,
                              ext_buffer *out) {
  if (!out)
    return EXT_INVALID;
  out->data = NULL;
  out->len = 0;
  if (!h || !valid_call(o))
    return EXT_INVALID;
  int64_t stage_start = mono_ms();
  uint32_t timeout = o->timeout_ms;
  int64_t deadline = mono_ms() + timeout;
  yyjson_doc *request = ext_parse(input, len);
  if (!request) {
    observe_event(h, o, "invoke", NULL, EXT_INVALID, NULL, stage_start);
    return EXT_INVALID;
  }
  yyjson_val *r = yyjson_doc_get_root(request);
  int64_t wall_deadline;
  ext_status s =
      ext_request(yyjson_doc_get_root(h->selection), r, &wall_deadline);
  if (s) {
    observe_event(h, o, "invoke", r, s, NULL, stage_start);
    yyjson_doc_free(request);
    return s;
  }
  int64_t remaining = wall_deadline - ext_wall_ms();
  if (remaining <= 0) {
    observe_event(h, o, "invoke", r, EXT_TIMEOUT, NULL, stage_start);
    yyjson_doc_free(request);
    return EXT_TIMEOUT;
  }
  if (remaining < (int64_t)timeout)
    deadline = mono_ms() + remaining;
  /* A request that carries an event rate is one that may report progress; the
   * limits it names are applied to what the plugin sends, as a backstop. */
  int declared;
  int64_t events, bytes;
  ext_request_progress(r, &declared, &events, &bytes);
  ext_progress_sink sink;
  if (declared)
    ext_progress_sink_init(&sink, progress ? progress->deliver : NULL,
                           progress ? progress->user : NULL, events, bytes);
  frame_call call = {h,
                     o,
                     deadline,
                     yyjson_get_str(yyjson_obj_get(r, "id")),
                     declared,
                     1,
                     &sink};
  s = enter(h, deadline, 0, o);
  if (s) {
    observe_event(h, o, "invoke", r, s, NULL, stage_start);
    yyjson_doc_free(request);
    return s;
  }
  if (!h->started) {
    s = EXT_INVALID;
    goto done;
  }
  if (h->hooks.authorize ? h->hooks.authorize(h->hooks.user, o, input, len)
                         : h->authorize(h->user, input, len)) {
    s = EXT_DENIED;
    goto done;
  }
  if (closed(h)) {
    s = EXT_CLOSED;
    goto done;
  }
  if (canceled(o)) {
    s = EXT_CANCELED;
    goto done;
  }
  if (mono_ms() >= deadline) {
    s = EXT_TIMEOUT;
    goto done;
  }
  size_t compact_len;
  char *compact = yyjson_write(request, 0, &compact_len);
  if (!compact) {
    s = EXT_NOMEM;
    goto done;
  }
  yyjson_doc *response = NULL;
  int extension_backend = h->extension.invoke || h->extension_context.invoke;
  /* A backend that hands over its frames names this call in them: it is on the
   * list for as long as the backend runs, and ends only when every thread that
   * was handing it frames, and every hook of its requests, has finished. */
  pending_call registered = {0};
  if (extension_backend && h->frames) {
    registered.id = call.id;
    registered.declared = declared;
    registered.sink = &sink;
    registered.options = o;
    registered.deadline = deadline;
    pthread_mutex_lock(&h->mu);
    link_call(h, &registered);
    pthread_mutex_unlock(&h->mu);
  }
  s = extension_backend
          ? extension_call(h, (const uint8_t *)compact, compact_len, deadline,
                           o, out)
      : h->threaded
          ? exchange_threaded(h, (const uint8_t *)compact, compact_len,
                              deadline, o, &call, out, &response)
          : exchange(h, (const uint8_t *)compact, compact_len, deadline, o,
                     &call, 0, out, &response);
  free(compact);
  if (extension_backend && h->frames) {
    pthread_mutex_lock(&h->mu);
    end_call(h, &registered);
    pthread_mutex_unlock(&h->mu);
  }
  if (!s) {
    if (!response)
      response = ext_parse(out->data, out->len);
    if (!response)
      s = EXT_INVALID;
    else
      s = ext_response(yyjson_doc_get_root(response),
                       yyjson_get_str(yyjson_obj_get(r, "id")));
  }
  yyjson_doc_free(response);
  if (!s && canceled(o))
    s = EXT_CANCELED;
  if (!s && mono_ms() >= deadline)
    s = EXT_TIMEOUT;
  if (!s && closed(h))
    s = EXT_CLOSED;
  if (s) {
    ext_buffer_free(out);
    fail(h);
  }
done:
  /* The marker for events never reported comes last, whatever the outcome. */
  if (declared)
    ext_progress_sink_finish(&sink);
  if (h->hooks.observe) {
    yyjson_doc *observed = out->data ? ext_parse(out->data, out->len) : NULL;
    observe_event(h, o, "invoke", r, s,
                  observed ? yyjson_doc_get_root(observed) : NULL, stage_start);
    yyjson_doc_free(observed);
  }
  yyjson_doc_free(request);
  leave(h);
  return s;
}

ext_status ext_host_invoke_with_options(ext_host *h, const uint8_t *input,
                                        size_t len, const ext_call_options *o,
                                        ext_buffer *out) {
  return invoke_impl(h, input, len, o, NULL, out);
}

ext_status ext_host_start(ext_host *h, uint32_t timeout) {
  ext_call_options o = {sizeof(o), timeout, NULL, NULL};
  return ext_host_start_with_options(h, &o);
}
ext_status ext_host_invoke(ext_host *h, const uint8_t *data, size_t len,
                           uint32_t timeout, ext_buffer *out) {
  ext_call_options o = {sizeof(o), timeout, NULL, NULL};
  return ext_host_invoke_with_options(h, data, len, &o, out);
}
ext_status ext_host_drain(ext_host *h, uint32_t timeout) {
  ext_call_options o = {sizeof(o), timeout, NULL, NULL};
  return ext_host_drain_with_options(h, &o);
}

/* Builds authoritative request metadata from the immutable selection. */
static ext_status call_impl(ext_host *h, const uint8_t *input, size_t len,
                            const ext_call_options *options,
                            const ext_progress_options *progress,
                            ext_buffer *out) {
  if (!out)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  if (!h || !valid_call(options))
    return EXT_INVALID;
  if (progress && (progress->struct_size != sizeof(*progress) ||
                   progress->events_per_second > (1u << 20) ||
                   progress->bytes_per_second > (1u << 30)))
    return EXT_INVALID;
  int64_t started = mono_ms();
  yyjson_doc *doc = ext_parse(input, len);
  if (!doc)
    return EXT_INVALID;
  yyjson_val *call = yyjson_doc_get_root(doc),
             *ref = yyjson_obj_get(call, "contract"),
             *name = yyjson_obj_get(call, "operation");
  ext_status status = EXT_INVALID;
  /* Only the shape is checked here. A name or version no validated descriptor
   * can hold is an unsupported operation, as in the Go session, not invalid. */
  if (!EXT_FIELDS(call, "contract", "operation", "payload") ||
      !EXT_FIELDS(ref, "name", "version") ||
      !yyjson_is_str(yyjson_obj_get(ref, "name")) ||
      !yyjson_is_str(yyjson_obj_get(ref, "version")) || !yyjson_is_str(name))
    goto done;
  /* A call that is refused before it has a request is reported without an id,
   * as the Go session reports it, and in the same order: a caller that is
   * already canceled, an operation the descriptor does not list, then a
   * session that is no longer ready. */
  if (canceled(options)) {
    observe_event(h, options, "invoke", call, EXT_CANCELED, NULL, started);
    status = EXT_CANCELED;
    goto done;
  }
  yyjson_val *operation =
      ext_lookup(yyjson_doc_get_root(h->selection), ref, name);
  if (!operation) {
    observe_event(h, options, "invoke", call, EXT_UNSUPPORTED, NULL, started);
    status = EXT_UNSUPPORTED;
    goto done;
  }
  pthread_mutex_lock(&h->mu);
  ext_status gate = h->closed || h->state == EXT_HOST_FAILED ||
                            h->state == EXT_HOST_CLOSED
                        ? EXT_CLOSED
                    : h->state == EXT_HOST_DRAINING ? EXT_DRAINING
                                                    : EXT_OK;
  pthread_mutex_unlock(&h->mu);
  if (gate) {
    observe_event(h, options, "invoke", call, gate, NULL, started);
    status = gate;
    goto done;
  }
  pthread_mutex_lock(&h->mu);
  if (h->next == UINT64_MAX) {
    pthread_mutex_unlock(&h->mu);
    goto done;
  }
  uint64_t sequence = ++h->next;
  pthread_mutex_unlock(&h->mu);
  char id[32], stamp[40], date[48];
  snprintf(id, sizeof(id), "%llu", (unsigned long long)sequence);
  int64_t wall = ext_wall_ms() + options->timeout_ms;
  time_t seconds = (time_t)(wall / 1000);
  struct tm t;
  ext_gmtime(&seconds, &t);
  strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%S", &t);
  snprintf(date, sizeof(date), "%s.%03lldZ", stamp, (long long)(wall % 1000));
  yyjson_mut_doc *request = yyjson_mut_doc_new(NULL);
  if (!request) {
    status = EXT_NOMEM;
    goto done;
  }
  yyjson_mut_val *root = yyjson_mut_obj(request);
  yyjson_mut_doc_set_root(request, root);
  int ok =
      root &&
      yyjson_mut_obj_add_str(request, root, "apiVersion", "ext.plugin/v1") &&
      yyjson_mut_obj_add_str(request, root, "id", id) &&
      yyjson_mut_obj_add_str(request, root, "deadline", date);
  const char *keys[] = {"plugin", "contract", "operation", "surface",
                        "payload"};
  yyjson_val *values[] = {
      yyjson_obj_get(yyjson_doc_get_root(h->selection), "identity"), ref, name,
      yyjson_obj_get(operation, "surface"), yyjson_obj_get(call, "payload")};
  for (size_t i = 0; i < 5 && ok; i++)
    if (values[i]) {
      yyjson_mut_val *copy = yyjson_val_mut_copy(request, values[i]);
      ok = copy && yyjson_mut_obj_add_val(request, root, keys[i], copy);
    }
  /* An operation that declares progress is called with the event rate both
   * ends apply, the defaults where the caller named none. */
  if (ok && yyjson_is_true(yyjson_obj_get(operation, "progress"))) {
    yyjson_mut_val *limits = yyjson_mut_obj(request);
    ok = limits &&
         yyjson_mut_obj_add_uint(
             request, limits, "eventsPerSecond",
             progress && progress->events_per_second
                 ? progress->events_per_second
                 : EXT_PROGRESS_DEFAULT_EVENTS) &&
         yyjson_mut_obj_add_uint(
             request, limits, "bytesPerSecond",
             progress && progress->bytes_per_second
                 ? progress->bytes_per_second
                 : EXT_PROGRESS_DEFAULT_BYTES) &&
         yyjson_mut_obj_add_val(request, root, "progress", limits);
  }
  size_t count = 0;
  char *bytes = ok ? yyjson_mut_write(request, 0, &count) : NULL;
  status = bytes ? invoke_impl(h, (const uint8_t *)bytes, count, options,
                               progress, out)
                 : EXT_NOMEM;
  free(bytes);
  yyjson_mut_doc_free(request);
done:
  yyjson_doc_free(doc);
  return status;
}

ext_status ext_host_call(ext_host *h, const uint8_t *input, size_t len,
                         const ext_call_options *options, ext_buffer *out) {
  return call_impl(h, input, len, options, NULL, out);
}
ext_status ext_host_call_progress(ext_host *h, const uint8_t *input, size_t len,
                                  const ext_call_options *options,
                                  const ext_progress_options *progress,
                                  ext_buffer *out) {
  return call_impl(h, input, len, options, progress, out);
}

/* ---- frames from an extension backend ---- */

ext_status ext_host_backend_progress(ext_host *h, const uint8_t *frame,
                                     size_t len, int *delivered) {
  if (delivered)
    *delivered = 0;
  if (!h || !h->frames || !frame)
    return EXT_INVALID;
  yyjson_doc *doc = ext_parse(frame, len);
  if (!doc)
    return EXT_PROTOCOL;
  ext_status s = progress_frame(h, yyjson_doc_get_root(doc), delivered);
  yyjson_doc_free(doc);
  return s;
}
ext_status ext_host_backend_reverse_admit(ext_host *h, const uint8_t *frame,
                                          size_t len,
                                          ext_reverse_ticket **ticket,
                                          ext_buffer *answer) {
  if (!ticket || !answer)
    return EXT_INVALID;
  *ticket = NULL;
  *answer = (ext_buffer){0};
  if (!h || !h->frames || !frame)
    return EXT_INVALID;
  yyjson_doc *doc = ext_parse(frame, len);
  if (!doc)
    return EXT_PROTOCOL;
  reverse_worker *w;
  char *refusal;
  size_t refusal_len = 0;
  ext_status s = reverse_admit(h, yyjson_doc_get_root(doc), &w, &refusal,
                               &refusal_len, NULL);
  yyjson_doc_free(doc);
  if (s)
    return s;
  if (refusal) {
    answer->data = (uint8_t *)refusal;
    answer->len = refusal_len;
  } else {
    *ticket = (ext_reverse_ticket *)w;
  }
  return EXT_OK;
}
ext_status ext_host_backend_reverse_run(ext_host *h, ext_reverse_ticket *ticket,
                                        const ext_cancel *caller,
                                        ext_buffer *answer) {
  if (!answer)
    return EXT_INVALID;
  *answer = (ext_buffer){0};
  reverse_worker *w = (reverse_worker *)ticket;
  if (!h || !w || w->host != h)
    return EXT_INVALID;
  /* The caller's own signal ends the hooks early, as the engine's does. */
  ext_cancel_link(w->cancel, caller);
  size_t len = 0;
  int64_t until;
  char *line = reverse_line(w, &len, &until);
  finish_worker(w);
  if (!line)
    return EXT_NOMEM;
  answer->data = (uint8_t *)line;
  answer->len = len;
  return EXT_OK;
}
