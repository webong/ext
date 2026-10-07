#ifndef EXT_HOST_H
#define EXT_HOST_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
#if defined(EXT_HOST_STATIC)
#define EXT_HOST_API
#elif defined(_WIN32) && defined(EXT_HOST_BUILD)
#define EXT_HOST_API __declspec(dllexport)
#elif defined(_WIN32)
#define EXT_HOST_API __declspec(dllimport)
#else
#define EXT_HOST_API __attribute__((visibility("default")))
#endif
/* Experimental embedding ABI, independent of ext.plugin/v1 and guest ABI v1.
 * No ABI stability commitment until the prototype is evaluated. */
#define EXT_HOST_ABI_VERSION 2u
#define EXT_HOST_MAX_FRAME (24u * 1024u * 1024u)
typedef struct ext_host ext_host;
typedef struct {
  uint8_t *data;
  size_t len;
} ext_buffer;
typedef int32_t ext_status;
enum {
  EXT_OK,
  EXT_INVALID,
  EXT_DENIED,
  EXT_MISMATCH,
  EXT_UNSUPPORTED,
  EXT_CLOSED,
  EXT_TIMEOUT,
  EXT_IO,
  EXT_NOMEM,
  EXT_DRAINING,
  EXT_NOT_FOUND,
  EXT_AMBIGUOUS,
  EXT_CANCELED,
  EXT_UPDATING,
  EXT_CAPACITY,
  EXT_SEQUENCE
};
typedef struct ext_cancel ext_cancel;
EXT_HOST_API ext_status ext_cancel_create(ext_cancel **out);
EXT_HOST_API void ext_cancel_signal(ext_cancel *);
EXT_HOST_API int ext_cancel_is_signaled(const ext_cancel *);
/* Free only after all calls and signal operations using it have joined. */
EXT_HOST_API void ext_cancel_destroy(ext_cancel *);
typedef struct {
  uint32_t struct_size;
  uint32_t timeout_ms;
  const ext_cancel *cancel;
  void
      *value; /* borrowed binding-owned context; engine never dereferences it */
} ext_call_options;
/* Policies receive borrowed UTF-8 JSON; zero allows. They execute on the
 * calling thread, must return promptly, must not throw or reenter the same
 * host, and may not retain the borrowed bytes. user must live until destroy
 * returns. */
typedef int32_t (*ext_policy)(void *user, const uint8_t *json, size_t len);
typedef struct {
  uint32_t abi_version;
  uint32_t struct_size;
  const uint8_t *descriptor;
  size_t descriptor_len;
  ext_policy
      verify; /* before spawning; verify artifact in application policy */
  ext_policy authorize; /* validated request, before dispatch */
  void *user;
} ext_host_options;
typedef int32_t (*ext_context_policy)(void *, const ext_call_options *,
                                      const uint8_t *, size_t);
typedef void (*ext_observer)(void *, const ext_call_options *, const uint8_t *,
                             size_t);
typedef struct {
  uint32_t struct_size;
  void *user;
  ext_context_policy verify;
  ext_context_policy authorize;
  ext_observer observe;
} ext_host_hooks;
/* Configure once before start or any concurrent use. Optional contextual policy
 * hooks replace the corresponding legacy policy. Observer gets bounded metadata
 * only, never payloads or private error text; callbacks must not reenter. */
EXT_HOST_API ext_status ext_host_set_hooks(ext_host *, const ext_host_hooks *);
/* Backend configuration is independent of host selection and policy. Unknown
 * kinds return EXT_UNSUPPORTED; new backends do not change ext_host_options. */
#define EXT_BACKEND_JSONLINE_PROCESS 1u
typedef struct {
  const char *executable; /* absolute immutable verified path; no shell */
} ext_jsonline_process_options;
/* Explicit launch configuration. Arrays contain count elements (no sentinel
 * required). All strings/arrays are copied; the environment is never inherited.
 * Maximum 256 arguments (4096 bytes each), 256 NAME=value entries (8192 bytes
 * each, unique nonempty names). argv[0] is supplied by the engine. */
#define EXT_BACKEND_JSONLINE_PROCESS_CONFIG 4u
typedef struct {
  uint32_t struct_size;
  const char *executable;
  const char *const *arguments;
  size_t argument_count;
  const char *const *environment;
  size_t environment_count;
} ext_jsonline_process_config;
/* An extension returns one descriptor (connect) or response envelope (invoke)
 * through emit. emit copies bytes into engine-owned memory; call exactly once
 * on success, synchronously, and never retain emit/context or input pointers.
 * Returning an error after emit is allowed; the engine discards the result.
 * All callbacks must return a ext_status, must not unwind, and must not reenter
 * this host. close is the exception: it runs concurrently with connect/invoke
 * and must interrupt pending work. It runs once, including before connect.
 * release runs once at destroy, after all host calls have joined. Ownership of
 * user transfers only on successful ext_host_create. Code must remain loaded.
 * timeout_ms is remaining time, including queue and policy time. Native work
 * must cooperate; the engine cannot forcibly interrupt arbitrary native code.
 */
#define EXT_BACKEND_EXTENSION 2u
#define EXT_BACKEND_CONCURRENT 1u
typedef ext_status (*ext_emit)(void *context, const uint8_t *data, size_t len);
typedef struct {
  uint32_t struct_size;
  uint32_t flags;
  void *user;
  ext_status (*connect)(void *user, uint32_t timeout_ms, ext_emit emit,
                        void *context);
  ext_status (*invoke)(void *user, const uint8_t *request, size_t len,
                       uint32_t timeout_ms, ext_emit emit, void *context);
  void (*close)(void *user);
  void (*release)(void *user);
} ext_backend_extension;
/* Context-aware extension; same ownership as ext_backend_extension. */
#define EXT_BACKEND_EXTENSION_CONTEXT 3u
typedef struct {
  uint32_t struct_size;
  uint32_t flags;
  void *user;
  ext_status (*connect)(void *, const ext_call_options *, ext_emit, void *);
  ext_status (*invoke)(void *, const uint8_t *, size_t,
                       const ext_call_options *, ext_emit, void *);
  void (*close)(void *);
  void (*release)(void *);
} ext_backend_extension_context;
typedef struct {
  uint32_t struct_size;
  uint32_t kind;
  const void *config;
  size_t config_size;
} ext_backend_options;
EXT_HOST_API uint32_t ext_host_abi_version(void);
/* Copies backend configuration and descriptor. Does not execute guest code.
 * Both policies required. */
EXT_HOST_API ext_status ext_host_create(const ext_host_options *,
                                        const ext_backend_options *,
                                        ext_host **out);
/* Starts the selected backend. JSON-line process launches a child with
 * argv[0]=executable, empty environment and inherited
 * stderr. Handshake checks the full selection. One call, no retry.
 * timeout_ms: 1..UINT32_MAX. */
EXT_HOST_API ext_status ext_host_start(ext_host *, uint32_t timeout_ms);
EXT_HOST_API ext_status ext_host_start_with_options(ext_host *,
                                                    const ext_call_options *);
EXT_HOST_API ext_status ext_host_invoke_with_options(ext_host *,
                                                     const uint8_t *, size_t,
                                                     const ext_call_options *,
                                                     ext_buffer *);
EXT_HOST_API ext_status ext_host_drain_with_options(ext_host *,
                                                    const ext_call_options *);
/* Calls are serialized unless an extension declares EXT_BACKEND_CONCURRENT.
 * Queue time counts toward timeout. Copies no retained caller pointers.
 * Input/output are CTX envelopes; domain errors return EXT_OK with
 * Response.error. Caller owns returned buffer and frees via the same engine
 * instance that allocated it. Never pass handles or buffers between separately
 * linked engine copies. Request deadline and timeout_ms both bound dispatch.
 * Failed dispatched calls close the session; local validation/policy denial
 * leave it usable. */
EXT_HOST_API ext_status ext_host_invoke(ext_host *, const uint8_t *, size_t,
                                        uint32_t timeout_ms, ext_buffer *out);
/* Input: {contract:{name,version},operation,payload?}. Engine supplies
 * identity, surface, deadline and unique ID. Output is a complete response
 * envelope. */
EXT_HOST_API ext_status ext_host_call(ext_host *, const uint8_t *, size_t,
                                      const ext_call_options *, ext_buffer *);
/* Thread-safe and idempotent. Stops admission, wakes I/O, kills owned child.
 * A timeout/cancellation after dispatch terminates this session. No
 * process-tree containment. Policies are synchronous and cannot be forcibly
 * interrupted. */
EXT_HOST_API void ext_host_close(ext_host *);
/* Drain stops admission and waits for admitted calls. Timeout reopens
 * admission; it does not abort active work. A simultaneous drain returns
 * EXT_DRAINING. timeout_ms: 1..UINT32_MAX. close still aborts immediately
 * during a drain. */
EXT_HOST_API ext_status ext_host_drain(ext_host *, uint32_t timeout_ms);
typedef enum {
  EXT_HOST_CREATED,
  EXT_HOST_READY,
  EXT_HOST_DRAINING,
  EXT_HOST_CLOSED,
  EXT_HOST_FAILED
} ext_host_state;
EXT_HOST_API ext_host_state ext_host_get_state(ext_host *);
/* After all calls/start/close have returned, exactly one owner destroys the
 * handle. Reaps child and frees resources. No concurrent use or double destroy.
 */
EXT_HOST_API void ext_host_destroy(ext_host *);
EXT_HOST_API void ext_buffer_free(ext_buffer *);
/* Strict bounded JSON validation used by bindings/conformance, not a schema
 * API. */
EXT_HOST_API ext_status ext_host_validate_json(const uint8_t *, size_t);
EXT_HOST_API const char *ext_host_status_string(ext_status);
EXT_HOST_API ext_status ext_engine_sha256(const uint8_t *, size_t,
                                          uint8_t out[32]);
/* Read-only filesystem integrity. Rejects symlinks and special files. Root
 * and file contents must be protected by the application against modification.
 * This verifies bytes, never publisher trust. Directory traversal is bounded
 * to 1024 nested directories. Digests are 32 raw bytes. */
EXT_HOST_API ext_status ext_package_verify(const uint8_t *manifest, size_t len,
                                           const char *root);
EXT_HOST_API ext_status ext_directory_digest(const char *root, uint8_t out[32]);
/* Pure engine services: strict bounded JSON input/output, no runtime callbacks,
 * filesystem access, discovery or authorization. Returns an owned JSON value.
 * See services.md for operations and schemas. Free output with ext_buffer_free.
 */
EXT_HOST_API ext_status ext_engine_call(const char *operation,
                                        const uint8_t *input, size_t input_len,
                                        ext_buffer *out);
#ifdef __cplusplus
}
#endif
#endif
