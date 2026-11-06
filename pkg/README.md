# Public packages

Reusable libraries live here so ctx, ctn and the wider ecosystem share one
public surface. Product implementation packages stay private to each product,
and [`res/`](../res/README.md) is a separate container for reusable resource
libraries. Go libraries in both containers are separate Go modules; language
packages such as `ext-ts` and `ext-web` use their own toolchains.

Each Go library here is its own module rather than a package inside one
repository-wide module. The root `go.work` lists those modules so the repository
still builds and tests as a single Go workspace. Other language packages remain
independently buildable and publishable through their own toolchains.

Keep dependencies pointing inward so a library never depends back on its
consumers.

| Module | Depends on | Third-party dependencies |
| --- | --- | --- |
| [`ext-go/engine`](ext-go/engine/README.md) | nothing | none (standard library only) |
| [`ext-go/cgoengine`](ext-go/cgoengine/README.md) | `ext-go/engine` | none |
| [`ext-go/hashicorp`](ext-go/hashicorp/README.md) | `ext-go/engine` | `go-plugin`, `grpc`, `protobuf` |
| [`ext-go/wasm`](ext-go/wasm/README.md) | `ext-go/engine`, `res/web/host` | `wazero` through `res/web/host` |
| [`ext-go`](ext-go/README.md) (umbrella) | all of the above, `res/system` | none |

A consumer that only needs `ext-go/engine` never downloads the C engine, the
HashiCorp stack or the WASI runtime, and each backend is opt-in. A backend never
depends on `cgoengine`.

| Library | Contents |
| --- | --- |
| [`ext-go`](ext-go/README.md) | The Go plugin library: the contract, C engine bindings and plugin-specific backends; its WASM binding uses `res/web/host` |
| [`ext-web`](ext-web/README.md) | The JavaScript/TypeScript package for code that runs inside a browser extension: peer messaging, in-browser management, workspace helpers, a userscript registry, a page bridge and themes. Private, built and tested with Node; it is not a plugin SDK |

## Plugin language SDKs and bindings

Language-facing plugin packages live here; `ext-go` is the Go one, a tree of
modules that holds the contract, engine bindings and plugin-specific backends.
Its `wasm` module depends on `res/web/host` for reusable WebAssembly mechanics. The portable C engine and its public
headers live in [`lib/plugin`](../lib/plugin/README.md); these packages bind to it.

| Directory | Contents |
| --- | --- |
| [`ext-go`](ext-go/README.md) | Go host/guest/resource binding for the C engine and Go-to-C guest exports |
| [`ext-rust`](ext-rust/README.md) | Rust SDK and reusable C engine host/guest binding |
| [`ext-zig`](ext-zig/README.md) | Zig SDK and header-generated C engine binding |
| [`ext-ts`](ext-ts/README.md) | JavaScript SDK, TypeScript declarations and async Node C engine binding |
| [`ext-python`](ext-python/README.md) | Standard-library Python guest SDK (`ext_io.plugin`) serving JSON lines, and a host SDK (`ext_io.host`) on the C engine |
| [`ext-ruby`](ext-ruby/README.md) | Standard-library Ruby guest SDK (`ExtIO::Plugin`, gem `ext-io`) serving JSON lines, and a host SDK (`ExtIO::Host`, gem `ext-io-host`) on the C engine |
| [`ext-php`](ext-php/README.md) | PHP guest SDK (`ExtIO\Plugin`, Composer `webong/ext-io`) serving JSON lines; needs only `ext-json` |
| [`ext-lua`](ext-lua/README.md) | Pure-Lua guest SDK (`ext_io.plugin`, rock `ext-io`) serving JSON lines on Lua 5.1 to 5.5 and LuaJIT |
| [`ext-ios`](ext-ios/README.md) | Swift SDK for iOS and macOS: plugin guest and hosts, web and clipboard libraries, and local system and network observation |
| [`ext-android`](ext-android/README.md) | Java SDK for Android: plugin guest and hosts, web and clipboard libraries, and app-scoped system and local network observation |

`ext-ts` is the plugin SDK for JavaScript and TypeScript. [`ext-web`](ext-web/README.md) is a
different package: it is for code that runs inside a browser extension, and it is named for
that platform the way `ext-ios` and `ext-android` are named for theirs. It does not use the
plugin engine.

The Go host binding import is `github.com/webong/ext/pkg/ext-go/cgoengine`; its package
name is `cgoengine`. Go guest C exports use
`github.com/webong/ext/pkg/ext-go/cgoengine/cshared/guest`.

C embedding examples live in each language's `examples/` directory. HashiCorp's
native runtime integration remains the separate `ext-go/hashicorp` module.
WebAssembly inspection and execution live in
[`res/web/host/engine/wasm`](../res/web/host/engine/wasm/README.md); [`ext-go/wasm`](ext-go/wasm/README.md)
binds that reusable library to the plugin engine.

This layout does not complete the shared-engine migration. Rust, Zig and
TypeScript still contain independent SDK implementations, and the native Go
engine remains the reference. See the
[parity requirements](../docs/plugin-engine-parity.md).
