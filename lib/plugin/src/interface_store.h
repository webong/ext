#ifndef EXT_INTERFACE_STORE_H
#define EXT_INTERFACE_STORE_H
#include "services.h"

/* Parses the optional application-supplied package layout. Strings borrow
 * their storage from input. An omitted layout leaves manifest_path unset. */
ext_plugin_status ext_interface_store_layout(yyjson_val *input, ext_plugin_store_layout *layout);
#endif
