#include "ext_plugin_host.h"
#include "reverse.h"
#include <stdatomic.h>
#include <stdlib.h>
struct ext_plugin_cancel {
  atomic_int signaled;
  const ext_plugin_cancel *parent; /* also signaled when this one is; set before use */
};
ext_plugin_status ext_plugin_cancel_create(ext_plugin_cancel **out) {
  if (!out)
    return EXT_PLUGIN_INVALID;
  *out = malloc(sizeof(**out));
  if (!*out)
    return EXT_PLUGIN_NOMEM;
  atomic_init(&(*out)->signaled, 0);
  (*out)->parent = NULL;
  return EXT_PLUGIN_OK;
}
void ext_plugin_cancel_signal(ext_plugin_cancel *c) {
  if (c)
    atomic_store(&c->signaled, 1);
}
int ext_plugin_cancel_is_signaled(const ext_plugin_cancel *c) {
  return c && (atomic_load(&c->signaled) || ext_plugin_cancel_is_signaled(c->parent));
}
void ext_cancel_link(ext_plugin_cancel *c, const ext_plugin_cancel *parent) {
  if (c)
    c->parent = parent;
}
void ext_plugin_cancel_destroy(ext_plugin_cancel *c) { free(c); }
uint32_t ext_plugin_host_abi_version(void) { return EXT_PLUGIN_HOST_ABI_VERSION; }
const char *ext_plugin_host_status_string(ext_plugin_status s) {
  static const char *names[] = {
      "ok",        "invalid",   "denied",   "mismatch",      "unsupported",
      "closed",    "timeout",   "io",       "out of memory", "draining",
      "not found", "ambiguous", "canceled", "updating",      "capacity",
      "sequence",  "protocol",  "expired",       "exists",   "missing"};
  return s >= 0 && (size_t)s < sizeof(names) / sizeof(names[0]) ? names[s]
                                                                : "unknown";
}
void ext_plugin_buffer_free(ext_plugin_buffer *b) {
  if (b) {
    free(b->data);
    b->data = NULL;
    b->len = 0;
  }
}
