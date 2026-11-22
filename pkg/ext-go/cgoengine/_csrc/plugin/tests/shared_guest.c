/* A C ABI guest (ext_plugin.h) for the shared-library backend test, written on
 * the C guest engine (ext_guest.h). It serves the conformance descriptor.
 * Variants built from this file:
 *   EXT_TEST_BAD_VERSION   reports ABI version 2
 *   EXT_TEST_NO_CLOSE      omits ext_plugin_close
 * A request whose payload value is "abi-fail" makes the ABI call itself fail,
 * and "slow" sleeps past a short deadline, as transport faults do. */
#define _POSIX_C_SOURCE 200809L
#include "ext_guest.h"
#include "ext_plugin.h"
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
static void pause_ms(unsigned ms) { Sleep(ms); }
#else
#include <time.h>
static void pause_ms(unsigned ms) {
  struct timespec t = {(time_t)(ms / 1000), (long)(ms % 1000) * 1000000L};
  nanosleep(&t, NULL);
}
#endif

static const char descriptor[] =
    "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"ctx/"
    "conformance\",\"revision\":\"fixture-1\"},\"contracts\":[{\"name\":"
    "\"ext.conformance\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"},"
    "{\"name\":\"wait\"},{\"name\":\"private-error\"},{\"name\":\"public-"
    "error\"}]}]}";

static atomic_uint_fast64_t opened, closed_handles, next_handle;

static int has(const uint8_t *data, size_t len, const char *needle) {
  size_t n = strlen(needle);
  for (size_t i = 0; i + n <= len; i++)
    if (!memcmp(data + i, needle, n))
      return 1;
  return 0;
}

static ext_status handle(void *user, void *call_user, const uint8_t *request,
                         size_t len, uint32_t remaining_ms, ext_guest_emit emit,
                         void *sink) {
  (void)user;
  (void)call_user;
  (void)remaining_ms;
  if (has(request, len, "\"operation\":\"public-error\"")) {
    static const char e[] = "{\"code\":\"denied\",\"message\":\"no\"}";
    return emit(sink, EXT_GUEST_PUBLIC_ERROR, (const uint8_t *)e, sizeof(e) - 1);
  }
  if (has(request, len, "\"operation\":\"private-error\""))
    return EXT_IO;
  static const char p[] = "{\"echoed\":true}";
  return emit(sink, EXT_GUEST_PAYLOAD, (const uint8_t *)p, sizeof(p) - 1);
}

static ext_guest *guest(void) {
  static ext_guest *g;
  static atomic_int ready;
  if (!atomic_load(&ready)) {
    ext_guest_options o = {EXT_HOST_ABI_VERSION, sizeof(o), (const uint8_t *)descriptor,
                           sizeof(descriptor) - 1, 0, NULL, handle};
    if (ext_guest_create(&o, &g))
      abort();
    atomic_store(&ready, 1);
  }
  return g;
}

EXT_PLUGIN_EXPORT uint32_t EXT_PLUGIN_CALL ext_plugin_abi_version(void) {
#ifdef EXT_TEST_BAD_VERSION
  return 2;
#else
  return EXT_PLUGIN_ABI_VERSION;
#endif
}

EXT_PLUGIN_EXPORT uint64_t EXT_PLUGIN_CALL ext_plugin_open(void) {
  atomic_fetch_add(&opened, 1);
  return atomic_fetch_add(&next_handle, 1) + 1;
}

static uint32_t reply(const uint8_t *data, size_t len, uint8_t *out, uint32_t cap,
                      uint32_t *out_len) {
  if (len > cap)
    return EXT_PLUGIN_FAILED;
  memcpy(out, data, len);
  *out_len = (uint32_t)len;
  return EXT_PLUGIN_OK;
}

EXT_PLUGIN_EXPORT uint32_t EXT_PLUGIN_CALL ext_plugin_call(
    uint64_t handle_id, uint32_t operation, const uint8_t *request,
    uint32_t request_len, uint8_t *response, uint32_t response_capacity,
    uint32_t *response_len) {
  *response_len = 0;
  if (!handle_id)
    return EXT_PLUGIN_INVALID;
  if (operation == EXT_PLUGIN_HANDSHAKE) {
    if (!has(request, request_len, "{\"deadline\":\""))
      return EXT_PLUGIN_INVALID;
    return reply((const uint8_t *)descriptor, sizeof(descriptor) - 1, response,
                 response_capacity, response_len);
  }
  if (operation != EXT_PLUGIN_INVOKE)
    return EXT_PLUGIN_INVALID;
  if (has(request, request_len, "\"value\":\"abi-fail\""))
    return EXT_PLUGIN_FAILED;
  if (has(request, request_len, "\"value\":\"slow\""))
    pause_ms(300);
  ext_buffer out = {0};
  if (ext_guest_invoke(guest(), request, request_len, 5000, NULL, &out))
    return EXT_PLUGIN_FAILED;
  uint32_t s = reply(out.data, out.len, response, response_capacity, response_len);
  ext_buffer_free(&out);
  return s;
}

#ifndef EXT_TEST_NO_CLOSE
EXT_PLUGIN_EXPORT void EXT_PLUGIN_CALL ext_plugin_close(uint64_t handle_id) {
  if (handle_id)
    atomic_fetch_add(&closed_handles, 1);
}
#endif

/* Read by the test through the same loaded image. */
EXT_PLUGIN_EXPORT void EXT_PLUGIN_CALL ext_test_guest_counts(uint64_t *open_count,
                                                             uint64_t *close_count) {
  *open_count = atomic_load(&opened);
  *close_count = atomic_load(&closed_handles);
}
