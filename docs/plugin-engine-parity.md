# Shared plugin engine parity

## One Go API, two builds

`pkg/plugin-go` is the single Go entry point. With the `ext_cengine` tag and cgo
it runs on the C engine; in any other build (no cgo, no tag, an unsupported
platform) the same API is served by the pure-Go implementation in the same module (`contract`, `author`, `process` and the packages around them),
so `CGO_ENABLED=0` programs keep building. `goengine.CEngine` reports which one
a build is. The data model (descriptors, requests, responses, errors, backends)
is defined in `pkg/plugin-go/contract` and re-exported by the `pkg/plugin-go` root. The common API is `Open`,
`OpenProcess`, `Session`, `NewGuest`, `Guest`, `NewRegistry`, `GuestFromRegistry`,
the manifest, route and schema functions, the digests, and the `store` and
`host` packages. Engine-only APIs (`Host`, `Streams`, `Instances`, `NewProcess`)
need the C engine build. Both builds are tested in CI.

## Required destination

CTX is in development. The shared engine must reach the current Go SDK's
behavioral and supported-platform coverage before Go's common implementation is
retired. Parity is a requirement, not an optional follow-up. ABI and internal API
changes are acceptable during this work; every language binding must be updated
together. The plugin wire contract remains independently versioned.

The destination is one implementation of common engine behavior with idiomatic
language bindings. Runtime backends may use the runtime that owns their mechanism:
HashiCorp and native Go remain Go integrations. A non-Go host reaches those through
an explicit bridge or runtime module, not by silently embedding Go into the C
core. Guests may implement the public wire contract without linking any engine.

## Definition of parity

Parity means matching observable behavior and failure semantics, not merely
successfully calling a guest. A callable Go backend behind C is runtime reachability;
it does not establish portable C implementation or complete SDK parity.

The existing Go implementation and its conformance cases are the baseline. Every
row below needs shared evidence across relevant linkage modes and language
bindings before it can be marked complete. Nothing in this document declares
full parity achieved.

| Area / Go source | Current shared-engine status | Remaining acceptance work |
| --- | --- | --- |
| Contract validation (`contract/contract.go`) | Shared descriptor/request/response validation, matching, selection and requirement APIs; a generated one-position mutation matrix (about 1,500 descriptors, requests and responses) agrees with Go | Language defaults; `TestPairwiseMutationDifferential` also changes two independent fields at once (20,000 seeded pairs by default, 500,000 with `EXT_PAIR_SCALE=25`, none diverging); mutations of three or more fields |
| Compatibility (`contract/compatibility.go`) | Shared exact protocol negotiation and route/profile accounting | Cross-language parity fixtures and release versioning |
| Host session (`contract/session.go`) | C-generated calls/IDs, verification, contextual authorization, cancellation, immutable matching, state, queued admission, abort and drain; Go session facade | Binding semantics audit; `TestSessionErrorCausesMatchGo`, `TestOpenErrorCausesMatchGo` and `TestDrainErrorCausesMatchGo` compare error classes and final state with the Go session |
| Reverse calls, progress and data endpoints (`contract/reverse.go`, `contract/reverse_session.go`, `contract/transport/jsonline`, `contract/endpoint`; [ADR](adr-plugin-reverse-progress-endpoints.md)) | **All five stages of the C engine are done, and extension backends carry the frames too: declarations, progress frames, call-scoped and session-scoped reverse calls, the endpoint validator, and the hand-over of frames by a backend that owns its transport.** The pure-Go baseline and the pure-Go build of `cengine` implement everything. *Stage 1:* the C engine validates and matches a descriptor's `reverse` list and each operation's `progress` flag, and accepts a request's optional `progress` rate and `parent`, exactly as Go does (`TestFramesDescriptorDifferential`, `TestFramesRequestDifferential`, `TestFramesMatchDifferential`). *Stage 2:* a host that owns the child's pipes (the process backends, which `OpenProcess` and `cengine/host` use) validates each `progress` frame, applies the shared token-bucket limit (`src/progress.c`), folds markers, and delivers events through `ext_host_call_progress` with a final marker before the call returns; anything else a plugin sends fails the session with `EXT_PROTOCOL` (Go's `ErrProtocol`). *Stage 3:* `reverse_request` frames are admitted in Go's order and answered with Go's codes (`src/reverse.c`, `admit_reverse` in `host.c`): no services configured is `reverse_unavailable` whatever the request names; otherwise a parent that is not the live call fails the session, then `reverse_undeclared`, `reverse_unavailable` before the handshake matched, `too_large`, `expired`, `busy`. The engine owns those checks, the deadline chain (the earliest of the plugin's deadline, the call's and the services' bound) and the shape of every answer; what it leaves to the host is policy, through the `authorize` and `handle` hooks of `ext_host_set_services`, which receive the `Request` as JSON and the options of the request. *Stage 4:* a plugin whose descriptor lists reverse operations gets a reader thread once the handshake matched (a plugin that lists none keeps the single-threaded loop of stage 2), so it can speak when no call is in flight: a request with no parent is session scoped and is served with `services.session_value` as the options' value (the Go facade passes the context the session was opened with, without its cancellation, so its values carry through). Each admitted request runs on a worker thread, up to `max_in_flight` at a time (`busy` beyond that), with an engine-owned cancel signal that is set when its call ends, when the caller cancels, or when the host closes; the Go facade turns it into the handler's `context` cancellation. A call does not return until the workers of its call-scoped requests have, so the options they borrow stay valid. A violation or a lost connection fails the session: the call in flight reports why, and a later call finds the host closed, as the Go client's connection behaves. Writes are one frame at a time under a lock. `Options.Reverse` is honored by `OpenProcess` and `cengine/host`. *Stage 5:* the endpoint rules (`contract/endpoint` Decode, Check and Validate) are the engine services `endpoint.check` and `endpoint.validate` (`src/endpoint.c`): strict decoding, the scheme, address (the rules of `net.SplitHostPort` and `net.ParseIP`, loopback only), path, token and expiry rules in Go's order, and the policy (admitted schemes, lifetime, an exact clock), answered with `EXT_INVALID`, `EXT_DENIED` or the new `EXT_EXPIRED` and never with text, so no part of an endpoint reaches an error. The grant itself (a lifetime tied to a session, one-time tokens, dialing) stays in Go. `testdata/v1/endpoint-cases.json` is a language-neutral fixture of 213 outcomes that both implementations reproduce (`TestEndpointFixture`, `lib/plugin/tests/endpoint.c`); `TestEndpointMutationDifferential` (647 one-field mutations), `TestEndpointAddressDifferential` (60,880 address spellings), `TestEndpointTimeDifferential` (360 expiries to the nanosecond, in other zones, at both edges of the lifetime) and `TestEndpointErrorsCarryNoValue` compare them further. The engine's time parser no longer uses the platform's `timegm` (it fails for year 1 and `_mkgmtime` stops at 3000), and refuses the zero time in any zone. *Stage 6:* an extension backend (`Open` with `Connect`: in-process, JSON-line client, HashiCorp, WASI, native Go) hands the engine its frames when it is created with `EXT_BACKEND_FRAMES`, which `NewWithBackend` always sets. `ext_host_backend_progress` takes a progress frame; `ext_host_backend_reverse_admit` takes a reverse request and either refuses it with the stable code or returns a ticket that `ext_host_backend_reverse_run` executes on any thread, optionally ended early by a signal of the backend's own (the plugin's context, for a plugin in the host's process). Validation, limits, the admission order, the deadline chain, the shape of every answer and cancellation (when the call ends or the host closes) are the engine's, as for a process it reads; a call is registered while its invoke callback runs, so frames find it by id and the call waits for the hooks of its requests. A violation returns `EXT_PROTOCOL` and the backend fails its connection. The Go facade (`backend_frames.go`) supplies the progress sink (which tells a plugin in the host's process when the limit dropped an event), the `plugin.ReverseHost` that a `plugin.ReverseBackend` is given, and the callers an in-process plugin finds with `plugin.ReverseFrom` and `plugin.SessionReverseFrom`; `Close` waits for frames in flight before the host can be destroyed. `Open` honors `Options.Reverse`. Evidence: `lib/plugin/tests/backend.c` (progress delivery, limits and the delivered flag, concurrent calls each getting only their own events, every refusal and answer shape, the in-flight bound, the backend's own cancel signal, cancellation when a call ends and when the host closes, tickets of another host, each framing violation, and the flag and API checks) under the address, undefined-behavior and thread sanitizers; `TestBackendReverseCallsMatchGo` and `TestInProcessReverseCallsMatchGo` (the 30 reverse cases through the Go session and the engine over a JSON-line backend and over an in-process plugin), `TestBackendProgressMatchesGo`, `TestBackendProgressViolationsMatchGo`, `TestInProcessProgressReportsDrops`, `TestInProcessReverseEndsWithThePluginsContext`, `TestBackendReverseAfterCloseIsRefused`, and the session-scoped, in-flight, cancellation and idle scenarios, which now run over three backends. *Bindings:* the Rust, Zig and Node host bindings expose the three new capabilities (`call_progress` with a per-call callback, services with `authorize` and `handle` hooks, and the endpoint validator) and map `EXT_PROTOCOL` and `EXT_EXPIRED`; the Swift and Java SDKs bind the endpoint validator. Each runs the 213 outcomes of `endpoint-cases.json`, and the three host bindings run the engine's scripted test guest (`ext_reverse_test --guest <mode>`, whose `--descriptor` prints the descriptor it serves): `pkg/plugin-rust/engine/tests/frames.rs`, `pkg/plugin-ts/engine/test.mjs` and the tests at the end of `pkg/plugin-zig/engine/src/root.zig`; `scripts/plugin-cengine.sh` points them at it. The iOS xcframework scripts listed the engine's sources by hand and still omitted `progress.c`, `reverse.c` and `endpoint.c`, which `services.c` needs; they are fixed. Evidence for stages 1 to 5: `lib/plugin/tests/reverse.c` (validator, answers, configuration, about 50 scripted-guest cases, and the thread scenarios: a request when idle, six at once against four slots, cancellation when a call ends and when the host closes, a violation and an exit between calls) and `frames.c`, repeated under the address, undefined-behavior and thread sanitizers; `TestFrameValidationDifferential` (1,070 one-field mutations of progress and reverse request frames, no divergence); `TestReverseCallsMatchGo` (30 cases through the Go session and the C engine, comparing what the guest got, how the call ended, the final state and everything the policy was shown), `TestSessionScopedReverseMatchesGo`, `TestReverseInFlightBoundMatchesGo`, `TestReverseCancellationMatchesGo`, `TestIdleBehaviorMatchesGo`, `TestReverseRequestSeenByServices`, `TestReverseOptionsMatchGo`; `TestRetryHintBoundMatchesGo` (the engine accepted any retry hint an int64 holds; Go allows a year) | A request that a backend admits and never runs holds the host open, where Go's session only leaves its count raised; HashiCorp, native Go, the C ABI and the WebAssembly reactor never carry a frame, as under the Go session (a WebAssembly command speaks JSON lines through the same client as a process, so it does). The engine reads times as RFC 3339 and nothing else, where Go also accepts an offset of 24 hours or 60 minutes, a comma before the fraction and year 0000 (`TestEndpointTimeSpellingsAreStricterInTheEngine`). A plugin that declares no reverse operations is read only while a call is in flight, so a request it makes between calls is not answered until the next call (a Go guest cannot make one). The payload bound is measured on the compact form of the payload, not the bytes the plugin sent. A frame that follows the response fails the call that read it in C when the plugin has no reader thread, where Go fails the session afterwards. After a failure between calls the host is already failed, where Go's session stays ready until a call observes it. Progress callbacks run on an engine thread for a plugin with a reader thread. The guest-side halves of progress and reverse calls in the guest SDKs (planned in [plugin-guest-frames.md](plugin-guest-frames.md); Swift has no JSON-line guest): a guest that speaks the JSON-line wire in its own process can report progress and call its host. The Rust SDK does (a reader thread, or the waiting thread reading inline, as a WASI command must), and passes the shared fixtures and the frames suite through the Go session and the C engine in all three forms; so does the Java SDK (`JsonLine` reads on a thread of its own and the JNI binding passes the connection to the handler as the engine's `call_user`; `Call` gets `progress`, `progressLimits`, `reverse` and `sessionReverse`; it reproduces the fixtures with no exceptions, and passes the frames suite through the Go session and the C engine, over a Go client and with the engine owning the process), the Python SDK (inline: the thread that waits for the host's answer reads the connection, so one reverse call at a time and no session-scoped caller, and the operations that need a reader of its own answer `unsupported`; it reproduces the fixtures with no exceptions and passes the frames suite through the Go session and the C engine), and the TypeScript SDK (`serveGuest` keeps reading while the handler runs, so reverse calls overlap and a session-scoped caller outlives its call; it passes the fixtures with the listed exceptions that follow from JavaScript numbers, and the frames suite through the Go session and the C engine, over a Go client and with the engine owning the process), which also had to accept the bare hello the engine sends, with no plugin or contract member, as the Go and Rust guests do; the Zig SDK has it too, in inline mode like Python (`frames.zig`, `Context.progress` and `Context.reverse`, `wire.requestFromValue`): it reproduces the fixtures with no exceptions and passes the frames suite natively through the Go session and the C engine and as a WASI command through the Go session; the C ABI guests (`ext_plugin_*`) and WebAssembly reactors have no frame channel for them. The Swift and Java SDKs bind only the endpoint validator, since their hosts run reactors, which never send a frame. The Zig tests of the engine binding were written without a Zig toolchain and have only run in CI |
| Observability (`contract/observe.go`) | C metadata-only events with Go/Rust/Zig hooks and bounded asynchronous Node observation | Match all stage/error semantics; document Node delivery limits |
| Guest engine (`contract/guest.go`) | C immutable dispatcher, deadlines, owned buffers and error sanitization; Go/Rust/Zig/Node bindings; a request matrix (27 one-field mutations, each with live and cancelled contexts) compares the C-backed and Go guests, including identical `invalid_request` responses (the Go and Node facades map `EXT_INVALID` to that response) | Rust and Zig expose raw bytes: a request with an unusable ID or unparseable JSON is a failed call (`EXT_INVALID`) there, not an `invalid_request` response as in Go and Node; decide whether the C engine should answer those itself |
| Typed authoring (`pkg/plugin-go/contract/guest/author`) | Go registry construction, guest snapshots and typed host calls can use C schema evaluation; Rust/Zig/Node accept native handlers | Zig typed capability contracts; final default SDK integration. Rust (`capability`, with `capability::stream`), TypeScript (`healthMethod`, `configurationMethod`, `openStream`) and Python (`ext_plugin.capability`, with `Router`) now define the health, configuration and stream contracts with the Go library's wire shapes and limits, each pinned by tests |
| Schema (`pkg/plugin-go/contract/validate/schema`) | Shared bounded schema validation and checks; a generated matrix of 30 schemas by about 90 raw values (2,037 pairs, including numbers, nulls, Unicode, surrogate escapes and malformed JSON) agrees with Go | Multi-keyword generated schemas |
| Streams and capabilities (`pkg/plugin-go/contract/guest/stream`, `pkg/plugin-go/contract/guest/capability`) | C scoped pull-stream lifecycle, capacity, sequence, bounded batches, expiry, close/release; Go integration; Node bindings | `TestStreamErrorCausesMatchGo` compares 17 scripted-reader scenarios (sequence, limits, capacity, reader errors, oversize and invalid batches, scope, expiry, close-while-reading, concurrent reads) with the Go service; `TestCapabilityBehaviorMatchesGo` runs the configuration and health helpers over the Go and the C-backed registry, instances and session and compares every outcome |
| Instances (`pkg/plugin-go/contract/guest/instance`) | C revisions, configuration digests, capacity, leases, replacement, factory lifetime and draining cleanup; Go integration | `TestInstanceErrorCausesMatchGo` compares error classes for validate, create, capacity, disposal on release, remove, replace and close, and cancelled contexts; a disposer's error now reaches the call that retired the instance, as in Go |
| Packages and trust (`pkg/plugin-go/contract/package`, `contract/integrity.go`) | Shared manifest/entrypoint/dependency services, SHA-256, POSIX artifact verification, directory digest and per-artifact digests (`ext_artifact_digest`, Go `ArtifactDigest`); the manifest's optional `license`, namespaced `metadata` (sizes are weights, independent of source spelling) and tree artifacts (`kind: "tree"`, overlap rules, directory-digest verification) are validated and verified in C; differential tests (`TestManifestExtensionsDifferential`, `TestTreeIntegrityDifferential`) compare about 70 manifests and tree layouts against Go | The package store is rebuilt in `pkg/plugin-go/cengine/host/store` over the engine's validation and digests (install, upgrade, remove, list, verify, layered roots), with a differential test against `pkg/plugin-go/cengine/host/store`; the store logic itself is Go in the binding, not C, so Rust, Zig and Node have no store; Unicode-version compatibility; graph binding coverage; the same pairwise matrix covers manifests (`TestPairwiseMutationDifferential`) |
| Interoperability (`pkg/plugin-go/contract/host/interop`, `pkg/plugin-go/contract/transport/bridge`) | C route resolver and production JSON-line/HashiCorp frontends in both directions, tested with C-owned sessions | Windows bridge supervisor and release packaging |
| JSON-line process | C absolute-path launch, copied argument/environment configuration, direct-child cleanup; a launch backend (kind 5) with a working directory, bounded stderr capture and a scrubbed `PATH` base environment, compared with `pkg/plugin-go/contract/host/process` by `TestLaunchMatchesGoProcess` and covered by the native tests under the address, UB and thread sanitizers; `pkg/plugin-go/cengine/host` opens an installed package over it | the Windows launch-path rule: the C engine refuses `.bat`/`.cmd` (they run through `cmd.exe`, whose argument parsing is an injection hazard) and accepts only `.exe` and `.com`, which is stricter than Go's `process` package and pinned by `TestBatchFilesAreNotLaunched`; `TestDestroyIgnoresGrandchildHoldingStderr` shows destroy does not wait for a grandchild that holds stderr open (like Go's `process`, only the direct child is killed) |
| In-process, HashiCorp, WASI, native Go, C ABI | C-session conformance with Rust/Zig C ABI and WASI guests, native Go guest, and both HashiCorp transports; direct process engine | Remaining platform combinations; portable runtime modules |
| Static/shared distribution | Static/shared host and guest libraries, exact CMake package version, external install-consumer checks and macOS sanitizers | Execute CI on all platforms; reproducible release packages |
| Go/Rust/Zig/JS bindings | Go session/resources; Rust host/guest/lease/stream ownership; Zig resource wrappers and generated FFI; async Node host/guest/integrity/observation and instance/stream managers | Zig has no typed `ext.health`/`ext.configuration`/`ext.stream` definitions yet (no Zig toolchain was available to build them); replace independent SDK implementations Package validation, resolution, selection and route resolution in the Rust, Zig and Node bindings all call the C engine (`ext_engine_call`), so they already accept the manifest's `license`, `metadata` and tree artifacts; the Python guest reads only a package's `descriptor` key and ignores other fields on purpose, since a guest needs nothing else. |
| Platforms | Native C host runs on Linux, macOS and Windows (MinGW-w64: `channel_win32.c` with CreateProcess, pipes and a Job Object, `fs_win32.c` refusing symbolic links and junctions and verifying each opened file's final path, `random_win32.c`); the Plugin engine workflow builds it and runs the native tests and the C-backed Go suites on all three, and experimental jobs do the same on Windows ARM64 with clang and build the engine with MSVC (`src/sync.h` supplies the pthread subset over Win32; native tests only, since cgo links the MinGW build), including the host, store and tree integrity over the Windows implementation; portable C and Rust guest-only builds execute in WASI | Windows: a fully handle-relative filesystem walk (files are now opened and their final path checked against the root's canonical path, which refuses a directory swapped for a junction after the check, but enumeration is still path-based), the thread sanitizer (`TestLargeFrameRoundTrip` moves an 8 MiB frame both ways on every platform and `TestDestroyIgnoresGrandchildHoldingStderr` runs on Windows too); all supported Go-backend/platform combinations |

## Implementation order

1. Backend extension boundary and C-owned lifecycle; reuse existing Go runtimes
   through this boundary while implementing shared mechanics.
2. Complete host contract, cancellation, selection, requirements and observation.
3. Portable guest engine, typed language authoring and shared schema semantics.
4. Stream/capability, instance, package and route APIs.
5. Complete backend/platform coverage and language bindings.
6. Run differential conformance, lifecycle/race/sanitizer and representative
   performance checks. Switch public Go APIs to bindings, then delete duplicated
   mechanics once the evidence covers their replacement.

Do not remove working Go paths or claim the C engine is the default SDK before
these requirements are met. Static linkage is already the default *distribution
choice for consumers of the C engine*; it is not a completed SDK migration.

## Evidence for the current increment

The C-backed Go session runs the reference session tests. Contract/schema,
package/route and filesystem checks compare results against Go. The reference
instance and stream tests run against C lifecycle implementations. Additional
checks cover queued cancellation/drain, concurrent factories, retained leases,
stream expiry, scoped reads and once-only cleanup.

The static and shared macOS builds have exercised Go race detection, C
address/undefined-behavior sanitizers and a separate C thread-sanitizer build.
The same engine has been called from Go, Rust, Zig and Node. New binding checks
cover Rust services/cancellation/guests and resource leases, Zig generated host/guest
and resource APIs, and Node async handlers, policy, cancellation, late promises,
integrity, observation and cleanup. A deadline-rounding regression has repeated
Go race checks. Go's decoder now rejects unpaired surrogate escapes, as the C parser does, instead of silently substituting U+FFFD. Raw optional-null and numeric schema cases compare against Go, as do the generated schema and contract mutation matrices (`schema_matrix_test.go`, `contract_matrix_test.go`). Node instance and stream managers pass their lifecycle suite; the full Node suite (11 tests, three runs) also ran clean under AddressSanitizer and UndefinedBehaviorSanitizer on macOS. Preload the sanitizer runtime into a re-signed copy of `node` and run it directly: wrapping it in `timeout` makes the runtime abort with an interceptor error, which is a harness artifact, not an engine defect.

The production bridge passed both directions with gRPC and net/rpc under Go and
C-backed sessions. The C-session runtime matrix passed Rust/Zig JSON-line, native
C ABI and WASI guest conformance and native Go plugin conformance. The portable
guest library and its Rust guest-only binding also execute as WASI modules.
External CMake consumers load the installed static/shared host and guest targets.

## Building the C engine locally

The `ext_cengine` build tag links `pkg/plugin-go` against the C engine, so those tests
need the CMake library built first. `go test -tags ext_cengine ./pkg/plugin-go` fails to
link with `ld: library 'ext_host_static' not found` until you do. Note that
`go build` of a library package does not link, so it appears to succeed; the
failure surfaces when `go test` builds the test binary.

The cgo directives in `pkg/plugin-go/engine.go` use a bare `-lext_host_static` with no
library search path, so point the linker at your build directory:

```bash
cmake -S lib/plugin -B /tmp/lib/plugin-build \
  -DCMAKE_BUILD_TYPE=Release -DEXT_BUILD_SHARED=ON
cmake --build /tmp/lib/plugin-build -j 4

export CGO_LDFLAGS="-L/tmp/lib/plugin-build -Wl,-rpath,/tmp/lib/plugin-build"
export EXT_CENGINE_GUEST=/tmp/go-guest
export EXT_CENGINE_FAULT_GUEST=/tmp/fault-guest

go build -tags ext_cengine -o "$EXT_CENGINE_GUEST" \
  ./pkg/plugin-go/crosslang/testdata/go-guest
go build -tags ext_cengine -o "$EXT_CENGINE_FAULT_GUEST" \
  ./pkg/plugin-go/cengine/testdata/faultguest

go test -tags ext_cengine -race ./pkg/plugin-go/cengine
go test -tags ext_cengine,ext_cengine_shared -race ./pkg/plugin-go/cengine   # shared linkage
```

The two guest binaries are required: without `EXT_CENGINE_GUEST` and
`EXT_CENGINE_FAULT_GUEST` the tagged tests fail with `EXT_CENGINE_GUEST required`.

`scripts/plugin-cengine.sh` does all of the above plus the sanitizer builds,
Rust and Zig guests, and the Node addon, then uploads benchmarks. It also
requires Zig 0.17.0, which the C-only path above does not.

These are scenario-level results, not proof of full parity. CI runs on Linux, macOS and Windows. Remaining error and observer semantics,
release packaging, and the final switch
from independent SDK implementations are still required. Keep working reference
paths until these acceptance requirements are met.
