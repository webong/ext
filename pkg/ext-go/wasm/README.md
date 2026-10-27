# WebAssembly plugin backend

`github.com/webong/ext/pkg/ext-go/wasm` binds the protocol-neutral WebAssembly
runtime in `github.com/webong/ext/res/web/wasm` to the ext plugin engine. It owns
the `engine.Backend` implementations, JSON-line transport, plugin reactor ABI,
profiles, and engine error mapping.

Generic WebAssembly inspection, WASI command execution, persistent byte streams,
and raw reactor hosting live in `res/web/wasm`. That package does not import the
plugin engine. Applications that only need WebAssembly can use it directly;
plugin hosts use this module.

Cross-language interoperability tests live in the `pkg/ext-go` umbrella module.
Run them with `scripts/plugin-crosslang.sh` from the repository root.

See the [interoperability guide](../../../docs/plugin-interoperability.md) and
the [runtime authoring guide](../../../docs/plugin-runtimes.md).
