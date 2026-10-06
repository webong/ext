#ifndef CTX_WIRE_H
#define CTX_WIRE_H
#include "ctx_host.h"
#include "yyjson.h"
yyjson_doc *ctx_parse(const uint8_t *, size_t);
int ctx_descriptor(yyjson_val *);
int ctx_match(yyjson_val *, yyjson_val *);
ctx_status ctx_request(yyjson_val *, yyjson_val *, int64_t *deadline_ms);
ctx_status ctx_response(yyjson_val *, const char *id);
int64_t ctx_wall_ms(void);
int ctx_fields(yyjson_val *, const char *const *, size_t);
int ctx_text(yyjson_val *, int revision, int optional);
int ctx_same(yyjson_val *, yyjson_val *);
int ctx_identity(yyjson_val *);
int ctx_same_identity(yyjson_val *, yyjson_val *);
int ctx_ref(yyjson_val *);
int ctx_same_ref(yyjson_val *, yyjson_val *);
yyjson_val *ctx_lookup(yyjson_val *, yyjson_val *, yyjson_val *);
#define CTX_FIELDS(v, ...)                                                     \
  ctx_fields(v, (const char *const[]){__VA_ARGS__},                            \
             sizeof((const char *const[]){__VA_ARGS__}) / sizeof(char *))
#endif
