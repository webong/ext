#ifndef EXT_INTERFACE_SCOPE_H
#define EXT_INTERFACE_SCOPE_H
#include "interface_host.h"
typedef struct ext_interface_scope ext_interface_scope;
ext_plugin_status ext_interface_scope_open(yyjson_val *, ext_interface_scope **);
ext_plugin_status ext_interface_scope_call(ext_interface_scope *, const char *, yyjson_val *, char **, size_t *);
void ext_interface_scope_shutdown(ext_interface_scope *);
void ext_interface_scope_free(ext_interface_scope *);
#endif
