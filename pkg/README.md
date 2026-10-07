# Public packages

Reusable libraries live here so ctx, ctn and the wider ecosystem share one
public surface. Product implementation packages stay private to each product,
and [`res/`](../res/README.md) is a separate container for the browser and
credential modules. Every library in both containers is its own Go module.

Each library here is its own Go module rather than a package inside one
repository-wide module. The root `go.work` lists them so the repository still
builds and tests as a single workspace, while each module stays independently
buildable and publishable.

Keep dependencies pointing inward so a library never depends back on its
consumers.

| Module | Depends on | Third-party dependencies |
| --- | --- | --- |
| [`graph`](graph/README.md) | none | `golang.org/x/sys` |
| [`plugin`](plugin/README.md) | `pkg/graph` | none |
| `plugin-go` | `pkg/plugin` | none |
| `plugin-cshared` | `pkg/plugin` | none |
| [`plugin-hashicorp`](plugin-hashicorp/README.md) | `pkg/plugin` | `go-plugin`, `grpc`, `protobuf` |
| [`plugin-wasm`](plugin-wasm/README.md) | `pkg/plugin`, `pkg/plugin-go` | `wazero` |

A consumer that only needs `pkg/plugin` or `pkg/graph` never downloads the
HashiCorp or WASI runtime stacks, and each runtime backend is opt-in.

| Library | Contents |
| --- | --- |
| [`graph`](graph/README.md) | Host, system and process discovery, storage and transactions. `graph/supervisor` supervises and recovers local processes. |
| [`plugin`](plugin/README.md) | Shared host/guest contract, the adapter process protocol, runtime backends and conformance fixtures |

## Plugin language SDKs and bindings

Language-facing plugin packages live here. `plugin/` owns the contract, runtime
backends and shared conformance fixtures; `plugin-engine/` is the portable C
engine and its public headers.

| Directory | Contents |
| --- | --- |
| [`plugin-engine`](plugin-engine/README.md) | Portable C engine, headers, static and shared libraries |
| [`plugin-go`](plugin-go/README.md) | Go host/guest/resource binding for the C engine and Go-to-C guest exports |
| [`plugin-rust`](plugin-rust/README.md) | Rust SDK and reusable C engine host/guest binding |
| [`plugin-zig`](plugin-zig/README.md) | Zig SDK and header-generated C engine binding |
| [`plugin-ts`](plugin-ts/README.md) | JavaScript SDK, TypeScript declarations and async Node C engine binding |

The Go host binding import is `github.com/webong/ext/pkg/plugin-go`; its package
name is `goengine`. Go guest C exports use
`github.com/webong/ext/pkg/plugin-go/cshared/guest`.

C embedding examples live in each language's `examples/` directory. Native
runtime integrations such as HashiCorp are separate modules
(`plugin-hashicorp`, `plugin-wasm`).

This layout does not complete the shared-engine migration. Rust, Zig and
TypeScript still contain independent SDK implementations, and the native Go
engine remains the reference. See the
[parity requirements](../docs/plugin-engine-parity.md).
