# Public packages

Reusable libraries live here so CTX and its ecosystem share one public surface.
CTX implementation packages stay private to CTX, and [`res/`](../res/README.md) is a separate
container for the browser and credential modules. Every library in both
containers is its own Go module.

Each library here is its own Go module rather than a package inside one
repository-wide module. The root `go.work` lists them so the repository still
builds and tests as a single workspace, while each module stays independently
buildable and publishable.

Keep dependencies pointing inward so a library never depends back on its
consumers.

| Module | Depends on | Third-party dependencies |
| --- | --- | --- |
| [`adapter`](adapter/README.md) | `pkg/plugin` | none |
| [`supervisor`](supervisor/README.md) | `pkg/graph` | none |
| [`graph`](graph/README.md) | none | `golang.org/x/sys` |
| [`plugin`](plugin/README.md) | `pkg/graph` | none |
| [`go`](go/README.md) | `pkg/plugin` | none |
| [`plugin-hashicorp`](../plugin-hashicorp/README.md) | `pkg/plugin` | `go-plugin`, `grpc`, `protobuf` |
| [`plugin-wasm`](../plugin-wasm/README.md) | `pkg/plugin`, `pkg/plugin-go` | `wazero` |

Six of the eight modules carry no third-party dependencies at all. A consumer
that only needs `pkg/plugin` or `pkg/graph` never downloads the
HashiCorp or WASI runtime stacks, and each runtime backend is opt-in.

| Library | Contents |
| --- | --- |
| [`graph`](graph/README.md) | Host, system and process discovery, storage and transactions |
| [`plugin`](plugin/README.md) | Shared host/guest contract, runtime backends and conformance fixtures |
| [`supervisor`](supervisor/README.md) | Local process supervision and recovery |
| [`adapter`](adapter/README.md) | Public adapter process protocol |

## Plugin language SDKs and bindings

Language-facing plugin packages live here. `plugin/` owns the engine, contract,
runtime backends and shared conformance fixtures.

| Directory | Contents |
| --- | --- |
| [`go`](go/README.md) | C host/guest/resource binding and Go-to-C guest exports |
| [`rust`](rust/README.md) | Rust SDK and reusable C engine host/guest binding |
| [`zig`](zig/README.md) | Zig SDK and header-generated C engine binding |
| [`typescript`](typescript/README.md) | JavaScript SDK, TypeScript declarations and async Node C engine binding |

The Go host binding import is `github.com/webong/ctx/pkg/plugin-go`; its package name
is `goengine`. Go guest C exports use `github.com/webong/ctx/pkg/plugin-go/cshared/guest`.
Rust crate and npm package names are unchanged; their repository locations moved.

C embedding examples live in each language's `examples/` directory. The C engine
and its public headers remain in [`plugin/cengine`](plugin/cengine/README.md).
Native runtime integrations such as HashiCorp remain inside `plugin/`.

This layout change does not complete the shared-engine migration. Rust/Zig/TS
still contain independent SDK implementations, and the native Go engine remains
the reference. See the [parity requirements](../docs/plugin-engine-parity.md).
