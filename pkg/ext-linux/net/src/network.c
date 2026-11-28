#include "ext_system_net.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <ifaddrs.h>
#include <limits.h>
#include <linux/if_packet.h>
#include <net/if.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>

enum { resolver_limit = 256 };

ext_system_net_options ext_system_net_default_options(void) {
    return (ext_system_net_options){256, 256, 4096, 5000};
}

static int valid(ext_system_net_options o) {
    return o.max_interfaces && o.max_interfaces <= 65536 &&
           o.max_addresses && o.max_addresses <= 65536 &&
           o.max_routes && o.max_routes <= 65536 &&
           o.timeout_ms && o.timeout_ms <= 3600000;
}

int ext_system_net_validate_options(ext_system_net_options options) {
    return valid(options) ? 0 : -1;
}

static int expired(const struct timespec *start, uint32_t timeout_ms) {
    struct timespec now;
    if (clock_gettime(CLOCK_MONOTONIC, &now)) return 1;
    int64_t elapsed = (int64_t)(now.tv_sec - start->tv_sec) * 1000 +
                      (now.tv_nsec - start->tv_nsec) / 1000000;
    return elapsed >= timeout_ms;
}

static void copy(char *out, size_t capacity, const char *value) {
    if (capacity) snprintf(out, capacity, "%s", value ? value : "");
}

static const char *scope(const unsigned char *ip, int family) {
    if (family == AF_INET) {
        if (ip[0] == 127) return "loopback";
        if (ip[0] == 0 && ip[1] == 0 && ip[2] == 0 && ip[3] == 0) return "unspecified";
        if ((ip[0] == 169 && ip[1] == 254) ||
            (ip[0] == 224 && ip[1] == 0 && ip[2] == 0)) return "link";
        if (ip[0] == 10 || (ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
            (ip[0] == 192 && ip[1] == 168)) return "private";
        if (ip[0] >= 224 && ip[0] <= 239) return "multicast";
        return "global";
    }
    static const unsigned char zero[16] = {0};
    if (memcmp(ip, zero, 16) == 0) return "unspecified";
    if (memcmp(ip, zero, 15) == 0 && ip[15] == 1) return "loopback";
    if ((ip[0] == 0xfe && (ip[1] & 0xc0) == 0x80) ||
        (ip[0] == 0xff && (ip[1] & 15) == 2)) return "link";
    if ((ip[0] & 0xfe) == 0xfc) return "private";
    if (ip[0] == 0xff) return "multicast";
    return "global";
}

int ext_system_net_parse_address(const char *literal, int bits,
                                  ext_system_net_address *out) {
    if (!literal || !out || !*literal || strlen(literal) > 45 ||
        strchr(literal, '%') || strchr(literal, '/') ||
        strchr(literal, '[') || strchr(literal, ']')) return -1;
    int family = strchr(literal, ':') ? AF_INET6 : AF_INET;
    if (bits < 0 || bits > (family == AF_INET ? 32 : 128)) return -1;
    if (family == AF_INET) {
        int groups = 0;
        const char *part = literal;
        for (const char *p = literal;; ++p) {
            if (*p != '.' && *p != '\0') {
                if (*p < '0' || *p > '9') return -1;
                continue;
            }
            size_t length = (size_t)(p - part);
            if (!length || length > 3 ||
                (length > 1 && part[0] == '0')) return -1;
            ++groups;
            if (*p == '\0') break;
            part = p + 1;
        }
        if (groups != 4) return -1;
    }
    unsigned char address[16] = {0};
    if (inet_pton(family, literal, address) != 1) return -1;
    memset(out, 0, sizeof *out);
    if (!inet_ntop(family, address, out->ip, sizeof out->ip)) return -1;
    out->family = family == AF_INET ? 4 : 6;
    out->prefix_length = (uint8_t)bits;
    copy(out->scope, sizeof out->scope, scope(address, family));
    return 0;
}

static int prefix(const unsigned char *mask, size_t bytes) {
    int count = 0, ended = 0;
    for (size_t i = 0; i < bytes; ++i)
        for (int bit = 7; bit >= 0; --bit) {
            int set = (mask[i] >> bit) & 1;
            if (set && ended) return -1;
            if (set) ++count;
            else ended = 1;
        }
    return count;
}

static int index_of(ext_system_net_snapshot *s, const char *name) {
    for (size_t i = 0; i < s->interface_count; ++i)
        if (strcmp(s->interfaces[i].name, name) == 0) return (int)i;
    return -1;
}

static int interfaces(ext_system_net_snapshot *s, ext_system_net_options o,
                      const struct timespec *start) {
    struct ifaddrs *list = NULL;
    if (getifaddrs(&list)) {
        s->interface_state = EXT_SYSTEM_NET_UNAVAILABLE;
        return 0;
    }
    size_t address_capacity = o.max_interfaces * (size_t)o.max_addresses;
    if (address_capacity > 65536) address_capacity = 65536;
    s->interfaces = calloc(o.max_interfaces, sizeof *s->interfaces);
    s->addresses = calloc(address_capacity, sizeof *s->addresses);
    if (!s->interfaces || !s->addresses) { freeifaddrs(list); return -3; }
    s->interface_state = EXT_SYSTEM_NET_COMPLETE;
    for (struct ifaddrs *node = list; node; node = node->ifa_next) {
        if (expired(start, o.timeout_ms)) { freeifaddrs(list); return -2; }
        if (!node->ifa_name || index_of(s, node->ifa_name) >= 0) continue;
        if (s->interface_count >= o.max_interfaces) {
            s->interface_state = EXT_SYSTEM_NET_PARTIAL; continue;
        }
        ext_system_net_interface *item = &s->interfaces[s->interface_count++];
        item->index = if_nametoindex(node->ifa_name);
        copy(item->name, sizeof item->name, node->ifa_name);
        item->flags = node->ifa_flags;
        if (strlen(node->ifa_name) >= sizeof item->name)
            s->interface_state = EXT_SYSTEM_NET_PARTIAL;
    }
    for (size_t i = 0; i < s->interface_count; ++i) {
        ext_system_net_interface *item = &s->interfaces[i];
        item->address_first = s->address_count;
        for (struct ifaddrs *node = list; node; node = node->ifa_next) {
            if (expired(start, o.timeout_ms)) { freeifaddrs(list); return -2; }
            if (!node->ifa_name || strcmp(node->ifa_name, item->name)) continue;
            if (!node->ifa_addr) continue;
            int family = node->ifa_addr->sa_family;
            if (family == AF_PACKET && item->hardware_address[0] == '\0') {
                struct sockaddr_ll *link = (struct sockaddr_ll *)node->ifa_addr;
                if (link->sll_halen == 6)
                    snprintf(item->hardware_address, sizeof item->hardware_address,
                             "%02x:%02x:%02x:%02x:%02x:%02x",
                             link->sll_addr[0], link->sll_addr[1],
                             link->sll_addr[2], link->sll_addr[3],
                             link->sll_addr[4], link->sll_addr[5]);
                continue;
            }
            if (family != AF_INET && family != AF_INET6) continue;
            if (item->address_count >= o.max_addresses ||
                s->address_count >= address_capacity) {
                s->interface_state = EXT_SYSTEM_NET_PARTIAL; continue;
            }
            const void *ip = family == AF_INET ?
                (const void *)&((struct sockaddr_in *)node->ifa_addr)->sin_addr :
                (const void *)&((struct sockaddr_in6 *)node->ifa_addr)->sin6_addr;
            const void *mask = node->ifa_netmask ?
                (family == AF_INET ?
                 (const void *)&((struct sockaddr_in *)node->ifa_netmask)->sin_addr :
                 (const void *)&((struct sockaddr_in6 *)node->ifa_netmask)->sin6_addr) : NULL;
            int bits = mask ? prefix(mask, family == AF_INET ? 4 : 16) : -1;
            if (bits < 0) { s->interface_state = EXT_SYSTEM_NET_PARTIAL; continue; }
            ext_system_net_address *address = &s->addresses[s->address_count++];
            address->family = family == AF_INET ? 4 : 6;
            address->prefix_length = (uint8_t)bits;
            if (!inet_ntop(family, ip, address->ip, sizeof address->ip)) {
                --s->address_count; s->interface_state = EXT_SYSTEM_NET_PARTIAL; continue;
            }
            copy(address->scope, sizeof address->scope, scope(ip, family));
            ++item->address_count;
        }
        /* sysfs names are kernel-supplied, but still reject path separators. */
        if (strchr(item->name, '/') || strcmp(item->name, ".") == 0 ||
            strcmp(item->name, "..") == 0) {
            s->interface_state = EXT_SYSTEM_NET_PARTIAL;
            continue;
        }
        char path[PATH_MAX];
        if (snprintf(path, sizeof path, "/sys/class/net/%s/mtu", item->name) >= (int)sizeof path) continue;
        FILE *file = fopen(path, "r");
        if (file) {
            if (fscanf(file, "%d", &item->mtu) != 1) item->mtu = 0;
            fclose(file);
        }
    }
    freeifaddrs(list);
    return 0;
}

static int ipv4_hex(const char *text, char *out, size_t capacity) {
    unsigned long value;
    char tail;
    if (sscanf(text, "%8lx%c", &value, &tail) != 1 || strlen(text) != 8 ||
        value > UINT32_MAX) return -1;
    unsigned char bytes[4] = {
        (unsigned char)value, (unsigned char)(value >> 8),
        (unsigned char)(value >> 16), (unsigned char)(value >> 24)
    };
    return inet_ntop(AF_INET, bytes, out, capacity) ? 0 : -1;
}

static int ipv6_hex(const char *text, char *out, size_t capacity) {
    if (strlen(text) != 32) return -1;
    for (size_t i = 0; i < 32; ++i)
        if (!isxdigit((unsigned char)text[i])) return -1;
    unsigned char bytes[16];
    for (size_t i = 0; i < 16; ++i) {
        unsigned int value;
        if (sscanf(text + i * 2, "%2x", &value) != 1) return -1;
        bytes[i] = (unsigned char)value;
    }
    return inet_ntop(AF_INET6, bytes, out, capacity) ? 0 : -1;
}

static int collect_routes(ext_system_net_snapshot *s, ext_system_net_options o,
                          const struct timespec *start, const char *path, int v6) {
    FILE *file = fopen(path, "r");
    if (!file) { s->route_state = EXT_SYSTEM_NET_PARTIAL; return 0; }
    char *line = NULL;
    size_t capacity = 0;
    int first = !v6, code = 0;
    while (getline(&line, &capacity, file) >= 0) {
        if (expired(start, o.timeout_ms)) { code = -2; break; }
        if (first) { first = 0; continue; }
        if (s->route_count >= o.max_routes) {
            s->route_state = EXT_SYSTEM_NET_PARTIAL; break;
        }
        char iface[64], dest[40], gateway[40], mask[16];
        unsigned long metric = 0, bits = 0;
        ext_system_net_route *route = &s->routes[s->route_count];
        char destination[46], next[46];
        if (!v6) {
            unsigned long flags, ref, use;
            int parsed = sscanf(line, "%63s %8s %8s %lx %lu %lu %lu %8s",
                                iface, dest, gateway, &flags, &ref, &use,
                                &metric, mask);
            if (parsed != 8 || ipv4_hex(dest, destination, sizeof destination) ||
                ipv4_hex(gateway, next, sizeof next)) {
                s->route_state = EXT_SYSTEM_NET_PARTIAL; continue;
            }
            char mask_ip[46];
            unsigned char bytes[4];
            if (ipv4_hex(mask, mask_ip, sizeof mask_ip) ||
                inet_pton(AF_INET, mask_ip, bytes) != 1 ||
                (bits = (unsigned long)prefix(bytes, 4)) > 32) {
                s->route_state = EXT_SYSTEM_NET_PARTIAL; continue;
            }
        } else {
            char source[40], source_bits[4], prefix_hex[4];
            char ref[16], use[16], flags[16];
            int parsed = sscanf(line, "%39s %3s %39s %3s %39s %lx %15s %15s %15s %63s",
                                dest, prefix_hex, source, source_bits,
                                gateway, &metric, ref, use, flags, iface);
            if (parsed != 10 || sscanf(prefix_hex, "%lx", &bits) != 1 ||
                bits > 128 || ipv6_hex(dest, destination, sizeof destination) ||
                ipv6_hex(gateway, next, sizeof next)) {
                s->route_state = EXT_SYSTEM_NET_PARTIAL; continue;
            }
        }
        snprintf(route->destination, sizeof route->destination, "%s/%lu", destination, bits);
        copy(route->gateway, sizeof route->gateway,
             strcmp(next, v6 ? "::" : "0.0.0.0") == 0 ? "" : next);
        copy(route->interface_name, sizeof route->interface_name, iface);
        route->metric = metric > UINT32_MAX ? UINT32_MAX : (uint32_t)metric;
        route->is_default = bits == 0 &&
            strcmp(destination, v6 ? "::" : "0.0.0.0") == 0;
        ++s->route_count;
    }
    if (ferror(file)) s->route_state = EXT_SYSTEM_NET_PARTIAL;
    free(line);
    fclose(file);
    return code;
}

static int add_unique(char (*values)[256], size_t *count, const char *text) {
    if (!text || !*text) return 0;
    for (size_t i = 0; i < *count; ++i)
        if (strcmp(values[i], text) == 0) return 0;
    if (*count >= resolver_limit) return 1;
    copy(values[(*count)++], 256, text);
    return 0;
}

static int resolver(ext_system_net_snapshot *s, ext_system_net_options o,
                    const struct timespec *start) {
    s->servers = calloc(resolver_limit, sizeof *s->servers);
    s->search = calloc(resolver_limit, sizeof *s->search);
    s->resolver_options = calloc(resolver_limit, sizeof *s->resolver_options);
    if (!s->servers || !s->search || !s->resolver_options) return -3;
    FILE *file = fopen("/etc/resolv.conf", "r");
    if (!file) { s->resolver_state = EXT_SYSTEM_NET_UNAVAILABLE; return 0; }
    s->resolver_state = EXT_SYSTEM_NET_COMPLETE;
    char *line = NULL;
    size_t capacity = 0;
    int code = 0;
    while (getline(&line, &capacity, file) >= 0) {
        if (expired(start, o.timeout_ms)) { code = -2; break; }
        char *hash = strchr(line, '#');
        if (hash) *hash = '\0';
        char *save = NULL;
        char *kind = strtok_r(line, " \t\r\n", &save);
        if (!kind) continue;
        char (*list)[256] = NULL;
        size_t *count = NULL;
        if (strcmp(kind, "nameserver") == 0) {
            list = s->servers; count = &s->server_count;
        } else if (strcmp(kind, "search") == 0 || strcmp(kind, "domain") == 0) {
            list = s->search; count = &s->search_count;
        } else if (strcmp(kind, "options") == 0) {
            list = s->resolver_options; count = &s->resolver_option_count;
        }
        if (!list) continue;
        char *word;
        int first = 1;
        while ((word = strtok_r(NULL, " \t\r\n", &save)) != NULL) {
            if (strlen(word) >= 256) { s->resolver_state = EXT_SYSTEM_NET_PARTIAL; continue; }
            if (strcmp(kind, "nameserver") == 0 && !first) break;
            if (strcmp(kind, "domain") == 0 && s->search_count) break;
            if (add_unique(list, count, word))
                s->resolver_state = EXT_SYSTEM_NET_PARTIAL;
            first = 0;
        }
    }
    if (ferror(file)) s->resolver_state = EXT_SYSTEM_NET_PARTIAL;
    free(line);
    fclose(file);
    return code;
}

int ext_system_net_observe(ext_system_net_options options,
                           ext_system_net_snapshot **out) {
    if (!out || !valid(options)) return -1;
    *out = NULL;
    struct timespec start;
    if (clock_gettime(CLOCK_MONOTONIC, &start)) return -2;
    ext_system_net_snapshot *snapshot = calloc(1, sizeof *snapshot);
    if (!snapshot) return -3;
    snapshot->route_state = EXT_SYSTEM_NET_COMPLETE;
    int code = interfaces(snapshot, options, &start);
    if (!code) {
        snapshot->routes = calloc(options.max_routes, sizeof *snapshot->routes);
        if (!snapshot->routes) code = -3;
    }
    if (!code) code = collect_routes(snapshot, options, &start, "/proc/net/route", 0);
    if (!code) code = collect_routes(snapshot, options, &start, "/proc/net/ipv6_route", 1);
    if (!code) code = resolver(snapshot, options, &start);
    if (code) { ext_system_net_free(snapshot); return code; }
    *out = snapshot;
    return 0;
}

void ext_system_net_free(ext_system_net_snapshot *snapshot) {
    if (!snapshot) return;
    free(snapshot->interfaces);
    free(snapshot->addresses);
    free(snapshot->routes);
    free(snapshot->servers);
    free(snapshot->search);
    free(snapshot->resolver_options);
    free(snapshot);
}
