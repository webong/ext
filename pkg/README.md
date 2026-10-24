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
| [`plugin-go/contract`](plugin-go/contract/README.md) | nothing | none (standard library only) |
| [`plugin-go/cengine`](plugin-go/cengine/README.md) | `plugin-go/contract` | none |
| [`plugin-go/wasm`](plugin-go/wasm/README.md) | `plugin-go/contract` | `wazero` |
| [`plugin-go/hashicorp`](plugin-go/hashicorp/README.md) | `plugin-go/contract` | `go-plugin`, `grpc`, `protobuf` |
| [`plugin-go`](plugin-go/README.md) (umbrella) | all of the above, `res/system` | none |

A consumer that only needs `plugin-go/contract` never downloads the C engine, the
HashiCorp stack or the WASI runtime, and each backend is opt-in. A backend never
depends on `cengine`.

| Library | Contents |
| --- | --- |
| [`plugin-go`](plugin-go/README.md) | The Go plugin library, a tree of modules: the contract, the C engine binding and the runtime backends |

## Plugin language SDKs and bindings

Language-facing plugin packages live here; `plugin-go` is the Go one, a tree of
modules that holds the contract, the engine binding and the runtime backends. The portable C engine and its public
headers live in [`lib/plugin`](../lib/plugin/README.md); these packages bind to it.

| Directory | Contents |
| --- | --- |
| [`plugin-go`](plugin-go/README.md) | Go host/guest/resource binding for the C engine and Go-to-C guest exports |
| [`ext-rust`](ext-rust/README.md) | Rust SDK and reusable C engine host/guest binding |
| [`ext-zig`](ext-zig/README.md) | Zig SDK and header-generated C engine binding |
| [`ext-ts`](ext-ts/README.md) | JavaScript SDK, TypeScript declarations and async Node C engine binding |
| [`ext-python`](ext-python/README.md) | Standard-library Python guest SDK (`ext_plugin`) serving JSON lines |
| [`ext-ios`](ext-ios/README.md) | Swift SDK for iOS and macOS: plugin guest and hosts (web view, WAMR) over the C engine, plus `ExtWeb`, the cookie, userscript and bookmarklet rules (the code is `res/web/ios`) |
| [`ext-android`](ext-android/README.md) | Java SDK for Android: plugin guest and hosts (WebView, WAMR) over the C engine, plus the same web rules in `io.github.webong.ext.web` (the code is `res/web/android`) |

The Go host binding import is `github.com/webong/ext/pkg/plugin-go/cengine`; its package
name is `goengine`. Go guest C exports use
`github.com/webong/ext/pkg/plugin-go/cengine/cshared/guest`.

C embedding examples live in each language's `examples/` directory. Native
runtime integrations such as HashiCorp are separate modules
(`plugin-go/hashicorp`, `plugin-go/wasm`).

This layout does not complete the shared-engine migration. Rust, Zig and
TypeScript still contain independent SDK implementations, and the native Go
engine remains the reference. See the
[parity requirements](../docs/plugin-engine-parity.md).
