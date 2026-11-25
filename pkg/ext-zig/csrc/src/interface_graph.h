#ifndef EXT_INTERFACE_GRAPH_H
#define EXT_INTERFACE_GRAPH_H
#include "interface_host.h"
typedef struct ext_interface_graph ext_interface_graph;
ext_plugin_status ext_interface_graph_open(yyjson_val *, ext_interface_graph **);
ext_plugin_status ext_interface_graph_call(ext_interface_graph *, const char *, yyjson_val *, char **, size_t *);
void ext_interface_graph_shutdown(ext_interface_graph *);
void ext_interface_graph_free(ext_interface_graph *);
#endif
