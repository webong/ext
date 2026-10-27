# Go plugin API

Import `github.com/webong/ext/pkg/ext-go/cgoengine` (package `cgoengine`): the Go entry
point to the ext plugin engine. One API, two builds:

- **C engine.** With the `ext_cengine` build tag and cgo (Linux, macOS, and
  Windows with MinGW-w64) the engine functions run in the shared C engine in
  `lib/plugin`.
- **Pure Go.** In any other build (no cgo, no tag, an unsupported platform) the
  same API is served by the pure-Go implementation over `pkg/ext-go/engine`, so
  `CGO_ENABLED=0` programs keep working. `cgoengine.CEngine` reports which build
  you have.

The data model (`Descriptor`, `Request`, `Response`, `Identity`, the `Err*`
classes, `Backend`, `BackendProfile` and the contract helpers) is `pkg/ext-go/engine`'s,
re-exported so a program imports this package alone. The common API is `Open`,
`OpenProcess`, `Session`, `NewGuest`, `Guest`, `NewRegistry`,
`GuestFromRegistry`, `ResolveRoute`, `ValidateSchema`, `CheckSchema`,
`DirectoryDigest`, `ArtifactDigest` and `SHA256`. Subpackages:

- [`packages`](packages): validate, resolve and verify `ext.package/v1` manifests.
- [`host`](host): open an installed package as a running plugin; the same API as
  [`engine/host`](../engine/host), over the engine's process runtime and sessions.
- [`host/store`](host/store): the installed package store, with layered roots, `Verify` and
  `ListUnverified` (a manifest-only listing that hashes nothing; the result is unverified, see the
  [`engine` README](../engine/README.md#listing-installed-packages)); the same
  API as [`engine/host/store`](../engine/host/store), routed through the engine's digests and validation.

An application that needs only the store and host takes the `engine` module and uses its pure-Go
`host` and `host/store`; take these when you want the engine's behavior. Swapping one import for the
other never needs a code change (an architecture test enforces the identical API, and
`TestContractStoreMatchesEngineStore` compares their behavior).

Manifest validation and plan resolution in the C engine are bounded by the manifest size: they sort
instead of comparing every pair, and a directory digest stops at 250,000 files or 64 MiB of path text
(`EXT_INVALID`). Earlier versions took minutes on a 4 MB manifest; see `lib/plugin/services.md`.

The module depends on `pkg/ext-go/engine` only, which uses the standard library only. `Host`, `Streams`, `Instances`, `NewProcess`
and `NewLaunch` are engine-only and need the C engine build.

## Packages

| Package | Role |
| --- | --- |
| `cgoengine` (this package) | The entry point: re-exports the data model; sessions, guests, digests and routes on the C engine or in pure Go |
| `engine` | The pure-Go engine and the portable contract: descriptors, requests, responses, errors, validation, selection, requirements, the pure-Go session and guest, and the adapter process protocol |
| `packages` | Validate, resolve and verify `ext.package/v1` manifests |
| `store` | The installed package store under a caller-supplied root, with layered roots and `Verify` |
| `host` | Opens an installed package as a running plugin and calls it by dotted method name |
| `packagekit` | The `ext.package/v1` manifest, dependency resolution and authoring helpers |
| `author`, `schema`, `jsonschema`, `stream`, `instance`, `capability` | Typed authoring, the manifest schema vocabulary, bounded JSON Schema validation, pull streams, instances and the configuration and health helpers |
| `process`, `jsonline`, `inprocess`, `nativego` | Runtime backends: child process over JSON lines, the JSON-line transport, trusted in-process endpoints, native Go plugins |
| `interop`, `bridge` | Route resolution and bridges between backends |
| `adapter` | Host side of ctx adapters: manifests, the installed store, packaging, trust and invocation |
| `plugintest` | Shared conformance fixtures |
| `cshared` | The C plugin ABI in Go: the loader that opens such libraries as a backend (cgo; the header is `lib/plugin/include/ext_plugin.h`), `cshared/abi` (the envelope's constants and wire types, no cgo) and `cshared/guest` (the Go guest lifecycle that exports it) |

Runtime backends that own a heavy runtime stay outside the engine module:
[`pkg/ext-go/hashicorp`](../hashicorp/README.md) owns go-plugin, net/rpc and
gRPC, while [`pkg/ext-go/wasm`](../wasm/README.md) binds the reusable
[`res/web/wasm`](../../../res/web/wasm/README.md) wazero runtime to the engine.
The portable C engine is [`lib/plugin`](../../../lib/plugin/README.md), and the shared conformance
fixtures are in `lib/plugin/testdata`.

## Building with the C engine

```sh
cmake -S lib/plugin -B /tmp/ext-cengine -DCMAKE_BUILD_TYPE=Release
cmake --build /tmp/ext-cengine --target ext_host_static
CGO_LDFLAGS='-L/tmp/ext-cengine' \
  go build -tags ext_cengine ./pkg/ext-go/cgoengine/examples/host
```

Static linkage is the default. To use the optional shared library, enable
`EXT_BUILD_SHARED` in CMake, add the `ext_cengine_shared` Go build tag and configure
the dynamic loader's library path. See the [engine guide](../../../lib/plugin/README.md)
for lifecycle, ownership, backend extension and current limitations.

- [`examples/host`](examples/host/main.go): host using the C engine.
- [`cshared/guest`](cshared/guest/guest.go): Go guest lifecycle for the public
  C plugin ABI; does not depend on the C host engine or its build tag.

The pure-Go implementation remains in `pkg/ext-go` as the fallback build and the
baseline the engine is compared against, while
[shared-engine parity](../../../docs/plugin-engine-parity.md) is completed.

## Shared engine APIs

- `Open` provides C-owned session admission, calls, cancellation, observation and
  drain with existing Go runtime backends. Caller context values are preserved.
- `NewRegistry` and `GuestFromRegistry` provide typed authoring with C schema
  evaluation. `author.Call` also uses C schemas with a C-backed Host/Session.
- `NewInstances` binds Go factories to C revisions, replacement and leases.
- `NewStreams` binds Go readers to C scope, sequencing, capacity and expiry.
- Package validation/selection/resolution, route resolution, schemas, SHA-256,
  artifact verification and directory digests delegate to shared C services.

Backend, policy and resource callbacks remain Go application code. Their errors
are retained locally when the call carries a Go context; guest responses still
sanitize private failures. Resources require explicit close; guest/low-level host
handles require destruction after their users have joined.

## Building the C engine build

A published Go module contains only its own directory, so this module carries the C
sources it is built from as `_csrc`, a generated copy of `lib/plugin` (a test fails if
the copy drifts). The default build uses no C. For the C engine build, build the
library from `_csrc`, then point cgo at it:

```sh
dir=$(go list -m -f '{{.Dir}}' github.com/webong/ext/pkg/ext-go/cgoengine)
cmake -S "$dir/_csrc" -B build -DCMAKE_BUILD_TYPE=Release && cmake --build build
CGO_LDFLAGS="-L$PWD/build" go build -tags ext_cengine ./...
```
