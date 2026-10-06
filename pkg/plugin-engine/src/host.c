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

static int canceled(const ctx_call_options *o) {
  return ctx_cancel_is_signaled(o->cancel);
}
static int valid_call(const ctx_call_options *o) {
  return o && o->struct_size == sizeof(*o) && o->timeout_ms;
}
struct ctx_host {
  pthread_mutex_t mu;
  pthread_cond_t changed;
  int active, running, started, closed, fd;
  ctx_host_state state;
  ctx_host_hooks hooks;
  ctx_backend_extension extension;
  ctx_backend_extension_context extension_context;
  pid_t pid;
  uint64_t next;
  char *path;
  char **arguments, **environment;
  yyjson_doc *selection;
  uint8_t *descriptor;
  size_t descriptor_len;
  ctx_policy verify, authorize;
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
static ctx_status copy_strings(const char *const *input, size_t count,
                               const char *prefix, int environment,
                               char ***out) {
  if (count > 256 || (count && !input))
    return CTX_INVALID;
  size_t offset = prefix ? 1 : 0;
  char **copy = calloc(count + offset + 1, sizeof(*copy));
  if (!copy)
    return CTX_NOMEM;
  if (prefix && !(copy[0] = strdup(prefix))) {
    free(copy);
    return CTX_NOMEM;
  }
  ctx_status status = CTX_OK;
  for (size_t i = 0; i < count; i++) {
    if (!input[i] || strlen(input[i]) > (environment ? 8192 : 4096)) {
      status = CTX_INVALID;
      break;
    }
    if (environment) {
      const char *equal = strchr(input[i], '=');
      if (!equal || equal == input[i]) {
        status = CTX_INVALID;
        break;
      }
      size_t key = (size_t)(equal - input[i]);
      for (size_t j = 0; j < i; j++)
        if (!strncmp(input[i], input[j], key) && input[j][key] == '=') {
          status = CTX_INVALID;
          break;
        }
      if (status)
        break;
    }
    if (!(copy[offset + i] = strdup(input[i]))) {
      status = CTX_NOMEM;
      break;
    }
  }
  if (status)
    free_strings(copy);
  else
    *out = copy;
  return status;
}
ctx_status ctx_host_create(const ctx_host_options *o,
                           const ctx_backend_options *b, ctx_host **out) {
  if (!out)
    return CTX_INVALID;
  *out = NULL;
  if (!o || o->abi_version != CTX_HOST_ABI_VERSION ||
      o->struct_size != sizeof(*o) || !o->verify || !o->authorize || !b ||
      b->struct_size != sizeof(*b))
    return CTX_INVALID;
  const ctx_jsonline_process_options *process = NULL;
  ctx_jsonline_process_options compatible_process;
  const ctx_jsonline_process_config *process_config = NULL;
  const ctx_backend_extension *extension = NULL;
  const ctx_backend_extension_context *extension_context = NULL;
  if (b->kind == CTX_BACKEND_JSONLINE_PROCESS_CONFIG) {
    if (!b->config || b->config_size != sizeof(*process_config))
      return CTX_INVALID;
    process_config = b->config;
    if (process_config->struct_size != sizeof(*process_config))
      return CTX_INVALID;
    compatible_process.executable = process_config->executable;
    process = &compatible_process;
    if (!process->executable || process->executable[0] != '/' ||
        strlen(process->executable) > 4096)
      return CTX_INVALID;
  } else if (b->kind == CTX_BACKEND_JSONLINE_PROCESS) {
    if (!b->config || b->config_size != sizeof(*process))
      return CTX_INVALID;
    process = b->config;
    if (!process->executable || process->executable[0] != '/')
      return CTX_INVALID;
  } else if (b->kind == CTX_BACKEND_EXTENSION) {
    if (!b->config || b->config_size != sizeof(*extension))
      return CTX_INVALID;
    extension = b->config;
    if (extension->struct_size != sizeof(*extension) ||
        (extension->flags & ~CTX_BACKEND_CONCURRENT) || !extension->connect ||
        !extension->invoke || !extension->close || !extension->release)
      return CTX_INVALID;
  } else if (b->kind == CTX_BACKEND_EXTENSION_CONTEXT) {
    if (!b->config || b->config_size != sizeof(*extension_context))
      return CTX_INVALID;
    extension_context = b->config;
    if (extension_context->struct_size != sizeof(*extension_context) ||
        (extension_context->flags & ~CTX_BACKEND_CONCURRENT) ||
        !extension_context->connect || !extension_context->invoke ||
        !extension_context->close || !extension_context->release)
      return CTX_INVALID;
  } else {
    return CTX_UNSUPPORTED;
  }
  yyjson_doc *d = ctx_parse(o->descriptor, o->descriptor_len);
  if (!d || !ctx_descriptor(yyjson_doc_get_root(d))) {
    yyjson_doc_free(d);
    return CTX_INVALID;
  }
  ctx_host *h = calloc(1, sizeof(*h));
  if (!h) {
    yyjson_doc_free(d);
    return CTX_NOMEM;
  }
  h->fd = -1;
  h->path = process ? strdup(process->executable) : NULL;
  h->descriptor = malloc(o->descriptor_len);
  if ((process && !h->path) || !h->descriptor) {
    free(h->path);
    free(h->descriptor);
    free(h);
    yyjson_doc_free(d);
    return CTX_NOMEM;
  }
  if (pthread_mutex_init(&h->mu, NULL)) {
    free(h->path);
    free(h->descriptor);
    free(h);
    yyjson_doc_free(d);
    return CTX_IO;
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
    return CTX_IO;
  }
  memcpy(h->descriptor, o->descriptor, o->descriptor_len);
  h->descriptor_len = o->descriptor_len;
  h->selection = d;
  h->verify = o->verify;
  h->authorize = o->authorize;
  h->user = o->user;
  if (process) {
    ctx_status status =
        copy_strings(process_config ? process_config->arguments : NULL,
                     process_config ? process_config->argument_count : 0,
                     h->path, 0, &h->arguments);
    if (!status)
      status =
          copy_strings(process_config ? process_config->environment : NULL,
                       process_config ? process_config->environment_count : 0,
                       NULL, 1, &h->environment);
    if (status) {
      ctx_host_destroy(h);
      return status;
    }
  }
  if (extension)
    h->extension = *extension;
  if (extension_context)
    h->extension_context = *extension_context;
  *out = h;
  return CTX_OK;
}
ctx_status ctx_host_set_hooks(ctx_host *h, const ctx_host_hooks *hooks) {
  if (!h || !hooks || hooks->struct_size != sizeof(*hooks))
    return CTX_INVALID;
  pthread_mutex_lock(&h->mu);
  ctx_status s = (h->state == CTX_HOST_CREATED && !h->active && !h->started)
                     ? CTX_OK
                     : CTX_INVALID;
  if (!s)
    h->hooks = *hooks;
  pthread_mutex_unlock(&h->mu);
  return s;
}
static void observe_event(ctx_host *h, const ctx_call_options *options,
                          const char *stage, yyjson_val *request,
                          ctx_status status, yyjson_val *response,
                          int64_t start) {
  if (!h->hooks.observe)
    return;
  static const char *codes[] = {
      "ok",        "invalid",           "denied",  "mismatch", "unsupported",
      "closed",    "deadline_exceeded", "failed",  "failed",   "draining",
      "not_found", "ambiguous",         "canceled"};
  const char *code =
      status >= 0 && status <= CTX_CANCELED ? codes[status] : "failed";
  yyjson_val *remote =
      yyjson_obj_get(yyjson_obj_get(response, "error"), "code");
  if (!status && ctx_text(remote, 0, 0))
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
    int valid = i == 0 ? (CTX_FIELDS(v, "name", "version") && ctx_ref(v))
                       : ctx_text(v, i == 2, 0);
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
void ctx_host_close(ctx_host *h) {
  if (!h)
    return;
  int notify = 0;
  pthread_mutex_lock(&h->mu);
  if (!h->closed) {
    h->closed = 1;
    if (h->state != CTX_HOST_FAILED)
      h->state = CTX_HOST_CLOSED;
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
void ctx_host_destroy(ctx_host *h) {
  if (!h)
    return;
  ctx_host_close(h);
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
static void wait_changed(ctx_host *h, int64_t deadline,
                         const ctx_call_options *o) {
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
static ctx_status enter(ctx_host *h, int64_t deadline, int starting,
                        const ctx_call_options *o) {
  pthread_mutex_lock(&h->mu);
  ctx_status status = canceled(o)                                 ? CTX_CANCELED
                      : h->closed                                 ? CTX_CLOSED
                      : h->state == CTX_HOST_DRAINING             ? CTX_DRAINING
                      : (!starting && h->state != CTX_HOST_READY) ? CTX_INVALID
                      : mono_ms() >= deadline                     ? CTX_TIMEOUT
                                                                  : CTX_OK;
  if (status) {
    pthread_mutex_unlock(&h->mu);
    return status;
  }
  /* Admission precedes serialized backend queueing. Drain must wait for every
   * admitted call, including callers waiting for the backend gate. */
  h->active++;
  uint32_t flags = h->extension.flags | h->extension_context.flags;
  while (h->running && !h->closed &&
         (starting || !(flags & CTX_BACKEND_CONCURRENT))) {
    if (canceled(o) || mono_ms() >= deadline)
      break;
    wait_changed(h, deadline, o);
  }
  status = canceled(o)             ? CTX_CANCELED
           : h->closed             ? CTX_CLOSED
           : mono_ms() >= deadline ? CTX_TIMEOUT
                                   : CTX_OK;
  if (status) {
    h->active--;
    pthread_cond_broadcast(&h->changed);
  } else
    h->running++;
  pthread_mutex_unlock(&h->mu);
  return status;
}
static void leave(ctx_host *h) {
  pthread_mutex_lock(&h->mu);
  h->active--;
  h->running--;
  pthread_cond_broadcast(&h->changed);
  pthread_mutex_unlock(&h->mu);
}
static void fail(ctx_host *h) {
  pthread_mutex_lock(&h->mu);
  if (!h->closed)
    h->state = CTX_HOST_FAILED;
  pthread_mutex_unlock(&h->mu);
  ctx_host_close(h);
}
ctx_host_state ctx_host_get_state(ctx_host *h) {
  if (!h)
    return CTX_HOST_CLOSED;
  pthread_mutex_lock(&h->mu);
  ctx_host_state state = h->state;
  pthread_mutex_unlock(&h->mu);
  return state;
}
ctx_status ctx_host_drain_with_options(ctx_host *h, const ctx_call_options *o) {
  if (!h || !valid_call(o))
    return CTX_INVALID;
  if (canceled(o))
    return CTX_CANCELED;
  int64_t deadline = mono_ms() + o->timeout_ms;
  pthread_mutex_lock(&h->mu);
  if (h->closed) {
    pthread_mutex_unlock(&h->mu);
    return CTX_OK;
  }
  if (h->state != CTX_HOST_READY) {
    ctx_status s = h->state == CTX_HOST_DRAINING ? CTX_DRAINING : CTX_INVALID;
    pthread_mutex_unlock(&h->mu);
    return s;
  }
  h->state = CTX_HOST_DRAINING;
  pthread_cond_broadcast(&h->changed);
  while (h->active && !h->closed) {
    if (canceled(o) || mono_ms() >= deadline) {
      h->state = CTX_HOST_READY;
      pthread_cond_broadcast(&h->changed);
      pthread_mutex_unlock(&h->mu);
      return canceled(o) ? CTX_CANCELED : CTX_TIMEOUT;
    }
    wait_changed(h, deadline, o);
  }
  if (!h->closed && canceled(o)) {
    h->state = CTX_HOST_READY;
    pthread_cond_broadcast(&h->changed);
    pthread_mutex_unlock(&h->mu);
    return CTX_CANCELED;
  }
  pthread_mutex_unlock(&h->mu);
  ctx_host_close(h);
  return CTX_OK;
}
/* The result sink prevents foreign allocator ownership from crossing the ABI.
 */
typedef struct {
  ctx_buffer *out;
  int emitted;
  ctx_status status;
} result_sink;
static ctx_status emit_result(void *context, const uint8_t *data, size_t len) {
  result_sink *sink = context;
  if (sink->emitted++ || !data || !len || len > CTX_HOST_MAX_FRAME) {
    sink->status = CTX_INVALID;
    return sink->status;
  }
  sink->out->data = malloc(len);
  if (!sink->out->data) {
    sink->status = CTX_NOMEM;
    return sink->status;
  }
  memcpy(sink->out->data, data, len);
  sink->out->len = len;
  return CTX_OK;
}
static ctx_status extension_call(ctx_host *h, const uint8_t *data, size_t len,
                                 int64_t deadline, const ctx_call_options *o,
                                 ctx_buffer *out) {
  int64_t remaining = deadline - mono_ms();
  if (remaining <= 0)
    return CTX_TIMEOUT;
  if (canceled(o))
    return CTX_CANCELED;
  result_sink sink = {out, 0, CTX_OK};
  ctx_status s;
  if (h->extension_context.connect) {
    ctx_call_options call = *o;
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
  if (s < CTX_OK || s > CTX_CANCELED)
    s = CTX_INVALID;
  if (!s)
    s = sink.status ? sink.status : sink.emitted == 1 ? CTX_OK : CTX_INVALID;
  return s;
}
static int closed(ctx_host *h) {
  pthread_mutex_lock(&h->mu);
  int result = h->closed;
  pthread_mutex_unlock(&h->mu);
  return result;
}
static ctx_status ready(ctx_host *h, short events, int64_t deadline,
                        const ctx_call_options *o) {
  for (;;) {
    if (canceled(o))
      return CTX_CANCELED;
    if (closed(h))
      return CTX_CLOSED;
    int64_t ms = deadline - mono_ms();
    if (ms <= 0)
      return CTX_TIMEOUT;
    struct pollfd p = {h->fd, events, 0};
    if (o->cancel && ms > 10)
      ms = 10;
    if (ms > INT_MAX)
      ms = INT_MAX;
    int r = poll(&p, 1, (int)ms);
    if (r < 0 && errno == EINTR)
      continue;
    if (r < 0)
      return CTX_IO;
    if (!r)
      continue;
    if (p.revents & events)
      return CTX_OK;
    return closed(h) ? CTX_CLOSED : CTX_IO;
  }
}
static ctx_status write_bytes(ctx_host *h, const uint8_t *p, size_t n,
                              int64_t deadline, const ctx_call_options *o) {
  while (n) {
    ctx_status s = ready(h, POLLOUT, deadline, o);
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
      return CTX_IO;
    p += r;
    n -= (size_t)r;
  }
  return CTX_OK;
}
static ctx_status exchange(ctx_host *h, const uint8_t *p, size_t n,
                           int64_t deadline, const ctx_call_options *o,
                           ctx_buffer *out) {
  ctx_status s = write_bytes(h, p, n, deadline, o);
  if (!s)
    s = write_bytes(h, (const uint8_t *)"\n", 1, deadline, o);
  if (s)
    return s;
  size_t len = 0;
  for (;;) {
    if (len == h->capacity) {
      size_t next = h->capacity ? h->capacity * 2 : 4096;
      if (next > CTX_HOST_MAX_FRAME + 1)
        next = CTX_HOST_MAX_FRAME + 1;
      if (next <= len)
        return CTX_INVALID;
      uint8_t *b = realloc(h->readbuf, next);
      if (!b)
        return CTX_NOMEM;
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
      return CTX_IO;
    uint8_t *end = memchr(h->readbuf + len, '\n', (size_t)r);
    len += (size_t)r;
    if (end) {
      size_t frame = (size_t)(end - h->readbuf);
      if (frame + 1 != len || frame > CTX_HOST_MAX_FRAME)
        return CTX_INVALID;
      out->data = malloc(frame ? frame : 1);
      if (!out->data)
        return CTX_NOMEM;
      memcpy(out->data, h->readbuf, frame);
      out->len = frame;
      return CTX_OK;
    }
  }
}
static ctx_status spawn_child(ctx_host *h) {
  int fds[2];
  if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds))
    return CTX_IO;
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
      return CTX_IO;
    }
  }
#ifdef SO_NOSIGPIPE
  int yes = 1;
  if (setsockopt(fds[0], SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof(yes))) {
    close(fds[0]);
    close(fds[1]);
    return CTX_IO;
  }
#endif
  posix_spawn_file_actions_t actions;
  int err = posix_spawn_file_actions_init(&actions);
  if (err) {
    close(fds[0]);
    close(fds[1]);
    return CTX_IO;
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
    return err == ECANCELED ? CTX_CLOSED : CTX_IO;
  }
  if (fcntl(h->fd, F_SETFL, fcntl(h->fd, F_GETFL) | O_NONBLOCK) < 0)
    return CTX_IO;
  return CTX_OK;
}
ctx_status ctx_host_start_with_options(ctx_host *h, const ctx_call_options *o) {
  if (!h || !valid_call(o))
    return CTX_INVALID;
  uint32_t timeout = o->timeout_ms;
  int64_t deadline = mono_ms() + timeout;
  ctx_status s = enter(h, deadline, 1, o);
  if (s)
    return s;
  if (h->started) {
    leave(h);
    return CTX_INVALID;
  }
  h->started = 1;
  int64_t stage_start = mono_ms();
  if (h->hooks.verify
          ? h->hooks.verify(h->hooks.user, o, h->descriptor, h->descriptor_len)
          : h->verify(h->user, h->descriptor, h->descriptor_len))
    s = CTX_DENIED;
  observe_event(h, o, "verify", NULL, s, NULL, stage_start);
  stage_start = mono_ms();
  if (!s && canceled(o))
    s = CTX_CANCELED;
  if (!s && mono_ms() >= deadline)
    s = CTX_TIMEOUT;
  if (!s && closed(h))
    s = CTX_CLOSED;
  int connecting = !s;
  if (!s && !(h->extension.connect || h->extension_context.connect))
    s = spawn_child(h);
  ctx_buffer response = {0};
  yyjson_doc *doc = NULL;
  if (!s && (h->extension.connect || h->extension_context.connect))
    s = extension_call(h, NULL, 0, deadline, o, &response);
  if (connecting)
    observe_event(h, o, "connect", NULL, s, NULL, stage_start);
  int handshaking = !s;
  stage_start = mono_ms();
  if (!s && !(h->extension.connect || h->extension_context.connect)) {
    char hello[192], stamp[40];
    int64_t wall = ctx_wall_ms() + (deadline - mono_ms());
    time_t seconds = (time_t)(wall / 1000);
    struct tm t;
    gmtime_r(&seconds, &t);
    strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%S", &t);
    int n = snprintf(hello, sizeof(hello),
                     "{\"apiVersion\":\"ctx.plugin/"
                     "v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\","
                     "\"deadline\":\"%s.%03lldZ\"}",
                     stamp, (long long)(wall % 1000));
    s = exchange(h, (uint8_t *)hello, (size_t)n, deadline, o, &response);
  }
  if (!s) {
    doc = ctx_parse(response.data, response.len);
    if (!doc)
      s = CTX_INVALID;
    else if (h->extension.connect || h->extension_context.connect) {
      if (!ctx_match(yyjson_doc_get_root(h->selection),
                     yyjson_doc_get_root(doc)))
        s = CTX_MISMATCH;
    } else {
      s = ctx_response(yyjson_doc_get_root(doc), "hello");
      if (!s && !ctx_match(yyjson_doc_get_root(h->selection),
                           yyjson_obj_get(yyjson_doc_get_root(doc), "payload")))
        s = CTX_MISMATCH;
    }
  }
  if (!s && canceled(o))
    s = CTX_CANCELED;
  if (!s && mono_ms() >= deadline)
    s = CTX_TIMEOUT;
  if (!s && closed(h))
    s = CTX_CLOSED;
  if (handshaking)
    observe_event(h, o, "handshake", NULL, s, NULL, stage_start);
  yyjson_doc_free(doc);
  ctx_buffer_free(&response);
  if (s) {
    fail(h);
  } else {
    pthread_mutex_lock(&h->mu);
    if (!h->closed)
      h->state = CTX_HOST_READY;
    else
      s = CTX_CLOSED;
    pthread_mutex_unlock(&h->mu);
  }
  leave(h);
  return s;
}
ctx_status ctx_host_invoke_with_options(ctx_host *h, const uint8_t *input,
                                        size_t len, const ctx_call_options *o,
                                        ctx_buffer *out) {
  if (!out)
    return CTX_INVALID;
  out->data = NULL;
  out->len = 0;
  if (!h || !valid_call(o))
    return CTX_INVALID;
  int64_t stage_start = mono_ms();
  uint32_t timeout = o->timeout_ms;
  int64_t deadline = mono_ms() + timeout;
  yyjson_doc *request = ctx_parse(input, len);
  if (!request) {
    observe_event(h, o, "invoke", NULL, CTX_INVALID, NULL, stage_start);
    return CTX_INVALID;
  }
  yyjson_val *r = yyjson_doc_get_root(request);
  int64_t wall_deadline;
  ctx_status s =
      ctx_request(yyjson_doc_get_root(h->selection), r, &wall_deadline);
  if (s) {
    observe_event(h, o, "invoke", r, s, NULL, stage_start);
    yyjson_doc_free(request);
    return s;
  }
  int64_t remaining = wall_deadline - ctx_wall_ms();
  if (remaining <= 0) {
    observe_event(h, o, "invoke", r, CTX_TIMEOUT, NULL, stage_start);
    yyjson_doc_free(request);
    return CTX_TIMEOUT;
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
    s = CTX_INVALID;
    goto done;
  }
  if (h->hooks.authorize ? h->hooks.authorize(h->hooks.user, o, input, len)
                         : h->authorize(h->user, input, len)) {
    s = CTX_DENIED;
    goto done;
  }
  if (closed(h)) {
    s = CTX_CLOSED;
    goto done;
  }
  if (canceled(o)) {
    s = CTX_CANCELED;
    goto done;
  }
  if (mono_ms() >= deadline) {
    s = CTX_TIMEOUT;
    goto done;
  }
  size_t compact_len;
  char *compact = yyjson_write(request, 0, &compact_len);
  if (!compact) {
    s = CTX_NOMEM;
    goto done;
  }
  s = (h->extension.invoke || h->extension_context.invoke)
          ? extension_call(h, (const uint8_t *)compact, compact_len, deadline,
                           o, out)
          : exchange(h, (const uint8_t *)compact, compact_len, deadline, o,
                     out);
  free(compact);
  if (!s) {
    yyjson_doc *response = ctx_parse(out->data, out->len);
    if (!response)
      s = CTX_INVALID;
    else {
      s = ctx_response(yyjson_doc_get_root(response),
                       yyjson_get_str(yyjson_obj_get(r, "id")));
      yyjson_doc_free(response);
    }
  }
  if (!s && canceled(o))
    s = CTX_CANCELED;
  if (!s && mono_ms() >= deadline)
    s = CTX_TIMEOUT;
  if (!s && closed(h))
    s = CTX_CLOSED;
  if (s) {
    ctx_buffer_free(out);
    fail(h);
  }
done:
  if (h->hooks.observe) {
    yyjson_doc *observed = out->data ? ctx_parse(out->data, out->len) : NULL;
    observe_event(h, o, "invoke", r, s,
                  observed ? yyjson_doc_get_root(observed) : NULL, stage_start);
    yyjson_doc_free(observed);
  }
  yyjson_doc_free(request);
  leave(h);
  return s;
}

ctx_status ctx_host_start(ctx_host *h, uint32_t timeout) {
  ctx_call_options o = {sizeof(o), timeout, NULL, NULL};
  return ctx_host_start_with_options(h, &o);
}
ctx_status ctx_host_invoke(ctx_host *h, const uint8_t *data, size_t len,
                           uint32_t timeout, ctx_buffer *out) {
  ctx_call_options o = {sizeof(o), timeout, NULL, NULL};
  return ctx_host_invoke_with_options(h, data, len, &o, out);
}
ctx_status ctx_host_drain(ctx_host *h, uint32_t timeout) {
  ctx_call_options o = {sizeof(o), timeout, NULL, NULL};
  return ctx_host_drain_with_options(h, &o);
}

/* Builds authoritative request metadata from the immutable selection. */
ctx_status ctx_host_call(ctx_host *h, const uint8_t *input, size_t len,
                         const ctx_call_options *options, ctx_buffer *out) {
  if (!out)
    return CTX_INVALID;
  *out = (ctx_buffer){0};
  if (!h || !valid_call(options))
    return CTX_INVALID;
  yyjson_doc *doc = ctx_parse(input, len);
  if (!doc)
    return CTX_INVALID;
  yyjson_val *call = yyjson_doc_get_root(doc),
             *ref = yyjson_obj_get(call, "contract"),
             *name = yyjson_obj_get(call, "operation");
  ctx_status status = CTX_INVALID;
  if (!CTX_FIELDS(call, "contract", "operation", "payload") ||
      !CTX_FIELDS(ref, "name", "version") || !ctx_ref(ref) ||
      !ctx_text(name, 0, 0))
    goto done;
  yyjson_val *operation =
      ctx_lookup(yyjson_doc_get_root(h->selection), ref, name);
  if (!operation) {
    status = CTX_UNSUPPORTED;
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
  int64_t wall = ctx_wall_ms() + options->timeout_ms;
  time_t seconds = (time_t)(wall / 1000);
  struct tm t;
  gmtime_r(&seconds, &t);
  strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%S", &t);
  snprintf(date, sizeof(date), "%s.%03lldZ", stamp, (long long)(wall % 1000));
  yyjson_mut_doc *request = yyjson_mut_doc_new(NULL);
  if (!request) {
    status = CTX_NOMEM;
    goto done;
  }
  yyjson_mut_val *root = yyjson_mut_obj(request);
  yyjson_mut_doc_set_root(request, root);
  int ok =
      root &&
      yyjson_mut_obj_add_str(request, root, "apiVersion", "ctx.plugin/v1") &&
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
  status = bytes ? ctx_host_invoke_with_options(h, (const uint8_t *)bytes,
                                                count, options, out)
                 : CTX_NOMEM;
  free(bytes);
  yyjson_mut_doc_free(request);
done:
  yyjson_doc_free(doc);
  return status;
}
