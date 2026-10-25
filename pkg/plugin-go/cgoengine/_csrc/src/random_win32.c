#define WIN32_LEAN_AND_MEAN
#include "platform.h"
#include <windows.h>
#include <bcrypt.h>

ext_status ext_random_bytes(uint8_t *out, size_t n) {
  while (n) {
    ULONG chunk = n > 0x10000000u ? 0x10000000u : (ULONG)n;
    if (BCryptGenRandom(NULL, out, chunk, BCRYPT_USE_SYSTEM_PREFERRED_RNG))
      return EXT_IO;
    out += chunk;
    n -= chunk;
  }
  return EXT_OK;
}
