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

/* Write side, used by the package store (store.c; fs_write_posix.c and, on
 * Windows, fs_win32.c supply the operating-system access, and none of them is
 * translated for c2goengine). Every path is a portable relative path below the
 * root of an ext_fs: slash separated, no empty component, no "." or "..".
 * No call follows a link at any component, and none replaces an existing
 * entry unless it says so. Everything it creates is owner-only (directories
 * 0700, files 0600 or 0700). */
typedef enum { EXT_FS_NONE, EXT_FS_DIR, EXT_FS_FILE, EXT_FS_OTHER } ext_fs_kind;
typedef struct {
  char *name;
  ext_fs_kind kind;
} ext_fs_entry;
/* The kind of the entry at path without following a link: a link or a special
 * file is EXT_FS_OTHER, and an entry whose parent is missing is EXT_FS_NONE.
 * EXT_INVALID when a parent exists but is not a plain directory. */
ext_status ext_fs_kind_of(ext_fs *fs, const char *path, ext_fs_kind *out);
/* The immediate entries of the plain directory at path ("" is the root), in no
 * particular order. Release them with ext_fs_list_free. */
ext_status ext_fs_list(ext_fs *fs, const char *path, ext_fs_entry **out,
                       size_t *count);
void ext_fs_list_free(ext_fs_entry *entries, size_t count);
/* Creates the directory and any missing parents. A plain directory already
 * there is fine; any other entry at a component is EXT_INVALID. */
ext_status ext_fs_mkdirs(ext_fs *fs, const char *path);
/* Creates a new uniquely named empty directory below the directory at parent
 * ("" is the root) whose name starts with prefix (which holds no separator)
 * and returns the name in *name, to be freed. */
ext_status ext_fs_mkdtemp(ext_fs *fs, const char *parent, const char *prefix,
                          char **name);
/* Copies the regular file at src_path in src to a new file at dst_path in dst,
 * creating missing parent directories; EXT_INVALID when dst_path exists. The
 * source is opened without following links and must still be the file that was
 * checked when it is read. At most limit bytes are read: a longer file is
 * EXT_INVALID (the copy is left for the caller to remove). Returns the SHA-256
 * of what was copied and its length. */
ext_status ext_fs_copy_file(ext_fs *src, const char *src_path, ext_fs *dst,
                            const char *dst_path, int executable,
                            uint64_t limit, uint8_t sum[32], uint64_t *size);
/* Writes data to a new file (EXT_INVALID when it exists), creating missing
 * parent directories. */
ext_status ext_fs_write_file(ext_fs *fs, const char *path, const uint8_t *data,
                             size_t len, int executable);
/* Renames the entry at from to to, which must not exist (POSIX also lets a
 * directory replace an empty one). Both stay below the root. */
ext_status ext_fs_rename(ext_fs *fs, const char *from, const char *to);
/* Removes the file or directory tree at path, unlinking any link it meets
 * without following it. A missing path is not an error. The root itself cannot
 * be removed. */
ext_status ext_fs_remove_all(ext_fs *fs, const char *path);
/* Creates the directory at path and its missing parents with owner-only access,
 * the way a store's root comes to exist. The path is a native path and is
 * resolved by the operating system. */
ext_status ext_fs_make_root(const char *path);
/* The descriptor behind a root, for the write side of the same platform (the
 * Windows implementation shares fs_win32.c and needs none). */
#ifndef _WIN32
int ext_fs_fd(ext_fs *fs);
#endif
#endif
