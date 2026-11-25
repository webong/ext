# Ext examples

Runnable teaching examples live here, grouped by what they demonstrate. They
consume public libraries; conformance examples, fixtures and test helpers stay
with their owning packages or in `tests/crosslang`.

| Area | Examples |
| --- | --- |
| Plugins in Go | [Basic host/guest](plugins/go/plugin), [typed SDK](plugins/go/plugin-sdk), [HashiCorp](plugins/go/plugin-hashicorp), [dynamic runtimes](plugins/go/plugin-runtimes), [Go/TypeScript interoperability](plugins/go/plugin-typescript), [package metadata](plugins/go/plugin-package) |
| Guest SDKs | [Ruby](plugins/ruby/echo), [PHP](plugins/php/echo), [Lua](plugins/lua/echo) |
| Ledger | [Go consumer with its own graph vocabulary](ledger/go/consumer-shaped) |
| Web | [Browser extension host and guest](web/extensions), [WASI guest in a WebView](web/go/plugin-webview) |
| Adapters | [Shell](adapters/echo), [Go](plugins/go/adapters/go_echo) |
| Android | [Consumer application](android-consumer) |

## Go consumers

`plugins/go`, `ledger/go`, and `web/go` are separate, unpublished Go modules
listed in the repository's `go.work`. They use the bindings at their existing
module paths. No library imports an example module, and examples are never
version-tagged or published as libraries.

From the repository root:

```sh
go test ./examples/plugins/go/... ./examples/ledger/go/... ./examples/web/go/...
go run ./examples/plugins/go/plugin-sdk --backend inprocess
go run ./examples/plugins/go/plugin-typescript
```

The plugin hosts use Ext's native library selection. Set `EXT_PLUGIN_LIBRARY` to
a built `libext_plugin_host` when it is not installed in the normal search path.
The ledger demo takes `EXT_LEDGER_LIBRARY` as the path to a built ledger library:

```sh
EXT_LEDGER_LIBRARY=/absolute/path/to/libext_ledger.dylib \
  go run ./examples/ledger/go/consumer-shaped
```

Use the appropriate library filename for your platform. See the
[plugin runtime guide](../docs/plugin-runtimes.md) for native Go, C shared-library
and WASI builds. The [WebView example](web/go/plugin-webview/README.md) has its own
build and run instructions.

## Browser extensions

```sh
npm --prefix examples/web/extensions run build:examples
```

Load the generated extensions from `examples/web/extensions/dist`. See the
[extension library guide](../pkg/ext-web/README.md#host-and-guest-examples) for
Chromium and Firefox setup. Browser smoke checks run from this example project;
the library's own tests still run in `pkg/ext-web`.

Conformance guests for Rust, Zig, TypeScript frames and Android, and the native
binding conformance hosts, remain in their packages' `examples/` directories.
