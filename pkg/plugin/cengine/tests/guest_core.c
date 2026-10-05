#include "ctx_guest.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>
static ctx_status echo(void *u, void *call, const uint8_t *p, size_t n,
                       uint32_t ms, ctx_guest_emit emit, void *sink) {
  (void)u;
  (void)call;
  (void)p;
  (void)n;
  (void)ms;
  return emit(sink, CTX_GUEST_PAYLOAD, (const uint8_t *)"7", 1);
}
int main(void) {
  const char *descriptor =
      "{\"apiVersion\":\"ctx.plugin/"
      "v1\",\"identity\":{\"id\":\"test\",\"revision\":\"r1\"},\"contracts\":[{"
      "\"name\":\"test\",\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"}"
      "]}]}";
  const char *request =
      "{\"apiVersion\":\"ctx.plugin/"
      "v1\",\"id\":\"1\",\"plugin\":{\"id\":\"test\",\"revision\":\"r1\"},"
      "\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":"
      "\"echo\",\"deadline\":\"2099-01-01T00:00:00Z\"}";
  ctx_guest_options o = {CTX_HOST_ABI_VERSION,
                         sizeof(o),
                         (const uint8_t *)descriptor,
                         strlen(descriptor),
                         1000,
                         NULL,
                         echo};
  ctx_guest *g = NULL;
  assert(ctx_guest_create(&o, &g) == CTX_OK);
  ctx_buffer out = {0};
  assert(ctx_guest_invoke(g, (const uint8_t *)request, strlen(request), 1000,
                          NULL, &out) == CTX_OK);
  assert(out.len && strstr((const char *)out.data, "\"payload\":7"));
  ctx_buffer_free(&out);
  ctx_guest_destroy(g);
  puts("Portable C guest core passed.");
  return 0;
}
