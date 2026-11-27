#if defined(__linux__)
#define _GNU_SOURCE
#elif defined(__APPLE__)
#define _DARWIN_C_SOURCE
#endif
#include "volume.h"

#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

/* Checks always run: assert() would vanish under NDEBUG, taking the calls with it. */
#define CHECK(expr)                                                              \
    do {                                                                         \
        if (!(expr)) {                                                           \
            fprintf(stderr, "%s:%d: check failed: %s\n", __FILE__, __LINE__, #expr); \
            abort();                                                             \
        }                                                                        \
    } while (0)

static void join(char *out, size_t length, const char *root, const char *leaf) {
    CHECK(snprintf(out, length, "%s/%s", root, leaf) > 0);
}

static int stop(void *context) {
    (void)context;
    return 1;
}

int main(void) {
    char base[] = "/private/tmp/ext-system-volume.XXXXXX";
    if (!mkdtemp(base)) {
        strcpy(base, "/tmp/ext-system-volume.XXXXXX");
        CHECK(mkdtemp(base));
    }
    char path[PATH_MAX], file[PATH_MAX], link[PATH_MAX], moved[PATH_MAX];
    join(path, sizeof path, base, "private");
    join(file, sizeof file, path, "item");
    join(link, sizeof link, path, "link");
    join(moved, sizeof moved, base, "moved");
    ll_volume *volume = NULL;
    ll_volume_limits limits = {0};
    CHECK(ll_volume_create(path, limits, &volume) == LL_VOLUME_OK);
    ll_volume_info info;
    CHECK(ll_volume_info_get(volume, &info) == LL_VOLUME_OK);
    CHECK(info.created && info.entries == 0 && info.bytes == 0);
    ll_volume_close(volume);

    int fd = open(file, O_CREAT | O_EXCL | O_WRONLY, 0600);
    CHECK(fd >= 0 && write(fd, "data", 4) == 4);
    CHECK(close(fd) == 0);
    CHECK(ll_volume_inspect(path, limits, &volume) == LL_VOLUME_OK);
    CHECK(ll_volume_info_get(volume, &info) == LL_VOLUME_OK);
    CHECK(!info.created && info.entries == 1 && info.bytes == 4);
    CHECK(ll_volume_remove(volume, stop, NULL) == LL_VOLUME_CANCELED);
    CHECK(access(path, F_OK) == 0);
    int removed = ll_volume_remove(volume, NULL, NULL);
    if (removed) fprintf(stderr, "remove returned %d\n", removed);
    CHECK(removed == LL_VOLUME_OK);
    ll_volume_close(volume);
    CHECK(access(path, F_OK) != 0);

    CHECK(ll_volume_create(path, limits, &volume) == LL_VOLUME_OK);
    ll_volume_close(volume);
    CHECK(symlink("/etc/passwd", link) == 0);
    CHECK(ll_volume_inspect(path, limits, &volume) == LL_VOLUME_UNSAFE);
    CHECK(volume == NULL);
    CHECK(unlink(link) == 0);

    fd = open(file, O_CREAT | O_EXCL | O_WRONLY, 0600);
    CHECK(fd >= 0 && write(fd, "data", 4) == 4);
    CHECK(close(fd) == 0);
    ll_volume_limits tiny = {.max_entries = 1, .max_bytes = 3, .max_depth = 32};
    CHECK(ll_volume_inspect(path, tiny, &volume) == LL_VOLUME_LIMIT);
    CHECK(volume == NULL);
    CHECK(unlink(file) == 0);

    CHECK(ll_volume_inspect(path, limits, &volume) == LL_VOLUME_OK);
    CHECK(rename(path, moved) == 0);
    CHECK(mkdir(path, 0700) == 0);
    CHECK(ll_volume_remove(volume, NULL, NULL) == LL_VOLUME_STALE);
    ll_volume_close(volume);
    CHECK(rmdir(path) == 0);
    CHECK(rmdir(moved) == 0);
    CHECK(ll_volume_create("/private/tmp", limits, &volume) == LL_VOLUME_UNSAFE);
    CHECK(rmdir(base) == 0);
    puts("ok native private volume");
    return 0;
}
