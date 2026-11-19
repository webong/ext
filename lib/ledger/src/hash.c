/* SHA-256 (FIPS 180-4) for idempotency fingerprints and CRC-32 (IEEE 802.3, as
 * zlib computes it) for the framing of ledger lines. */
#include "ledger_internal.h"

#include <string.h>

static const uint32_t k256[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be,
    0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa,
    0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85,
    0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
    0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f,
    0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};

#define ROTR(x, n) (((x) >> (n)) | ((x) << (32 - (n))))

static void sha256_block(uint32_t h[8], const unsigned char *p) {
  uint32_t w[64];
  for (int i = 0; i < 16; i++) w[i] = (uint32_t)p[4 * i] << 24 | (uint32_t)p[4 * i + 1] << 16 | (uint32_t)p[4 * i + 2] << 8 | p[4 * i + 3];
  for (int i = 16; i < 64; i++) {
    uint32_t s0 = ROTR(w[i - 15], 7) ^ ROTR(w[i - 15], 18) ^ (w[i - 15] >> 3);
    uint32_t s1 = ROTR(w[i - 2], 17) ^ ROTR(w[i - 2], 19) ^ (w[i - 2] >> 10);
    w[i] = w[i - 16] + s0 + w[i - 7] + s1;
  }
  uint32_t a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
  for (int i = 0; i < 64; i++) {
    uint32_t t1 = hh + (ROTR(e, 6) ^ ROTR(e, 11) ^ ROTR(e, 25)) + ((e & f) ^ (~e & g)) + k256[i] + w[i];
    uint32_t t2 = (ROTR(a, 2) ^ ROTR(a, 13) ^ ROTR(a, 22)) + ((a & b) ^ (a & c) ^ (b & c));
    hh = g;
    g = f;
    f = e;
    e = d + t1;
    d = c;
    c = b;
    b = a;
    a = t1 + t2;
  }
  h[0] += a;
  h[1] += b;
  h[2] += c;
  h[3] += d;
  h[4] += e;
  h[5] += f;
  h[6] += g;
  h[7] += hh;
}

void ll_sha256_hex(const void *data, size_t len, char out[65]) {
  uint32_t h[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
  const unsigned char *p = data;
  size_t left = len;
  while (left >= 64) {
    sha256_block(h, p);
    p += 64;
    left -= 64;
  }
  unsigned char tail[128] = {0};
  memcpy(tail, p, left);
  tail[left] = 0x80;
  size_t blocks = left < 56 ? 1 : 2;
  uint64_t bits = (uint64_t)len * 8;
  for (int i = 0; i < 8; i++) tail[blocks * 64 - 1 - i] = (unsigned char)(bits >> (8 * i));
  for (size_t i = 0; i < blocks; i++) sha256_block(h, tail + 64 * i);
  static const char hexd[] = "0123456789abcdef";
  for (int i = 0; i < 8; i++)
    for (int j = 0; j < 4; j++) {
      unsigned char byte = (unsigned char)(h[i] >> (24 - 8 * j));
      out[8 * i + 2 * j] = hexd[byte >> 4];
      out[8 * i + 2 * j + 1] = hexd[byte & 0xF];
    }
  out[64] = 0;
}

uint32_t ll_crc32(const void *data, size_t len) {
  /* A nibble table: constant, so there is nothing to initialise or race on. */
  static const uint32_t table[16] = {0x00000000, 0x1DB71064, 0x3B6E20C8, 0x26D930AC, 0x76DC4190, 0x6B6B51F4, 0x4DB26158, 0x5005713C,
                                     0xEDB88320, 0xF00F9344, 0xD6D6A3E8, 0xCB61B38C, 0x9B64C2B0, 0x86D3D2D4, 0xA00AE278, 0xBDBDF21C};
  uint32_t c = 0xFFFFFFFFu;
  const unsigned char *p = data;
  for (size_t i = 0; i < len; i++) {
    c ^= p[i];
    c = table[c & 0xF] ^ (c >> 4);
    c = table[c & 0xF] ^ (c >> 4);
  }
  return c ^ 0xFFFFFFFFu;
}
