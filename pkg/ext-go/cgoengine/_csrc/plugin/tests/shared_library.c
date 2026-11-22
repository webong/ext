/* The shared-library backend (ext_shared_library_backend_create) against the C
 * test guests in shared_guest.c. With arguments, it also runs a library built
 * elsewhere, such as the Go c-shared guest the Go loader runs:
 *   ext_shared_library_test <library> <descriptor.json>
 */
#define _POSIX_C_SOURCE 200809L
#include "ext_host.h"
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

static const char descriptor[] =
    "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"ctx/"
    "conformance\",\"revision\":\"fixture-1\"},\"contracts\":[{\"name\":"
    "\"ext.conformance\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"},"
    "{\"name\":\"wait\"},{\"name\":\"private-error\"},{\"name\":\"public-"
    "error\"}]}]}";
static const char other[] =
    "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"ctx/"
    "other\",\"revision\":\"fixture-1\"},\"contracts\":[{\"name\":"
    "\"ext.conformance\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"}]}]}";

static int32_t allow(void *u, const uint8_t *p, size_t n) {
  (void)u, (void)p, (void)n;
  return 0;
}
static int32_t deny(void *u, const uint8_t *p, size_t n) {
  (void)u, (void)p, (void)n;
  return 1;
}

static ext_status open_host(const char *library, const uint8_t *desc, size_t desc_len,
                            ext_policy verify, ext_host **out) {
  ext_shared_library_options lo = {sizeof(lo), library};
  ext_backend_extension_context ext;
  ext_status s = ext_shared_library_backend_create(&lo, &ext);
  if (s)
    return s;
  ext_host_options o = {EXT_HOST_ABI_VERSION, sizeof(o), desc, desc_len, verify, allow, NULL};
  ext_backend_options b = {sizeof(b), EXT_BACKEND_EXTENSION_CONTEXT, &ext, sizeof(ext)};
  s = ext_host_create(&o, &b, out);
  if (s)
    ext.release(ext.user);
  return s;
}

static ext_status call(ext_host *h, const char *operation, const char *value,
                       uint32_t timeout_ms, ext_buffer *out) {
  char request[512];
  int n = snprintf(request, sizeof(request),
                   "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":"
                   "\"ctx/conformance\",\"revision\":\"fixture-1\"},\"contract\":{"
                   "\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":"
                   "\"%s\",\"deadline\":\"2099-01-01T00:00:00Z\",\"payload\":{"
                   "\"value\":%s}}",
                   operation, value);
  return ext_host_invoke(h, (const uint8_t *)request, (size_t)n, timeout_ms, out);
}

static int contains(const ext_buffer *b, const char *needle) {
  size_t n = strlen(needle);
  for (size_t i = 0; i + n <= b->len; i++)
    if (!memcmp(b->data + i, needle, n))
      return 1;
  return 0;
}

typedef void (*counts_fn)(uint64_t *, uint64_t *);
static void counts(const char *library, uint64_t *opened, uint64_t *closed) {
  void *p;
#ifdef _WIN32
  HMODULE h = LoadLibraryA(library);
  assert(h);
  p = (void *)GetProcAddress(h, "ext_test_guest_counts");
#else
  void *h = dlopen(library, RTLD_NOW | RTLD_LOCAL);
  assert(h);
  p = dlsym(h, "ext_test_guest_counts");
#endif
  assert(p);
  counts_fn f;
  memcpy(&f, &p, sizeof(p));
  f(opened, closed);
}

static void rejects_bad_options(void) {
  ext_backend_extension_context ext;
  assert(ext_shared_library_backend_create(NULL, &ext) == EXT_INVALID);
  ext_shared_library_options lo = {sizeof(lo), "relative/libguest.so"};
  assert(ext_shared_library_backend_create(&lo, &ext) == EXT_INVALID);
  lo.path = "";
  assert(ext_shared_library_backend_create(&lo, &ext) == EXT_INVALID);
  lo.path = NULL;
  assert(ext_shared_library_backend_create(&lo, &ext) == EXT_INVALID);
  ext_shared_library_options wrong = {sizeof(wrong) - 1, EXT_TEST_GUEST};
  assert(ext_shared_library_backend_create(&wrong, &ext) == EXT_INVALID);
#ifndef _WIN32
  lo.path = "/tmp/\xff.so";
  assert(ext_shared_library_backend_create(&lo, &ext) == EXT_INVALID);
#endif
  lo.path = EXT_TEST_GUEST;
  assert(ext_shared_library_backend_create(&lo, NULL) == EXT_INVALID);
  /* Creating loads nothing, so a missing file is found only at start. */
#ifdef _WIN32
  lo.path = "C:\\does\\not\\exist.dll";
#else
  lo.path = "/does/not/exist.so";
#endif
  assert(ext_shared_library_backend_create(&lo, &ext) == EXT_OK);
  ext.release(ext.user);
}

static void serves_calls(void) {
  uint64_t opened0, closed0, opened, closed;
  counts(EXT_TEST_GUEST, &opened0, &closed0);
  ext_host *h = NULL;
  assert(open_host(EXT_TEST_GUEST, (const uint8_t *)descriptor, sizeof(descriptor) - 1,
                   allow, &h) == EXT_OK);
  assert(ext_host_start(h, 3000) == EXT_OK);
  ext_buffer out = {0};
  assert(call(h, "echo", "7", 3000, &out) == EXT_OK);
  assert(contains(&out, "\"echoed\":true"));
  ext_buffer_free(&out);
  assert(call(h, "public-error", "7", 3000, &out) == EXT_OK);
  assert(contains(&out, "\"code\":\"denied\""));
  ext_buffer_free(&out);
  /* A handler failure is a domain error, sanitized; the session continues. */
  assert(call(h, "private-error", "7", 3000, &out) == EXT_OK);
  assert(contains(&out, "\"error\""));
  assert(!contains(&out, "EXT_IO"));
  ext_buffer_free(&out);
  counts(EXT_TEST_GUEST, &opened, &closed);
  assert(opened == opened0 + 1 && closed == closed0);
  /* A transport failure ends the session, and nothing is retried. */
  assert(call(h, "echo", "\"abi-fail\"", 3000, &out) != EXT_OK);
  ext_buffer_free(&out);
  assert(call(h, "echo", "7", 3000, &out) != EXT_OK);
  ext_buffer_free(&out);
  ext_host_close(h);
  ext_host_destroy(h);
  counts(EXT_TEST_GUEST, &opened, &closed);
  assert(opened == opened0 + 1 && closed == closed0 + 1);
}

static void shares_the_image(void) {
  uint64_t opened0, closed0, opened, closed;
  counts(EXT_TEST_GUEST, &opened0, &closed0);
  ext_host *a = NULL, *b = NULL;
  assert(open_host(EXT_TEST_GUEST, (const uint8_t *)descriptor, sizeof(descriptor) - 1,
                   allow, &a) == EXT_OK);
  assert(open_host(EXT_TEST_GUEST, (const uint8_t *)descriptor, sizeof(descriptor) - 1,
                   allow, &b) == EXT_OK);
  assert(ext_host_start(a, 3000) == EXT_OK);
  assert(ext_host_start(b, 3000) == EXT_OK);
  counts(EXT_TEST_GUEST, &opened, &closed);
  assert(opened == opened0 + 2);
  ext_host_destroy(a);
  ext_host_destroy(b);
  counts(EXT_TEST_GUEST, &opened, &closed);
  assert(closed == closed0 + 2);
}

static void checks_the_handshake(void) {
  ext_host *h = NULL;
  assert(open_host(EXT_TEST_GUEST, (const uint8_t *)other, sizeof(other) - 1, allow,
                   &h) == EXT_OK);
  assert(ext_host_start(h, 3000) == EXT_MISMATCH);
  ext_host_destroy(h);
}

static void refuses_bad_libraries(void) {
  ext_host *h = NULL;
  /* The verify policy runs before the library is loaded. */
  assert(open_host(EXT_TEST_BAD_VERSION_GUEST, (const uint8_t *)descriptor,
                   sizeof(descriptor) - 1, deny, &h) == EXT_OK);
  assert(ext_host_start(h, 3000) == EXT_DENIED);
  ext_host_destroy(h);
  const char *bad[] = {EXT_TEST_BAD_VERSION_GUEST, EXT_TEST_NO_CLOSE_GUEST};
  for (size_t i = 0; i < 2; i++) {
    for (int again = 0; again < 2; again++) { /* a failed image stays failed */
      assert(open_host(bad[i], (const uint8_t *)descriptor, sizeof(descriptor) - 1,
                       allow, &h) == EXT_OK);
      assert(ext_host_start(h, 3000) == EXT_UNSUPPORTED);
      ext_host_destroy(h);
    }
  }
#ifdef _WIN32
  const char *missing = "C:\\does\\not\\exist.dll";
#else
  const char *missing = "/does/not/exist.so";
#endif
  assert(open_host(missing, (const uint8_t *)descriptor, sizeof(descriptor) - 1, allow,
                   &h) == EXT_OK);
  assert(ext_host_start(h, 3000) == EXT_NOT_FOUND);
  ext_host_destroy(h);
}

static void ends_a_late_call(void) {
  ext_host *h = NULL;
  assert(open_host(EXT_TEST_GUEST, (const uint8_t *)descriptor, sizeof(descriptor) - 1,
                   allow, &h) == EXT_OK);
  assert(ext_host_start(h, 3000) == EXT_OK);
  ext_buffer out = {0};
  assert(call(h, "echo", "\"slow\"", 100, &out) == EXT_TIMEOUT);
  ext_buffer_free(&out);
  assert(call(h, "echo", "7", 3000, &out) != EXT_OK);
  ext_buffer_free(&out);
  ext_host_destroy(h);
}

static ext_buffer read_file(const char *path) {
  FILE *f = fopen(path, "rb");
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

/* A library built elsewhere, serving the conformance descriptor. */
static void serves_external(const char *library, const char *descriptor_path) {
  ext_buffer d = read_file(descriptor_path);
  ext_host *h = NULL;
  assert(open_host(library, d.data, d.len, allow, &h) == EXT_OK);
  assert(ext_host_start(h, 5000) == EXT_OK);
  ext_buffer out = {0};
  assert(call(h, "echo", "7", 5000, &out) == EXT_OK);
  assert(contains(&out, "\"value\":7"));
  ext_buffer_free(&out);
  assert(call(h, "public-error", "7", 5000, &out) == EXT_OK);
  assert(contains(&out, "\"error\""));
  ext_buffer_free(&out);
  ext_host_destroy(h);
  ext_buffer_free(&d);
}

int main(int argc, char **argv) {
  if (argc == 3) {
    serves_external(argv[1], argv[2]);
    puts("ok external");
    return 0;
  }
  rejects_bad_options();
  serves_calls();
  shares_the_image();
  checks_the_handshake();
  refuses_bad_libraries();
  ends_a_late_call();
  puts("ok");
  return 0;
}
