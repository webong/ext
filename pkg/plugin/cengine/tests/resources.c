#define _POSIX_C_SOURCE 200809L
#include "ctx_instance.h"
#include "ctx_stream.h"
#include <assert.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
static atomic_int disposed, closed, reading;
static void pause_ms(unsigned ms) {
  struct timespec t = {ms / 1000, (long)(ms % 1000) * 1000000};
  nanosleep(&t, NULL);
}
static ctx_status validate(void *u, const uint8_t *p, size_t n) {
  (void)u;
  return ctx_host_validate_json(p, n);
}
static ctx_status create(void *u, const ctx_call_options *o,
                         const ctx_cancel *life, const uint8_t *k, size_t kn,
                         const uint8_t *p, size_t n, void **out) {
  (void)u;
  (void)o;
  (void)life;
  (void)k;
  (void)kn;
  *out = malloc(n + 1);
  if (!*out)
    return CTX_NOMEM;
  memcpy(*out, p, n);
  ((char *)*out)[n] = 0;
  return CTX_OK;
}
static ctx_status dispose(void *u, void *v) {
  (void)u;
  free(v);
  atomic_fetch_add(&disposed, 1);
  return CTX_OK;
}
static void instances(void) {
  ctx_instance_options options = {sizeof(options), 2,       NULL, validate,
                                  create,          dispose, NULL};
  ctx_instances *m = NULL;
  assert(ctx_instances_create(&options, &m) == CTX_OK);
  ctx_call_options call = {sizeof(call), 1000, NULL, NULL};
  assert(ctx_instances_configure(m, (const uint8_t *)"key", 3,
                                 (const uint8_t *)"one", 3,
                                 (const uint8_t *)"1", 1, &call) == CTX_OK);
  ctx_lease *one = NULL, *two = NULL;
  assert(ctx_instances_acquire(m, (const uint8_t *)"key", 3, &one) == CTX_OK);
  assert(!strcmp(ctx_lease_value(one), "1"));
  assert(ctx_instances_configure(
             m, (const uint8_t *)"key", 3, (const uint8_t *)"one", 3,
             (const uint8_t *)"2", 1, &call) == CTX_MISMATCH);
  assert(ctx_instances_configure(m, (const uint8_t *)"key", 3,
                                 (const uint8_t *)"two", 3,
                                 (const uint8_t *)"2", 1, &call) == CTX_OK);
  assert(ctx_instances_acquire(m, (const uint8_t *)"key", 3, &two) == CTX_OK);
  assert(!strcmp(ctx_lease_value(two), "2"));
  assert(ctx_instances_configure(
             m, (const uint8_t *)"key", 3, (const uint8_t *)"three", 5,
             (const uint8_t *)"3", 1, &call) == CTX_CAPACITY);
  assert(!atomic_load(&disposed));
  assert(ctx_lease_release(one) == CTX_OK);
  assert(atomic_load(&disposed) == 1);
  call.timeout_ms = 5;
  assert(ctx_instances_close(m, &call) == CTX_TIMEOUT);
  assert(ctx_instances_destroy(m) == CTX_DRAINING);
  assert(ctx_lease_release(two) == CTX_OK);
  call.timeout_ms = 1000;
  assert(ctx_instances_close(m, &call) == CTX_OK);
  assert(atomic_load(&disposed) == 2);
  assert(ctx_instances_destroy(m) == CTX_OK);
}
static ctx_status open_stream(void *u, const ctx_call_options *o,
                              const ctx_cancel *life, const uint8_t *p,
                              size_t n, void **out) {
  (void)u;
  (void)o;
  (void)life;
  (void)p;
  (void)n;
  *out = malloc(1);
  return *out ? CTX_OK : CTX_NOMEM;
}
static ctx_status read_stream(void *u, void *v, const ctx_call_options *o,
                              const ctx_cancel *life, uint32_t limit,
                              ctx_emit emit, void *sink) {
  (void)v;
  (void)o;
  (void)limit;
  if (u) {
    atomic_store(&reading, 1);
    while (!ctx_cancel_is_signaled(life))
      pause_ms(1);
    return CTX_CANCELED;
  }
  const char *batch = "{\"items\":[{\"v\":1}],\"done\":false}";
  return emit(sink, (const uint8_t *)batch, strlen(batch));
}
static ctx_status close_stream(void *u, void *v) {
  (void)u;
  (void)v;
  atomic_fetch_add(&closed, 1);
  return CTX_OK;
}
static void release_stream(void *u, void *v) {
  (void)u;
  free(v);
}
static void open_id(ctx_streams *s, char id[49]) {
  ctx_call_options call = {sizeof(call), 1000, NULL, NULL};
  ctx_buffer out = {0};
  assert(ctx_streams_open(s, (const uint8_t *)"one", 3, (const uint8_t *)"{}",
                          2, &call, &out) == CTX_OK);
  assert(out.len == 50);
  memcpy(id, out.data + 1, 48);
  id[48] = 0;
  ctx_buffer_free(&out);
}
typedef struct {
  ctx_streams *s;
  char id[49];
  ctx_status status;
} reading_args;
static void *read_thread(void *p) {
  reading_args *a = p;
  ctx_buffer out = {0};
  ctx_call_options call = {sizeof(call), 1000, NULL, NULL};
  a->status = ctx_streams_read(a->s, (const uint8_t *)"one", 3, a->id, 1, 1,
                               &call, &out);
  ctx_buffer_free(&out);
  return NULL;
}
static void streams(void) {
  ctx_stream_options options = {
      sizeof(options), 1,           1000,         NULL,
      open_stream,     read_stream, close_stream, release_stream};
  ctx_streams *s = NULL;
  assert(ctx_streams_create(&options, &s) == CTX_OK);
  char id[49];
  open_id(s, id);
  ctx_call_options call = {sizeof(call), 1000, NULL, NULL};
  ctx_buffer out = {0};
  assert(ctx_streams_open(s, (const uint8_t *)"one", 3, (const uint8_t *)"{}",
                          2, &call, &out) == CTX_CAPACITY);
  assert(ctx_streams_read(s, (const uint8_t *)"other", 5, id, 1, 1, &call,
                          &out) == CTX_DENIED);
  assert(ctx_streams_read(s, (const uint8_t *)"one", 3, id, 2, 1, &call,
                          &out) == CTX_SEQUENCE);
  assert(ctx_streams_read(s, (const uint8_t *)"one", 3, id, 1, 1, &call,
                          &out) == CTX_OK);
  ctx_buffer_free(&out);
  assert(ctx_streams_read(s, (const uint8_t *)"one", 3, id, 1, 1, &call,
                          &out) == CTX_SEQUENCE);
  assert(ctx_streams_remove(s, (const uint8_t *)"one", 3, id) == CTX_OK);
  assert(ctx_streams_remove(s, (const uint8_t *)"one", 3, id) == CTX_OK);
  assert(atomic_load(&closed) == 1);
  assert(ctx_streams_close(s) == CTX_OK);
  assert(ctx_streams_destroy(s) == CTX_OK);
  options.max_age_ms = 25;
  options.user = (void *)1;
  assert(ctx_streams_create(&options, &s) == CTX_OK);
  reading_args a = {.s = s};
  open_id(s, a.id);
  pthread_t worker;
  assert(!pthread_create(&worker, NULL, read_thread, &a));
  pthread_join(worker, NULL);
  assert(a.status == CTX_CANCELED);
  assert(ctx_streams_close(s) == CTX_OK);
  assert(atomic_load(&closed) == 2);
  assert(ctx_streams_destroy(s) == CTX_OK);
  options.max_age_ms = 1000;
  atomic_store(&reading, 0);
  assert(ctx_streams_create(&options, &s) == CTX_OK);
  a.s = s;
  open_id(s, a.id);
  assert(!pthread_create(&worker, NULL, read_thread, &a));
  while (!atomic_load(&reading))
    pause_ms(1);
  assert(ctx_streams_close(s) == CTX_OK);
  pthread_join(worker, NULL);
  assert(a.status == CTX_CANCELED);
  assert(atomic_load(&closed) == 3);
  assert(ctx_streams_destroy(s) == CTX_OK);
}
int main(void) {
  instances();
  streams();
  puts("C instance leases and stream lifecycle checks passed.");
  return 0;
}
