//go:build cgo && (linux || darwin || freebsd || windows)

#include "loader.h"
#include <stdio.h>
#include <stdlib.h>
#ifdef _WIN32
#include <windows.h>
#define CTX_CALL __cdecl
typedef HMODULE image_handle;
static image_handle load_image(const char *path, char *error, size_t cap) {
    int count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, NULL, 0);
    if (!count) { snprintf(error, cap, "invalid UTF-8 library path"); return NULL; }
    wchar_t *wide = (wchar_t *)malloc((size_t)count * sizeof(wchar_t));
    if (!wide) { snprintf(error, cap, "library path allocation failed"); return NULL; }
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wide, count);
    /* Do not search the working directory for transitive DLL dependencies. */
    image_handle h = LoadLibraryExW(wide, NULL,
        LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_DEFAULT_DIRS);
    free(wide);
    if (!h) snprintf(error, cap, "LoadLibraryExW failed (%lu)", (unsigned long)GetLastError());
    return h;
}
#define LOOKUP(h, name) GetProcAddress(h, name)
#else
#include <dlfcn.h>
#define CTX_CALL
typedef void *image_handle;
static image_handle load_image(const char *path, char *error, size_t cap) {
    image_handle h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!h) { const char *message = dlerror(); snprintf(error, cap, "%s", message ? message : "dlopen failed"); }
    return h;
}
#define LOOKUP(h, name) dlsym(h, name)
#endif

typedef uint32_t (CTX_CALL *version_fn)(void);
typedef uint64_t (CTX_CALL *open_fn)(void);
typedef uint32_t (CTX_CALL *call_fn)(uint64_t, uint32_t, const uint8_t *, uint32_t, uint8_t *, uint32_t, uint32_t *);
typedef void (CTX_CALL *close_fn)(uint64_t);
struct ctx_library { open_fn open; call_fn call; close_fn close; };

ctx_library *ctx_library_load(const char *path, char *error, size_t cap) {
    image_handle h = load_image(path, error, cap);
    if (!h) return NULL;
    /* Never unload even on ABI failure: native initialization already ran. */
    version_fn version = (version_fn)LOOKUP(h, "ctx_plugin_abi_version");
    open_fn open = (open_fn)LOOKUP(h, "ctx_plugin_open");
    call_fn call = (call_fn)LOOKUP(h, "ctx_plugin_call");
    close_fn close = (close_fn)LOOKUP(h, "ctx_plugin_close");
    if (!version || !open || !call || !close) {
        snprintf(error, cap, "missing required CTX C ABI exports"); return NULL;
    }
    if (version() != 1u) { snprintf(error, cap, "unsupported CTX C ABI version"); return NULL; }
    ctx_library *library = (ctx_library *)malloc(sizeof(ctx_library));
    if (!library) { snprintf(error, cap, "library allocation failed"); return NULL; }
    library->open = open; library->call = call; library->close = close;
    return library;
}
uint64_t ctx_library_open(ctx_library *library) { return library->open(); }
uint32_t ctx_library_call(ctx_library *library, uint64_t handle, uint32_t op,
    const uint8_t *input, uint32_t input_len,
    uint8_t *output, uint32_t output_cap, uint32_t *output_len) {
    return library->call(handle, op, input, input_len, output, output_cap, output_len);
}
void ctx_library_close(ctx_library *library, uint64_t handle) { library->close(handle); }
