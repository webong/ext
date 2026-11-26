#ifndef LL_VOLUME_H
#define LL_VOLUME_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ll_volume ll_volume;

typedef struct {
    uint32_t max_entries;
    uint64_t max_bytes;
    uint32_t max_depth;
} ll_volume_limits;

typedef struct {
    uint64_t device;
    uint64_t inode;
    uint32_t entries;
    uint64_t bytes;
    int created;
} ll_volume_info;

enum {
    LL_VOLUME_OK = 0,
    LL_VOLUME_INVALID = 1,
    LL_VOLUME_UNSAFE = 2,
    LL_VOLUME_STALE = 3,
    LL_VOLUME_LIMIT = 4,
    LL_VOLUME_IO = 5,
    LL_VOLUME_CANCELED = 6,
    LL_VOLUME_NOT_FOUND = 7,
    LL_VOLUME_UNSUPPORTED = 8
};

/* Zero limits select 10,000 entries, 1 GiB and 32 path components. */
ll_volume_limits ll_volume_default_limits(void);

/* The absolute caller-selected path is never chosen or authorized by this library.
 * An opaque inspected handle must be kept for removal. No link is followed. */
int ll_volume_create(const char *path, ll_volume_limits limits,
                             ll_volume **out);
int ll_volume_inspect(const char *path, ll_volume_limits limits,
                              ll_volume **out);
/* Scalar entry point for language bindings that do not marshal C structs by value. */
int ll_volume_open(const char *path, uint32_t max_entries,
                           uint64_t max_bytes, uint32_t max_depth,
                           int create, ll_volume **out);
int ll_volume_open_cancellable(const char *path, uint32_t max_entries,
                           uint64_t max_bytes, uint32_t max_depth,
                           int create, int (*canceled)(void *), void *context,
                           ll_volume **out);
int ll_volume_info_get(const ll_volume *volume,
                               ll_volume_info *out);
/* canceled may be NULL; return nonzero to stop bounded traversal. */
int ll_volume_remove(ll_volume *volume,
                             int (*canceled)(void *), void *context);
void ll_volume_close(ll_volume *volume);

#ifdef __cplusplus
}
#endif
#endif
