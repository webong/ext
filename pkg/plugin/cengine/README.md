# Shared C plugin engine (development)

This is the shared CTX host, guest and resource engine under development. It runs
alongside the existing Go library. It is **not a replacement, stable public ABI,
or a complete port of the Go SDK**. Full SDK parity is required before the
common Go implementation is retired; see the [parity checklist](../../../docs/plugin-engine-parity.md).
No registry publication is configured.

The same native engine can be embedded in Go, Rust, Zig and Node.js. Static
linking is the default; a shared library is optional. The
engine itself contains no Go runtime. The guest executable may use any runtime;
the baseline fixture happens to be Go. CTX's existing `ctx.plugin/v1` guest
protocol and existing language guest implementations remain unchanged.

## What is implemented

- Experimental versioned [host embedding API](include/ctx_host.h).
- Linux/macOS JSON-line subprocess backend, owning one direct child per session.
- Immutable descriptor selection, exact handshake matching independent of
  declaration order, request validation, response correlation, domain errors.
- Required verification callback before spawning and authorization callback
  before dispatch. Applications authenticate the executable and choose policy.
- Bounded strict JSON: 24 MiB frames, depth 64, UTF-8, duplicate keys (including
  escaped aliases), unknown envelope fields and exact number text preservation.
- Serialized JSON-line execution; extensions may declare concurrent invocation.
- Backend extension vtable with engine-owned result sinks, close and release.
- Lifecycle state and bounded graceful drain; a drain timeout reopens admission.
- Millisecond deadlines, monotonic I/O/queue waits, per-call cancellation and
  original binding context; cancellation after dispatch terminates the session.
- Shared guest dispatch, contract/schema/package/route services and integrity.
- Instance leases and replacement, scoped pull-stream lifecycle and expiry.
- Metadata-only observation and typed Go registry integration.
- C-owned result buffers returned to the caller and freed through the same ABI.
- Bindings in [Go](../../go/README.md), [Rust](../../rust/engine/README.md),
  [Zig](../../zig/engine/README.md) and [Node](../../typescript/engine/README.md).
  They are additive while the existing language SDKs remain the reference.

See the [service and resource APIs](services.md) for the common operations.
The binding examples use permissive fixture policies; applications supply their
own verification and authorization. Rust and Zig expose instance and stream
ownership wrappers as well as raw FFI. Node host/guest callbacks may be async
and have AbortSignal support; integrity and observation are available. Node
resource wrappers remain pending.

## Ownership and threading

1. `ctx_host_create` copies backend configuration and the descriptor. The caller keeps
   policy callback code and user data alive until `ctx_host_destroy` returns.
2. `ctx_host_start` verifies, spawns and handshakes. Start exactly once. A failed
   start closes the session; there is no implicit retry.
3. `ctx_host_invoke` receives borrowed request bytes for the duration of the call
   and returns an owned `ctx_buffer`. Free it with `ctx_buffer_free`, never a
   language allocator. Use the functions from the same engine instance that
   created the handle/buffer, including when multiple addons embed static copies.
   Never exchange these objects across engine copies. Domain errors are valid response envelopes with CTX_OK.
4. Calls may come from multiple threads; the backend declares whether C serializes
   them. JSON-line is serialized. Queued
   calls count queue time toward their timeout. Admission is not FIFO.
5. `ctx_host_close` is idempotent, concurrent-safe and wakes blocked I/O. After
   all operations return, the owner calls `ctx_host_destroy` exactly once. It
   reaps the child and frees the handle. No calls may race with destruction.

Policies run synchronously on the calling thread and must return promptly.
They must not retain borrowed JSON, reenter the same host, or unwind/throw across
C. Cancellation cannot forcibly interrupt a policy callback or `posix_spawn`.
The Go wrapper joins its cancellation callback before allowing handle disposal.

The input request supplies its identity, contract, operation, ID and absolute
RFC3339 deadline. The engine validates them against the selection. The smaller
of the request deadline and the caller's 1..UINT32_MAX millisecond timeout bounds dispatch.
Local validation/policy rejection does not terminate a healthy C session;
dispatched transport failure does. Canceling an admitted call that is still
queued does not abort a different running call. Wire deadlines with fractional
milliseconds are rounded up; Go also enforces their original precision.

## Build and consume

Prerequisites: C11 compiler, CMake, pthreads/POSIX, Go with cgo, Rust/Cargo, Zig
0.17.0, Python 3, Node.js with matching Node-API headers. No network fetches are
needed by the C build or its Rust example. The pinned MIT-licensed JSON parser
is vendored; see [provenance](vendor/README.md).

```sh
cmake -S pkg/plugin/cengine -B /tmp/ctx-cengine -DCMAKE_BUILD_TYPE=Release
cmake --build /tmp/ctx-cengine
cmake --install /tmp/ctx-cengine --prefix /tmp/ctx-host-package
```

The default install contains `libctx_host_static.a`, `libctx_guest_static.a`,
four public headers, versioned CMake package metadata and the CTX, parser and
Unicode-table licenses. Add `-DCTX_BUILD_SHARED=ON` to
also build/install host and guest shared libraries (`.so` / `.dylib`, ABI major 2).
There are no published binary bundles; builds target the local architecture.
Windows support has not been implemented in this subprocess backend.

CMake consumers use:

```cmake
find_package(ctx_host CONFIG REQUIRED)
target_link_libraries(my_app PRIVATE CTX::host) # static default
# CTX::host_static is explicit; CTX::host_shared exists when installed.
# CTX::guest / guest_static / guest_shared omit host and resource runtimes.
```

The exported targets carry header paths, static API definitions and required
system libraries. The static archive uses position-independent code so it can
be embedded in a Node addon. Manual C consumers define `CTX_HOST_STATIC` and
link `-lctx_host_static -pthread -lm`. Parser symbols are privately prefixed
at build time, allowing applications to link their own yyjson version.

Go integration is opt-in and excluded from ordinary Go builds:

```sh
CGO_LDFLAGS='-L/tmp/ctx-cengine' \
  go build -tags ctx_cengine ./pkg/go/examples/host
```

For shared linkage, add the `ctx_cengine_shared` build tag and the appropriate
runtime library search path. The Rust example defaults to static linkage and
accepts `--features shared`. Zig and Node builds select the archive or dynamic
library through linker flags; the runner demonstrates both choices.

Static embedding needs no separately deployed CTX library. Node still loads a
`.node` addon, with the C engine compiled into that addon. Go's independent SDK
continues to work without cgo; choosing this C binding requires a C toolchain.
Browsers need a WASM embedding; they cannot load a native `.node` addon.

## Portable guest core

`-DCTX_BUILD_HOST=OFF` builds the guest dispatcher, strict wire parser, pure JSON
services, hashing and cancellation without pthreads, process launch, filesystem
access, instances or streams. Use `CTX::guest` or `-lctx_guest_static`. Native
builds can also select that smaller library independently of the host target.

The checked-in `cmake/wasi-zig.cmake` toolchain builds WASI Preview 1 with Zig
0.17.0, including the correct Wasm archiver. `scripts/plugin-wasi-core.sh` builds
and executes both a C guest and the Rust `guest-only` binding in wazero. It needs
Zig, Go and Rust's `wasm32-wasip1` target. Guests still own their transport loop;
using the public wire contract requires no engine linkage. Native Windows host
support remains separate unfinished work.

## Embedding and execution are separate choices

Embedding chooses static or shared linkage. Backend configuration chooses how
plugins execute. Static engine linkage does not make a subprocess guest built-in.
The built-in C backend remains JSON-line subprocess; a reviewed bridge can expose
HashiCorp behind that same contract. An engine service is deferred.

Experimental embedding ABI **2** separates `ctx_host_options` (descriptor and
policy) from `ctx_backend_options` (kind, configuration pointer and size).
`CTX_BACKEND_JSONLINE_PROCESS` accepts `ctx_jsonline_process_options`.
Unknown kinds return `CTX_UNSUPPORTED`. `CTX_BACKEND_EXTENSION` accepts a
`ctx_backend_extension` vtable for application-supplied runtime mechanics.
Rebuild all ABI 1 bindings; guest `ctx.plugin/v1` is unchanged.

### Runtime extensions

An extension supplies connect/handshake, invoke, close and release callbacks.
Connect returns a descriptor; invoke returns a response envelope. Both emit
exactly one bounded result into the C engine's copying sink. Callback code and
user state stay alive until release. Close is called once and can run concurrently
with connect/invoke; it must interrupt runtime work. Release runs only after all
host calls have joined. Failed create leaves ownership with the caller.

Go consumers use `goengine.NewWithBackend` with a reviewed `Connect` factory:

```go
host, err := goengine.NewWithBackend(descriptor, goengine.BackendOptions{
    Concurrent: true,
    Connect: func(ctx context.Context, selected plugin.Descriptor) (plugin.Backend, error) {
        return hashicorp.ConnectProcess(ctx, reviewedProcess)
    },
}, verifySelection, authorizeRequest)
```

The policies take validated JSON bytes. The factory runs after verification,
and C checks its handshake against the selection. The same boundary accepts
other Go `plugin.Backend` implementations without nesting a Go `plugin.Session`.
This provides backend reachability, not a claim that those runtimes are ported
into C. Backend contexts preserve the Go caller's values, bounded by per-call deadlines
and backend lifetime cancellation. Local Go error causes remain available to
the calling Go application; only status codes cross the C ABI.

`Host.State()` exposes C-owned lifecycle state. `Host.Drain(duration)` and
`Host.DrainContext(ctx)` wait for admitted calls; timeout or cancellation reopens
admission. `Host.Close()` aborts. `goengine.Open` provides a Session facade;
`NewRegistry`, `GuestFromRegistry` and typed host calls use C schema evaluation.
Complete Go session API parity remains on the checklist.

## Reproduce checks and measurements

From the repository root:

```sh
ZIG_BIN=/path/to/zig \
NODE_INCLUDE_DIR=/path/to/node/include/node \
CTX_CENGINE_BUILD_DIR=/tmp/ctx-cengine-proof \
  scripts/plugin-cengine.sh
```

Set `CTX_CENGINE_LINKAGE=shared` to exercise the same host examples and Go
conformance harness through the optional shared library. Use separate output
directories for each run. Native sanitizer checks use the default static target.

The runner retains binaries, sanitizer builds, JSON results and raw benchmarks
in the output directory. It checks:

- Existing `plugintest.Run` backend conformance through the Go wrapper.
- Direct C admission, policy denial, handshake mismatch and cleanup behavior.
- Shared invalid-JSON fixtures, malformed/oversized/truncated guest replies,
  response correlation, fragmented frames and stalled/exiting guests.
- Go race detection for wrapper lifecycle and callbacks.
- C address/undefined-behavior sanitizers and a separate thread-sanitizer build.
- Go/Rust/Zig/Node hosts calling the same engine; Node event-loop responsiveness.
- Rust/Zig resource ownership, Node integrity/observation and installed CMake targets.
- End-to-end startup, latency, throughput and host peak RSS against the existing
  Go JSON-line host. Three fresh-process repetitions, alternating engine order.

To also run the C engine's Go conformance harness against prebuilt Rust and Zig
command guests, set `CTX_CENGINE_RUST_GUEST` and `CTX_CENGINE_ZIG_GUEST` to those
absolute paths. These artifacts can be built from `pkg/rust` and `pkg/zig`.

Use `CTX_CROSSLANG_ENGINE=c` with `scripts/plugin-crosslang.sh` for Rust/Zig
JSON-line, C ABI and WASI guests and a native Go guest through C-owned sessions.
The runtime integration remains in Go; this does not port those runtimes to C.

Earlier measurements: [prototype evaluation](evaluation.md). Tests establish
these scenarios, not exhaustive wire compatibility or memory safety.

## Explicit scope limits

The built-in process backend accepts an absolute executable and explicit, copied
argument/environment arrays. The default environment is empty; stderr is inherited. Applications must use reviewed
immutable artifacts and ensure unrelated inherited file descriptors have
close-on-exec set. The engine owns/reaps its direct child; embedding applications
must not reap that PID independently. There is no process-tree containment or
OS sandbox. Artifact discovery, signature verification mechanisms and deployment
remain application/library-layer responsibilities, not hardcoded product rules.

WASI, HashiCorp, native Go and C shared-library runtimes can be supplied through
the Go backend extension; they are **not ported into C**. The C engine now owns schema, package, instance, stream, observation and guest
mechanics. Complete foreign wrappers, Windows support, error/observer edge-case
parity and the SDK migration remain required work in the checklist above. `Close` is abortive; `Drain`
provides bounded graceful shutdown.

The parser and output copy allocate native memory; the frame limit is not a
whole-process memory limit. The measured Go allocation count excludes that C
memory. Production hardening would require allocation-failure/fuzz testing,
resource accounting, API compatibility review and broader platform/workload checks.
