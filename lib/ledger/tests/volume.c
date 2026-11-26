#if defined(__linux__)
#define _GNU_SOURCE
#elif defined(__APPLE__)
#define _DARWIN_C_SOURCE
#endif
#include "volume.h"

#include <assert.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

static void join(char *out, size_t length, const char *root, const char *leaf) {
    assert(snprintf(out, length, "%s/%s", root, leaf) > 0);
}

static int stop(void *context) {
    (void)context;
    return 1;
}

int main(void) {
    char base[] = "/private/tmp/ext-system-volume.XXXXXX";
    if (!mkdtemp(base)) {
        strcpy(base, "/tmp/ext-system-volume.XXXXXX");
        assert(mkdtemp(base));
    }
    char path[PATH_MAX], file[PATH_MAX], link[PATH_MAX], moved[PATH_MAX];
    join(path, sizeof path, base, "private");
    join(file, sizeof file, path, "item");
    join(link, sizeof link, path, "link");
    join(moved, sizeof moved, base, "moved");
    ll_volume *volume = NULL;
    ll_volume_limits limits = {0};
    assert(ll_volume_create(path, limits, &volume) == LL_VOLUME_OK);
    ll_volume_info info;
    assert(ll_volume_info_get(volume, &info) == LL_VOLUME_OK);
    assert(info.created && info.entries == 0 && info.bytes == 0);
    ll_volume_close(volume);

    int fd = open(file, O_CREAT | O_EXCL | O_WRONLY, 0600);
    assert(fd >= 0 && write(fd, "data", 4) == 4);
    assert(close(fd) == 0);
    assert(ll_volume_inspect(path, limits, &volume) == LL_VOLUME_OK);
    assert(ll_volume_info_get(volume, &info) == LL_VOLUME_OK);
    assert(!info.created && info.entries == 1 && info.bytes == 4);
    assert(ll_volume_remove(volume, stop, NULL) == LL_VOLUME_CANCELED);
    assert(access(path, F_OK) == 0);
    int removed = ll_volume_remove(volume, NULL, NULL);
    if (removed) fprintf(stderr, "remove returned %d\n", removed);
    assert(removed == LL_VOLUME_OK);
    ll_volume_close(volume);
    assert(access(path, F_OK) != 0);

    assert(ll_volume_create(path, limits, &volume) == LL_VOLUME_OK);
    ll_volume_close(volume);
    assert(symlink("/etc/passwd", link) == 0);
    assert(ll_volume_inspect(path, limits, &volume) == LL_VOLUME_UNSAFE);
    assert(volume == NULL);
    assert(unlink(link) == 0);

    fd = open(file, O_CREAT | O_EXCL | O_WRONLY, 0600);
    assert(fd >= 0 && write(fd, "data", 4) == 4);
    assert(close(fd) == 0);
    ll_volume_limits tiny = {.max_entries = 1, .max_bytes = 3, .max_depth = 32};
    assert(ll_volume_inspect(path, tiny, &volume) == LL_VOLUME_LIMIT);
    assert(volume == NULL);
    assert(unlink(file) == 0);

    assert(ll_volume_inspect(path, limits, &volume) == LL_VOLUME_OK);
    assert(rename(path, moved) == 0);
    assert(mkdir(path, 0700) == 0);
    assert(ll_volume_remove(volume, NULL, NULL) == LL_VOLUME_STALE);
    ll_volume_close(volume);
    assert(rmdir(path) == 0);
    assert(rmdir(moved) == 0);
    assert(ll_volume_create("/private/tmp", limits, &volume) == LL_VOLUME_UNSAFE);
    assert(rmdir(base) == 0);
    puts("ok native private volume");
    return 0;
}
