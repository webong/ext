/* SwiftPM requires a C target to contain a source file. Swift cannot import
 * macros defined by an expression, so ExtPluginExports restates this constant;
 * the assertion fails the build if ext_plugin.h ever changes it. */
#include "ext_plugin.h"
#include "ext_plugin_shim.h"

_Static_assert(EXT_PLUGIN_MAX_FRAME_BYTES == 25165824u,
               "update extPluginMaxFrameBytes in ExtPluginExports to match ext_plugin.h");
