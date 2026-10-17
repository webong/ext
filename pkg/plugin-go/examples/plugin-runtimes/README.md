# One typed plugin, three dynamic runtimes

- `echo/`: shared typed method, schema, descriptor, handler and authorization.
- `nativego/`: Go `-buildmode=plugin` factory export.
- `wasi/`: Go `wasip1/wasm` command serving CTX JSON lines.
- `cshared/`: Go `-buildmode=c-shared` with four C ABI exports.
- `host/`: one verified host calling the same method through each backend.

See [the runtime authoring guide](../../../../docs/plugin-runtimes.md) for build/run
commands, supported targets, package metadata and lifecycle behavior.
