# WASI plugin backend

`github.com/webong/ctx/pkg/plugin/wasm` runs guests compiled to WASI Preview 1
through wazero. A guest is a command that speaks the CTX JSON-line protocol on
its standard input and output; an arbitrary `.wasm` file is not automatically a
CTX plugin.

This is a separate Go module so consumers that do not need an in-process WASI
runtime never download wazero. Native and portable C guests can also be built
for `wasm32-wasip1` and run through the same protocol.

## Cross-language conformance

`crosslang` in this module holds the interoperability tests that build real
foreign artifacts: Rust, Zig, and Go guests and hosts, native C ABI guests, and
WASI modules. Run them with `scripts/plugin-crosslang.sh` from the repository
root; they build artifacts the tests then execute, so a plain `go test ./...`
does not exercise them.

`cmd/wasirun` checks a portable C guest core compiled to WASI. Use it through
`scripts/plugin-wasi-core.sh`.

See the [interoperability guide](../../../docs/plugin-interoperability.md) and
the [runtime authoring guide](../../../docs/plugin-runtimes.md).
