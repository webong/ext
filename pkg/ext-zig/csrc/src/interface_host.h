/* Host sessions behind the library interface (interface.c). */
#ifndef EXT_INTERFACE_HOST_H
#define EXT_INTERFACE_HOST_H
#include "ext_plugin_host.h"
#include "yyjson.h"
#include <stddef.h>

typedef struct ext_interface_host ext_interface_host;
ext_plugin_status ext_interface_host_open(yyjson_val *config, ext_interface_host **out);
ext_plugin_status ext_interface_host_call(ext_interface_host *h, const char *op, yyjson_val *in, char **out,
                                          size_t *out_len);
/* shutdown wakes every waiting callback and closes the session, so calls in flight return; free
 * runs once no call holds the host. */
void ext_interface_host_shutdown(ext_interface_host *h);
void ext_interface_host_free(ext_interface_host *h);
/* Pin an existing described host while another host's registry borrows it.
 * Closing its public handle still shuts it down; storage survives until release. */
ext_plugin_status ext_interface_host_borrow(yyjson_val *handle, ext_interface_host **out, void **token);
void ext_interface_host_release(void *token);
/* A graph owns one event queue and borrows it for each contained host. */
ext_interface_host *ext_interface_events_new(void);
/* Ask application policy for a bounded JSON result, without exposing a host. */
ext_plugin_status ext_interface_events_request(ext_interface_host *events, const char *kind,
  const uint8_t *data, size_t len, uint32_t timeout_ms, char **out, size_t *out_len);
void ext_interface_host_event_sink(ext_interface_host *host, ext_interface_host *sink,
                                  yyjson_val *consumer, yyjson_val *routes);
ext_plugin_status ext_interface_host_route_events(uint64_t handle, ext_interface_host *sink,
                                                  yyjson_val *consumer, yyjson_val *routes);
#endif
