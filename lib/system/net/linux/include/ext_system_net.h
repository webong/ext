#ifndef EXT_SYSTEM_NET_H
#define EXT_SYSTEM_NET_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef enum {
    EXT_SYSTEM_NET_COMPLETE,
    EXT_SYSTEM_NET_PARTIAL,
    EXT_SYSTEM_NET_UNAVAILABLE,
    EXT_SYSTEM_NET_UNSUPPORTED
} ext_system_net_state;

typedef struct {
    uint32_t max_interfaces;
    uint32_t max_addresses;
    uint32_t max_routes;
    uint32_t timeout_ms;
} ext_system_net_options;

typedef struct {
    char ip[46];
    uint8_t prefix_length;
    uint8_t family; /* 4 or 6 */
    char scope[16];
} ext_system_net_address;

typedef struct {
    uint32_t index;
    char name[64];
    int mtu;
    char hardware_address[24];
    uint32_t flags;
    size_t address_first;
    size_t address_count;
} ext_system_net_interface;

typedef struct {
    char destination[50];
    char gateway[46];
    char interface_name[64];
    uint32_t metric;
    int is_default;
} ext_system_net_route;

typedef struct {
    ext_system_net_interface *interfaces;
    size_t interface_count;
    ext_system_net_address *addresses;
    size_t address_count;
    ext_system_net_route *routes;
    size_t route_count;
    char (*servers)[256];
    size_t server_count;
    char (*search)[256];
    size_t search_count;
    char (*resolver_options)[256];
    size_t resolver_option_count;
    ext_system_net_state interface_state;
    ext_system_net_state route_state;
    ext_system_net_state resolver_state;
} ext_system_net_snapshot;

ext_system_net_options ext_system_net_default_options(void);
int ext_system_net_validate_options(ext_system_net_options options);
/* Strict literal IP normalization and scope, held to network/v1 fixtures. */
int ext_system_net_parse_address(const char *literal, int prefix,
                                  ext_system_net_address *out);
/* Returns 0 for a bounded observation, -1 for invalid options, -2 for
 * cancellation or deadline, and -3 for allocation failure. Native read
 * failures are expressed in the corresponding collection state. */
int ext_system_net_observe(ext_system_net_options options,
                           ext_system_net_snapshot **out);
void ext_system_net_free(ext_system_net_snapshot *snapshot);

#ifdef __cplusplus
}
#endif
#endif
