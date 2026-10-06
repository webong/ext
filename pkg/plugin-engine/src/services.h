#ifndef CTX_SERVICES_H
#define CTX_SERVICES_H
#include "wire.h"
int ctx_protocols(yyjson_val *);
yyjson_val *ctx_negotiate(yyjson_val *, yyjson_val *);
int ctx_schema_valid(yyjson_val *, unsigned, unsigned *);
int ctx_schema_check(yyjson_val *, yyjson_val *);
ctx_status ctx_package_service(const char *, yyjson_val *, ctx_buffer *);
ctx_status ctx_manifest_valid(yyjson_val *);
#endif
