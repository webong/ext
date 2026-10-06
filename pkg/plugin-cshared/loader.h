#ifndef CTX_LOADER_H
#define CTX_LOADER_H
#include <stdint.h>
#include <stddef.h>
typedef struct ctx_library ctx_library;
ctx_library *ctx_library_load(const char *path, char *error, size_t error_cap);
uint64_t ctx_library_open(ctx_library *library);
uint32_t ctx_library_call(ctx_library *library, uint64_t handle, uint32_t op,
    const uint8_t *input, uint32_t input_len,
    uint8_t *output, uint32_t output_cap, uint32_t *output_len);
void ctx_library_close(ctx_library *library, uint64_t handle);
#endif
