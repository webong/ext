#if defined(__linux__)
#define _GNU_SOURCE
#elif defined(__APPLE__)
#define _DARWIN_C_SOURCE
#endif
#include "volume.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef O_NOFOLLOW
#error "private volumes require O_NOFOLLOW"
#endif

struct ll_volume {
    int parent;
    int root;
    char *path;
    char *leaf;
    dev_t device;
    ino_t inode;
    ll_volume_limits limits;
    ll_volume_info info;
};

ll_volume_limits ll_volume_default_limits(void) {
    return (ll_volume_limits){10000, UINT64_C(1) << 30, 32};
}

static int valid_limits(ll_volume_limits *limits) {
    ll_volume_limits defaults = ll_volume_default_limits();
    if (!limits->max_entries) limits->max_entries = defaults.max_entries;
    if (!limits->max_bytes) limits->max_bytes = defaults.max_bytes;
    if (!limits->max_depth) limits->max_depth = defaults.max_depth;
    return limits->max_entries <= 1000000 &&
           limits->max_bytes <= (UINT64_C(1) << 40) &&
           limits->max_depth <= 128;
}

static bool broad_path(const char *path) {
    static const char *roots[] = {
        "/", "/Users", "/home", "/root", "/var", "/tmp", "/private",
        "/private/tmp", "/var/tmp",
        "/etc", "/usr", "/bin", "/sbin", "/opt", "/Library", "/System",
        "/Applications", "/Volumes", "/dev", "/proc", "/sys", "/run",
        "/mnt", "/media", "/srv", "/boot", "/snap"
    };
    for (size_t i = 0; i < sizeof roots / sizeof roots[0]; ++i)
        if (strcmp(path, roots[i]) == 0) return true;
    const char *home = getenv("HOME");
    if (home) {
        size_t size = strlen(home);
        while (size > 1 && home[size - 1] == '/') --size;
        if (strlen(path) == size && strncmp(path, home, size) == 0) return true;
    }
    const char *tmp = getenv("TMPDIR");
    if (tmp) {
        size_t size = strlen(tmp);
        while (size > 1 && tmp[size - 1] == '/') --size;
        if (strlen(path) == size && strncmp(path, tmp, size) == 0) return true;
    }
    return false;
}

static int path_parts(const char *path, ll_volume_limits limits,
                      char ***parts_out, size_t *count_out) {
    if (!path || path[0] != '/' || strlen(path) > 4096)
        return LL_VOLUME_INVALID;
    if (broad_path(path)) return LL_VOLUME_UNSAFE;
    const char *effective = path;
    char *resolved_alias = NULL;
#ifdef __APPLE__
    /* macOS exposes these fixed system paths as links into /private. Admit
     * only the system-owned aliases; every other link is still rejected. */
    static const char *aliases[] = {"/var", "/tmp", "/etc"};
    for (size_t i = 0; i < sizeof aliases / sizeof aliases[0]; ++i) {
        size_t prefix = strlen(aliases[i]);
        if (strncmp(path, aliases[i], prefix) != 0 || path[prefix] != '/')
            continue;
        char target[64];
        ssize_t length = readlink(aliases[i], target, sizeof target - 1);
        if (length < 0) return LL_VOLUME_IO;
        target[length] = '\0';
        char expected[64];
        snprintf(expected, sizeof expected, "private%s", aliases[i]);
        if (strcmp(target, expected) != 0) return LL_VOLUME_UNSAFE;
        size_t size = strlen(path) + sizeof "/private";
        resolved_alias = malloc(size);
        if (!resolved_alias) return LL_VOLUME_IO;
        snprintf(resolved_alias, size, "/private%s", path);
        effective = resolved_alias;
        break;
    }
#endif
    bool used_alias = resolved_alias != NULL;
    if (strlen(effective) > 4096) { free(resolved_alias); return LL_VOLUME_INVALID; }
    char *copy = strdup(effective + 1);
    free(resolved_alias);
    if (!copy) return LL_VOLUME_IO;
    size_t count = 0;
    for (char *p = copy; *p; ++p) if (*p == '/') ++count;
    ++count;
    size_t logical_count = count - used_alias;
    if (logical_count < 2 || logical_count > limits.max_depth) {
        free(copy);
        return logical_count < 2 ? LL_VOLUME_UNSAFE : LL_VOLUME_LIMIT;
    }
    char **parts = calloc(count, sizeof *parts);
    if (!parts) {
        free(copy);
        return LL_VOLUME_IO;
    }
    size_t n = 0;
    char *save = NULL;
    for (char *part = strtok_r(copy, "/", &save); part;
         part = strtok_r(NULL, "/", &save)) {
        size_t length = strlen(part);
        if (!length || length > 255 || strcmp(part, ".") == 0 ||
            strcmp(part, "..") == 0 || n >= count) {
            int unsafe = strcmp(part, "..") == 0;
            free(parts);
            free(copy);
            return unsafe ? LL_VOLUME_UNSAFE :
                   LL_VOLUME_INVALID;
        }
        parts[n++] = strdup(part);
        if (!parts[n-1]) {
            for (size_t i = 0; i < n; ++i) free(parts[i]);
            free(parts);
            free(copy);
            return LL_VOLUME_IO;
        }
    }
    free(copy);
    if (n != count) {
        for (size_t i = 0; i < n; ++i) free(parts[i]);
        free(parts);
        return LL_VOLUME_INVALID;
    }
    *parts_out = parts;
    *count_out = n;
    return LL_VOLUME_OK;
}

static void free_parts(char **parts, size_t count) {
    for (size_t i = 0; i < count; ++i) free(parts[i]);
    free(parts);
}

static int traverse(const char *path, ll_volume_limits limits,
                    int create, int (*canceled)(void *), void *context,
                    ll_volume **out) {
    if (!out || !valid_limits(&limits)) return LL_VOLUME_INVALID;
    *out = NULL;
    char **parts = NULL;
    size_t count = 0;
    int code = path_parts(path, limits, &parts, &count);
    if (code) return code;
    int parent = open("/", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (parent < 0) { free_parts(parts, count); return LL_VOLUME_IO; }
    int created = 0;
    for (size_t i = 0; i < count; ++i) {
        if (canceled && canceled(context)) {
            code = LL_VOLUME_CANCELED; goto fail;
        }
        struct stat before;
        if (fstatat(parent, parts[i], &before, AT_SYMLINK_NOFOLLOW) < 0) {
            if (!create && errno == ENOENT) {
                code = LL_VOLUME_NOT_FOUND;
                goto fail;
            }
            if (!create || errno != ENOENT ||
                mkdirat(parent, parts[i], 0700) < 0) {
                code = LL_VOLUME_IO;
                goto fail;
            }
            if (i == count - 1) created = 1;
            if (fstatat(parent, parts[i], &before, AT_SYMLINK_NOFOLLOW) < 0) {
                code = LL_VOLUME_STALE; goto fail;
            }
        } else if (!S_ISDIR(before.st_mode)) {
            code = LL_VOLUME_UNSAFE;
            goto fail;
        }
        int next = openat(parent, parts[i],
                          O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
        if (next < 0) {
            code = errno == ELOOP || errno == ENOTDIR ?
                   LL_VOLUME_UNSAFE : LL_VOLUME_IO;
            goto fail;
        }
        struct stat stat_next, stat_parent;
        if (fstat(next, &stat_next) < 0 || fstat(parent, &stat_parent) < 0) {
            close(next); code = LL_VOLUME_IO; goto fail;
        }
        if (stat_next.st_dev != before.st_dev ||
            stat_next.st_ino != before.st_ino) {
            close(next); code = LL_VOLUME_STALE; goto fail;
        }
        if (i == count - 1 && (stat_next.st_mode & 0077 ||
                                stat_next.st_dev != stat_parent.st_dev)) {
            close(next); code = LL_VOLUME_UNSAFE; goto fail;
        }
        if (i == count - 1) {
            ll_volume *volume = calloc(1, sizeof *volume);
            if (!volume) { close(next); code = LL_VOLUME_IO; goto fail; }
            volume->parent = parent;
            volume->root = next;
            volume->path = strdup(path);
            volume->leaf = strdup(parts[i]);
            volume->device = stat_next.st_dev;
            volume->inode = stat_next.st_ino;
            volume->limits = limits;
            volume->info.device = (uint64_t)stat_next.st_dev;
            volume->info.inode = (uint64_t)stat_next.st_ino;
            volume->info.created = created;
            if (!volume->path || !volume->leaf) {
                ll_volume_close(volume);
                free_parts(parts, count);
                return LL_VOLUME_IO;
            }
            free_parts(parts, count);
            *out = volume;
            return LL_VOLUME_OK;
        }
        close(parent);
        parent = next;
    }
    code = LL_VOLUME_INVALID;
fail:
    close(parent);
    free_parts(parts, count);
    return code;
}

static int scan(int fd, dev_t device, ll_volume_limits limits,
                uint32_t *entries, uint64_t *bytes,
                int (*canceled)(void *), void *context) {
    int copy = openat(fd, ".", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (copy < 0) return LL_VOLUME_IO;
    DIR *dir = fdopendir(copy);
    if (!dir) { close(copy); return LL_VOLUME_IO; }
    int code = LL_VOLUME_OK;
    struct dirent *entry;
    errno = 0;
    while ((entry = readdir(dir)) != NULL) {
        if (strcmp(entry->d_name, ".") == 0 ||
            strcmp(entry->d_name, "..") == 0) continue;
        if (canceled && canceled(context)) {
            code = LL_VOLUME_CANCELED; break;
        }
        if (++*entries > limits.max_entries) {
            code = LL_VOLUME_LIMIT; break;
        }
        struct stat info;
        if (fstatat(fd, entry->d_name, &info, AT_SYMLINK_NOFOLLOW) < 0) {
            code = LL_VOLUME_IO; break;
        }
        if (info.st_dev != device || S_ISLNK(info.st_mode) ||
            (!S_ISDIR(info.st_mode) && !S_ISREG(info.st_mode))) {
            code = LL_VOLUME_UNSAFE; break;
        }
        if (S_ISDIR(info.st_mode)) {
            int child = openat(fd, entry->d_name,
                               O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
            if (child < 0) { code = LL_VOLUME_UNSAFE; break; }
            struct stat opened;
            if (fstat(child, &opened) < 0 || opened.st_dev != info.st_dev ||
                opened.st_ino != info.st_ino) code = LL_VOLUME_STALE;
            else code = scan(child, device, limits, entries, bytes,
                             canceled, context);
            close(child);
            if (code) break;
        } else {
            if (info.st_size < 0 || (uint64_t)info.st_size >
                limits.max_bytes - *bytes) {
                code = LL_VOLUME_LIMIT; break;
            }
            *bytes += (uint64_t)info.st_size;
        }
        errno = 0;
    }
    if (!code && errno) code = LL_VOLUME_IO;
    closedir(dir);
    return code;
}

static int measure(ll_volume *volume,
                   int (*canceled)(void *), void *context) {
    uint32_t entries = 0;
    uint64_t bytes = 0;
    int code = scan(volume->root, volume->device, volume->limits,
                    &entries, &bytes, canceled, context);
    if (!code) {
        volume->info.entries = entries;
        volume->info.bytes = bytes;
    }
    return code;
}

int ll_volume_create(const char *path, ll_volume_limits limits,
                             ll_volume **out) {
    int code = traverse(path, limits, 1, NULL, NULL, out);
    if (code) return code;
    code = measure(*out, NULL, NULL);
    if (code) { ll_volume_close(*out); *out = NULL; }
    return code;
}

int ll_volume_inspect(const char *path, ll_volume_limits limits,
                              ll_volume **out) {
    int code = traverse(path, limits, 0, NULL, NULL, out);
    if (code) return code;
    code = measure(*out, NULL, NULL);
    if (code) { ll_volume_close(*out); *out = NULL; }
    return code;
}

int ll_volume_open(const char *path, uint32_t max_entries,
                           uint64_t max_bytes, uint32_t max_depth,
                           int create, ll_volume **out) {
    ll_volume_limits limits = {max_entries, max_bytes, max_depth};
    return create ? ll_volume_create(path, limits, out) :
                    ll_volume_inspect(path, limits, out);
}

int ll_volume_open_cancellable(const char *path, uint32_t max_entries,
                           uint64_t max_bytes, uint32_t max_depth,
                           int create, int (*canceled)(void *), void *context,
                           ll_volume **out) {
    ll_volume_limits limits = {max_entries, max_bytes, max_depth};
    if (!out || !valid_limits(&limits)) return LL_VOLUME_INVALID;
    int code = traverse(path, limits, create, canceled, context, out);
    if (code) return code;
    code = measure(*out, canceled, context);
    if (code) { ll_volume_close(*out); *out = NULL; }
    return code;
}

int ll_volume_info_get(const ll_volume *volume,
                               ll_volume_info *out) {
    if (!volume || !out) return LL_VOLUME_INVALID;
    *out = volume->info;
    return LL_VOLUME_OK;
}

static int remove_contents(int fd, dev_t device,
                           int (*canceled)(void *), void *context) {
    int copy = openat(fd, ".", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    if (copy < 0) return LL_VOLUME_IO;
    DIR *dir = fdopendir(copy);
    if (!dir) { close(copy); return LL_VOLUME_IO; }
    int code = LL_VOLUME_OK;
    struct dirent *entry;
    errno = 0;
    while ((entry = readdir(dir)) != NULL) {
        if (strcmp(entry->d_name, ".") == 0 ||
            strcmp(entry->d_name, "..") == 0) continue;
        if (canceled && canceled(context)) {
            code = LL_VOLUME_CANCELED; break;
        }
        struct stat info;
        if (fstatat(fd, entry->d_name, &info, AT_SYMLINK_NOFOLLOW) < 0) {
            code = LL_VOLUME_IO; break;
        }
        if (info.st_dev != device || S_ISLNK(info.st_mode) ||
            (!S_ISDIR(info.st_mode) && !S_ISREG(info.st_mode))) {
            code = LL_VOLUME_UNSAFE; break;
        }
        if (S_ISDIR(info.st_mode)) {
            int child = openat(fd, entry->d_name,
                               O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
            if (child < 0) { code = LL_VOLUME_UNSAFE; break; }
            struct stat opened;
            if (fstat(child, &opened) < 0 || opened.st_dev != info.st_dev ||
                opened.st_ino != info.st_ino) code = LL_VOLUME_STALE;
            else code = remove_contents(child, device, canceled, context);
            close(child);
            if (code) break;
            struct stat current;
            if (fstatat(fd, entry->d_name, &current, AT_SYMLINK_NOFOLLOW) < 0 ||
                current.st_dev != info.st_dev || current.st_ino != info.st_ino) {
                code = LL_VOLUME_STALE; break;
            }
            if (unlinkat(fd, entry->d_name, AT_REMOVEDIR) < 0) {
                code = LL_VOLUME_IO; break;
            }
        } else if (unlinkat(fd, entry->d_name, 0) < 0) {
            code = LL_VOLUME_IO; break;
        }
        errno = 0;
    }
    if (!code && errno) code = LL_VOLUME_IO;
    closedir(dir);
    return code;
}

int ll_volume_remove(ll_volume *volume,
                             int (*canceled)(void *), void *context) {
    if (!volume) return LL_VOLUME_INVALID;
    struct stat root, current;
    if (fstat(volume->root, &root) < 0 ||
        fstatat(volume->parent, volume->leaf, &current,
                AT_SYMLINK_NOFOLLOW) < 0 ||
        !S_ISDIR(current.st_mode) ||
        root.st_dev != volume->device || root.st_ino != volume->inode ||
        current.st_dev != root.st_dev || current.st_ino != root.st_ino)
        return LL_VOLUME_STALE;
    int code = measure(volume, canceled, context);
    if (code) return code;
    code = remove_contents(volume->root, volume->device, canceled, context);
    if (code) return code;
    if (fstatat(volume->parent, volume->leaf, &current,
                AT_SYMLINK_NOFOLLOW) < 0 ||
        current.st_dev != volume->device || current.st_ino != volume->inode)
        return LL_VOLUME_STALE;
    if (unlinkat(volume->parent, volume->leaf, AT_REMOVEDIR) < 0)
        return LL_VOLUME_IO;
    return LL_VOLUME_OK;
}

void ll_volume_close(ll_volume *volume) {
    if (!volume) return;
    if (volume->root >= 0) close(volume->root);
    if (volume->parent >= 0) close(volume->parent);
    free(volume->path);
    free(volume->leaf);
    free(volume);
}
