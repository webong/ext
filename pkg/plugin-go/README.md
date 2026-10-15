# Go plugin API

Import `github.com/webong/ext/pkg/plugin-go` (package `goengine`): the one Go entry
point to the ext plugin system. One API, two builds:

- **C engine.** With the `ext_cengine` build tag and cgo (Linux, macOS, and
  Windows with MinGW-w64) the engine functions run in the shared C engine in
  `lib/plugin`.
- **Pure Go.** In any other build (no cgo, no tag, an unsupported platform) the
  same API is served by the pure-Go implementation in `pkg/plugin-go`, so
  `CGO_ENABLED=0` programs keep working. `goengine.CEngine` reports which build
  you have.

The data model (`Descriptor`, `Request`, `Response`, `Identity`, the `Err*`
classes, `Backend`, `BackendProfile` and the contract helpers) is `pkg/plugin-go`'s,
re-exported so a program imports this package alone. The common API is `Open`,
`OpenProcess`, `Session`, `NewGuest`, `Guest`, `NewRegistry`,
`GuestFromRegistry`, `ResolveRoute`, `ValidateSchema`, `CheckSchema`,
`DirectoryDigest`, `ArtifactDigest` and `SHA256`. Subpackages:

- [`packages`](packages): validate, resolve and verify `ext.package/v1` manifests.
- [`store`](store): the installed package store, with layered roots and `Verify`.
- [`host`](host): open an installed package as a running plugin.

The root package does not import `packagekit`, so a small program such as an
adapter does not reach `pkg/graph`. `Host`, `Streams`, `Instances`, `NewProcess`
and `NewLaunch` are engine-only and need the C engine build.

## Packages

| Package | Role |
| --- | --- |
| `goengine` (this package) | The entry point: re-exports the data model; sessions, guests, digests and routes on the C engine or in pure Go |
| `contract` | The portable contract: descriptors, requests, responses, errors, validation, selection, requirements, the pure-Go session and guest, and the adapter process protocol |
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

Runtime backends that own a heavy runtime are separate modules:
[`pkg/plugin-hashicorp`](../plugin-hashicorp/README.md) (go-plugin, net/rpc and
gRPC) and [`pkg/plugin-wasm`](../plugin-wasm/README.md) (wazero; guests in other
languages run through it). The portable C engine is [`lib/plugin`](../../lib/plugin/README.md), and the shared conformance
fixtures are in `lib/plugin/testdata`.

## Building with the C engine

```sh
cmake -S lib/plugin -B /tmp/ext-cengine -DCMAKE_BUILD_TYPE=Release
cmake --build /tmp/ext-cengine --target ext_host_static
CGO_LDFLAGS='-L/tmp/ext-cengine' \
  go build -tags ext_cengine ./pkg/plugin-go/examples/host
```

Static linkage is the default. To use the optional shared library, enable
`EXT_BUILD_SHARED` in CMake, add the `ext_cengine_shared` Go build tag and configure
the dynamic loader's library path. See the [engine guide](../../lib/plugin/README.md)
for lifecycle, ownership, backend extension and current limitations.

- [`examples/host`](examples/host/main.go): host using the C engine.
- [`cshared/guest`](cshared/guest/guest.go): Go guest lifecycle for the public
  C plugin ABI; does not depend on the C host engine or its build tag.

The pure-Go implementation remains in `pkg/plugin-go` as the fallback build and the
baseline the engine is compared against, while
[shared-engine parity](../../docs/plugin-engine-parity.md) is completed.

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
