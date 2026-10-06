# Shared plugin engine parity

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
| Contract validation (`plugin/contract.go`) | Shared descriptor/request/response validation, matching, selection and requirement APIs | Exhaustive raw-wire edge cases and language defaults |
| Compatibility (`plugin/compatibility.go`) | Shared exact protocol negotiation and route/profile accounting | Cross-language parity fixtures and release versioning |
| Host session (`plugin/session.go`) | C-generated calls/IDs, verification, contextual authorization, cancellation, immutable matching, state, queued admission, abort and drain; Go session facade | Remaining error-cause fidelity and binding semantics audit |
| Observability (`plugin/observe.go`) | C metadata-only events with Go/Rust/Zig hooks and bounded asynchronous Node observation | Match all stage/error semantics; document Node delivery limits |
| Guest engine (`plugin/guest.go`) | C immutable dispatcher, deadlines, owned buffers and error sanitization; Go/Rust/Zig/Node bindings | Complete cancellation/default/invalid-input differential coverage |
| Typed authoring (`pkg/plugin/author`) | Go registry construction, guest snapshots and typed host calls can use C schema evaluation; Rust/Zig/Node accept native handlers | Typed foreign conveniences and final default SDK integration |
| Schema (`pkg/plugin/schema`) | Shared bounded schema validation and checks; Go differential fixtures | Exhaustive raw JSON null/number edge cases |
| Streams and capabilities (`pkg/plugin/stream`, `pkg/plugin/capability`) | C scoped pull-stream lifecycle, capacity, sequence, bounded batches, expiry, close/release; Go integration; Node bindings | Remaining race/error differential cases; capability helpers |
| Instances (`pkg/plugin/instance`) | C revisions, configuration digests, capacity, leases, replacement, factory lifetime and draining cleanup; Go integration | Remaining disposal error-cause fidelity |
| Packages and trust (`pkg/plugin/packagekit`, `plugin/integrity.go`) | Shared manifest/entrypoint/dependency services, SHA-256, POSIX artifact verification and directory digest | Windows filesystem implementation; Unicode-version compatibility; graph binding coverage |
| Interoperability (`pkg/plugin/interop`, `pkg/plugin/bridge`) | C route resolver and production JSON-line/HashiCorp frontends in both directions, tested with C-owned sessions | Windows bridge supervisor and release packaging |
| JSON-line process | C absolute-path launch, copied argument/environment configuration, direct-child cleanup | Windows process implementation and platform launch tests |
| In-process, HashiCorp, WASI, native Go, C ABI | C-session conformance with Rust/Zig C ABI and WASI guests, native Go guest, and both HashiCorp transports; direct process engine | Remaining platform combinations; portable runtime modules |
| Static/shared distribution | Static/shared host and guest libraries, exact CMake package version, external install-consumer checks and macOS sanitizers | Execute CI on all platforms; reproducible release packages |
| Go/Rust/Zig/JS bindings | Go session/resources; Rust host/guest/lease/stream ownership; Zig resource wrappers and generated FFI; async Node host/guest/integrity/observation and instance/stream managers | Remaining typed conveniences; replace independent SDK implementations |
| Platforms | Native C host remains POSIX/Linux/macOS; portable C and Rust guest-only builds execute in WASI | Windows implementation and all supported Go-backend/platform combinations |

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
Go race checks. Raw optional-null and numeric schema cases compare against Go.

The production bridge passed both directions with gRPC and net/rpc under Go and
C-backed sessions. The C-session runtime matrix passed Rust/Zig JSON-line, native
C ABI and WASI guest conformance and native Go plugin conformance. The portable
guest library and its Rust guest-only binding also execute as WASI modules.
External CMake consumers load the installed static/shared host and guest targets.

## Building the C engine locally

The `ctx_cengine` build tag links `pkg/plugin-go` against the C engine, so those tests
need the CMake library built first. `go test -tags ctx_cengine ./pkg/plugin-go` fails to
link with `ld: library 'ctx_host_static' not found` until you do. Note that
`go build` of a library package does not link, so it appears to succeed; the
failure surfaces when `go test` builds the test binary.

The cgo directives in `pkg/plugin-go/engine.go` use a bare `-lctx_host_static` with no
library search path, so point the linker at your build directory:

```bash
cmake -S pkg/plugin-engine -B /tmp/pkg/plugin-engine-build \
  -DCMAKE_BUILD_TYPE=Release -DCTX_BUILD_SHARED=ON
cmake --build /tmp/pkg/plugin-engine-build -j 4

export CGO_LDFLAGS="-L/tmp/pkg/plugin-engine-build -Wl,-rpath,/tmp/pkg/plugin-engine-build"
export CTX_CENGINE_GUEST=/tmp/go-guest
export CTX_CENGINE_FAULT_GUEST=/tmp/fault-guest

go build -tags ctx_cengine -o "$CTX_CENGINE_GUEST" \
  ./pkg/plugin-wasm/crosslang/testdata/go-guest
go build -tags ctx_cengine -o "$CTX_CENGINE_FAULT_GUEST" \
  ./pkg/plugin-engine/tests/faultguest

go test -tags ctx_cengine -race ./pkg/plugin-go
go test -tags ctx_cengine,ctx_cengine_shared -race ./pkg/plugin-go   # shared linkage
```

The two guest binaries are required: without `CTX_CENGINE_GUEST` and
`CTX_CENGINE_FAULT_GUEST` the tagged tests fail with `CTX_CENGINE_GUEST required`.

`scripts/plugin-cengine.sh` does all of the above plus the sanitizer builds,
Rust and Zig guests, and the Node addon, then uploads benchmarks. It also
requires Zig 0.17.0, which the C-only path above does not.

These are scenario-level results, not proof of full parity. Linux CI is configured;
Windows is not implemented. Remaining error and observer semantics,
release packaging, and the final switch
from independent SDK implementations are still required. Keep working reference
paths until these acceptance requirements are met.
