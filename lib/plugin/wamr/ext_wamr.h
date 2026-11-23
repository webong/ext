/* A WebAssembly reactor backend for the shared plugin engine, on WAMR.
 *
 * It is the in-process fallback for hosts that cannot use a platform web engine.
 * It loads a reactor (docs/plugin-reactor-abi.md) and drives its ext_plugin_guest_*
 * exports through the engine's extension-backend interface. It is optional and
 * built only with EXT_WITH_WAMR. WAMR itself must be built with its instruction
 * metering and thread manager options on, or deadlines cannot interrupt a guest.
 *
 * The module is untrusted code: the host provides exactly the WASI imports the
 * ABI lists (no filesystem, network or standard input) and ext_plugin.reverse,
 * and a module importing anything else fails to load. A guest that traps, overruns its deadline, runs
 * out of memory or exits ends the whole instance. */
#ifndef EXT_WAMR_H
#define EXT_WAMR_H
#include "ext_plugin_host.h"
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif

typedef void (*ext_wamr_diagnostic)(void *user, const uint8_t *data, size_t len);

typedef struct {
  uint32_t struct_size;
  /* Guest linear memory cap in 64 KiB pages. Zero means 4096 (256 MiB). The
   * ABI's response buffer alone is 384 pages (24 MiB). */
  uint32_t memory_limit_pages;
  /* Bytes of interpreter stack. Zero means 1 MiB. */
  uint32_t stack_size;
  /* A budget of WebAssembly instructions per call, on top of the deadline.
   * Zero means no instruction budget. */
  uint64_t instruction_limit;
  /* Bytes of guest stdout/stderr passed to diagnostic over the instance's life;
   * the rest is discarded. Zero means 65536. */
  uint32_t max_diagnostic_bytes;
  /* Optional. Called synchronously on the calling thread, never re-entrantly. */
  ext_wamr_diagnostic diagnostic;
  void *diagnostic_user;
} ext_wamr_options;

/* Prepares a backend for ext_plugin_host_create (kind EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT,
 * config = out, config_size = sizeof(*out)). The module bytes are copied and are
 * not run until the host starts. Ownership of out->user passes to the host on a
 * successful ext_plugin_host_create; if ext_plugin_host_create fails the caller must call
 * out->release(out->user). Returns EXT_PLUGIN_INVALID for empty, oversized (over
 * 64 MiB) or malformed arguments and EXT_PLUGIN_NOMEM when memory runs out. Process
 * start-up of the WAMR runtime happens on first use and is never undone. */
ext_plugin_status ext_wamr_backend_create(const uint8_t *module, size_t module_len,
                                   const ext_wamr_options *options,
                                   ext_plugin_backend_extension_context *out);

/* Lets the guest call its host (ext_plugin.reverse, docs/plugin-reactor-abi.md):
 * backend_user is out->user from ext_wamr_backend_create, and host the host
 * ext_plugin_host_create made over it. Call it once, after ext_plugin_host_create and before
 * ext_plugin_host_start; offer the services with ext_plugin_host_set_services or
 * ext_plugin_registry_attach as for any host. The backend is created with
 * EXT_PLUGIN_BACKEND_FRAMES, so a plugin whose descriptor declares reverse operations is
 * accepted. Until it is bound, the guest's reverse calls are answered status 2
 * (unavailable). The hooks run on the thread inside ext_plugin_host_invoke or
 * ext_plugin_host_call, and the call's deadline keeps running while they do. EXT_PLUGIN_INVALID
 * for a null argument, a second bind or a backend that has started. */
ext_plugin_status ext_wamr_backend_bind_host(void *backend_user, ext_plugin_host *host);

#ifdef __cplusplus
}
#endif
#endif
