# WebAssembly plugin backend

`github.com/webong/ext/pkg/ext-go/wasm` binds the wazero implementation in
`runtime/` to the Ext plugin engine. It owns
the `core/plugin.Backend` implementations, the binding to the existing JSON-line
transport, plugin reactor ABI, profiles, and shared error mapping.

The runtime-neutral contract stays in `pkg/ext-go/core/wasm`. Generic
WebAssembly inspection, WASI command execution, persistent byte streams, and
raw reactor hosting live in `runtime/`, which imports no plugin package.
Applications that only need WebAssembly can import `runtime/` directly.

Cross-language interoperability tests live in `tests/crosslang`.
Run them with `scripts/plugin-crosslang.sh` from the repository root.

See the [interoperability guide](../../../docs/plugin-interoperability.md) and
the [runtime authoring guide](../../../docs/plugin-runtimes.md).

## Source boundaries

Descriptors, backend interfaces, profiles and errors come directly from
`core/plugin`, with the same type and error identities as the published
`engine/plugin` aliases. The WASI command backend still embeds the existing
`engine/plugin/transport/jsonline.Client`; its lifecycle and framing code remain
there. The reactor uses the wazero runtime in this module, with the public
contract defined by `src/ctx/web`.

This is a plugin backend (guest execution), not a driver for accessing Ext's
canonical C libraries. No C driver, module path, default or build tag changed.
