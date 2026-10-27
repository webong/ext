# WebAssembly runtime

`github.com/webong/ext/res/web/wasm` runs guests compiled to WASI Preview 1
through wazero. It assigns no protocol to a command's standard input and output;
callers can use the byte stream for their own framing.

This is the `wasm` package of the `res/web` Go module. It owns portable module
inspection, bounded WASI command execution, persistent byte streams, and raw
application-defined reactor hosting. It imports wazero but no plugin engine.
`pkg/ext-go/wasm` binds these mechanisms to `engine.Backend`, the CTX JSON-line
protocol, and the ext plugin reactor ABI.

## Cross-language conformance

`pkg/ext-go/crosslang` holds the interoperability tests that build real
foreign artifacts: Rust, Zig, and Go guests and hosts, native C ABI guests, and
WASI modules. Run them with `scripts/plugin-crosslang.sh` from the repository
root; they build artifacts the tests then execute, so a plain `go test ./...`
does not exercise them.

`cmd/wasirun` checks a portable C guest core compiled to WASI. Use it through
`scripts/plugin-wasi-core.sh`.

See the [interoperability guide](../../../docs/plugin-interoperability.md) and
the [runtime authoring guide](../../../docs/plugin-runtimes.md).
