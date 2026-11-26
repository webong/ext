#include "ext_system_net.h"

#include <assert.h>
#include <stdio.h>
#include <string.h>

int main(void) {
    ext_system_net_snapshot *snapshot = NULL;
    ext_system_net_options options = ext_system_net_default_options();
    assert(options.max_interfaces == 256 && options.max_addresses == 256 &&
           options.max_routes == 4096 && options.timeout_ms == 5000);
    assert(ext_system_net_observe((ext_system_net_options){0}, &snapshot) == -1);
    assert(snapshot == NULL);
    assert(ext_system_net_observe(options, &snapshot) == 0);
    assert(snapshot && snapshot->interface_count <= options.max_interfaces);
    assert(snapshot->route_count <= options.max_routes);
    for (size_t i = 0; i < snapshot->interface_count; ++i) {
        ext_system_net_interface *item = &snapshot->interfaces[i];
        assert(item->name[0] && item->address_count <= options.max_addresses);
        assert(item->address_first + item->address_count <= snapshot->address_count);
    }
    for (size_t i = 0; i < snapshot->route_count; ++i)
        assert(strchr(snapshot->routes[i].destination, '/'));
    ext_system_net_free(snapshot);
    puts("ok native Linux network snapshot");
    return 0;
}
