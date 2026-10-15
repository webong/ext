#define _POSIX_C_SOURCE 200809L
#include "ext_instance.h"
#include "ext_stream.h"
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
static ext_status validate(void *u, const uint8_t *p, size_t n) {
  (void)u;
  return ext_host_validate_json(p, n);
}
static ext_status create(void *u, const ext_call_options *o,
                         const ext_cancel *life, const uint8_t *k, size_t kn,
                         const uint8_t *p, size_t n, void **out) {
  (void)u;
  (void)o;
  (void)life;
  (void)k;
  (void)kn;
  *out = malloc(n + 1);
  if (!*out)
    return EXT_NOMEM;
  memcpy(*out, p, n);
  ((char *)*out)[n] = 0;
  return EXT_OK;
}
static ext_status dispose(void *u, void *v) {
  (void)u;
  free(v);
  atomic_fetch_add(&disposed, 1);
  return EXT_OK;
}
static void instances(void) {
  ext_instance_options options = {sizeof(options), 2,       NULL, validate,
                                  create,          dispose, NULL};
  ext_instances *m = NULL;
  assert(ext_instances_create(&options, &m) == EXT_OK);
  ext_call_options call = {sizeof(call), 1000, NULL, NULL};
  assert(ext_instances_configure(m, (const uint8_t *)"key", 3,
                                 (const uint8_t *)"one", 3,
                                 (const uint8_t *)"1", 1, &call) == EXT_OK);
  ext_lease *one = NULL, *two = NULL;
  assert(ext_instances_acquire(m, (const uint8_t *)"key", 3, &one) == EXT_OK);
  assert(!strcmp(ext_lease_value(one), "1"));
  assert(ext_instances_configure(
             m, (const uint8_t *)"key", 3, (const uint8_t *)"one", 3,
             (const uint8_t *)"2", 1, &call) == EXT_MISMATCH);
  assert(ext_instances_configure(m, (const uint8_t *)"key", 3,
                                 (const uint8_t *)"two", 3,
                                 (const uint8_t *)"2", 1, &call) == EXT_OK);
  assert(ext_instances_acquire(m, (const uint8_t *)"key", 3, &two) == EXT_OK);
  assert(!strcmp(ext_lease_value(two), "2"));
  assert(ext_instances_configure(
             m, (const uint8_t *)"key", 3, (const uint8_t *)"three", 5,
             (const uint8_t *)"3", 1, &call) == EXT_CAPACITY);
  assert(!atomic_load(&disposed));
  assert(ext_lease_release(one) == EXT_OK);
  assert(atomic_load(&disposed) == 1);
  call.timeout_ms = 5;
  assert(ext_instances_close(m, &call) == EXT_TIMEOUT);
  assert(ext_instances_destroy(m) == EXT_DRAINING);
  assert(ext_lease_release(two) == EXT_OK);
  call.timeout_ms = 1000;
  assert(ext_instances_close(m, &call) == EXT_OK);
  assert(atomic_load(&disposed) == 2);
  assert(ext_instances_destroy(m) == EXT_OK);
}
static ext_status open_stream(void *u, const ext_call_options *o,
                              const ext_cancel *life, const uint8_t *p,
                              size_t n, void **out) {
  (void)u;
  (void)o;
  (void)life;
  (void)p;
  (void)n;
  *out = malloc(1);
  return *out ? EXT_OK : EXT_NOMEM;
}
static ext_status read_stream(void *u, void *v, const ext_call_options *o,
                              const ext_cancel *life, uint32_t limit,
                              ext_emit emit, void *sink) {
  (void)v;
  (void)o;
  (void)limit;
  if (u) {
    atomic_store(&reading, 1);
    while (!ext_cancel_is_signaled(life))
      pause_ms(1);
    return EXT_CANCELED;
  }
  const char *batch = "{\"items\":[{\"v\":1}],\"done\":false}";
  return emit(sink, (const uint8_t *)batch, strlen(batch));
}
static ext_status close_stream(void *u, void *v) {
  (void)u;
  (void)v;
  atomic_fetch_add(&closed, 1);
  return EXT_OK;
}
static void release_stream(void *u, void *v) {
  (void)u;
  free(v);
}
static void open_id(ext_streams *s, char id[49]) {
  ext_call_options call = {sizeof(call), 1000, NULL, NULL};
  ext_buffer out = {0};
  assert(ext_streams_open(s, (const uint8_t *)"one", 3, (const uint8_t *)"{}",
                          2, &call, &out) == EXT_OK);
  assert(out.len == 50);
  memcpy(id, out.data + 1, 48);
  id[48] = 0;
  ext_buffer_free(&out);
}
typedef struct {
  ext_streams *s;
  char id[49];
  ext_status status;
} reading_args;
static void *read_thread(void *p) {
  reading_args *a = p;
  ext_buffer out = {0};
  ext_call_options call = {sizeof(call), 1000, NULL, NULL};
  a->status = ext_streams_read(a->s, (const uint8_t *)"one", 3, a->id, 1, 1,
                               &call, &out);
  ext_buffer_free(&out);
  return NULL;
}
static void streams(void) {
  ext_stream_options options = {
      sizeof(options), 1,           1000,         NULL,
      open_stream,     read_stream, close_stream, release_stream};
  ext_streams *s = NULL;
  assert(ext_streams_create(&options, &s) == EXT_OK);
  char id[49];
  open_id(s, id);
  ext_call_options call = {sizeof(call), 1000, NULL, NULL};
  ext_buffer out = {0};
  assert(ext_streams_open(s, (const uint8_t *)"one", 3, (const uint8_t *)"{}",
                          2, &call, &out) == EXT_CAPACITY);
  assert(ext_streams_read(s, (const uint8_t *)"other", 5, id, 1, 1, &call,
                          &out) == EXT_DENIED);
  assert(ext_streams_read(s, (const uint8_t *)"one", 3, id, 2, 1, &call,
                          &out) == EXT_SEQUENCE);
  assert(ext_streams_read(s, (const uint8_t *)"one", 3, id, 1, 1, &call,
                          &out) == EXT_OK);
  ext_buffer_free(&out);
  assert(ext_streams_read(s, (const uint8_t *)"one", 3, id, 1, 1, &call,
                          &out) == EXT_SEQUENCE);
  assert(ext_streams_remove(s, (const uint8_t *)"one", 3, id) == EXT_OK);
  assert(ext_streams_remove(s, (const uint8_t *)"one", 3, id) == EXT_OK);
  assert(atomic_load(&closed) == 1);
  assert(ext_streams_close(s) == EXT_OK);
  assert(ext_streams_destroy(s) == EXT_OK);
  options.max_age_ms = 25;
  options.user = (void *)1;
  assert(ext_streams_create(&options, &s) == EXT_OK);
  reading_args a = {.s = s};
  open_id(s, a.id);
  pthread_t worker;
  assert(!pthread_create(&worker, NULL, read_thread, &a));
  pthread_join(worker, NULL);
  assert(a.status == EXT_CANCELED);
  assert(ext_streams_close(s) == EXT_OK);
  assert(atomic_load(&closed) == 2);
  assert(ext_streams_destroy(s) == EXT_OK);
  options.max_age_ms = 1000;
  atomic_store(&reading, 0);
  assert(ext_streams_create(&options, &s) == EXT_OK);
  a.s = s;
  open_id(s, a.id);
  assert(!pthread_create(&worker, NULL, read_thread, &a));
  while (!atomic_load(&reading))
    pause_ms(1);
  assert(ext_streams_close(s) == EXT_OK);
  pthread_join(worker, NULL);
  assert(a.status == EXT_CANCELED);
  assert(atomic_load(&closed) == 3);
  assert(ext_streams_destroy(s) == EXT_OK);
}
int main(void) {
  instances();
  streams();
  puts("C instance leases and stream lifecycle checks passed.");
  return 0;
}
