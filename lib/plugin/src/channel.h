#ifndef EXT_CHANNEL_H
#define EXT_CHANNEL_H
/* Internal platform layer under the host: one child process and the byte
 * stream to it. host.c owns the protocol, deadlines, cancellation and state;
 * a channel only spawns the child, moves bytes and kills or reaps it.
 * Implemented by channel_posix.c (socket pair, poll) and channel_win32.c
 * (anonymous pipes with a reader and a writer thread). */
#include "ext_host.h"
#include <stddef.h>
#include <stdint.h>

typedef struct ext_channel ext_channel;
typedef struct {
  const char *path;           /* absolute path of a regular file */
  char *const *arguments;     /* NULL-terminated, argv[0] included */
  char *const *environment;   /* NULL-terminated NAME=value entries */
  const char *directory;      /* working directory, NULL inherits */
  int capture_stderr;         /* retain the first stderr_limit bytes */
  size_t stderr_limit;
} ext_launch;

/* Spawns the child and sets up the stream. EXT_IO on failure to start,
 * EXT_INVALID for a path that is not a regular file, EXT_UNSUPPORTED for a
 * working directory the platform cannot honor. */
ext_status ext_channel_open(const ext_launch *launch, ext_channel **out);
/* EXT_OK when the stream can be read (write == 0) or written (write != 0)
 * without blocking, EXT_TIMEOUT when timeout_ms passes first, EXT_IO on error
 * or hangup. May return EXT_TIMEOUT early; callers loop against a deadline. */
ext_status ext_channel_wait(ext_channel *c, int write, int timeout_ms);
/* Nonblocking transfer: the byte count, 0 when it would block, -1 on error or
 * end of stream. */
ptrdiff_t ext_channel_read(ext_channel *c, uint8_t *buffer, size_t length);
ptrdiff_t ext_channel_write(ext_channel *c, const uint8_t *buffer,
                            size_t length);
/* Kills the child and unblocks waiters. Idempotent; callable from any thread. */
void ext_channel_kill(ext_channel *c);
/* Kills if needed, reaps the child, stops helper threads and frees c. */
void ext_channel_destroy(ext_channel *c);
/* Copies up to capacity bytes of the retained stderr; returns the retained
 * size. */
size_t ext_channel_stderr(ext_channel *c, uint8_t *buffer, size_t capacity);
#endif
