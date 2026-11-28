# Go plugin API

`github.com/webong/ext/pkg/ext-go/cgoengine` holds the Go bindings of the C libraries in `lib/`,
one package each. It carries no C and has no root package.

Import `github.com/webong/ext/pkg/ext-go/cgoengine/plugin` (package `plugin`; import it as
`cgoengine` beside `engine/plugin`): the Go entry point to the ext plugin engine. One API, two builds:

- **C engine.** With the `ext_cengine` build tag and cgo (Linux, macOS, and
  Windows with MinGW-w64) the engine functions run in the shared C engine in
  `lib/plugin`.
- **Shared-library driver.** Other builds open the canonical C host through
  `cengine/plugin`, using `EXT_PLUGIN_LIBRARY`, an embedded copy, or `ext-lib`
  beside the executable. `CGO_ENABLED=0` needs no compiler but still needs that
  library. A missing library fails explicitly. Guests remain language-native.

`CEngine` is true for both host drivers.

The data model (`Descriptor`, `Request`, `Response`, `Identity`, the `Err*`
classes, `Backend`, `BackendProfile` and the contract helpers) is `pkg/ext-go/engine`'s,
re-exported so a program imports this package alone. The common API is `Open`,
`OpenProcess`, `Session`, `NewGuest`, `Guest`, `NewRegistry`,
`GuestFromRegistry`, `ResolveRoute`, `ValidateSchema`, `CheckSchema`,
`DirectoryDigest`, `ArtifactDigest` and `SHA256`. Subpackages:

- [`packages`](packages): validate, resolve and verify `ext.io/v1` manifests.
- [`host`](host): open an installed package as a running plugin; the same API as
  [`cengine/plugin/host`](../cengine/plugin/host), over the engine's process runtime and sessions.
- [`host/store`](host/store): the installed package store, with layered roots, `Verify` and
  `ListUnverified` (a manifest-only listing that hashes nothing; the result is unverified, see the
  [`engine` README](../engine/README.md#listing-installed-packages)); the same
  API as [`engine/plugin/host/store`](../engine/host/store), routed through the engine's digests and validation.

The old `engine/plugin/host` package has moved to `cengine/plugin/host`.
Both package-host entry points run C-owned sessions and registry routes.
The store APIs remain separately importable.

Manifest validation and plan resolution in the C engine are bounded by the manifest size: they sort
instead of comparing every pair, and a directory digest stops at 250,000 files or 64 MiB of path text
(`EXT_PLUGIN_INVALID`). Earlier versions took minutes on a 4 MB manifest; see `lib/plugin/services.md`.

The module depends on `core`, `engine` and `cengine`. `Host`, `Streams`, `Instances`, `NewProcess`
and `NewLaunch` are engine-only and need the C engine build.

## Packages

| Package | Role |
| --- | --- |
| `cgoengine` (this package) | The entry point: re-exports the data model; sessions, guests, digests and routes on the C engine or in pure Go |
| `engine` | The pure-Go engine and the portable contract: descriptors, requests, responses, errors, validation, selection, requirements, the pure-Go session and guest, and the adapter process protocol |
| `packages` | Validate, resolve and verify `ext.io/v1` manifests |
| `store` | The installed package store under a caller-supplied root, with layered roots and `Verify` |
| `host` | Opens an installed package as a running plugin and calls it by dotted method name |
| `packagekit` | The `ext.io/v1` manifest, dependency resolution and authoring helpers |
| `author`, `schema`, `jsonschema`, `stream`, `instance`, `capability` | Typed authoring, the manifest schema vocabulary, bounded JSON Schema validation, pull streams, instances and the configuration and health helpers |
| `process`, `jsonline`, `inprocess`, `nativego` | Runtime backends: child process over JSON lines, the JSON-line transport, trusted in-process endpoints, native Go plugins |
| `interop`, `bridge` | Route resolution and bridges between backends |
| `adapter` | Host side of ext adapters: manifests, the installed store, packaging, trust and invocation |
| `plugintest` | Shared conformance fixtures |

Runtime backends that own a heavy runtime stay outside the engine module:
[`pkg/ext-go/hashicorp`](../hashicorp/README.md) owns go-plugin, net/rpc and
gRPC, while [`pkg/ext-go/wasm`](../wasm/README.md) binds its wazero implementation
to the [`pkg/ext-go/core/wasm`](../core/wasm/README.md) contract and the plugin engine.
The portable C engine is [`lib/plugin`](../../../lib/plugin/README.md), and the shared conformance
fixtures are in `lib/plugin/testdata`.

## Building with the C engine

```sh
cmake -S lib/plugin -B /tmp/ext-cengine -DCMAKE_BUILD_TYPE=Release
cmake --build /tmp/ext-cengine --target ext_plugin_host_static
CGO_LDFLAGS='-L/tmp/ext-cengine' \
  go build -tags ext_cengine ./pkg/ext-go/cgoengine/plugin/examples/host
```

Static linkage is the default. To use the optional shared library, enable
`EXT_BUILD_SHARED` in CMake, add the `ext_cengine_shared` Go build tag and configure
the dynamic loader's library path. See the [engine guide](../../../lib/plugin/README.md)
for lifecycle, ownership, backend extension and current limitations.

- [`plugin/examples/host`](plugin/examples/host/main.go): host using the C engine.
- [`cshared/guest`](cshared/guest/guest.go): Go guest lifecycle for the public
  C plugin ABI; does not depend on the C host engine or its build tag.

The default build loads the canonical C host and package store through purego.
The `ext_cengine` build links C through cgo. Neither silently falls back to a Go
host or package store. The Go reference store adapter is compiled only in tests
for [shared-engine parity](../../../docs/plugin-engine-parity.md).

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

This module carries no C, and the default build uses none. For the C engine build, bring the
engine into the project from its release and point cgo at it:

```sh
go get -tool github.com/webong/ext/cmd/ext
go tool ext bundle fetch -lib ext_plugin -driver cgo   # internal/extlib/plugin: include/ and lib/
CGO_CFLAGS="-I$PWD/internal/extlib/plugin/include" CGO_LDFLAGS="-L$PWD/internal/extlib/plugin/lib/<goos>_<goarch>" \
  go build -tags ext_cengine ./...
```

Inside this repository, the scripts give it `lib/plugin/include` and a CMake build of `lib/plugin`.

## Driver and backend boundaries

This module preserves its plugin API, build tags, and current implementation
selection: the tagged cgo build links the C engine, while the default build uses
the shared native host and store through `cengine/plugin` and the described C
interface. A missing native library is an error, never a Go implementation fallback.

A driver describes this access to the C library. Yaegi, WASM, and HashiCorp are
plugin backends: they describe guest execution and consume `core/plugin` types,
which retain identity through the existing `engine/plugin` aliases.
