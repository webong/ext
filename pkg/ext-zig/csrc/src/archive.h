#ifndef EXT_ARCHIVE_H
#define EXT_ARCHIVE_H
/* Internal: bounded extraction of a pinned package archive, for the package
 * store (store.c). The rules are those of the Go store's ExtractArchive and
 * both are held to testdata/v1/archive-cases.json. */
#include "fs.h"

typedef struct {
  uint64_t max_archive_bytes;  /* the archive file */
  uint64_t max_artifact_bytes; /* one extracted file */
  uint64_t max_tree_bytes;     /* all extracted files */
  uint32_t max_tree_files;     /* entries, directories included */
} ext_archive_limits;

/* Writes the lower-case form of a SHA-256 in hexadecimal to out and returns 1,
 * or returns 0 when text is not 64 hexadecimal characters. */
int ext_archive_digest_text(const char *text, char out[65]);

/* Checks the regular file at the native path archive against digest (64
 * lower-case hexadecimal characters; NULL skips the check) and extracts it
 * into the empty directory at dest below fs. The archive is a zip, a
 * gzip-compressed tar or a plain tar, told apart by its content; anything else
 * is EXT_PLUGIN_UNSUPPORTED. Only directories and regular files are written (0600, or
 * 0700 for an entry with its owner-execute bit); a link or special entry, an
 * unsafe or repeated path, a body that is not what its header declares and
 * anything over a limit are EXT_PLUGIN_INVALID, a digest that differs EXT_PLUGIN_MISMATCH and
 * a missing archive EXT_PLUGIN_MISSING. On success *root is the path below fs of the
 * package: dest when plugin.json is at the top of the archive, or the one
 * top-level directory that holds it. Free it. On failure dest may hold a
 * partial tree. */
ext_plugin_status ext_archive_extract(const char *archive, const char *digest,
                               ext_fs *fs, const char *dest,
                               const ext_archive_limits *limits, char **root);
#endif
