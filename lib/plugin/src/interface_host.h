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
#endif
