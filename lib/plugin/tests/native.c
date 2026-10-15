#define _POSIX_C_SOURCE 200809L
#include "ext_host.h"
#include <assert.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
static ext_buffer read_file(const char *p) {
  FILE *f = fopen(p, "rb");
  assert(f);
  assert(!fseek(f, 0, SEEK_END));
  long n = ftell(f);
  assert(n > 0);
  rewind(f);
  ext_buffer b = {malloc((size_t)n), (size_t)n};
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
  ext_host *host;
  ext_buffer request;
  ext_status result;
} call;
static void *invoke(void *p) {
  call *c = p;
  ext_buffer out = {0};
  c->result =
      ext_host_invoke(c->host, c->request.data, c->request.len, 3000, &out);
  ext_buffer_free(&out);
  return NULL;
}
/* Absolute paths that look right on this platform. */
#ifdef _WIN32
#define TEST_ROOT_DIR "C:\\"
#define TEST_MISSING "C:\\does\\not\\exist"
#else
#define TEST_ROOT_DIR "/"
#define TEST_MISSING "/does/not/exist"
#endif
int main(int argc, char **argv) {
  assert(argc == 5);
  ext_buffer descriptor = read_file(argv[2]), request = read_file(argv[3]),
             wait = read_file(argv[4]);
  const char *invalid[] = {
      "{\"x\":1,\"\\u0078\":2}",  "{} {}", "[1,]", "{\"x\":}", "\xff",
      "{\"x\":{\"x\":1,\"x\":2}}"};
  for (size_t i = 0; i < sizeof(invalid) / sizeof(*invalid); i++)
    assert(ext_host_validate_json((const uint8_t *)invalid[i],
                                  strlen(invalid[i])) == EXT_INVALID);
  ext_jsonline_process_options process = {argv[1]};
  ext_backend_options backend = {sizeof(backend), EXT_BACKEND_JSONLINE_PROCESS,
                                 &process, sizeof(process)};
  ext_host_options o = {EXT_HOST_ABI_VERSION,
                        sizeof(o),
                        descriptor.data,
                        descriptor.len,
                        allow,
                        allow,
                        NULL};
  for (int i = 0; i < 20; i++) {
    ext_host *h = NULL;
    assert(ext_host_create(&o, &backend, &h) == EXT_OK);
    assert(ext_host_start(h, 3000) == EXT_OK);
    call calls[4];
    pthread_t threads[4];
    for (int k = 0; k < 4; k++) {
      calls[k] = (call){h, request, 0};
      assert(!pthread_create(&threads[k], NULL, invoke, &calls[k]));
    }
    for (int k = 0; k < 4; k++) {
      pthread_join(threads[k], NULL);
      assert(calls[k].result == EXT_OK);
    }
    call active = {h, wait, 0};
    pthread_t thread;
    assert(!pthread_create(&thread, NULL, invoke, &active));
    struct timespec delay = {0, 5000000};
    nanosleep(&delay, NULL);
    ext_host_close(h);
    ext_host_close(h);
    pthread_join(thread, NULL);
    assert(active.result != EXT_OK);
    ext_host_destroy(h);
  }
  ext_host *h = NULL;
  o.verify = deny;
  process.executable = TEST_MISSING;
  assert(ext_host_create(&o, &backend, &h) == EXT_OK);
  assert(ext_host_start(h, 100) == EXT_DENIED);
  ext_host_destroy(h);
  o.verify = allow;
  process.executable = argv[1];
  o.authorize = deny;
  assert(ext_host_create(&o, &backend, &h) == EXT_OK);
  assert(ext_host_start(h, 3000) == EXT_OK);
  ext_buffer out = {0};
  assert(ext_host_invoke(h, request.data, request.len, 3000, &out) ==
         EXT_DENIED);
  assert(!out.data && !out.len);
  ext_host_destroy(h);
  /* Launch backend: working directory, bounded stderr capture, validation. */
  {
    o.verify = allow;
    o.authorize = allow;
    ext_jsonline_process_launch launch = {sizeof(launch), argv[1], NULL, 0,
                                          NULL,           0,       TEST_ROOT_DIR,  16};
    ext_backend_options launch_backend = {sizeof(launch_backend),
                                          EXT_BACKEND_JSONLINE_PROCESS_LAUNCH,
                                          &launch, sizeof(launch)};
    ext_host *l = NULL;
    assert(ext_host_create(&o, &launch_backend, &l) == EXT_OK);
    assert(ext_host_start(l, 3000) == EXT_OK);
    ext_buffer reply = {0};
    assert(ext_host_invoke(l, request.data, request.len, 3000, &reply) ==
           EXT_OK);
    ext_buffer_free(&reply);
    uint8_t retained[16];
    size_t length = 99;
    assert(ext_host_process_stderr(l, retained, sizeof(retained), &length) ==
               EXT_OK &&
           length <= sizeof(retained));
    assert(ext_host_process_stderr(l, NULL, 0, &length) == EXT_OK);
    assert(ext_host_process_stderr(l, NULL, 1, &length) == EXT_INVALID);
    ext_host_destroy(l);

    launch.working_directory = "relative";
    assert(ext_host_create(&o, &launch_backend, &l) == EXT_INVALID);
    launch.working_directory = TEST_MISSING;
    assert(ext_host_create(&o, &launch_backend, &l) == EXT_OK);
    assert(ext_host_start(l, 1000) != EXT_OK);
    ext_host_destroy(l);
    launch.working_directory = NULL;
    launch.stderr_bytes = (16u << 20) + 1;
    assert(ext_host_create(&o, &launch_backend, &l) == EXT_INVALID);
    launch.stderr_bytes = 0;
    launch.executable = TEST_ROOT_DIR;
    assert(ext_host_create(&o, &launch_backend, &l) == EXT_OK);
    assert(ext_host_start(l, 1000) != EXT_OK);
    ext_host_destroy(l);
    const char *bad_env[] = {"NOEQUALS"};
    launch.executable = argv[1];
    launch.environment = bad_env;
    launch.environment_count = 1;
    assert(ext_host_create(&o, &launch_backend, &l) == EXT_INVALID);
    ext_host *plain = NULL;
    assert(ext_host_create(&o, &backend, &plain) == EXT_OK);
    assert(ext_host_process_stderr(plain, NULL, 0, &length) == EXT_OK &&
           length == 0);
    ext_host_destroy(plain);
  }
  ext_buffer_free(&descriptor);
  ext_buffer_free(&request);
  ext_buffer_free(&wait);
  puts("C lifecycle/concurrency tests passed");
  return 0;
}
