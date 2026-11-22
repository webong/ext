#define _POSIX_C_SOURCE 200809L
#include "platform.h"
#include <errno.h>
#include <fcntl.h>
#include <unistd.h>

ext_status ext_random_bytes(uint8_t *out, size_t n) {
  int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
  if (fd < 0)
    return EXT_IO;
  size_t used = 0;
  ext_status s = EXT_OK;
  while (used < n) {
    ssize_t r = read(fd, out + used, n - used);
    if (r > 0)
      used += (size_t)r;
    else if (!r || errno != EINTR) {
      s = EXT_IO;
      break;
    }
  }
  close(fd);
  return s;
}
