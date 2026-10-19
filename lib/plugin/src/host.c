#define _GNU_SOURCE
#include "channel.h"
#include "platform.h"
#include "progress.h"
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
struct ext_host {
  pthread_mutex_t mu;
  pthread_cond_t changed;
  int active, running, started, closed;
  ext_host_state state;
  ext_host_hooks hooks;
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
        (extension->flags & ~EXT_BACKEND_CONCURRENT) || !extension->connect ||
        !extension->invoke || !extension->close || !extension->release)
      return EXT_INVALID;
  } else if (b->kind == EXT_BACKEND_EXTENSION_CONTEXT) {
    if (!b->config || b->config_size != sizeof(*extension_context))
      return EXT_INVALID;
    extension_context = b->config;
    if (extension_context->struct_size != sizeof(*extension_context) ||
        (extension_context->flags & ~EXT_BACKEND_CONCURRENT) ||
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
   * that owns the child's pipes carries progress frames; reverse requests are not
   * carried yet, and an extension backend hands the engine one response at a
   * time, so it carries neither. */
  if (ext_descriptor_declares_reverse(yyjson_doc_get_root(d)) ||
      ((extension || extension_context) &&
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
    pthread_cond_broadcast(&h->changed);
  }
  pthread_mutex_unlock(&h->mu);
  if (notify && h->extension.close)
    h->extension.close(h->extension.user);
  if (notify && h->extension_context.close)
    h->extension_context.close(h->extension_context.user);
}
void ext_host_destroy(ext_host *h) {
  if (!h)
    return;
  ext_host_close(h);
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
  if (s < EXT_OK || s > EXT_CANCELED)
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
  const char *id;
  int declared;
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
/* Routes one line the plugin sent. A line without a "frame" tag is the response;
 * *response says so, and the caller validates it. */
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
  /* Reverse requests are not carried yet; a descriptor that declares them is
   * refused when the host is created. */
  if (yyjson_equals_str(tag, "reverse_request"))
    return EXT_UNSUPPORTED;
  return EXT_PROTOCOL;
}
/* Writes one request line and reads lines until the response. Progress frames
 * that arrive first are validated, limited and passed on through call. The
 * response must be the last thing the plugin has sent. On success *parsed is the
 * response, parsed, and out holds its bytes. */
static ext_status exchange(ext_host *h, const uint8_t *p, size_t n,
                           int64_t deadline, const ext_call_options *o,
                           const frame_call *call, ext_buffer *out,
                           yyjson_doc **parsed) {
  *parsed = NULL;
  ext_status s = write_bytes(h, p, n, deadline, o);
  if (!s)
    s = write_bytes(h, (const uint8_t *)"\n", 1, deadline, o);
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
        else if (end + 1 != len)
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
    frame_call hello_call = {"hello", 0, NULL};
    s = exchange(h, (uint8_t *)hello, (size_t)n, deadline, o, &hello_call,
                 &response, &doc);
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
  frame_call call = {yyjson_get_str(yyjson_obj_get(r, "id")), declared, &sink};
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
  s = (h->extension.invoke || h->extension_context.invoke)
          ? extension_call(h, (const uint8_t *)compact, compact_len, deadline,
                           o, out)
          : exchange(h, (const uint8_t *)compact, compact_len, deadline, o,
                     &call, out, &response);
  free(compact);
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
  yyjson_val *operation =
      ext_lookup(yyjson_doc_get_root(h->selection), ref, name);
  if (!operation) {
    status = EXT_UNSUPPORTED;
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
