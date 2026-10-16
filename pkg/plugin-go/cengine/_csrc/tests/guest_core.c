#include "ext_guest.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>
static ext_status echo(void *u, void *call, const uint8_t *p, size_t n,
                       uint32_t ms, ext_guest_emit emit, void *sink) {
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
  ext_guest_destroy(g);
  puts("Portable C guest core passed.");
  return 0;
}
