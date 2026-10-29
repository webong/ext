#ifndef EXT_FS_H
#define EXT_FS_H
/* Internal platform layer under package integrity: reading files below a root
 * without following links. integrity.c owns the digest logic; fs_posix.c and
 * fs_win32.c supply the operating-system access. The application must protect
 * the root and its files against modification while they are read: these
 * calls refuse links, they are not a sandbox. */
#include "ext_host.h"
#include <stddef.h>
#include <stdint.h>

typedef struct ext_fs ext_fs;
typedef ext_status (*ext_fs_visit)(void *user, const char *path,
                                   const uint8_t sum[32]);

/* Opens root, which must be a directory and not itself a link. EXT_IO when it
 * cannot be opened, EXT_INVALID when it is not a plain directory. */
ext_status ext_fs_open(const char *root, ext_fs **out);
void ext_fs_close(ext_fs *fs);
/* SHA-256 of the regular file at a portable relative path (slash separated),
 * refusing a link at any component. EXT_IO when missing, EXT_INVALID when a
 * component is a link or the final one is not a regular file. */
ext_status ext_fs_hash_file(ext_fs *fs, const char *path, uint8_t out[32]);
/* Reads one regular file without following a link at any path component.
 * At most limit bytes are returned in an owned ext_buffer. */
ext_status ext_fs_read_file(ext_fs *fs, const char *path, size_t limit,
                            ext_buffer *out);
/* Calls visit for every regular file below the directory at path ("" is the
 * root itself) with its slash-separated path relative to that directory and its
 * SHA-256. Every component of path must be a plain directory. Links and
 * special files are EXT_INVALID, paths holding a line break are EXT_INVALID,
 * and traversal stops at 1024 nested directories. A nonzero status from visit
 * stops the walk and is returned. */
ext_status ext_fs_walk(ext_fs *fs, const char *path, ext_fs_visit visit,
                       void *user);
#endif
