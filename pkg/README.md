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
| [`plugin-go`](plugin-go/README.md) | `pkg/graph`, `pkg/plugin-cshared` | none |
| `plugin-cshared` | `pkg/plugin-go` | none |
| [`plugin-hashicorp`](plugin-hashicorp/README.md) | `pkg/plugin-go` | `go-plugin`, `grpc`, `protobuf` |
| [`plugin-wasm`](plugin-wasm/README.md) | `pkg/plugin-go` | `wazero` |

A consumer that only needs `pkg/plugin-go` or `pkg/graph` never downloads the
HashiCorp or WASI runtime stacks, and each runtime backend is opt-in.

| Library | Contents |
| --- | --- |
| [`graph`](graph/README.md) | Host, system and process discovery, storage and transactions. `graph/supervisor` supervises and recovers local processes. |
| [`plugin-go`](plugin-go/README.md) | The Go plugin library: the contract, sessions, guests, packages, the store and runtime backends, on the C engine or in pure Go |

## Plugin language SDKs and bindings

Language-facing plugin packages live here; `plugin-go` is the Go one and holds the
contract and the runtime backends. The portable C engine and its public
headers live in [`lib/plugin`](../lib/plugin/README.md); these packages bind to it.

| Directory | Contents |
| --- | --- |
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
