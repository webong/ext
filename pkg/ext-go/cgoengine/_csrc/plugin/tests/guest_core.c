#include "ext_guest.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>
static int called;
static ext_status echo(void *u, void *call, const uint8_t *p, size_t n,
                       uint32_t ms, ext_guest_emit emit, void *sink) {
  called++;
  (void)u;
  (void)call;
  (void)p;
  (void)n;
  (void)ms;
  return emit(sink, EXT_GUEST_PAYLOAD, (const uint8_t *)"7", 1);
}
int main(void) {
  const char *descriptor =
      "{\"apiVersion\":\"ext.plugin/"
      "v1\",\"identity\":{\"id\":\"test\",\"revision\":\"r1\"},\"contracts\":[{"
      "\"name\":\"test\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"}"
      "]}]}";
  const char *request =
      "{\"apiVersion\":\"ext.plugin/"
      "v1\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},"
      "\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":"
      "\"echo\",\"deadline\":\"2099-01-01T00:00:00Z\"}";
  ext_guest_options o = {EXT_HOST_ABI_VERSION,
                         sizeof(o),
                         (const uint8_t *)descriptor,
                         strlen(descriptor),
                         1000,
                         NULL,
                         echo};
  ext_guest *g = NULL;
  assert(ext_guest_create(&o, &g) == EXT_OK);
  ext_buffer out = {0};
  assert(ext_guest_invoke(g, (const uint8_t *)request, strlen(request), 1000,
                          NULL, &out) == EXT_OK);
  assert(out.len && strstr((const char *)out.data, "\"payload\":7"));
  ext_buffer_free(&out);

  /* A request that does not match the descriptor is answered by the engine as a
   * public invalid_request response, echoing the id, whenever it has a usable
   * id: the same response for every binding, with the handler never called. */
  const char *mismatched[] = {
      "{\"apiVersion\":\"ext.plugin/v2\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"echo\",\"deadline\":\"2099-01-01T00:00:00Z\"}",
      "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"other\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"echo\",\"deadline\":\"2099-01-01T00:00:00Z\"}",
      "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},\"contract\":{\"name\":\"other\",\"version\":\"v1\"},\"operation\":\"echo\",\"deadline\":\"2099-01-01T00:00:00Z\"}",
      "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"nope\",\"deadline\":\"2099-01-01T00:00:00Z\"}",
      "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"echo\"}",
      "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"echo\",\"deadline\":\"2099-01-01T00:00:00Z\",\"surface\":\"s\"}",
  };
  for (size_t i = 0; i < sizeof(mismatched) / sizeof(mismatched[0]); i++) {
    called = 0;
    assert(ext_guest_invoke(g, (const uint8_t *)mismatched[i],
                            strlen(mismatched[i]), 1000, NULL,
                            &out) == EXT_OK);
    assert(strstr((const char *)out.data, "\"id\":\"1\"") &&
           strstr((const char *)out.data, "\"code\":\"invalid_request\"") &&
           !strstr((const char *)out.data, "\"payload\"") && !called);
    ext_buffer_free(&out);
  }

  /* A request with no usable id, or that is not a request at all, has nothing
   * to correlate a response with: it is a failed call, as a JSON-line transport
   * treats a frame it cannot decode, and the handler is never called. */
  const char *unusable[] = {
      "",
      "{",
      "[]",
      "7",
      "{}",
      "{\"id\":1}",
      "{\"id\":\"\",\"apiVersion\":\"ext.plugin/v1\"}",
      "{\"id\":\"-bad\",\"apiVersion\":\"ext.plugin/v1\"}",
      "{\"id\":\"x\",\"id\":\"y\"}",
  };
  for (size_t i = 0; i < sizeof(unusable) / sizeof(unusable[0]); i++) {
    called = 0;
    assert(ext_guest_invoke(g, (const uint8_t *)unusable[i],
                            strlen(unusable[i]), 1000, NULL,
                            &out) == EXT_INVALID);
    assert(!out.data && !out.len && !called);
  }
  ext_guest_destroy(g);
  puts("Portable C guest core passed.");
  return 0;
}
