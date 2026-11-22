#ifndef EXT_PLUGIN_HOST_H
#define EXT_PLUGIN_HOST_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
#if defined(EXT_PLUGIN_HOST_STATIC)
#define EXT_PLUGIN_HOST_API
#elif defined(_WIN32) && defined(EXT_PLUGIN_HOST_BUILD)
#define EXT_PLUGIN_HOST_API __declspec(dllexport)
#elif defined(_WIN32)
#define EXT_PLUGIN_HOST_API __declspec(dllimport)
#else
#define EXT_PLUGIN_HOST_API __attribute__((visibility("default")))
#endif
/* Experimental embedding ABI, independent of ext.plugin/v1 and guest ABI v1.
 * No ABI stability commitment until the prototype is evaluated. */
#define EXT_PLUGIN_HOST_ABI_VERSION 2u
#define EXT_PLUGIN_HOST_MAX_FRAME (24u * 1024u * 1024u)
typedef struct ext_plugin_host ext_plugin_host;
typedef struct {
  uint8_t *data;
  size_t len;
} ext_plugin_buffer;
typedef int32_t ext_plugin_status;
enum {
  EXT_PLUGIN_OK,
  EXT_PLUGIN_INVALID,
  EXT_PLUGIN_DENIED,
  EXT_PLUGIN_MISMATCH,
  EXT_PLUGIN_UNSUPPORTED,
  EXT_PLUGIN_CLOSED,
  EXT_PLUGIN_TIMEOUT,
  EXT_PLUGIN_IO,
  EXT_PLUGIN_NOMEM,
  EXT_PLUGIN_DRAINING,
  EXT_PLUGIN_NOT_FOUND,
  EXT_PLUGIN_AMBIGUOUS,
  EXT_PLUGIN_CANCELED,
  EXT_PLUGIN_UPDATING,
  EXT_PLUGIN_CAPACITY,
  EXT_PLUGIN_SEQUENCE,
  /* The peer broke the framing rules: a frame that is not valid, one the call
   * did not allow (progress for a call that did not declare it), or one this
   * host does not know. The session is closed. */
  EXT_PLUGIN_PROTOCOL,
  /* A time has run out: an endpoint whose expiry is already past. */
  EXT_PLUGIN_EXPIRED,
  /* What was to be created is already there: a package already installed. */
  EXT_PLUGIN_EXISTS,
  /* A file or directory that is not there: a package source, its manifest or an
   * artifact it lists. Other failures to read are EXT_PLUGIN_IO. */
  EXT_PLUGIN_MISSING
};
typedef struct ext_plugin_cancel ext_plugin_cancel;
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_cancel_create(ext_plugin_cancel **out);
EXT_PLUGIN_HOST_API void ext_plugin_cancel_signal(ext_plugin_cancel *);
EXT_PLUGIN_HOST_API int ext_plugin_cancel_is_signaled(const ext_plugin_cancel *);
/* Free only after all calls and signal operations using it have joined. */
EXT_PLUGIN_HOST_API void ext_plugin_cancel_destroy(ext_plugin_cancel *);
typedef struct {
  uint32_t struct_size;
  uint32_t timeout_ms;
  const ext_plugin_cancel *cancel;
  void
      *value; /* borrowed binding-owned context; engine never dereferences it */
} ext_plugin_call_options;
/* Policies receive borrowed UTF-8 JSON; zero allows. They execute on the
 * calling thread, must return promptly, must not throw or reenter the same
 * host, and may not retain the borrowed bytes. user must live until destroy
 * returns. */
typedef int32_t (*ext_plugin_policy)(void *user, const uint8_t *json, size_t len);
typedef struct {
  uint32_t abi_version;
  uint32_t struct_size;
  const uint8_t *descriptor;
  size_t descriptor_len;
  ext_plugin_policy
      verify; /* before spawning; verify artifact in application policy */
  ext_plugin_policy authorize; /* validated request, before dispatch */
  void *user;
} ext_plugin_host_options;
typedef int32_t (*ext_plugin_context_policy)(void *, const ext_plugin_call_options *,
                                      const uint8_t *, size_t);
typedef void (*ext_plugin_observer)(void *, const ext_plugin_call_options *, const uint8_t *,
                             size_t);
typedef struct {
  uint32_t struct_size;
  void *user;
  ext_plugin_context_policy verify;
  ext_plugin_context_policy authorize;
  ext_plugin_observer observe;
} ext_plugin_host_hooks;
/* Configure once before start or any concurrent use. Optional contextual policy
 * hooks replace the corresponding legacy policy. Observer gets bounded metadata
 * only, never payloads or private error text; callbacks must not reenter. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_set_hooks(ext_plugin_host *, const ext_plugin_host_hooks *);
/* Backend configuration is independent of host selection and policy. Unknown
 * kinds return EXT_PLUGIN_UNSUPPORTED; new backends do not change ext_plugin_host_options. */
#define EXT_PLUGIN_BACKEND_JSONLINE_PROCESS 1u
typedef struct {
  const char *executable; /* absolute immutable verified path; no shell */
} ext_plugin_jsonline_process_options;
/* Explicit launch configuration. Arrays contain count elements (no sentinel
 * required). All strings/arrays are copied; the environment is never inherited.
 * Maximum 256 arguments (4096 bytes each), 256 NAME=value entries (8192 bytes
 * each, unique nonempty names). argv[0] is supplied by the engine. */
#define EXT_PLUGIN_BACKEND_JSONLINE_PROCESS_CONFIG 4u
typedef struct {
  uint32_t struct_size;
  const char *executable;
  const char *const *arguments;
  size_t argument_count;
  const char *const *environment;
  size_t environment_count;
} ext_plugin_jsonline_process_config;
/* Process launch with the options a host needs to run an installed package
 * (backend kind 5). Like kind 4, but also: working_directory (absolute path;
 * NULL inherits the host's), a bounded capture of the child's stderr
 * (stderr_bytes retained, 0 uses 4096; read it with ext_plugin_host_process_stderr),
 * and a scrubbed base environment of PATH taken from the host, to which the
 * environment entries are added (an entry named PATH replaces it). The
 * executable must be an absolute path to a regular file when the host starts.
 * Working directories need posix_spawn_file_actions_addchdir_np (macOS, glibc
 * 2.29 or later); elsewhere a launch with one returns EXT_PLUGIN_UNSUPPORTED. */
#define EXT_PLUGIN_BACKEND_JSONLINE_PROCESS_LAUNCH 5u
typedef struct {
  uint32_t struct_size;
  const char *executable;
  const char *const *arguments;
  size_t argument_count;
  const char *const *environment;
  size_t environment_count;
  const char *working_directory;
  uint32_t stderr_bytes;
} ext_plugin_jsonline_process_launch;
/* An extension returns one descriptor (connect) or response envelope (invoke)
 * through emit. emit copies bytes into engine-owned memory; call exactly once
 * on success, synchronously, and never retain emit/context or input pointers.
 * Returning an error after emit is allowed; the engine discards the result.
 * All callbacks must return a ext_plugin_status, must not unwind, and must not reenter
 * this host. close is the exception: it runs concurrently with connect/invoke
 * and must interrupt pending work. It runs once, including before connect.
 * release runs once at destroy, after all host calls have joined. Ownership of
 * user transfers only on successful ext_plugin_host_create. Code must remain loaded.
 * timeout_ms is remaining time, including queue and policy time. Native work
 * must cooperate; the engine cannot forcibly interrupt arbitrary native code.
 */
#define EXT_PLUGIN_BACKEND_EXTENSION 2u
#define EXT_PLUGIN_BACKEND_CONCURRENT 1u
/* The backend hands the engine the tagged frames it reads from its plugin, with
 * ext_plugin_host_backend_progress and ext_plugin_host_backend_reverse_admit (see below). Only
 * such a backend may serve a plugin that declares progress or reverse operations;
 * the engine refuses the others when the host is created. */
#define EXT_PLUGIN_BACKEND_FRAMES 4u
typedef ext_plugin_status (*ext_plugin_emit)(void *context, const uint8_t *data, size_t len);
typedef struct {
  uint32_t struct_size;
  uint32_t flags;
  void *user;
  ext_plugin_status (*connect)(void *user, uint32_t timeout_ms, ext_plugin_emit emit,
                        void *context);
  ext_plugin_status (*invoke)(void *user, const uint8_t *request, size_t len,
                       uint32_t timeout_ms, ext_plugin_emit emit, void *context);
  void (*close)(void *user);
  void (*release)(void *user);
} ext_plugin_backend_extension;
/* Context-aware extension; same ownership as ext_plugin_backend_extension. */
#define EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT 3u
typedef struct {
  uint32_t struct_size;
  uint32_t flags;
  void *user;
  ext_plugin_status (*connect)(void *, const ext_plugin_call_options *, ext_plugin_emit, void *);
  ext_plugin_status (*invoke)(void *, const uint8_t *, size_t,
                       const ext_plugin_call_options *, ext_plugin_emit, void *);
  void (*close)(void *);
  void (*release)(void *);
} ext_plugin_backend_extension_context;
typedef struct {
  uint32_t struct_size;
  uint32_t kind;
  const void *config;
  size_t config_size;
} ext_plugin_backend_options;
/* A guest built as a C ABI shared library (ext_plugin_guest.h): C, C++, Rust, Zig, Go
 * -buildmode=c-shared, Swift, or any code that exports the four ext_plugin_guest_*
 * functions. ext_plugin_shared_library_backend_create prepares a backend for
 * ext_plugin_host_create (kind EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT, config = out,
 * config_size = sizeof(*out)). It copies the path and loads nothing: the library
 * is loaded, its ABI version checked and a handle opened in connect, so after the
 * host's verify policy has approved the artifact, and the handshake follows.
 * Ownership of out->user passes to the host on a successful ext_plugin_host_create; if
 * that fails the caller must call out->release(out->user).
 *
 * path is an absolute UTF-8 path, at most 4096 bytes, to a verified library;
 * use an immutable, revision-specific artifact path. A library is loaded once
 * per canonical path and never unloaded, even when its ABI check fails, since
 * its initializers have already run. Calls are serialized per backend, with the
 * ABI's 24 MiB response capacity and no retry. A transport failure (a nonzero
 * ABI status or an impossible response length) ends the session. close cannot
 * interrupt native code: it refuses new calls, and the handle is closed in
 * release, after every call has returned. Native code runs in the host process
 * with its full access. Returns EXT_PLUGIN_INVALID for a malformed path, EXT_PLUGIN_NOMEM, or
 * EXT_PLUGIN_UNSUPPORTED where the platform cannot load libraries. */
typedef struct {
  uint32_t struct_size;
  const char *path;
} ext_plugin_shared_library_options;
EXT_PLUGIN_HOST_API ext_plugin_status
ext_plugin_shared_library_backend_create(const ext_plugin_shared_library_options *options,
                                  ext_plugin_backend_extension_context *out);
EXT_PLUGIN_HOST_API uint32_t ext_plugin_host_abi_version(void);
/* Copies backend configuration and descriptor. Does not execute guest code.
 * Both policies required. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_create(const ext_plugin_host_options *,
                                        const ext_plugin_backend_options *,
                                        ext_plugin_host **out);
/* Starts the selected backend. JSON-line process launches a child with
 * argv[0]=executable, empty environment and inherited
 * stderr. Handshake checks the full selection. One call, no retry.
 * timeout_ms: 1..UINT32_MAX. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_start(ext_plugin_host *, uint32_t timeout_ms);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_start_with_options(ext_plugin_host *,
                                                    const ext_plugin_call_options *);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_invoke_with_options(ext_plugin_host *,
                                                     const uint8_t *, size_t,
                                                     const ext_plugin_call_options *,
                                                     ext_plugin_buffer *);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_drain_with_options(ext_plugin_host *,
                                                    const ext_plugin_call_options *);
/* Calls are serialized unless an extension declares EXT_PLUGIN_BACKEND_CONCURRENT.
 * Queue time counts toward timeout. Copies no retained caller pointers.
 * Input/output are CTX envelopes; domain errors return EXT_PLUGIN_OK with
 * Response.error. Caller owns returned buffer and frees via the same engine
 * instance that allocated it. Never pass handles or buffers between separately
 * linked engine copies. Request deadline and timeout_ms both bound dispatch.
 * Failed dispatched calls close the session; local validation/policy denial
 * leave it usable. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_invoke(ext_plugin_host *, const uint8_t *, size_t,
                                        uint32_t timeout_ms, ext_plugin_buffer *out);
/* Input: {contract:{name,version},operation,payload?}. Engine supplies
 * identity, surface, deadline and unique ID. Output is a complete response
 * envelope. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_call(ext_plugin_host *, const uint8_t *, size_t,
                                      const ext_plugin_call_options *, ext_plugin_buffer *);
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
typedef void (*ext_plugin_progress_fn)(void *user, const uint8_t *json, size_t len);
typedef struct {
  uint32_t struct_size;
  ext_plugin_progress_fn deliver; /* NULL: events are limited and discarded */
  void *user;
  /* Events and bytes of text per second for the call, with a burst of twice
   * that; zero uses 50 events and 65536 bytes. At most 2^20 and 2^30. The rate
   * travels to the plugin in the request, so both ends apply the same one. */
  uint32_t events_per_second;
  uint32_t bytes_per_second;
} ext_plugin_progress_options;
/* ext_plugin_host_call with progress events. progress may be NULL, which is
 * ext_plugin_host_call: an operation that declares progress still runs, and its events
 * are discarded. For an operation that does not declare progress, progress is
 * ignored and fn is never called. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_call_progress(ext_plugin_host *, const uint8_t *,
                                               size_t,
                                               const ext_plugin_call_options *,
                                               const ext_plugin_progress_options *,
                                               ext_plugin_buffer *);

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
 * framing rules fails the session with EXT_PLUGIN_PROTOCOL.
 *
 * What the engine does not decide is policy. authorize runs for every request
 * that passed the checks (zero allows); handle serves it. Both receive the
 * request as the JSON of a Request (apiVersion, id, plugin, contract, operation,
 * surface, deadline, payload and, for a call-scoped request, parent: the id of
 * the call in flight) and an ext_plugin_call_options with these meanings:
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
 * handle emits exactly one result through emit and returns EXT_PLUGIN_OK: a payload (a
 * JSON value; empty means null) or a public error (the JSON of a RemoteError,
 * which the plugin receives unchanged if it is valid, and as operation_failed
 * if a peer would reject it). Returning anything else, or emitting twice, is a
 * failure the plugin sees as operation_failed. A result that outlives the
 * deadline is a failure too. */
#define EXT_PLUGIN_HOST_REVERSE_PAYLOAD 0u
#define EXT_PLUGIN_HOST_REVERSE_PUBLIC_ERROR 1u
typedef ext_plugin_status (*ext_plugin_reverse_emit)(void *sink, uint32_t kind,
                                       const uint8_t *data, size_t len);
typedef ext_plugin_status (*ext_plugin_reverse_handler)(void *user, const ext_plugin_call_options *,
                                          const uint8_t *request, size_t len,
                                          ext_plugin_reverse_emit emit, void *sink);
typedef struct {
  uint32_t struct_size;
  void *user;
  ext_plugin_context_policy authorize; /* required */
  ext_plugin_reverse_handler handle;   /* required */
  uint32_t timeout_ms;          /* zero uses 30000 */
  uint32_t max_payload_bytes;   /* zero uses 1 MiB; at most the frame limit */
  uint32_t max_in_flight;       /* zero uses 4; at most 1024 */
  void *session_value;          /* the options' value for session-scoped calls */
} ext_plugin_host_services;
/* Configure once, before start or any concurrent use. Copies the struct; user
 * and session_value must live until destroy returns. EXT_PLUGIN_DENIED when a hook is
 * missing, EXT_PLUGIN_INVALID for a bound out of range. A host created over an
 * extension backend serves reverse calls only if it was created with
 * EXT_PLUGIN_BACKEND_FRAMES. Destroy waits for the hooks that are running. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_set_services(ext_plugin_host *,
                                              const ext_plugin_host_services *);

/* Where the reverse hooks run. By default (EXT_PLUGIN_HOST_REVERSE_ENGINE_THREADS) the
 * engine runs authorize and handle on threads of its own, up to max_in_flight
 * at a time. With EXT_PLUGIN_HOST_REVERSE_CALLER_THREAD they run on a thread of the
 * application instead, one request at a time, for a host whose language cannot
 * run code on a thread it did not create (Ruby, Lua, PHP): the engine queues
 * each admitted request, and a thread that is waiting in a call on this host
 * (ext_plugin_host_call, ext_plugin_host_call_progress, ext_plugin_host_invoke) serves the queue
 * while it waits, whichever call the request belongs to. A session-scoped
 * request that arrives while no call is in flight waits in the queue until a
 * call starts or ext_plugin_host_serve_reverse runs. Everything else is as on engine
 * threads: admission, the codes, the deadline chain, cancellation (a request
 * still queued when its call ends, the host closes or its deadline passes is
 * answered expired without its hooks running) and the shape of every answer.
 * Requests a frames backend hands over with ext_plugin_host_backend_reverse_run run
 * where that call is made, in either mode.
 *
 * ext_plugin_host_set_reverse_mode configures it before start, like
 * ext_plugin_host_set_services; EXT_PLUGIN_INVALID for an unknown mode or a host that has
 * started. ext_plugin_host_serve_reverse runs the queued requests on this thread and
 * returns once it has served at least one and the queue is empty (EXT_PLUGIN_OK), when
 * timeout_ms passes with nothing to serve (EXT_PLUGIN_TIMEOUT), when cancel is
 * signaled (EXT_PLUGIN_CANCELED) or when the host is closed or failed (EXT_PLUGIN_CLOSED).
 * It is EXT_PLUGIN_UNSUPPORTED on a host that runs its hooks on engine threads. Neither
 * may be called from inside a hook of the same host. */
#define EXT_PLUGIN_HOST_REVERSE_ENGINE_THREADS 0u
#define EXT_PLUGIN_HOST_REVERSE_CALLER_THREAD 1u
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_set_reverse_mode(ext_plugin_host *, uint32_t mode);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_serve_reverse(ext_plugin_host *,
                                               const ext_plugin_call_options *);

/* A guest registry composes guests through their host: a reverse call one guest
 * makes is served by a forward call to another guest, along an edge the host
 * declared. The host stays the authority. Nothing changes on the wire: the
 * calling guest sees an ordinary reverse response and the target an ordinary
 * call, and the registry routes only between hosts the application created and
 * started; it discovers and launches nothing.
 *
 * ext_plugin_registry_register names the host that serves a forward contract; the host
 * must be started and its descriptor must list the contract (EXT_PLUGIN_UNSUPPORTED
 * otherwise, EXT_PLUGIN_EXISTS when the contract is already registered).
 * ext_plugin_registry_route declares one edge: a reverse call of from_operation in
 * from_contract is served by a call of to_operation in to_contract (EXT_PLUGIN_EXISTS
 * when that reverse operation is already routed). ext_plugin_registry_attach installs
 * the registry as the reverse handler of a source host, in place of
 * ext_plugin_host_set_services and under the same rules: services->handle must be NULL
 * (the registry serves the calls), services->authorize is required and runs
 * first, as for every reverse call, and the bounds and session_value apply as
 * they do there. Names are copied; hosts are borrowed.
 *
 * A reverse call the registry serves is answered:
 *   - reverse_undeclared when no edge leaves its (contract, operation);
 *   - reverse_unavailable when the edge's target contract is not registered,
 *     or its host is closed, draining or does not offer the operation;
 *   - denied when the target host's authorize refuses the forward call, or the
 *     call would re-enter a host already on its call chain (see below);
 *   - expired when the forward call ran out of time;
 *   - otherwise with the target's answer, unchanged: its payload, or its public
 *     error as the target returned it. Any other failure is operation_failed.
 * The forward call runs with the reverse call's time left and its cancel
 * signal, so a deadline or cancellation reaches the target. Its value is NULL:
 * the reverse call's value belongs to the source's binding, and the target may
 * be another one.
 *
 * Cycles. A call chain starts at a source host's reverse call and grows by one
 * host for each forward call the registry makes. A reverse call that a target
 * makes inside such a forward call continues that chain; a session-scoped one
 * starts a new chain at its source. An edge whose target host is already on the
 * chain (A to A, or A to B to A) is refused with denied instead of waiting on a
 * host that is busy with the call that leads to it. A chain that leaves the
 * registry (a handler of the application that calls another guest itself) is not
 * seen, and is bounded only by the deadline it carries.
 *
 * All functions are safe to call concurrently with each other and with calls
 * being routed. ext_plugin_registry_unregister removes a contract and waits for the
 * forward calls routed to its host to return, so the host may be destroyed after
 * it returns; it is EXT_PLUGIN_NOT_FOUND for a contract that is not registered.
 * ext_plugin_registry_destroy frees the registry; call it only once every host
 * attached to it has been destroyed. */
typedef struct ext_plugin_registry ext_plugin_registry;
typedef struct {
  uint32_t struct_size;
  const char *from_name, *from_version, *from_operation;
  const char *to_name, *to_version, *to_operation;
} ext_plugin_registry_edge;
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_registry_create(ext_plugin_registry **out);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_registry_register(ext_plugin_registry *, const char *name,
                                              const char *version,
                                              ext_plugin_host *host);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_registry_unregister(ext_plugin_registry *,
                                                const char *name,
                                                const char *version);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_registry_route(ext_plugin_registry *,
                                           const ext_plugin_registry_edge *);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_registry_attach(ext_plugin_registry *, ext_plugin_host *source,
                                            const ext_plugin_host_services *);
EXT_PLUGIN_HOST_API void ext_plugin_registry_destroy(ext_plugin_registry *);

/* Frames from an extension backend. A backend owns its transport and its threads,
 * so it reads the plugin's tagged frames itself and hands them to the engine, which
 * validates them and decides exactly as it does for a process it reads: progress
 * events are checked, limited and delivered to the call they belong to; a reverse
 * request is admitted or refused, and its hooks run. Both calls need a host whose
 * backend was created with EXT_PLUGIN_BACKEND_FRAMES (EXT_PLUGIN_INVALID otherwise), may be
 * made from any thread, and return EXT_PLUGIN_PROTOCOL for a frame that breaks the rules,
 * which the backend must treat as a failed connection. A frame names its call by
 * id, so a call is in flight from the moment its invoke callback is entered until
 * it returns.
 *
 * ext_plugin_host_backend_progress takes one progress frame, as JSON. *delivered, when
 * given, is set to 1 if the event was passed on and 0 if the rate limit dropped it.
 *
 * A reverse request takes two steps, so the backend's reader is never held while a
 * hook runs. ext_plugin_host_backend_reverse_admit takes one reverse_request frame and
 * either refuses it, setting *ticket to NULL and *answer to the reverse_response
 * frame to send, or admits it, setting *ticket and leaving *answer empty. Then
 * ext_plugin_host_backend_reverse_run, called exactly once for each ticket and on any
 * thread, runs the hooks, which can take as long as the request's deadline, and
 * sets *answer to the reverse_response frame to send. caller, when given, is a
 * signal of the backend's own that ends the hooks early, as the engine's own
 * signal does (the plugin's context, for a plugin in the host's process); it must
 * stay valid until run returns. The ticket is consumed. A
 * call does not return from its invoke callback's host until the requests of that
 * call have been run, so hooks that borrow the call's options stay valid; a request
 * that is admitted and never run holds the host open. Free *answer with
 * ext_plugin_buffer_free. */
typedef struct ext_plugin_reverse_ticket ext_plugin_reverse_ticket;
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_backend_progress(ext_plugin_host *, const uint8_t *frame,
                                                  size_t len, int *delivered);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_backend_reverse_admit(
    ext_plugin_host *, const uint8_t *frame, size_t len, ext_plugin_reverse_ticket **ticket,
    ext_plugin_buffer *answer);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_backend_reverse_run(ext_plugin_host *,
                                                     ext_plugin_reverse_ticket *ticket,
                                                     const ext_plugin_cancel *caller,
                                                     ext_plugin_buffer *answer);
/* Thread-safe and idempotent. Stops admission, wakes I/O, kills owned child.
 * A timeout/cancellation after dispatch terminates this session. No
 * process-tree containment. Policies are synchronous and cannot be forcibly
 * interrupted. */
EXT_PLUGIN_HOST_API void ext_plugin_host_close(ext_plugin_host *);
/* Drain stops admission and waits for admitted calls. Timeout reopens
 * admission; it does not abort active work. A simultaneous drain returns
 * EXT_PLUGIN_DRAINING. timeout_ms: 1..UINT32_MAX. close still aborts immediately
 * during a drain. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_drain(ext_plugin_host *, uint32_t timeout_ms);
typedef enum {
  EXT_PLUGIN_HOST_CREATED,
  EXT_PLUGIN_HOST_READY,
  EXT_PLUGIN_HOST_DRAINING,
  EXT_PLUGIN_HOST_CLOSED,
  EXT_PLUGIN_HOST_FAILED
} ext_plugin_host_state;
EXT_PLUGIN_HOST_API ext_plugin_host_state ext_plugin_host_get_state(ext_plugin_host *);
/* After all calls/start/close have returned, exactly one owner destroys the
 * handle. Reaps child and frees resources. No concurrent use or double destroy.
 */
EXT_PLUGIN_HOST_API void ext_plugin_host_destroy(ext_plugin_host *);
/* Copies up to capacity bytes of the retained stderr of a host created with
 * EXT_PLUGIN_BACKEND_JSONLINE_PROCESS_LAUNCH, and sets *length to the retained size.
 * The output is bounded, possibly truncated, and untrusted text. Hosts of other
 * kinds retain none (length 0). */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_process_stderr(ext_plugin_host *, uint8_t *buffer,
                                                size_t capacity,
                                                size_t *length);
EXT_PLUGIN_HOST_API void ext_plugin_buffer_free(ext_plugin_buffer *);
/* Strict bounded JSON validation used by bindings/conformance, not a schema
 * API. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_host_validate_json(const uint8_t *, size_t);
EXT_PLUGIN_HOST_API const char *ext_plugin_host_status_string(ext_plugin_status);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_sha256(const uint8_t *, size_t,
                                          uint8_t out[32]);
/* Read-only filesystem integrity. Rejects symlinks and special files. Root
 * and file contents must be protected by the application against modification.
 * This verifies bytes, never publisher trust. Directory traversal is bounded
 * to 1024 nested directories. Digests are 32 raw bytes. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_package_verify(const uint8_t *manifest, size_t len,
                                           const char *root);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_directory_digest(const char *root, uint8_t out[32]);
/* Digest of one artifact addressed by a portable relative path under root,
 * never following symbolic links: the SHA-256 of a file, or when tree is
 * nonzero the directory digest of a tree that holds at least one file. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_artifact_digest(const char *root, const char *path,
                                            int tree, uint8_t out[32]);
/* Package trust is scoped by a host-defined purpose (for example one
 * contract). Installing a package never records trust. A changed digest is
 * distinct from a package that has never been reviewed. The host owns the
 * trust-file path and must serialize writers across processes. The parent
 * directory must exist and be protected from untrusted local writers. */
typedef enum {
  EXT_PLUGIN_TRUST_UNTRUSTED = 0,
  EXT_PLUGIN_TRUST_TRUSTED = 1,
  EXT_PLUGIN_TRUST_CHANGED = 2
} ext_plugin_trust_state;
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_trust_state_of(const char *path, const char *scope,
                                          const char *id,
                                          const uint8_t current[32],
                                          ext_plugin_trust_state *out);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_trust_record(const char *path, const char *scope,
                                         const char *id,
                                         const uint8_t reviewed[32]);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_trust_revoke(const char *path, const char *scope,
                                         const char *id);
/* Verify one installed ext.package/v1 directory below root. Its directory is
 * named by the package id with '/' encoded as "%2f". The manifest id must
 * agree with that name, every listed artifact must match, and no unlisted
 * file may appear. On success out is the directory digest used for trust. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_digest(const char *root, const char *id,
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
 * Statuses: EXT_PLUGIN_INVALID for a package or id that is not valid, a directory that
 * is not a package, or an artifact over a limit; EXT_PLUGIN_MISMATCH for an artifact
 * whose digest differs, a directory that does not match its manifest, or an
 * upgrade that changes the id or the contracts; EXT_PLUGIN_EXISTS for an install over
 * an installed id; EXT_PLUGIN_NOT_FOUND for an id that is not installed. The manifest
 * returned is the installed plugin.json, verbatim. */
typedef struct ext_plugin_store ext_plugin_store;
typedef struct {
  /* Bounds on one artifact file (a file inside a tree counts too), on the files
   * in one tree and on the bytes of one tree. Zero takes the default: 4 GiB,
   * 250000 files and 16 GiB. */
  uint64_t max_artifact_bytes;
  uint32_t max_tree_files;
  uint64_t max_tree_bytes;
} ext_plugin_store_limits;
/* Keep the owner-execute bit of a file artifact (not a tree's files, which
 * always keep it) that has any execute bit in the source. The default installs
 * only entrypoints as executable. */
#define EXT_PLUGIN_STORE_PRESERVE_EXECUTABLE 1u
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_open(const char *root,
                                       const ext_plugin_store_limits *limits,
                                       ext_plugin_store **out);
EXT_PLUGIN_HOST_API void ext_plugin_store_close(ext_plugin_store *store);
/* Copy the package in the directory source into the store. install never
 * replaces an installed id; upgrade replaces an installed package with a new
 * revision of the same id and contracts (the previous installation survives any
 * failure); replace installs or replaces whatever contracts it has. On success
 * *manifest is the installed plugin.json, to be freed with ext_plugin_buffer_free. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_install(ext_plugin_store *store, const char *source,
                                          uint32_t flags, ext_plugin_buffer *manifest);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_upgrade(ext_plugin_store *store, const char *source,
                                          uint32_t flags, ext_plugin_buffer *manifest);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_replace(ext_plugin_store *store, const char *source,
                                          uint32_t flags, ext_plugin_buffer *manifest);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_remove(ext_plugin_store *store, const char *id);
/* Install the package in a pinned archive, or upgrade the installed package of
 * the same id to it (as ext_plugin_store_upgrade does). archive is the native path of
 * a zip, a gzip-compressed tar or a plain tar, told apart by its content;
 * sha256 is the digest the file must have, 64 hexadecimal characters in either
 * case; id, when not NULL or empty, is the package the archive must hold;
 * max_archive_bytes bounds the archive file, 512 MiB when zero. The file is
 * checked against the digest before anything is extracted. Extraction is
 * bounded by the store's limits (max_tree_files counts every entry,
 * max_tree_bytes the extracted total, max_artifact_bytes one file) and writes
 * only directories and regular files: a link or special entry, a path that is
 * absolute, leaves the archive, repeats another whatever its case or could not
 * exist on Windows, and an entry that is not what its header declares are
 * EXT_PLUGIN_INVALID. plugin.json is at the top of the archive or in its one top-level
 * directory. What was extracted is installed like a directory, so the package
 * is verified against its manifest. A package already installed at the
 * archive's revision must have the archive's content.
 *
 * A successful install leaves a receipt: the archive digest and the revision
 * it produced. When the receipt of an installed package names this digest and
 * the revision installed now, and the package still verifies, the call returns
 * it without opening the archive.
 *
 * Statuses, besides those of install and upgrade: EXT_PLUGIN_MISMATCH for a digest
 * that differs, an archive that holds another package than id, or other
 * content at the installed revision; EXT_PLUGIN_UNSUPPORTED for a format that is not
 * read (zstd, xz and bzip2 are not, nor is a zip in the zip64 form, which is
 * EXT_PLUGIN_INVALID); EXT_PLUGIN_MISSING for an archive that is not there. Downloading an
 * archive is the application's. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_install_archive(ext_plugin_store *store,
                                                  const char *archive,
                                                  const char *sha256,
                                                  const char *id, uint32_t flags,
                                                  uint64_t max_archive_bytes,
                                                  ext_plugin_buffer *manifest);
/* The receipt of an installed package as JSON: {"id", "revision",
 * "artifact_sha256"}, the last in lower case. EXT_PLUGIN_NOT_FOUND when no archive
 * install recorded one. It describes the install that wrote it: compare its
 * revision with the installed package before relying on it. ext_plugin_store_remove
 * deletes it. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_receipt(ext_plugin_store *store, const char *id,
                                          ext_plugin_buffer *receipt);
/* Read and verify one installed package, returning its plugin.json. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_load(ext_plugin_store *store, const char *id,
                                       ext_plugin_buffer *manifest);
/* The ids of the installed packages as a JSON array of strings in byte order.
 * Staging directories (a leading ".") are skipped; any other entry that is not a
 * package directory is EXT_PLUGIN_INVALID. Nothing is read or verified. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_ids(ext_plugin_store *store, ext_plugin_buffer *ids);
/* Whole-root reports, as JSON. Every line has "id" (the directory name when
 * that is not a valid package id), "directory" (the root joined with the
 * entry's name, lexically cleaned) and "layer" (0 for a store; the position of
 * the root in a layered view). Lines are sorted by id, then layer. Staging
 * directories (a leading ".") are skipped and a missing root is empty. The
 * status is only for a root that cannot be read, or for the failure that stops
 * a list.
 *
 * ext_plugin_store_list reads and verifies every package and returns an array of
 * {id, directory, layer, manifest}, the manifest being plugin.json as an
 * embedded value. It fails with the status of the first entry that is not a
 * package directory and of the first package that does not verify.
 *
 * ext_plugin_store_verify checks every entry and does not stop at a bad one: an array
 * of {id, directory, layer, status}, where status is the ext_plugin_status verifying
 * it gave (0, EXT_PLUGIN_OK, verified; EXT_PLUGIN_INVALID for an entry that is not a package
 * directory). It runs nothing and changes nothing.
 *
 * ext_plugin_store_list_unverified reads only the manifests and checks them against
 * their directory names; no artifact is read or hashed, so THE RESULT IS
 * UNVERIFIED and must not be launched or trusted as it stands. It returns
 * {"packages": [{id, directory, layer, manifest}], "problems": [{id, directory,
 * layer, status}]}; a package that cannot be read does not hide the others. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_list(ext_plugin_store *store, ext_plugin_buffer *packages);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_verify(ext_plugin_store *store, ext_plugin_buffer *checks);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_list_unverified(ext_plugin_store *store,
                                                  ext_plugin_buffer *listing);
/* Entrypoint resolution: pick the executable that serves a contract and a
 * protocol on a platform, for a package the caller has reviewed. It reads and
 * verifies the whole package (as ext_plugin_store_load does), requires that the
 * package serves the exact contract name and version, compares the directory
 * digest of the package with reviewed_digest (lowercase hex, the digest the
 * caller recorded when it reviewed the package) and then selects an entrypoint.
 * No package is trusted merely because it is installed: an empty or changed
 * digest is EXT_PLUGIN_DENIED, and nothing is selected without it.
 *
 * Only entrypoints with runtime "argv" that list protocol are considered. One
 * whose name ends in ".windows", ".linux", ".darwin", ".freebsd" or ".android"
 * is for that platform alone. Its artifact must have no os or arch, or the
 * given ones. The best match scores 2 for an artifact that names an os, 1 for
 * one that names an arch and 2 for an entrypoint name with the platform
 * suffix; the first entrypoint, in manifest order, with the highest score wins.
 * os and arch are Go's runtime.GOOS and runtime.GOARCH names; NULL takes those
 * of the platform the engine was built for. EXT_PLUGIN_UNSUPPORTED means the package
 * does not serve the contract or has no compatible entrypoint.
 *
 * On success *entrypoint is the JSON {"name","artifact","path","digest"}: the
 * entrypoint's name, its artifact's name, the artifact's path (the cleaned
 * package directory joined with the artifact's path; for a tree artifact, the
 * directory) and the package digest. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_store_resolve_entrypoint(
    ext_plugin_store *store, const char *id, const char *contract, const char *version,
    const char *protocol, const char *reviewed_digest, const char *os,
    const char *arch, ext_plugin_buffer *entrypoint);
/* A read-only view over several roots in priority order, highest first, such as
 * a user root before a system root. An id is served from the first root that
 * holds it; a copy in a lower root is shadowed and never read. If the winning
 * copy does not verify, load and list fail instead of falling back to a lower
 * one. The view never writes, so a system root is never created or modified
 * through it; install, upgrade and remove stay with the ext_plugin_store the caller
 * owns. It has no default roots. Roots are compared as cleaned paths: a repeated
 * root, an empty one, or no root at all is EXT_PLUGIN_INVALID. Reports are those of
 * ext_plugin_store_list, ext_plugin_store_verify and ext_plugin_store_list_unverified over all the
 * roots, one line per id from the root that serves it. */
typedef struct ext_plugin_layered ext_plugin_layered;
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_layered_open(const char *const *roots, size_t count,
                                         const ext_plugin_store_limits *limits,
                                         ext_plugin_layered **out);
EXT_PLUGIN_HOST_API void ext_plugin_layered_close(ext_plugin_layered *layered);
/* Read and verify the package from the first root that holds id, returning its
 * plugin.json and, when layer is not NULL, the position of that root.
 * EXT_PLUGIN_NOT_FOUND means no root holds it. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_layered_load(ext_plugin_layered *layered, const char *id,
                                         ext_plugin_buffer *manifest, size_t *layer);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_layered_list(ext_plugin_layered *layered,
                                         ext_plugin_buffer *packages);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_layered_verify(ext_plugin_layered *layered,
                                           ext_plugin_buffer *checks);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_layered_list_unverified(ext_plugin_layered *layered,
                                                    ext_plugin_buffer *listing);
/* Pure engine services: strict bounded JSON input/output, no runtime callbacks,
 * filesystem access, discovery or authorization. Returns an owned JSON value.
 * See services.md for operations and schemas. Free output with ext_plugin_buffer_free.
 */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_call(const char *operation,
                                        const uint8_t *input, size_t input_len,
                                        ext_plugin_buffer *out);
#ifdef __cplusplus
}
#endif
#endif
