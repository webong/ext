#include "ext_host.h"
#include <stdatomic.h>
#include <stdlib.h>
struct ext_cancel {
  atomic_int signaled;
};
ext_status ext_cancel_create(ext_cancel **out) {
  if (!out)
    return EXT_INVALID;
  *out = malloc(sizeof(**out));
  if (!*out)
    return EXT_NOMEM;
  atomic_init(&(*out)->signaled, 0);
  return EXT_OK;
}
void ext_cancel_signal(ext_cancel *c) {
  if (c)
    atomic_store(&c->signaled, 1);
}
int ext_cancel_is_signaled(const ext_cancel *c) {
  return c && atomic_load(&c->signaled);
}
void ext_cancel_destroy(ext_cancel *c) { free(c); }
uint32_t ext_host_abi_version(void) { return EXT_HOST_ABI_VERSION; }
const char *ext_host_status_string(ext_status s) {
  static const char *names[] = {
      "ok",        "invalid",   "denied",   "mismatch",      "unsupported",
      "closed",    "timeout",   "io",       "out of memory", "draining",
      "not found", "ambiguous", "canceled", "updating",      "capacity",
      "sequence"};
  return s >= 0 && (size_t)s < sizeof(names) / sizeof(names[0]) ? names[s]
                                                                : "unknown";
}
void ext_buffer_free(ext_buffer *b) {
  if (b) {
    free(b->data);
    b->data = NULL;
    b->len = 0;
  }
}
