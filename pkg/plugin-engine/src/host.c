#define _GNU_SOURCE
#include "wire.h"
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <spawn.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static int canceled(const ext_call_options *o) {
  return ext_cancel_is_signaled(o->cancel);
}
static int valid_call(const ext_call_options *o) {
  return o && o->struct_size == sizeof(*o) && o->timeout_ms;
}
struct ext_host {
  pthread_mutex_t mu;
  pthread_cond_t changed;
  int active, running, started, closed, fd;
  ext_host_state state;
  ext_host_hooks hooks;
  ext_backend_extension extension;
  ext_backend_extension_context extension_context;
  pid_t pid;
  uint64_t next;
  char *path;
  char **arguments, **environment;
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
    if (!process->executable || process->executable[0] != '/' ||
        strlen(process->executable) > 4096)
      return EXT_INVALID;
  } else if (b->kind == EXT_BACKEND_JSONLINE_PROCESS) {
    if (!b->config || b->config_size != sizeof(*process))
      return EXT_INVALID;
    process = b->config;
    if (!process->executable || process->executable[0] != '/')
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
  ext_host *h = calloc(1, sizeof(*h));
  if (!h) {
    yyjson_doc_free(d);
    return EXT_NOMEM;
  }
  h->fd = -1;
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
#ifndef __APPLE__
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
    ext_status status =
        copy_strings(process_config ? process_config->arguments : NULL,
                     process_config ? process_config->argument_count : 0,
                     h->path, 0, &h->arguments);
    if (!status)
      status =
          copy_strings(process_config ? process_config->environment : NULL,
                       process_config ? process_config->environment_count : 0,
                       NULL, 1, &h->environment);
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
    if (h->fd >= 0)
      shutdown(h->fd, SHUT_RDWR);
    if (h->pid > 0)
      kill(h->pid, SIGKILL);
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
  if (h->pid > 0) {
    int status;
    while (waitpid(h->pid, &status, 0) < 0 && errno == EINTR) {
    }
  }
  if (h->fd >= 0)
    close(h->fd);
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
static ext_status ready(ext_host *h, short events, int64_t deadline,
                        const ext_call_options *o) {
  for (;;) {
    if (canceled(o))
      return EXT_CANCELED;
    if (closed(h))
      return EXT_CLOSED;
    int64_t ms = deadline - mono_ms();
    if (ms <= 0)
      return EXT_TIMEOUT;
    struct pollfd p = {h->fd, events, 0};
    if (o->cancel && ms > 10)
      ms = 10;
    if (ms > INT_MAX)
      ms = INT_MAX;
    int r = poll(&p, 1, (int)ms);
    if (r < 0 && errno == EINTR)
      continue;
    if (r < 0)
      return EXT_IO;
    if (!r)
      continue;
    if (p.revents & events)
      return EXT_OK;
    return closed(h) ? EXT_CLOSED : EXT_IO;
  }
}
static ext_status write_bytes(ext_host *h, const uint8_t *p, size_t n,
                              int64_t deadline, const ext_call_options *o) {
  while (n) {
    ext_status s = ready(h, POLLOUT, deadline, o);
    if (s)
      return s;
#ifdef MSG_NOSIGNAL
    ssize_t r = send(h->fd, p, n, MSG_NOSIGNAL);
#else
    ssize_t r = send(h->fd, p, n, 0);
#endif
    if (r < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK))
      continue;
    if (r <= 0)
      return EXT_IO;
    p += r;
    n -= (size_t)r;
  }
  return EXT_OK;
}
static ext_status exchange(ext_host *h, const uint8_t *p, size_t n,
                           int64_t deadline, const ext_call_options *o,
                           ext_buffer *out) {
  ext_status s = write_bytes(h, p, n, deadline, o);
  if (!s)
    s = write_bytes(h, (const uint8_t *)"\n", 1, deadline, o);
  if (s)
    return s;
  size_t len = 0;
  for (;;) {
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
    s = ready(h, POLLIN, deadline, o);
    if (s)
      return s;
    ssize_t r = recv(h->fd, h->readbuf + len, h->capacity - len, 0);
    if (r < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK))
      continue;
    if (r <= 0)
      return EXT_IO;
    uint8_t *end = memchr(h->readbuf + len, '\n', (size_t)r);
    len += (size_t)r;
    if (end) {
      size_t frame = (size_t)(end - h->readbuf);
      if (frame + 1 != len || frame > EXT_HOST_MAX_FRAME)
        return EXT_INVALID;
      out->data = malloc(frame ? frame : 1);
      if (!out->data)
        return EXT_NOMEM;
      memcpy(out->data, h->readbuf, frame);
      out->len = frame;
      return EXT_OK;
    }
  }
}
static ext_status spawn_child(ext_host *h) {
  int fds[2];
  if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds))
    return EXT_IO;
  /* Keep both endpoints above stderr, even when the embedding app closed its
   * standard descriptors. Preserve caller descriptors across posix_spawn. */
  for (int i = 0; i < 2; i++) {
    if (fds[i] <= 2) {
      int next = fcntl(fds[i], F_DUPFD_CLOEXEC, 3);
      close(fds[i]);
      fds[i] = next;
    }
    if (fds[i] < 0 || fcntl(fds[i], F_SETFD, FD_CLOEXEC) < 0) {
      if (fds[0] >= 0)
        close(fds[0]);
      if (fds[1] >= 0)
        close(fds[1]);
      return EXT_IO;
    }
  }
#ifdef SO_NOSIGPIPE
  int yes = 1;
  if (setsockopt(fds[0], SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof(yes))) {
    close(fds[0]);
    close(fds[1]);
    return EXT_IO;
  }
#endif
  posix_spawn_file_actions_t actions;
  int err = posix_spawn_file_actions_init(&actions);
  if (err) {
    close(fds[0]);
    close(fds[1]);
    return EXT_IO;
  }
  if (!(err = posix_spawn_file_actions_adddup2(&actions, fds[1], STDIN_FILENO)))
    err = posix_spawn_file_actions_adddup2(&actions, fds[1], STDOUT_FILENO);
  if (!err)
    err = posix_spawn_file_actions_addclose(&actions, fds[0]);
  if (!err)
    err = posix_spawn_file_actions_addclose(&actions, fds[1]);
  pthread_mutex_lock(&h->mu);
  if (h->closed)
    err = ECANCELED;
  if (!err)
    err = posix_spawn(&h->pid, h->path, &actions, NULL, h->arguments,
                      h->environment);
  if (!err)
    h->fd = fds[0];
  pthread_mutex_unlock(&h->mu);
  posix_spawn_file_actions_destroy(&actions);
  close(fds[1]);
  if (err) {
    close(fds[0]);
    return err == ECANCELED ? EXT_CLOSED : EXT_IO;
  }
  if (fcntl(h->fd, F_SETFL, fcntl(h->fd, F_GETFL) | O_NONBLOCK) < 0)
    return EXT_IO;
  return EXT_OK;
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
    gmtime_r(&seconds, &t);
    strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%S", &t);
    int n = snprintf(hello, sizeof(hello),
                     "{\"apiVersion\":\"ext.plugin/"
                     "v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\","
                     "\"deadline\":\"%s.%03lldZ\"}",
                     stamp, (long long)(wall % 1000));
    s = exchange(h, (uint8_t *)hello, (size_t)n, deadline, o, &response);
  }
  if (!s) {
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
ext_status ext_host_invoke_with_options(ext_host *h, const uint8_t *input,
                                        size_t len, const ext_call_options *o,
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
  s = (h->extension.invoke || h->extension_context.invoke)
          ? extension_call(h, (const uint8_t *)compact, compact_len, deadline,
                           o, out)
          : exchange(h, (const uint8_t *)compact, compact_len, deadline, o,
                     out);
  free(compact);
  if (!s) {
    yyjson_doc *response = ext_parse(out->data, out->len);
    if (!response)
      s = EXT_INVALID;
    else {
      s = ext_response(yyjson_doc_get_root(response),
                       yyjson_get_str(yyjson_obj_get(r, "id")));
      yyjson_doc_free(response);
    }
  }
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
ext_status ext_host_call(ext_host *h, const uint8_t *input, size_t len,
                         const ext_call_options *options, ext_buffer *out) {
  if (!out)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  if (!h || !valid_call(options))
    return EXT_INVALID;
  yyjson_doc *doc = ext_parse(input, len);
  if (!doc)
    return EXT_INVALID;
  yyjson_val *call = yyjson_doc_get_root(doc),
             *ref = yyjson_obj_get(call, "contract"),
             *name = yyjson_obj_get(call, "operation");
  ext_status status = EXT_INVALID;
  if (!EXT_FIELDS(call, "contract", "operation", "payload") ||
      !EXT_FIELDS(ref, "name", "version") || !ext_ref(ref) ||
      !ext_text(name, 0, 0))
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
  gmtime_r(&seconds, &t);
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
  size_t count = 0;
  char *bytes = ok ? yyjson_mut_write(request, 0, &count) : NULL;
  status = bytes ? ext_host_invoke_with_options(h, (const uint8_t *)bytes,
                                                count, options, out)
                 : EXT_NOMEM;
  free(bytes);
  yyjson_mut_doc_free(request);
done:
  yyjson_doc_free(doc);
  return status;
}
