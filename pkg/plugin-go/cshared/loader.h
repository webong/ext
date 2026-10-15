#ifndef EXT_LOADER_H
#define EXT_LOADER_H
#include <stdint.h>
#include <stddef.h>
typedef struct ext_library ext_library;
ext_library *ext_library_load(const char *path, char *error, size_t error_cap);
uint64_t ext_library_open(ext_library *library);
uint32_t ext_library_call(ext_library *library, uint64_t handle, uint32_t op,
    const uint8_t *input, uint32_t input_len,
    uint8_t *output, uint32_t output_cap, uint32_t *output_len);
void ext_library_close(ext_library *library, uint64_t handle);
#endif
