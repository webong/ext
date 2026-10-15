#ifndef EXT_WIRE_H
#define EXT_WIRE_H
#include "ext_host.h"
#include "yyjson.h"
yyjson_doc *ext_parse(const uint8_t *, size_t);
int ext_descriptor(yyjson_val *);
int ext_match(yyjson_val *, yyjson_val *);
ext_status ext_request(yyjson_val *, yyjson_val *, int64_t *deadline_ms);
ext_status ext_response(yyjson_val *, const char *id);
int64_t ext_wall_ms(void);
int ext_fields(yyjson_val *, const char *const *, size_t);
int ext_text(yyjson_val *, int revision, int optional);
int ext_same(yyjson_val *, yyjson_val *);
int ext_identity(yyjson_val *);
int ext_same_identity(yyjson_val *, yyjson_val *);
int ext_ref(yyjson_val *);
int ext_same_ref(yyjson_val *, yyjson_val *);
yyjson_val *ext_lookup(yyjson_val *, yyjson_val *, yyjson_val *);
#define EXT_FIELDS(v, ...)                                                     \
  ext_fields(v, (const char *const[]){__VA_ARGS__},                            \
             sizeof((const char *const[]){__VA_ARGS__}) / sizeof(char *))
#endif
