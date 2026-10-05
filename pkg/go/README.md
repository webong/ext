# Go plugin bindings

Import `github.com/webong/ctx/pkg/go` as `goengine` to embed the CTX C host engine.
It is opt-in: build with `-tags ctx_cengine` and cgo enabled on Linux/macOS.

```sh
cmake -S pkg/plugin/cengine -B /tmp/ctx-cengine -DCMAKE_BUILD_TYPE=Release
cmake --build /tmp/ctx-cengine --target ctx_host_static
CGO_LDFLAGS='-L/tmp/ctx-cengine' \
  go build -tags ctx_cengine ./pkg/go/examples/host
```

Static linkage is the default. To use the optional shared library, enable
`CTX_BUILD_SHARED` in CMake, add the `ctx_cengine_shared` Go build tag and configure
the dynamic loader's library path. See the [engine guide](../../pkg/plugin/cengine/README.md)
for lifecycle, ownership, backend extension and current limitations.

- [`examples/host`](examples/host/main.go): host using the C engine.
- [`cshared/guest`](cshared/guest/guest.go): Go guest lifecycle for the public
  C plugin ABI; does not depend on the C host engine or its build tag.

The native Go contract and runtime implementations remain in `plugin/` while
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
