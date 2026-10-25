/* Stands in for channel_posix.c and channel_win32.c in the host profile: the channel layer (one
   child process and the byte stream to it, channel.h) is written in Go, which points
   ext_channel_ops_table at its functions before the engine opens a host. A global the C reads, not a
   function the Go calls, because ccgo drops functions nothing in C refers to. */
#include "channel.h"

typedef struct {
  int (*is_absolute)(const char *);
  ext_status (*open)(const ext_launch *, ext_channel **);
  ext_status (*wait)(ext_channel *, int, int);
  ptrdiff_t (*read)(ext_channel *, uint8_t *, size_t);
  ptrdiff_t (*write)(ext_channel *, const uint8_t *, size_t);
  void (*kill)(ext_channel *);
  void (*destroy)(ext_channel *);
  size_t (*stderr_copy)(ext_channel *, uint8_t *, size_t);
} ext_channel_ops;

const ext_channel_ops *ext_channel_ops_table;
#define ops (*ext_channel_ops_table)

int ext_path_is_absolute(const char *path) { return ops.is_absolute(path); }
ext_status ext_channel_open(const ext_launch *launch, ext_channel **out) {
  return ops.open(launch, out);
}
ext_status ext_channel_wait(ext_channel *c, int write, int timeout_ms) {
  return ops.wait(c, write, timeout_ms);
}
ptrdiff_t ext_channel_read(ext_channel *c, uint8_t *buffer, size_t length) {
  return ops.read(c, buffer, length);
}
ptrdiff_t ext_channel_write(ext_channel *c, const uint8_t *buffer,
                            size_t length) {
  return ops.write(c, buffer, length);
}
void ext_channel_kill(ext_channel *c) { ops.kill(c); }
void ext_channel_destroy(ext_channel *c) { ops.destroy(c); }
size_t ext_channel_stderr(ext_channel *c, uint8_t *buffer, size_t capacity) {
  return ops.stderr_copy(c, buffer, capacity);
}
