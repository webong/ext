#ifndef EXT_SHA256_H
#define EXT_SHA256_H
#include <stddef.h>
#include <stdint.h>
typedef struct {
  uint32_t h[8];
  uint64_t total;
  size_t used;
  uint8_t block[64];
} ext_hash;
void ext_hash_init(ext_hash *);
void ext_hash_update(ext_hash *, const uint8_t *, size_t);
void ext_hash_finish(ext_hash *, uint8_t[32]);
void ext_hash_hex(const uint8_t[32], char[65]);
#endif
