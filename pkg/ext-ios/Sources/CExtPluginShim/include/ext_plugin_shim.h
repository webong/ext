#ifndef EXT_PLUGIN_SHIM_H
#define EXT_PLUGIN_SHIM_H

/* A plugin built on ExtPluginExports defines this function, normally through
 * ExtPluginExports.exportGuest. It returns a retained ExtPluginGuest.Guest, or
 * NULL when the guest cannot be created. Declaring it in C makes the link fail,
 * rather than misbehave at run time, when a plugin forgets to define it. */
extern void *ext_plugin_guest_factory(void);

#endif
