#include "ctx_host.h"
#include <stdatomic.h>
#include <stdlib.h>
struct ctx_cancel {
  atomic_int signaled;
};
ctx_status ctx_cancel_create(ctx_cancel **out) {
  if (!out)
    return CTX_INVALID;
  *out = malloc(sizeof(**out));
  if (!*out)
    return CTX_NOMEM;
  atomic_init(&(*out)->signaled, 0);
  return CTX_OK;
}
void ctx_cancel_signal(ctx_cancel *c) {
  if (c)
    atomic_store(&c->signaled, 1);
}
int ctx_cancel_is_signaled(const ctx_cancel *c) {
  return c && atomic_load(&c->signaled);
}
void ctx_cancel_destroy(ctx_cancel *c) { free(c); }
uint32_t ctx_host_abi_version(void) { return CTX_HOST_ABI_VERSION; }
const char *ctx_host_status_string(ctx_status s) {
  static const char *names[] = {
      "ok",        "invalid",   "denied",   "mismatch",      "unsupported",
      "closed",    "timeout",   "io",       "out of memory", "draining",
      "not found", "ambiguous", "canceled", "updating",      "capacity",
      "sequence"};
  return s >= 0 && (size_t)s < sizeof(names) / sizeof(names[0]) ? names[s]
                                                                : "unknown";
}
void ctx_buffer_free(ctx_buffer *b) {
  if (b) {
    free(b->data);
    b->data = NULL;
    b->len = 0;
  }
}
