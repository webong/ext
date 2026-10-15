# Go plugin API

Import `github.com/webong/ext/pkg/plugin-go` (package `goengine`): the one Go entry
point to the ext plugin system. One API, two builds:

- **C engine.** With the `ext_cengine` build tag and cgo (Linux, macOS, and
  Windows with MinGW-w64) the engine functions run in the shared C engine in
  `lib/plugin`.
- **Pure Go.** In any other build (no cgo, no tag, an unsupported platform) the
  same API is served by the pure-Go implementation in `pkg/plugin`, so
  `CGO_ENABLED=0` programs keep working. `goengine.CEngine` reports which build
  you have.

The data model (`Descriptor`, `Request`, `Response`, `Identity`, the `Err*`
classes, `Backend`, `BackendProfile` and the contract helpers) is `pkg/plugin`'s,
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

The pure-Go implementation remains in `pkg/plugin` as the fallback build and the
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
