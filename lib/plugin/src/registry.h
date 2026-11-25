#ifndef EXT_REGISTRY_H
#define EXT_REGISTRY_H
#include "wire.h"

/* What the guest registry (registry.c) needs from the host that host.c keeps
 * private. */

/* Called with the id a call was given, before its request is sent, so the
 * registry can recognise the reverse calls the target makes inside it. A
 * nonzero return abandons the call with EXT_PLUGIN_NOMEM. It is not called for a call
 * refused before it has an id. */
typedef int (*ext_call_begun)(void *context, const char *id);
/* ext_plugin_host_call that reports the call's id to begun. */
ext_plugin_status ext_host_call_begun(ext_plugin_host *, const uint8_t *, size_t,
                               const ext_plugin_call_options *, ext_call_begun begun,
                               void *context, ext_plugin_buffer *out);
/* Whether the host's descriptor lists the contract ref, a {name, version}. */
int ext_host_serves(ext_plugin_host *, yyjson_val *ref);
int ext_host_serves_operation(ext_plugin_host *, yyjson_val *ref, const char *operation);
/* Internal registration for the described host's immutable operation routes. */
ext_plugin_status ext_registry_register_operation(ext_plugin_registry *, const char *name,
                                                  const char *version, const char *operation,
                                                  ext_plugin_host *);
#endif
