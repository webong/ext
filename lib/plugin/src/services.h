#ifndef EXT_SERVICES_H
#define EXT_SERVICES_H
#include "wire.h"
int ext_protocols(yyjson_val *);
yyjson_val *ext_negotiate(yyjson_val *, yyjson_val *);
int ext_schema_valid(yyjson_val *, unsigned, unsigned *);
int ext_schema_check(yyjson_val *, yyjson_val *);
ext_plugin_status ext_package_service(const char *, yyjson_val *, ext_plugin_buffer *);
int ext_portable_path(const char *s, size_t n);
ext_plugin_status ext_manifest_valid(yyjson_val *);
int ext_path_fold_equal(const char *a, const char *b);
int ext_path_fold_tree_contains(const char *tree, const char *path);
/* The path with every character in simple lower case, the form in which two
 * paths that ext_path_fold_equal calls equal are the same bytes. NULL when the
 * path is not valid UTF-8 or memory runs out; free it. */
char *ext_path_fold_key(const char *path);
#endif
