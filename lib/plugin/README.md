# Shared C plugin engine (development)

This is Ext's shared plugin host, guest and resource engine under development. It runs
alongside the existing Go library. It is **not a replacement, stable public ABI,
or a complete port of the Go SDK**. Full SDK parity is required before the
common Go implementation is retired; see the [parity checklist](../../docs/plugin-engine-parity.md).
No registry publication is configured.

Plugin and ledger use the [Ext graph model](../../docs/adr-ext-graph.md).
`package.graph` exports resolved package facts as a schema and transaction that
ledger can ingest unchanged. Plugin neither links ledger nor grants authority
through those facts.

The same native engine can be embedded in Go, Rust, Zig and Node.js. Static
linking is the default; a shared library is optional. The
engine itself contains no Go runtime. The guest executable may use any runtime;
the baseline fixture happens to be Go. Ext's existing `ext.plugin/v1` guest
protocol and existing language guest implementations remain unchanged.

## Names

Every public name of this library is in the `ext_plugin_` name space (macros `EXT_PLUGIN_`), and the
side comes next, so the host and the guest never meet and no other library of `lib/` collides with it
(`lib/ledger` is `ext_ledger_`):

| Prefix | Header | What it is |
| --- | --- | --- |
| `ext_plugin_host_*` | [`ext_plugin_host.h`](include/ext_plugin_host.h) | the host ABI: embed the engine, admit a plugin and call it |
| `ext_plugin_store_*` | `ext_plugin_host.h` | the installed package store |
| `ext_plugin_guest_*` (exports) | [`ext_plugin_guest.h`](include/ext_plugin_guest.h) | the guest ABI a plugin built as a C shared library (or a `.wasm`) exports: `abi_version`, `open`, `call`, `close`, and on wasm `alloc` and `free` |
| `ext_plugin_guest_*` (functions) | [`ext_plugin_author.h`](include/ext_plugin_author.h) | the C guest authoring API (`create`, `descriptor`, `invoke`, `emit`, ...) |
| `ext_plugin_instance_*`, `ext_plugin_instances_*`, `ext_plugin_stream_*`, `ext_plugin_streams_*` | [`ext_plugin_instance.h`](include/ext_plugin_instance.h), [`ext_plugin_stream.h`](include/ext_plugin_stream.h) | instance leases and pull streams |
| `ext_plugin_open`, `ext_plugin_call`, `ext_plugin_close`, `ext_plugin_abi_version`, `ext_plugin_free`, `ext_plugin_code_name` | `ext_plugin_host.h` | the library interface: the engine's objects behind four functions, as every library of `lib/` has; code names are identifiers (`not_found`), as the ledger's are; [`interface.json`](interface.json) describes it and `describe` serves it |
| `ext_plugin_services_call`, `ext_plugin_sha256` | `ext_plugin_host.h` | the engine's pure services by name on JSON (also handle 0 of the library interface), and its digest |
| `ext_plugin_buffer`, `ext_plugin_status`, `EXT_PLUGIN_OK` ... | `ext_plugin_host.h` | the shared buffer, status type and status codes |

The libraries are `libext_plugin_host` and `libext_plugin_guest` (static: `ext_plugin_host_static`,
`ext_plugin_guest_static`) and the CMake package is `find_package(ext_plugin)` with the targets
`EXT::host` and `EXT::guest`.

## The library interface

The engine's objects are also reachable through four functions, as every library of `lib/` has, so
a binding binds these and nothing else (ext-go's purego, cgo and ccgo drivers do): `ext_plugin_open`
opens an object from a JSON config and returns a handle, `ext_plugin_call` runs an operation by name
with a JSON object in and a JSON value out, and `ext_plugin_close` frees it. Handle 0 is the library
itself: every service of `ext_plugin_services_call` ([services.md](services.md)), and the digests and
trust records. The objects are hosts (a session with one plugin, over a child process or a shared library), the
package store and the layered view. A host's callbacks (the verify and authorize policies, progress,
the observer and reverse calls) become events in its queue: the binding takes them with `events` and
answers those that ask with `answer`, from another thread than the one waiting in `start` or `call`,
so a binding needs no way to be called from C. A call's own events (its progress) carry the tag the
call was given, so many calls with progress can run at once.

[`interface.json`](interface.json) describes the interface: its objects and their configs, each
operation's input and output as JSON Schema, the events each object raises and how each is answered
(`allow`, `result` or `none`), and the code names. It is the one source of that description: CMake
compiles it into the library, `describe` on handle 0 serves it as it is, and the interface test holds
the two equal. A binding is driven by it (ext-go's `cengine/library.Describe`, whose test calls every
operation it lists and requires an answer) or generated from it, so adding an operation here and in
`src/interface.c` is all a new operation takes. `include/ext_plugin_host.h` documents the same
configs and operations for C readers.

## What is implemented

- Experimental versioned [host embedding API](include/ext_plugin_host.h).
- Linux/macOS JSON-line subprocess backend, owning one direct child per session.
- Immutable descriptor selection, exact handshake matching independent of
  declaration order, request validation, response correlation, domain errors.
- Required verification callback before spawning and authorization callback
  before dispatch. Applications authenticate the executable and choose policy.
- Bounded strict JSON: 24 MiB frames, depth 64, UTF-8, duplicate keys (including
  escaped aliases), unknown envelope fields and exact number text preservation.
- Serialized JSON-line execution; extensions may declare concurrent invocation.
- Backend extension vtable with engine-owned result sinks, close and release.
- Shared-library backend for C ABI guests ([`ext_plugin_guest.h`](include/ext_plugin_guest.h)).
- Lifecycle state and bounded graceful drain; a drain timeout reopens admission.
- Millisecond deadlines, monotonic I/O/queue waits, per-call cancellation and
  original binding context; cancellation after dispatch terminates the session.
- Shared guest dispatch, contract/schema/package/route services and integrity.
- Instance leases and replacement, scoped pull-stream lifecycle and expiry.
- Metadata-only observation and typed Go registry integration.
- C-owned result buffers returned to the caller and freed through the same ABI.
- Bindings in [Go](../../pkg/ext-go/README.md), [Rust](../../pkg/ext-rust/engine/README.md),
  [Zig](../../pkg/ext-zig/engine/README.md) and [Node](../../pkg/ext-ts/engine/README.md).
  They are additive while the existing language SDKs remain the reference.
  Each binding's glue lives with the binding: the Go package carries its own cgo
  header (`ext_go_bridge.h`, header-only, so it has no `.c` files), and this
  directory holds the engine alone.

See the [service and resource APIs](services.md) for the common operations.
The binding examples use permissive fixture policies; applications supply their
own verification and authorization. Rust and Zig expose instance and stream
ownership wrappers as well as raw FFI. Node host/guest callbacks may be async
and have AbortSignal support; integrity and observation are available. Node
resource wrappers remain pending.

## Ownership and threading

1. `ext_plugin_host_create` copies backend configuration and the descriptor. The caller keeps
   policy callback code and user data alive until `ext_plugin_host_destroy` returns.
2. `ext_plugin_host_start` verifies, spawns and handshakes. Start exactly once. A failed
   start closes the session; there is no implicit retry.
3. `ext_plugin_host_invoke` receives borrowed request bytes for the duration of the call
   and returns an owned `ext_plugin_buffer`. Free it with `ext_plugin_buffer_free`, never a
   language allocator. Use the functions from the same engine instance that
   created the handle/buffer, including when multiple addons embed static copies.
   Never exchange these objects across engine copies. Domain errors are valid response envelopes with EXT_PLUGIN_OK.
4. Calls may come from multiple threads; the backend declares whether C serializes
   them. JSON-line is serialized. Queued
   calls count queue time toward their timeout. Admission is not FIFO.
5. `ext_plugin_host_close` is idempotent, concurrent-safe and wakes blocked I/O. After
   all operations return, the owner calls `ext_plugin_host_destroy` exactly once. It
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
needed by the C build or its Rust example. Two pinned MIT-licensed sources are
vendored: the JSON parser (yyjson) and the inflate decoder of miniz, which the
package store uses to read zip and gzip-compressed tar archives; see
[provenance](vendor/README.md).

```sh
cmake -S lib/plugin -B /tmp/ext-cengine -DCMAKE_BUILD_TYPE=Release
cmake --build /tmp/ext-cengine
cmake --install /tmp/ext-cengine --prefix /tmp/ext-host-package
```

The default install contains `libext_plugin_host_static.a`, `libext_plugin_guest_static.a`,
four public headers, versioned CMake package metadata and the Ext, parser and
Unicode-table licenses. Add `-DEXT_BUILD_SHARED=ON` to
also build/install host and guest shared libraries (`.so` / `.dylib`, ABI major 2).
There are no published binary bundles; builds target the local architecture.
Windows support has not been implemented in this subprocess backend.

CMake consumers use:

```cmake
find_package(ext_plugin CONFIG REQUIRED)
target_link_libraries(my_app PRIVATE EXT::host) # static default
# EXT::host_static is explicit; EXT::host_shared exists when installed.
# EXT::guest / guest_static / guest_shared omit host and resource runtimes.
```

The exported targets carry header paths, static API definitions and required
system libraries. The static archive uses position-independent code so it can
be embedded in a Node addon. Manual C consumers define `EXT_PLUGIN_HOST_STATIC` and
link `-lext_plugin_host_static -pthread -lm`, plus `-ldl` on Linux. Parser symbols are privately prefixed
at build time, allowing applications to link their own yyjson version; the inflate
decoder's functions are prefixed the same way, so an application's own miniz or zlib is untouched.

Prebuilt libraries are attached to each `ext-plugin-vX.Y.Z` GitHub release, one archive per
platform (`scripts/ext-plugin-release.sh`, see [module versioning](../../docs/module-versioning.md)):
a host that loads the engine at run time, such as the Python host, needs no C toolchain.

Go integration is opt-in and excluded from ordinary Go builds:

```sh
CGO_LDFLAGS='-L/tmp/ext-cengine' \
  go build -tags ext_cengine ./pkg/ext-go/cgoengine/plugin/examples/host
```

For shared linkage, add the `ext_cengine_shared` build tag and the appropriate
runtime library search path. The Rust example defaults to static linkage and
accepts `--features shared`. Zig and Node builds select the archive or dynamic
library through linker flags; the runner demonstrates both choices.

Static embedding needs no separately deployed Ext library. Node still loads a
`.node` addon, with the C engine compiled into that addon. Go's independent SDK
continues to work without cgo; choosing this C binding requires a C toolchain.
Browsers need a WASM embedding; they cannot load a native `.node` addon.

## Portable guest core

`-DEXT_BUILD_HOST=OFF` builds the guest dispatcher, strict wire parser, pure JSON
services, hashing and cancellation without pthreads, process launch, filesystem
access, instances or streams. Use `EXT::guest` or `-lext_plugin_guest_static`. Native
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

Experimental embedding ABI **2** separates `ext_plugin_host_options` (descriptor and
policy) from `ext_plugin_backend_options` (kind, configuration pointer and size).
`EXT_PLUGIN_BACKEND_JSONLINE_PROCESS` accepts `ext_plugin_jsonline_process_options`.
Unknown kinds return `EXT_PLUGIN_UNSUPPORTED`. `EXT_PLUGIN_BACKEND_EXTENSION` accepts a
`ext_plugin_backend_extension` vtable for application-supplied runtime mechanics.
Rebuild all ABI 1 bindings; guest `ext.plugin/v1` is unchanged.

### Runtime extensions

An extension supplies connect/handshake, invoke, close and release callbacks.
Connect returns a descriptor; invoke returns a response envelope. Both emit
exactly one bounded result into the C engine's copying sink. Callback code and
user state stay alive until release. Close is called once and can run concurrently
with connect/invoke; it must interrupt runtime work. Release runs only after all
host calls have joined. Failed create leaves ownership with the caller.

An extension that also reads the plugin's tagged frames (progress events and
reverse requests) is created with `EXT_PLUGIN_BACKEND_FRAMES` and hands them to the engine
with `ext_plugin_host_backend_progress`, `ext_plugin_host_backend_reverse_admit` and
`ext_plugin_host_backend_reverse_run`: the engine validates, limits, admits and answers as
it does for a process it reads, and the backend keeps its transport and threads.
Without the flag the engine refuses a descriptor that declares progress or reverse
operations, since the backend could not carry them.

### Shared-library guests

`ext_plugin_shared_library_backend_create` turns an absolute path to a C ABI guest (the four
`ext_plugin_guest_*` exports of [`ext_plugin_guest.h`](include/ext_plugin_guest.h), from C, C++, Rust, Zig, Go
`-buildmode=c-shared` or Swift) into an `EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT` backend, so any
language that binds this engine can host one without a loader of its own:

```c
ext_plugin_shared_library_options library = {sizeof(library), "/opt/plugins/echo/1.0.0/libecho.so"};
ext_plugin_backend_extension_context ext;
ext_plugin_shared_library_backend_create(&library, &ext);
ext_plugin_backend_options backend = {sizeof(backend), EXT_PLUGIN_BACKEND_EXTENSION_CONTEXT, &ext, sizeof(ext)};
ext_plugin_host_create(&host_options, &backend, &host);   /* then ext_plugin_host_start, ext_plugin_host_invoke ... */
```

It behaves as the Go loader in `pkg/ext-go/cengine/cguest` does. Nothing is loaded until
connect, which runs after the host's verify policy. Each canonical path is loaded once and
never unloaded. A library whose ABI version is not 1 or that lacks an export is refused.
Calls are serialized, with the ABI's 24 MiB response capacity and no retry. A nonzero ABI
status, an impossible length, or a call that outlives its deadline or cancellation ends the
session. Native code cannot be interrupted, so close only refuses further calls, and the
handle is closed in release. The C ABI has no frames, so a descriptor that declares progress
or reverse operations is refused, as for any extension without `EXT_PLUGIN_BACKEND_FRAMES`.
`tests/shared_library.c` checks it against C guests and, given a path, against the Go-built
guest that `tests/crosslang` runs through the Go loader.

Go consumers use `cgoengine.NewWithBackend` with a reviewed `Connect` factory:

```go
host, err := cgoengine.NewWithBackend(descriptor, cgoengine.BackendOptions{
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
admission. `Host.Close()` aborts. `cgoengine.Open` provides a Session facade;
`NewRegistry`, `GuestFromRegistry` and typed host calls use C schema evaluation.
Complete Go session API parity remains on the checklist.

## Reproduce checks and measurements

From the repository root:

```sh
ZIG_BIN=/path/to/zig \
NODE_INCLUDE_DIR=/path/to/node/include/node \
EXT_CENGINE_BUILD_DIR=/tmp/ext-cengine-proof \
  scripts/plugin-cengine.sh
```

Set `EXT_CENGINE_LINKAGE=shared` to exercise the same host examples and Go
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
command guests, set `EXT_CENGINE_RUST_GUEST` and `EXT_CENGINE_ZIG_GUEST` to those
absolute paths. These artifacts can be built from `pkg/ext-rust` and `pkg/ext-zig`.

Use `EXT_CROSSLANG_ENGINE=c` with `scripts/plugin-crosslang.sh` for Rust/Zig
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

Manifest validation and `package.resolve` are bounded by the input size: duplicate names and
paths and tree overlap are found by sorting, providers by a hash index, and one directory digest
stops at 250,000 files or 64 MiB of path text. Before these bounds a 4 MB manifest of 4096
artifacts could take minutes to validate (`services.md` has the details). `tests/limits.c` holds
verdicts to a naive reference and CPU time to a bound at the documented limits; `tests/fuzz.c` is a
libFuzzer target (`-DEXT_FUZZ=ON`, clang) for the parsers and validators, run with a per-input
`-timeout` because sanitizers do not see an input that is merely slow.

The parser and output copy allocate native memory; the frame limit is not a
whole-process memory limit. The measured Go allocation count excludes that C
memory. Production hardening would require allocation-failure testing, longer
fuzzing campaigns, resource accounting, API compatibility review and broader platform/workload checks.
