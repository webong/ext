#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
#ifndef _DARWIN_C_SOURCE
#define _DARWIN_C_SOURCE
#endif
#include "ext_wamr.h"
#include <assert.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

/* Runs the WAMR backend against a real reactor: the Rust SDK's conformance guest
 * (id ctx/conformance, contract ext.conformance v1, operations echo, wait,
 * private-error, public-error), given as argv[1]. */

typedef struct { uint8_t *data; size_t len; } captured;
static ext_status capture(void *context, const uint8_t *data, size_t len) {
  captured *c = context;
  free(c->data);
  c->data = malloc(len + 1);
  memcpy(c->data, data, len);
  c->data[len] = 0;
  c->len = len;
  return EXT_OK;
}
static uint8_t *slurp(const char *path, size_t *len) {
  FILE *f = fopen(path, "rb");
  assert(f);
  fseek(f, 0, SEEK_END);
  *len = (size_t)ftell(f);
  rewind(f);
  uint8_t *b = malloc(*len);
  assert(fread(b, 1, *len, f) == *len);
  fclose(f);
  return b;
}
static double seconds(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (double)t.tv_sec + (double)t.tv_nsec / 1e9;
}
static void deadline_in(unsigned ms, char out[40]) {
  struct timespec t;
  clock_gettime(CLOCK_REALTIME, &t);
  uint64_t at = (uint64_t)t.tv_sec * 1000 + (uint64_t)t.tv_nsec / 1000000 + ms;
  time_t s = (time_t)(at / 1000);
  struct tm tm;
  gmtime_r(&s, &tm);
  snprintf(out, 40, "%04d-%02d-%02dT%02d:%02d:%02d.%03uZ", tm.tm_year + 1900, tm.tm_mon + 1,
           tm.tm_mday, tm.tm_hour, tm.tm_min, tm.tm_sec, (unsigned)(at % 1000));
}
static char *request(const char *operation, const char *payload, unsigned deadline_ms) {
  char deadline[40];
  deadline_in(deadline_ms, deadline);
  size_t cap = strlen(payload) + 400;
  char *r = malloc(cap);
  snprintf(r, cap,
           "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"ctx/conformance\","
           "\"revision\":\"fixture-1\"},\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},"
           "\"operation\":\"%s\",\"payload\":%s,\"deadline\":\"%s\"}",
           operation, payload, deadline);
  return r;
}
static int32_t allow(void *u, const uint8_t *p, size_t n) {
  (void)u;
  (void)p;
  (void)n;
  return 0;
}
static char diagnostics[1024];
static void say(void *u, const uint8_t *d, size_t n) {
  (void)u;
  size_t used = strlen(diagnostics);
  if (used + n + 2 < sizeof(diagnostics)) {
    memcpy(diagnostics + used, d, n);
    diagnostics[used + n] = '\n';
  }
}

typedef struct { ext_backend_extension_context ext; uint8_t *module; size_t len; } live;
static live start(const uint8_t *module, size_t n, ext_wamr_options opt, ext_status *connected, captured *descriptor) {
  live l = {0};
  l.module = (uint8_t *)module;
  l.len = n;
  opt.struct_size = sizeof(opt);
  opt.diagnostic = say;
  assert(ext_wamr_backend_create(module, n, &opt, &l.ext) == EXT_OK);
  ext_call_options o = {sizeof(o), 20000, NULL, NULL};
  *connected = l.ext.connect(l.ext.user, &o, capture, descriptor);
  return l;
}
static ext_status invoke(live *l, const char *req, unsigned timeout_ms, const ext_cancel *cancel, captured *out) {
  ext_call_options o = {sizeof(o), timeout_ms, cancel, NULL};
  return l->ext.invoke(l->ext.user, (const uint8_t *)req, strlen(req), &o, capture, out);
}
static void finish(live *l) {
  l->ext.close(l->ext.user);
  l->ext.release(l->ext.user);
}

static const uint8_t command_module[] = {0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0,
                                         7, 10, 1, 6, '_', 's', 't', 'a', 'r', 't', 0, 0, 10, 4, 1, 2, 0, 11};
/* A module importing wasi_snapshot_preview1.fd_read, which the ABI does not offer. */
static const uint8_t forbidden_import[] = {
    0, 97, 115, 109, 1, 0, 0, 0, 1, 9, 1, 96, 4, 127, 127, 127, 127, 1, 127,
    2, 34, 1, 22, 'w', 'a', 's', 'i', '_', 's', 'n', 'a', 'p', 's', 'h', 'o', 't', '_', 'p', 'r', 'e', 'v', 'i', 'e', 'w', '1',
    7, 'f', 'd', '_', 'r', 'e', 'a', 'd', 0, 0};

typedef struct { live *l; unsigned delay_ms; const ext_cancel *cancel; int close; } stopper;
static void *stop_later(void *p) {
  stopper *s = p;
  usleep(s->delay_ms * 1000);
  if (s->close) s->l->ext.close(s->l->ext.user);
  if (s->cancel) ext_cancel_signal((ext_cancel *)s->cancel);
  return NULL;
}

int main(int argc, char **argv) {
  setvbuf(stdout, NULL, _IOLBF, 0);
  if (argc != 2) {
    fprintf(stderr, "usage: ext_wamr_test reactor.wasm\n");
    return 2;
  }
  size_t n;
  uint8_t *module = slurp(argv[1], &n);
  ext_wamr_options defaults = {sizeof(ext_wamr_options), 0, 0, 0, 0, NULL, NULL};
  captured d = {0}, r = {0};
  ext_status connected;

  /* The handshake returns the guest's descriptor. */
  live l = start(module, n, defaults, &connected, &d);
  assert(connected == EXT_OK);
  assert(strstr((char *)d.data, "ext.conformance") && strstr((char *)d.data, "ctx/conformance"));

  /* A call echoes, including non-ASCII text and an exact large number. */
  char *q = request("echo", "{\"text\":\"h\\u00e9llo \\ud83d\\ude42\",\"big\":12345678901234567890}", 10000);
  assert(invoke(&l, q, 10000, NULL, &r) == EXT_OK);
  assert(strstr((char *)r.data, "12345678901234567890") && strstr((char *)r.data, "\"id\":\"1\""));
  free(q);

  /* A 1 MiB frame passes through the 24 MiB buffer. */
  size_t big = 1u << 20;
  char *payload = malloc(big + 32);
  memcpy(payload, "{\"text\":\"", 9);
  memset(payload + 9, 'a', big);
  memcpy(payload + 9 + big, "\"}", 3);
  q = request("echo", payload, 20000);
  assert(invoke(&l, q, 20000, NULL, &r) == EXT_OK && r.len > big);
  free(q);
  free(payload);

  /* A domain error is a valid response, and a bad request does not end the session. */
  q = request("public-error", "{}", 10000);
  assert(invoke(&l, q, 10000, NULL, &r) == EXT_OK && strstr((char *)r.data, "error"));
  free(q);
  assert(invoke(&l, "{not json", 10000, NULL, &r) == EXT_INVALID);
  q = request("echo", "{\"still\":\"alive\"}", 10000);
  assert(invoke(&l, q, 10000, NULL, &r) == EXT_OK && strstr((char *)r.data, "alive"));
  free(q);

  /* Many calls reuse the one response buffer. */
  for (int i = 0; i < 200; i++) {
    q = request("echo", "{\"n\":1}", 10000);
    assert(invoke(&l, q, 10000, NULL, &r) == EXT_OK);
    free(q);
  }
  finish(&l);

  /* A call that overruns its deadline is stopped, and the instance is then gone:
   * the guest sleeps until a deadline far past the host's. */
  l = start(module, n, defaults, &connected, &d);
  assert(connected == EXT_OK);
  q = request("wait", "{}", 60000);
  double t0 = seconds();
  ext_status s = invoke(&l, q, 300, NULL, &r);
  double took = seconds() - t0;
  printf("deadline: status %d after %.0f ms\n", (int)s, took * 1000);
  assert(s == EXT_TIMEOUT && took < 2.0);
  free(q);
  q = request("echo", "{}", 10000);
  assert(invoke(&l, q, 10000, NULL, &r) == EXT_CLOSED);
  free(q);
  finish(&l);

  /* Cancellation stops a running call. */
  l = start(module, n, defaults, &connected, &d);
  assert(connected == EXT_OK);
  ext_cancel *cancel = NULL;
  assert(ext_cancel_create(&cancel) == EXT_OK);
  stopper st = {&l, 200, cancel, 0};
  pthread_t th;
  pthread_create(&th, NULL, stop_later, &st);
  q = request("wait", "{}", 60000);
  t0 = seconds();
  s = invoke(&l, q, 30000, cancel, &r);
  took = seconds() - t0;
  pthread_join(th, NULL);
  printf("cancel: status %d after %.0f ms\n", (int)s, took * 1000);
  assert(s == EXT_CANCELED && took < 2.0);
  free(q);
  ext_cancel_destroy(cancel);
  finish(&l);

  /* Close from another thread interrupts a call that is running. */
  l = start(module, n, defaults, &connected, &d);
  assert(connected == EXT_OK);
  stopper cl = {&l, 200, NULL, 1};
  pthread_create(&th, NULL, stop_later, &cl);
  q = request("wait", "{}", 60000);
  t0 = seconds();
  s = invoke(&l, q, 30000, NULL, &r);
  took = seconds() - t0;
  pthread_join(th, NULL);
  printf("close: status %d after %.0f ms\n", (int)s, took * 1000);
  assert(s != EXT_OK && took < 2.0);
  free(q);
  l.ext.release(l.ext.user);

  /* An instruction budget stops a guest that does too much, even with time left. */
  ext_wamr_options tight = {0};
  tight.instruction_limit = 1000;
  l = start(module, n, tight, &connected, &d);
  printf("instruction limit: connect status %d\n", (int)connected);
  assert(connected == EXT_TIMEOUT);
  finish(&l);

  /* The same through the engine's host API, which rewrites statuses it does not
   * accept from a backend: an overrun during invoke must reach the application as
   * a timeout, not as an invalid request. 64000 instructions fit the handshake. */
  {
    l = start(module, n, defaults, &connected, &d);
    assert(connected == EXT_OK);
    captured descriptor_for_budget = {0};
    capture(&descriptor_for_budget, d.data, d.len);
    finish(&l);
    ext_wamr_options budget = {sizeof(ext_wamr_options), 0, 0, 64000, 0, NULL, NULL};
    ext_backend_extension_context backend_budget;
    ext_status made = ext_wamr_backend_create(module, n, &budget, &backend_budget);
    assert(made == EXT_OK);
    ext_backend_options bo = {sizeof(bo), EXT_BACKEND_EXTENSION_CONTEXT, &backend_budget, sizeof(backend_budget)};
    ext_host_options ho = {EXT_HOST_ABI_VERSION, sizeof(ho), descriptor_for_budget.data, descriptor_for_budget.len, allow, allow, NULL};
    ext_host *budget_host = NULL;
    ext_status created = ext_host_create(&ho, &bo, &budget_host);
    assert(created == EXT_OK);
    ext_status started = ext_host_start(budget_host, 20000);
    assert(started == EXT_OK);
    (void)made;
    (void)created;
    (void)started;
    size_t text = 60000;
    char *heavy = malloc(text + 8);
    memset(heavy, 'a', text);
    heavy[text] = 0;
    char *payload_heavy = malloc(text + 32);
    snprintf(payload_heavy, text + 32, "{\"text\":\"%s\"}", heavy);
    q = request("echo", payload_heavy, 20000);
    ext_buffer heavy_out = {0};
    ext_status over = ext_host_invoke(budget_host, (const uint8_t *)q, strlen(q), 20000, &heavy_out);
    printf("instruction limit during invoke through ext_host: status %d\n", (int)over);
    assert(over == EXT_TIMEOUT);
    ext_buffer_free(&heavy_out);
    free(q);
    free(payload_heavy);
    free(heavy);
    ext_host_close(budget_host);
    ext_host_destroy(budget_host);
    free(descriptor_for_budget.data);
  }

  /* Memory is capped: the 24 MiB response buffer cannot fit in 16 pages. */
  ext_wamr_options small = {0};
  small.memory_limit_pages = 16;
  l = start(module, n, small, &connected, &d);
  printf("memory limit: connect status %d\n", (int)connected);
  assert(connected != EXT_OK);
  finish(&l);

  /* Modules that do not follow the ABI are refused, and say why. */
  diagnostics[0] = 0;
  l = start(command_module, sizeof(command_module), defaults, &connected, &d);
  assert(connected == EXT_UNSUPPORTED && strstr(diagnostics, "_start"));
  finish(&l);
  diagnostics[0] = 0;
  l = start(forbidden_import, sizeof(forbidden_import), defaults, &connected, &d);
  printf("forbidden import: status %d, %s", (int)connected, diagnostics);
  assert(connected == EXT_UNSUPPORTED && strstr(diagnostics, "wasi_snapshot_preview1.fd_read"));
  finish(&l);
  diagnostics[0] = 0;
  const uint8_t garbage[] = "not a wasm module at all";
  l = start(garbage, sizeof(garbage), defaults, &connected, &d);
  assert(connected == EXT_UNSUPPORTED && diagnostics[0]);
  finish(&l);
  ext_backend_extension_context none;
  ext_status refused = ext_wamr_backend_create(NULL, 0, NULL, &none);
  assert(refused == EXT_INVALID);
  (void)refused;

  /* Backends are created and destroyed repeatedly without leaking or crashing. */
  for (int i = 0; i < 20; i++) {
    l = start(module, n, defaults, &connected, &d);
    assert(connected == EXT_OK);
    q = request("echo", "{}", 10000);
    assert(invoke(&l, q, 10000, NULL, &r) == EXT_OK);
    free(q);
    finish(&l);
  }
  /* The whole path through the engine's host API, with its policies and handshake
   * matching, the way an application uses it. */
  l = start(module, n, defaults, &connected, &d);
  assert(connected == EXT_OK);
  captured descriptor = {0};
  capture(&descriptor, d.data, d.len);
  finish(&l);
  ext_backend_extension_context backend;
  assert(ext_wamr_backend_create(module, n, &defaults, &backend) == EXT_OK);
  ext_backend_options backend_options = {sizeof(backend_options), EXT_BACKEND_EXTENSION_CONTEXT, &backend, sizeof(backend)};
  ext_host_options host_options = {EXT_HOST_ABI_VERSION, sizeof(host_options), descriptor.data, descriptor.len, allow, allow, NULL};
  ext_host *host = NULL;
  assert(ext_host_create(&host_options, &backend_options, &host) == EXT_OK);
  assert(ext_host_start(host, 20000) == EXT_OK);
  q = request("echo", "{\"through\":\"the host\"}", 10000);
  ext_buffer out = {0};
  assert(ext_host_invoke(host, (const uint8_t *)q, strlen(q), 10000, &out) == EXT_OK);
  assert(out.len && memmem(out.data, out.len, "the host", 8));
  ext_buffer_free(&out);
  free(q);
  ext_host_close(host);
  ext_host_destroy(host);
  free(descriptor.data);
  printf("ok\n");
  return 0;
}
