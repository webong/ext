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
  EXT_SEQUENCE,
  /* The peer broke the framing rules: a frame that is not valid, one the call
   * did not allow (progress for a call that did not declare it), or one this
   * host does not know. The session is closed. */
  EXT_PROTOCOL,
  /* A time has run out: an endpoint whose expiry is already past. */
  EXT_EXPIRED,
  /* What was to be created is already there: a package already installed. */
  EXT_EXISTS
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
/* Process launch with the options a host needs to run an installed package
 * (backend kind 5). Like kind 4, but also: working_directory (absolute path;
 * NULL inherits the host's), a bounded capture of the child's stderr
 * (stderr_bytes retained, 0 uses 4096; read it with ext_host_process_stderr),
 * and a scrubbed base environment of PATH taken from the host, to which the
 * environment entries are added (an entry named PATH replaces it). The
 * executable must be an absolute path to a regular file when the host starts.
 * Working directories need posix_spawn_file_actions_addchdir_np (macOS, glibc
 * 2.29 or later); elsewhere a launch with one returns EXT_UNSUPPORTED. */
#define EXT_BACKEND_JSONLINE_PROCESS_LAUNCH 5u
typedef struct {
  uint32_t struct_size;
  const char *executable;
  const char *const *arguments;
  size_t argument_count;
  const char *const *environment;
  size_t environment_count;
  const char *working_directory;
  uint32_t stderr_bytes;
} ext_jsonline_process_launch;
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
/* The backend hands the engine the tagged frames it reads from its plugin, with
 * ext_host_backend_progress and ext_host_backend_reverse_admit (see below). Only
 * such a backend may serve a plugin that declares progress or reverse operations;
 * the engine refuses the others when the host is created. */
#define EXT_BACKEND_FRAMES 4u
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
/* Progress events. A plugin may report progress and log events while a call
 * runs, if its descriptor declares progress for the operation. The engine
 * validates each event, applies the rate limit as a backstop to what the plugin
 * sent, and passes on what is within it. fn receives the JSON of one Progress
 * object: {"kind":"progress"|"log","stream"?:"out"|"err","text"?,"completed"?,
 * "total"?,"dropped"?}, zero fields left out. dropped counts events lost to the
 * rate limit since the previous delivered one; the last event of a call that
 * lost events is a marker with only kind "log" and dropped, delivered before the
 * call returns, whatever its outcome. fn runs between the call's start and its
 * return, on the thread that is making the call, or on an engine thread when the
 * plugin declares reverse operations (the engine reads while no call is in
 * flight); it never runs once the call has returned. It must return promptly,
 * must not throw or reenter the host, and may not retain the borrowed bytes. user
 * must live until the call returns. */
typedef void (*ext_progress_fn)(void *user, const uint8_t *json, size_t len);
typedef struct {
  uint32_t struct_size;
  ext_progress_fn deliver; /* NULL: events are limited and discarded */
  void *user;
  /* Events and bytes of text per second for the call, with a burst of twice
   * that; zero uses 50 events and 65536 bytes. At most 2^20 and 2^30. The rate
   * travels to the plugin in the request, so both ends apply the same one. */
  uint32_t events_per_second;
  uint32_t bytes_per_second;
} ext_progress_options;
/* ext_host_call with progress events. progress may be NULL, which is
 * ext_host_call: an operation that declares progress still runs, and its events
 * are discarded. For an operation that does not declare progress, progress is
 * ignored and fn is never called. */
EXT_HOST_API ext_status ext_host_call_progress(ext_host *, const uint8_t *,
                                               size_t,
                                               const ext_call_options *,
                                               const ext_progress_options *,
                                               ext_buffer *);

/* Reverse calls. A plugin whose descriptor lists operations in "reverse" may call
 * them on its host: while a call of its own is in flight (call scoped, the
 * request names that call as its parent) or at any time after the handshake
 * (session scoped, no parent). The engine checks each request (the frame is well
 * formed, a parent is the call in flight, the operation is declared, the
 * payload is within bounds, the plugin's deadline has not passed, fewer than
 * max_in_flight are running) and refuses it with a stable code otherwise:
 * reverse_undeclared, reverse_unavailable (no services configured, or the
 * handshake has not matched), denied, too_large, busy, expired. The plugin gets
 * the refusal as its answer and the call goes on; a request that breaks the
 * framing rules fails the session with EXT_PROTOCOL.
 *
 * What the engine does not decide is policy. authorize runs for every request
 * that passed the checks (zero allows); handle serves it. Both receive the
 * request as the JSON of a Request (apiVersion, id, plugin, contract, operation,
 * surface, deadline, payload and, for a call-scoped request, parent: the id of
 * the call in flight) and an ext_call_options with these meanings:
 *   value      the binding's context for the request: the value of the call in
 *              flight for a call-scoped request, so a binding can derive the
 *              handler's context from the caller's, and services.session_value
 *              for a session-scoped one;
 *   cancel     an engine-owned signal for this request, set when the call in
 *              flight ends (call scoped), when the host is closed, or when the
 *              caller cancels the call; poll it, it is valid until the hook
 *              returns;
 *   timeout_ms the time left to this reverse call, the earliest of the plugin's
 *              deadline, the call's (call scoped) and services.timeout_ms.
 * Hooks run on engine threads, up to max_in_flight at a time, so they must be
 * safe to run concurrently, must honor cancel and the deadline, and must not
 * reenter the host. A call does not return until the hooks of its call-scoped
 * requests have, so value stays valid.
 *
 * handle emits exactly one result through emit and returns EXT_OK: a payload (a
 * JSON value; empty means null) or a public error (the JSON of a RemoteError,
 * which the plugin receives unchanged if it is valid, and as operation_failed
 * if a peer would reject it). Returning anything else, or emitting twice, is a
 * failure the plugin sees as operation_failed. A result that outlives the
 * deadline is a failure too. */
#define EXT_HOST_REVERSE_PAYLOAD 0u
#define EXT_HOST_REVERSE_PUBLIC_ERROR 1u
typedef ext_status (*ext_reverse_emit)(void *sink, uint32_t kind,
                                       const uint8_t *data, size_t len);
typedef ext_status (*ext_reverse_handler)(void *user, const ext_call_options *,
                                          const uint8_t *request, size_t len,
                                          ext_reverse_emit emit, void *sink);
typedef struct {
  uint32_t struct_size;
  void *user;
  ext_context_policy authorize; /* required */
  ext_reverse_handler handle;   /* required */
  uint32_t timeout_ms;          /* zero uses 30000 */
  uint32_t max_payload_bytes;   /* zero uses 1 MiB; at most the frame limit */
  uint32_t max_in_flight;       /* zero uses 4; at most 1024 */
  void *session_value;          /* the options' value for session-scoped calls */
} ext_host_services;
/* Configure once, before start or any concurrent use. Copies the struct; user
 * and session_value must live until destroy returns. EXT_DENIED when a hook is
 * missing, EXT_INVALID for a bound out of range. A host created over an
 * extension backend serves reverse calls only if it was created with
 * EXT_BACKEND_FRAMES. Destroy waits for the hooks that are running. */
EXT_HOST_API ext_status ext_host_set_services(ext_host *,
                                              const ext_host_services *);

/* Frames from an extension backend. A backend owns its transport and its threads,
 * so it reads the plugin's tagged frames itself and hands them to the engine, which
 * validates them and decides exactly as it does for a process it reads: progress
 * events are checked, limited and delivered to the call they belong to; a reverse
 * request is admitted or refused, and its hooks run. Both calls need a host whose
 * backend was created with EXT_BACKEND_FRAMES (EXT_INVALID otherwise), may be
 * made from any thread, and return EXT_PROTOCOL for a frame that breaks the rules,
 * which the backend must treat as a failed connection. A frame names its call by
 * id, so a call is in flight from the moment its invoke callback is entered until
 * it returns.
 *
 * ext_host_backend_progress takes one progress frame, as JSON. *delivered, when
 * given, is set to 1 if the event was passed on and 0 if the rate limit dropped it.
 *
 * A reverse request takes two steps, so the backend's reader is never held while a
 * hook runs. ext_host_backend_reverse_admit takes one reverse_request frame and
 * either refuses it, setting *ticket to NULL and *answer to the reverse_response
 * frame to send, or admits it, setting *ticket and leaving *answer empty. Then
 * ext_host_backend_reverse_run, called exactly once for each ticket and on any
 * thread, runs the hooks, which can take as long as the request's deadline, and
 * sets *answer to the reverse_response frame to send. caller, when given, is a
 * signal of the backend's own that ends the hooks early, as the engine's own
 * signal does (the plugin's context, for a plugin in the host's process); it must
 * stay valid until run returns. The ticket is consumed. A
 * call does not return from its invoke callback's host until the requests of that
 * call have been run, so hooks that borrow the call's options stay valid; a request
 * that is admitted and never run holds the host open. Free *answer with
 * ext_buffer_free. */
typedef struct ext_reverse_ticket ext_reverse_ticket;
EXT_HOST_API ext_status ext_host_backend_progress(ext_host *, const uint8_t *frame,
                                                  size_t len, int *delivered);
EXT_HOST_API ext_status ext_host_backend_reverse_admit(
    ext_host *, const uint8_t *frame, size_t len, ext_reverse_ticket **ticket,
    ext_buffer *answer);
EXT_HOST_API ext_status ext_host_backend_reverse_run(ext_host *,
                                                     ext_reverse_ticket *ticket,
                                                     const ext_cancel *caller,
                                                     ext_buffer *answer);
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
/* Copies up to capacity bytes of the retained stderr of a host created with
 * EXT_BACKEND_JSONLINE_PROCESS_LAUNCH, and sets *length to the retained size.
 * The output is bounded, possibly truncated, and untrusted text. Hosts of other
 * kinds retain none (length 0). */
EXT_HOST_API ext_status ext_host_process_stderr(ext_host *, uint8_t *buffer,
                                                size_t capacity,
                                                size_t *length);
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
/* Digest of one artifact addressed by a portable relative path under root,
 * never following symbolic links: the SHA-256 of a file, or when tree is
 * nonzero the directory digest of a tree that holds at least one file. */
EXT_HOST_API ext_status ext_artifact_digest(const char *root, const char *path,
                                            int tree, uint8_t out[32]);
/* Package trust is scoped by a host-defined purpose (for example one
 * contract). Installing a package never records trust. A changed digest is
 * distinct from a package that has never been reviewed. The host owns the
 * trust-file path and must serialize writers across processes. The parent
 * directory must exist and be protected from untrusted local writers. */
typedef enum {
  EXT_TRUST_UNTRUSTED = 0,
  EXT_TRUST_TRUSTED = 1,
  EXT_TRUST_CHANGED = 2
} ext_trust_state;
EXT_HOST_API ext_status ext_trust_state_of(const char *path, const char *scope,
                                          const char *id,
                                          const uint8_t current[32],
                                          ext_trust_state *out);
EXT_HOST_API ext_status ext_trust_record(const char *path, const char *scope,
                                         const char *id,
                                         const uint8_t reviewed[32]);
EXT_HOST_API ext_status ext_trust_revoke(const char *path, const char *scope,
                                         const char *id);
/* Verify one installed ext.package/v1 directory below root. Its directory is
 * named by the package id with '/' encoded as "%2f". The manifest id must
 * agree with that name, every listed artifact must match, and no unlisted
 * file may appear. On success out is the directory digest used for trust. */
EXT_HOST_API ext_status ext_store_digest(const char *root, const char *id,
                                         uint8_t out[32]);
/* The package store: install, upgrade, replace, remove and load reviewed
 * ext.package/v1 packages under a root the application supplies. A package is
 * its plugin.json manifest and the artifacts it lists, in a directory named by
 * the package id with '/' encoded as "%2f". The store copies and re-verifies
 * content; it never runs it or grants authority, and it has no default
 * location. A missing root is an empty store and is made (owner-only) by the
 * first install. One store serializes its own calls; several processes
 * mutating one root are the application's to serialize.
 *
 * Statuses: EXT_INVALID for a package or id that is not valid, a directory that
 * is not a package, or an artifact over a limit; EXT_MISMATCH for an artifact
 * whose digest differs, a directory that does not match its manifest, or an
 * upgrade that changes the id or the contracts; EXT_EXISTS for an install over
 * an installed id; EXT_NOT_FOUND for an id that is not installed. The manifest
 * returned is the installed plugin.json, verbatim. */
typedef struct ext_store ext_store;
typedef struct {
  /* Bounds on one artifact file (a file inside a tree counts too), on the files
   * in one tree and on the bytes of one tree. Zero takes the default: 4 GiB,
   * 250000 files and 16 GiB. */
  uint64_t max_artifact_bytes;
  uint32_t max_tree_files;
  uint64_t max_tree_bytes;
} ext_store_limits;
/* Keep the owner-execute bit of a file artifact (not a tree's files, which
 * always keep it) that has any execute bit in the source. The default installs
 * only entrypoints as executable. */
#define EXT_STORE_PRESERVE_EXECUTABLE 1u
EXT_HOST_API ext_status ext_store_open(const char *root,
                                       const ext_store_limits *limits,
                                       ext_store **out);
EXT_HOST_API void ext_store_close(ext_store *store);
/* Copy the package in the directory source into the store. install never
 * replaces an installed id; upgrade replaces an installed package with a new
 * revision of the same id and contracts (the previous installation survives any
 * failure); replace installs or replaces whatever contracts it has. On success
 * *manifest is the installed plugin.json, to be freed with ext_buffer_free. */
EXT_HOST_API ext_status ext_store_install(ext_store *store, const char *source,
                                          uint32_t flags, ext_buffer *manifest);
EXT_HOST_API ext_status ext_store_upgrade(ext_store *store, const char *source,
                                          uint32_t flags, ext_buffer *manifest);
EXT_HOST_API ext_status ext_store_replace(ext_store *store, const char *source,
                                          uint32_t flags, ext_buffer *manifest);
EXT_HOST_API ext_status ext_store_remove(ext_store *store, const char *id);
/* Read and verify one installed package, returning its plugin.json. */
EXT_HOST_API ext_status ext_store_load(ext_store *store, const char *id,
                                       ext_buffer *manifest);
/* The ids of the installed packages as a JSON array of strings in byte order.
 * Staging directories (a leading ".") are skipped; any other entry that is not a
 * package directory is EXT_INVALID. Nothing is read or verified. */
EXT_HOST_API ext_status ext_store_ids(ext_store *store, ext_buffer *ids);
/* Whole-root reports, as JSON. Every line has "id" (the directory name when
 * that is not a valid package id), "directory" (the root joined with the
 * entry's name, lexically cleaned) and "layer" (0 for a store; the position of
 * the root in a layered view). Lines are sorted by id, then layer. Staging
 * directories (a leading ".") are skipped and a missing root is empty. The
 * status is only for a root that cannot be read, or for the failure that stops
 * a list.
 *
 * ext_store_list reads and verifies every package and returns an array of
 * {id, directory, layer, manifest}, the manifest being plugin.json as an
 * embedded value. It fails with the status of the first entry that is not a
 * package directory and of the first package that does not verify.
 *
 * ext_store_verify checks every entry and does not stop at a bad one: an array
 * of {id, directory, layer, status}, where status is the ext_status verifying
 * it gave (0, EXT_OK, verified; EXT_INVALID for an entry that is not a package
 * directory). It runs nothing and changes nothing.
 *
 * ext_store_list_unverified reads only the manifests and checks them against
 * their directory names; no artifact is read or hashed, so THE RESULT IS
 * UNVERIFIED and must not be launched or trusted as it stands. It returns
 * {"packages": [{id, directory, layer, manifest}], "problems": [{id, directory,
 * layer, status}]}; a package that cannot be read does not hide the others. */
EXT_HOST_API ext_status ext_store_list(ext_store *store, ext_buffer *packages);
EXT_HOST_API ext_status ext_store_verify(ext_store *store, ext_buffer *checks);
EXT_HOST_API ext_status ext_store_list_unverified(ext_store *store,
                                                  ext_buffer *listing);
/* A read-only view over several roots in priority order, highest first, such as
 * a user root before a system root. An id is served from the first root that
 * holds it; a copy in a lower root is shadowed and never read. If the winning
 * copy does not verify, load and list fail instead of falling back to a lower
 * one. The view never writes, so a system root is never created or modified
 * through it; install, upgrade and remove stay with the ext_store the caller
 * owns. It has no default roots. Roots are compared as cleaned paths: a repeated
 * root, an empty one, or no root at all is EXT_INVALID. Reports are those of
 * ext_store_list, ext_store_verify and ext_store_list_unverified over all the
 * roots, one line per id from the root that serves it. */
typedef struct ext_layered ext_layered;
EXT_HOST_API ext_status ext_layered_open(const char *const *roots, size_t count,
                                         const ext_store_limits *limits,
                                         ext_layered **out);
EXT_HOST_API void ext_layered_close(ext_layered *layered);
/* Read and verify the package from the first root that holds id, returning its
 * plugin.json and, when layer is not NULL, the position of that root.
 * EXT_NOT_FOUND means no root holds it. */
EXT_HOST_API ext_status ext_layered_load(ext_layered *layered, const char *id,
                                         ext_buffer *manifest, size_t *layer);
EXT_HOST_API ext_status ext_layered_list(ext_layered *layered,
                                         ext_buffer *packages);
EXT_HOST_API ext_status ext_layered_verify(ext_layered *layered,
                                           ext_buffer *checks);
EXT_HOST_API ext_status ext_layered_list_unverified(ext_layered *layered,
                                                    ext_buffer *listing);
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
