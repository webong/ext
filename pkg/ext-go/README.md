# Go Ext library (umbrella)

`github.com/webong/ext/pkg/ext-go` aggregates dependencies for the Go ext library.

It has no root Go package; applications import the library packages they use. Each part is a
module of its own and a program that wants less takes less:

| Module | For |
| --- | --- |
| [`core`](core) | shared plugin and ledger contracts, observation types, plugin validation and driver selection |
| [`engine`](engine) | Go plugin sessions, guests and packages; ledger record aliases |
| [`cgoengine`](cgoengine) | the C engine binding, with a pure-Go build of the same API |
| [`c2goengine`](c2goengine) | the C engine's portable core translated to Go by ccgo (no cgo, no C compiler) |
| [`hashicorp`](hashicorp) | the HashiCorp go-plugin (gRPC) backend |
| [`cengine`](cengine) | native loading, C guest support and the C-backed ledger API |
| [`wasm`](wasm) | the engine-facing WebAssembly backend, built on the reusable `res/web/host/engine/wasm` runtime |

CTN owns the `inference.provider/v1`, `intelligence.perception/v1`, and
`intelligence.realtime/v1` bridges in nested modules under `src/ctn`.
CTN also owns the public `model.tensor/v1` bridge in `src/ctn/model`,
which peer products can use to call installed provider packages directly.
These bridges build on the generic Ext Go plugin engine.

The umbrella module holds examples and benchmarks that use several libraries.
Execution-graph snapshots come from `lib/plugin`; the CLI publishes their
observations through `lib/ledger`. Cross-language conformance lives in
`tests/crosslang`. Ext-go is a library and ships no public executable; the
transport bridge runs as `ext bridge --config …`, owned by `cmd/ext`.

The `wasm` module requires `res/web/host` for protocol-neutral WebAssembly inspection,
WASI execution and reactor hosting, then binds those mechanisms to
`engine.Backend`. `res/web/host` never imports the plugin engine.

## The C engine from Go

Go binds the C libraries like every other language (decision 8 of
[the library opener](../../docs/adr-library-opener.md)), and ext-go holds no binding of any one of them.
Each library describes its interface (lib/plugin's and lib/ledger's `interface.json`, served by
`describe`), and a program reaches it in one of two ways:

- driven by the description, with `cengine/library` alone: `Describe`, then `Open` an object by kind
  with handlers named for its events, and `Call` an operation by name;
- through a typed binding generated from it, in the program's own module:
  `//go:generate go tool ext bundle bind -lib ext_plugin` writes a type per object, a method per
  operation and a struct of handlers for its events.

```go
lib, err := library.OpenProvided("ext_plugin", "") // the copy ext bundle fetch embeds; or library.Open(path)
plugin, err := pluginbind.Bind(lib)                // the generated binding
host, err := plugin.OpenHost(pluginbind.HostConfig{...}, pluginbind.HostHandlers{
	Verify: func(descriptor json.RawMessage) error { ... }, Reverse: ...})
out, err := host.CallWith(pluginbind.HostCallInput{...}, pluginbind.HostCallHandlers{Progress: ...})
```

The rules stay the C library's: a binding turns values into the interface's JSON and back. A host's
callbacks are events, served by `cengine/library`'s event loop, which knows no library: handlers answer
them as the description says, and a call returns only once the events it raised are delivered. A
call's own events (its progress) carry its tag, so many calls with progress run at once. Sessions run
over a child process the engine starts; a Go backend (`engine.Options.Connect`) would need C to call
Go, which it never does. `tests/crosslang` holds lib/plugin's generated binding, with the session
adapter that holds it to the differential checks cgo and c2go pass.

`cgoengine` and `c2goengine` keep their own APIs until they reach the same interface and register
with `cengine`; there is no package that chooses between the engines.

See [module versioning](../../docs/module-versioning.md) for the migration table from the
earlier import paths.
