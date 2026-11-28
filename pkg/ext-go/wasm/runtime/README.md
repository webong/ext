# Go WebAssembly runtime

This package implements `pkg/ext-go/core/wasm.Runtime` with wazero. It
provides module inspection, bounded WASI Preview 1 command execution,
persistent byte streams, raw reactors, and compilation caches. It assigns no
Ext protocol to the bytes and imports no plugin package.

`pkg/ext-go/wasm` adds the Ext plugin backend. The WASM adapter uses this
package for its embedded engine and owns selection of external executables.
