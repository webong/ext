#ifndef CTX_HOST_H
#define CTX_HOST_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
#if defined(CTX_HOST_STATIC)
#define CTX_HOST_API
#elif defined(_WIN32) && defined(CTX_HOST_BUILD)
#define CTX_HOST_API __declspec(dllexport)
#elif defined(_WIN32)
#define CTX_HOST_API __declspec(dllimport)
#else
#define CTX_HOST_API __attribute__((visibility("default")))
#endif
/* Experimental embedding ABI, independent of ctx.plugin/v1 and guest ABI v1.
 * No ABI stability commitment until the prototype is evaluated. */
#define CTX_HOST_ABI_VERSION 2u
#define CTX_HOST_MAX_FRAME (24u * 1024u * 1024u)
typedef struct ctx_host ctx_host;
typedef struct {
  uint8_t *data;
  size_t len;
} ctx_buffer;
typedef int32_t ctx_status;
enum {
  CTX_OK,
  CTX_INVALID,
  CTX_DENIED,
  CTX_MISMATCH,
  CTX_UNSUPPORTED,
  CTX_CLOSED,
  CTX_TIMEOUT,
  CTX_IO,
  CTX_NOMEM,
  CTX_DRAINING,
  CTX_NOT_FOUND,
  CTX_AMBIGUOUS,
  CTX_CANCELED,
  CTX_UPDATING,
  CTX_CAPACITY,
  CTX_SEQUENCE
};
typedef struct ctx_cancel ctx_cancel;
CTX_HOST_API ctx_status ctx_cancel_create(ctx_cancel **out);
CTX_HOST_API void ctx_cancel_signal(ctx_cancel *);
CTX_HOST_API int ctx_cancel_is_signaled(const ctx_cancel *);
/* Free only after all calls and signal operations using it have joined. */
CTX_HOST_API void ctx_cancel_destroy(ctx_cancel *);
typedef struct {
  uint32_t struct_size;
  uint32_t timeout_ms;
  const ctx_cancel *cancel;
  void
      *value; /* borrowed binding-owned context; engine never dereferences it */
} ctx_call_options;
/* Policies receive borrowed UTF-8 JSON; zero allows. They execute on the
 * calling thread, must return promptly, must not throw or reenter the same
 * host, and may not retain the borrowed bytes. user must live until destroy
 * returns. */
typedef int32_t (*ctx_policy)(void *user, const uint8_t *json, size_t len);
typedef struct {
  uint32_t abi_version;
  uint32_t struct_size;
  const uint8_t *descriptor;
  size_t descriptor_len;
  ctx_policy
      verify; /* before spawning; verify artifact in application policy */
  ctx_policy authorize; /* validated request, before dispatch */
  void *user;
} ctx_host_options;
typedef int32_t (*ctx_context_policy)(void *, const ctx_call_options *,
                                      const uint8_t *, size_t);
typedef void (*ctx_observer)(void *, const ctx_call_options *, const uint8_t *,
                             size_t);
typedef struct {
  uint32_t struct_size;
  void *user;
  ctx_context_policy verify;
  ctx_context_policy authorize;
  ctx_observer observe;
} ctx_host_hooks;
/* Configure once before start or any concurrent use. Optional contextual policy
 * hooks replace the corresponding legacy policy. Observer gets bounded metadata
 * only, never payloads or private error text; callbacks must not reenter. */
CTX_HOST_API ctx_status ctx_host_set_hooks(ctx_host *, const ctx_host_hooks *);
/* Backend configuration is independent of host selection and policy. Unknown
 * kinds return CTX_UNSUPPORTED; new backends do not change ctx_host_options. */
#define CTX_BACKEND_JSONLINE_PROCESS 1u
typedef struct {
  const char *executable; /* absolute immutable verified path; no shell */
} ctx_jsonline_process_options;
/* Explicit launch configuration. Arrays contain count elements (no sentinel
 * required). All strings/arrays are copied; the environment is never inherited.
 * Maximum 256 arguments (4096 bytes each), 256 NAME=value entries (8192 bytes
 * each, unique nonempty names). argv[0] is supplied by the engine. */
#define CTX_BACKEND_JSONLINE_PROCESS_CONFIG 4u
typedef struct {
  uint32_t struct_size;
  const char *executable;
  const char *const *arguments;
  size_t argument_count;
  const char *const *environment;
  size_t environment_count;
} ctx_jsonline_process_config;
/* An extension returns one descriptor (connect) or response envelope (invoke)
 * through emit. emit copies bytes into engine-owned memory; call exactly once
 * on success, synchronously, and never retain emit/context or input pointers.
 * Returning an error after emit is allowed; the engine discards the result.
 * All callbacks must return a ctx_status, must not unwind, and must not reenter
 * this host. close is the exception: it runs concurrently with connect/invoke
 * and must interrupt pending work. It runs once, including before connect.
 * release runs once at destroy, after all host calls have joined. Ownership of
 * user transfers only on successful ctx_host_create. Code must remain loaded.
 * timeout_ms is remaining time, including queue and policy time. Native work
 * must cooperate; the engine cannot forcibly interrupt arbitrary native code.
 */
#define CTX_BACKEND_EXTENSION 2u
#define CTX_BACKEND_CONCURRENT 1u
typedef ctx_status (*ctx_emit)(void *context, const uint8_t *data, size_t len);
typedef struct {
  uint32_t struct_size;
  uint32_t flags;
  void *user;
  ctx_status (*connect)(void *user, uint32_t timeout_ms, ctx_emit emit,
                        void *context);
  ctx_status (*invoke)(void *user, const uint8_t *request, size_t len,
                       uint32_t timeout_ms, ctx_emit emit, void *context);
  void (*close)(void *user);
  void (*release)(void *user);
} ctx_backend_extension;
/* Context-aware extension; same ownership as ctx_backend_extension. */
#define CTX_BACKEND_EXTENSION_CONTEXT 3u
typedef struct {
  uint32_t struct_size;
  uint32_t flags;
  void *user;
  ctx_status (*connect)(void *, const ctx_call_options *, ctx_emit, void *);
  ctx_status (*invoke)(void *, const uint8_t *, size_t,
                       const ctx_call_options *, ctx_emit, void *);
  void (*close)(void *);
  void (*release)(void *);
} ctx_backend_extension_context;
typedef struct {
  uint32_t struct_size;
  uint32_t kind;
  const void *config;
  size_t config_size;
} ctx_backend_options;
CTX_HOST_API uint32_t ctx_host_abi_version(void);
/* Copies backend configuration and descriptor. Does not execute guest code.
 * Both policies required. */
CTX_HOST_API ctx_status ctx_host_create(const ctx_host_options *,
                                        const ctx_backend_options *,
                                        ctx_host **out);
/* Starts the selected backend. JSON-line process launches a child with
 * argv[0]=executable, empty environment and inherited
 * stderr. Handshake checks the full selection. One call, no retry.
 * timeout_ms: 1..UINT32_MAX. */
CTX_HOST_API ctx_status ctx_host_start(ctx_host *, uint32_t timeout_ms);
CTX_HOST_API ctx_status ctx_host_start_with_options(ctx_host *,
                                                    const ctx_call_options *);
CTX_HOST_API ctx_status ctx_host_invoke_with_options(ctx_host *,
                                                     const uint8_t *, size_t,
                                                     const ctx_call_options *,
                                                     ctx_buffer *);
CTX_HOST_API ctx_status ctx_host_drain_with_options(ctx_host *,
                                                    const ctx_call_options *);
/* Calls are serialized unless an extension declares CTX_BACKEND_CONCURRENT.
 * Queue time counts toward timeout. Copies no retained caller pointers.
 * Input/output are CTX envelopes; domain errors return CTX_OK with
 * Response.error. Caller owns returned buffer and frees via the same engine
 * instance that allocated it. Never pass handles or buffers between separately
 * linked engine copies. Request deadline and timeout_ms both bound dispatch.
 * Failed dispatched calls close the session; local validation/policy denial
 * leave it usable. */
CTX_HOST_API ctx_status ctx_host_invoke(ctx_host *, const uint8_t *, size_t,
                                        uint32_t timeout_ms, ctx_buffer *out);
/* Input: {contract:{name,version},operation,payload?}. Engine supplies
 * identity, surface, deadline and unique ID. Output is a complete response
 * envelope. */
CTX_HOST_API ctx_status ctx_host_call(ctx_host *, const uint8_t *, size_t,
                                      const ctx_call_options *, ctx_buffer *);
/* Thread-safe and idempotent. Stops admission, wakes I/O, kills owned child.
 * A timeout/cancellation after dispatch terminates this session. No
 * process-tree containment. Policies are synchronous and cannot be forcibly
 * interrupted. */
CTX_HOST_API void ctx_host_close(ctx_host *);
/* Drain stops admission and waits for admitted calls. Timeout reopens
 * admission; it does not abort active work. A simultaneous drain returns
 * CTX_DRAINING. timeout_ms: 1..UINT32_MAX. close still aborts immediately
 * during a drain. */
CTX_HOST_API ctx_status ctx_host_drain(ctx_host *, uint32_t timeout_ms);
typedef enum {
  CTX_HOST_CREATED,
  CTX_HOST_READY,
  CTX_HOST_DRAINING,
  CTX_HOST_CLOSED,
  CTX_HOST_FAILED
} ctx_host_state;
CTX_HOST_API ctx_host_state ctx_host_get_state(ctx_host *);
/* After all calls/start/close have returned, exactly one owner destroys the
 * handle. Reaps child and frees resources. No concurrent use or double destroy.
 */
CTX_HOST_API void ctx_host_destroy(ctx_host *);
CTX_HOST_API void ctx_buffer_free(ctx_buffer *);
/* Strict bounded JSON validation used by bindings/conformance, not a schema
 * API. */
CTX_HOST_API ctx_status ctx_host_validate_json(const uint8_t *, size_t);
CTX_HOST_API const char *ctx_host_status_string(ctx_status);
CTX_HOST_API ctx_status ctx_engine_sha256(const uint8_t *, size_t,
                                          uint8_t out[32]);
/* Read-only filesystem integrity. Rejects symlinks and special files. Root
 * and file contents must be protected by the application against modification.
 * This verifies bytes, never publisher trust. Directory traversal is bounded
 * to 1024 nested directories. Digests are 32 raw bytes. */
CTX_HOST_API ctx_status ctx_package_verify(const uint8_t *manifest, size_t len,
                                           const char *root);
CTX_HOST_API ctx_status ctx_directory_digest(const char *root, uint8_t out[32]);
/* Pure engine services: strict bounded JSON input/output, no runtime callbacks,
 * filesystem access, discovery or authorization. Returns an owned JSON value.
 * See services.md for operations and schemas. Free output with ctx_buffer_free.
 */
CTX_HOST_API ctx_status ctx_engine_call(const char *operation,
                                        const uint8_t *input, size_t input_len,
                                        ctx_buffer *out);
#ifdef __cplusplus
}
#endif
#endif
