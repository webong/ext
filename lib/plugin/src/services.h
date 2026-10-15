#ifndef EXT_SERVICES_H
#define EXT_SERVICES_H
#include "wire.h"
int ext_protocols(yyjson_val *);
yyjson_val *ext_negotiate(yyjson_val *, yyjson_val *);
int ext_schema_valid(yyjson_val *, unsigned, unsigned *);
int ext_schema_check(yyjson_val *, yyjson_val *);
ext_status ext_package_service(const char *, yyjson_val *, ext_buffer *);
ext_status ext_manifest_valid(yyjson_val *);
#endif
