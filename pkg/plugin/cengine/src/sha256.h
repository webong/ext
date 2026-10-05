#ifndef CTX_SHA256_H
#define CTX_SHA256_H
#include <stddef.h>
#include <stdint.h>
typedef struct {
  uint32_t h[8];
  uint64_t total;
  size_t used;
  uint8_t block[64];
} ctx_hash;
void ctx_hash_init(ctx_hash *);
void ctx_hash_update(ctx_hash *, const uint8_t *, size_t);
void ctx_hash_finish(ctx_hash *, uint8_t[32]);
void ctx_hash_hex(const uint8_t[32], char[65]);
#endif
