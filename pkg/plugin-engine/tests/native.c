#define _POSIX_C_SOURCE 200809L
#include "ctx_host.h"
#include <assert.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
static ctx_buffer read_file(const char *p) {
  FILE *f = fopen(p, "rb");
  assert(f);
  assert(!fseek(f, 0, SEEK_END));
  long n = ftell(f);
  assert(n > 0);
  rewind(f);
  ctx_buffer b = {malloc((size_t)n), (size_t)n};
  assert(b.data && fread(b.data, 1, b.len, f) == b.len);
  fclose(f);
  return b;
}
static int32_t allow(void *u, const uint8_t *p, size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return 0;
}
static int32_t deny(void *u, const uint8_t *p, size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return 1;
}
typedef struct {
  ctx_host *host;
  ctx_buffer request;
  ctx_status result;
} call;
static void *invoke(void *p) {
  call *c = p;
  ctx_buffer out = {0};
  c->result =
      ctx_host_invoke(c->host, c->request.data, c->request.len, 3000, &out);
  ctx_buffer_free(&out);
  return NULL;
}
int main(int argc, char **argv) {
  assert(argc == 5);
  ctx_buffer descriptor = read_file(argv[2]), request = read_file(argv[3]),
             wait = read_file(argv[4]);
  const char *invalid[] = {
      "{\"x\":1,\"\\u0078\":2}",  "{} {}", "[1,]", "{\"x\":}", "\xff",
      "{\"x\":{\"x\":1,\"x\":2}}"};
  for (size_t i = 0; i < sizeof(invalid) / sizeof(*invalid); i++)
    assert(ctx_host_validate_json((const uint8_t *)invalid[i],
                                  strlen(invalid[i])) == CTX_INVALID);
  ctx_jsonline_process_options process = {argv[1]};
  ctx_backend_options backend = {sizeof(backend), CTX_BACKEND_JSONLINE_PROCESS,
                                 &process, sizeof(process)};
  ctx_host_options o = {CTX_HOST_ABI_VERSION,
                        sizeof(o),
                        descriptor.data,
                        descriptor.len,
                        allow,
                        allow,
                        NULL};
  for (int i = 0; i < 20; i++) {
    ctx_host *h = NULL;
    assert(ctx_host_create(&o, &backend, &h) == CTX_OK);
    assert(ctx_host_start(h, 3000) == CTX_OK);
    call calls[4];
    pthread_t threads[4];
    for (int k = 0; k < 4; k++) {
      calls[k] = (call){h, request, 0};
      assert(!pthread_create(&threads[k], NULL, invoke, &calls[k]));
    }
    for (int k = 0; k < 4; k++) {
      pthread_join(threads[k], NULL);
      assert(calls[k].result == CTX_OK);
    }
    call active = {h, wait, 0};
    pthread_t thread;
    assert(!pthread_create(&thread, NULL, invoke, &active));
    struct timespec delay = {0, 5000000};
    nanosleep(&delay, NULL);
    ctx_host_close(h);
    ctx_host_close(h);
    pthread_join(thread, NULL);
    assert(active.result != CTX_OK);
    ctx_host_destroy(h);
  }
  ctx_host *h = NULL;
  o.verify = deny;
  process.executable = "/does/not/exist";
  assert(ctx_host_create(&o, &backend, &h) == CTX_OK);
  assert(ctx_host_start(h, 100) == CTX_DENIED);
  ctx_host_destroy(h);
  o.verify = allow;
  process.executable = argv[1];
  o.authorize = deny;
  assert(ctx_host_create(&o, &backend, &h) == CTX_OK);
  assert(ctx_host_start(h, 3000) == CTX_OK);
  ctx_buffer out = {0};
  assert(ctx_host_invoke(h, request.data, request.len, 3000, &out) ==
         CTX_DENIED);
  assert(!out.data && !out.len);
  ctx_host_destroy(h);
  ctx_buffer_free(&descriptor);
  ctx_buffer_free(&request);
  ctx_buffer_free(&wait);
  puts("C lifecycle/concurrency tests passed");
  return 0;
}
