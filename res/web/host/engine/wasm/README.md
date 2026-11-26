# WebAssembly host contract

`github.com/webong/ext/res/web/host/engine/wasm` defines a runtime-neutral
WebAssembly host API. It owns limits, errors, inspection results, WASI command
options, byte-stream and reactor interfaces, and the `Runtime` interface. It
does not import wazero or the Ext plugin engine and does not select a runtime.

The Go wazero implementation lives in
[`pkg/ext-go/wasm/runtime`](../../../../../pkg/ext-go/wasm/runtime). It supports
core modules and WASI Preview 1. The sibling `pkg/ext-go/wasm` package adapts it
to Ext plugin sessions. The installed-executable Wasmtime path remains in
[`adapters/wasm`](../../../../../adapters/wasm); that adapter does not implement
the in-process `Runtime` interface.

`reactor.js` stays here as page-side glue for the iOS and Android WebView hosts.

## Cross-language conformance

`tests/crosslang` holds the interoperability tests that build real
foreign artifacts: Rust, Zig, and Go guests and hosts, native C ABI guests, and
WASI modules. Run them with `scripts/plugin-crosslang.sh` from the repository
root; they build artifacts the tests then execute, so a plain `go test ./...`
does not exercise them.

`pkg/ext-go/wasm/runtime/cmd/wasirun` checks a portable C guest core compiled to WASI. Use it through
`scripts/plugin-wasi-core.sh`.

See the [interoperability guide](../../../../../docs/plugin-interoperability.md) and
the [runtime authoring guide](../../../../../docs/plugin-runtimes.md).
