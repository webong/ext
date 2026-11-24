# Go plugin library (umbrella)

`github.com/webong/ext/pkg/ext-go` requires every part of the Go plugin library at
matching versions, so a program that wants all of it takes one module. Each part is a
module of its own and a program that wants less takes less:

| Module | For |
| --- | --- |
| [`engine`](engine) | the pure-Go engine: the contract, sessions, guests, packages, bridges (standard library only) |
| [`cgoengine`](cgoengine) | the C engine binding, with a pure-Go build of the same API |
| [`c2goengine`](c2goengine) | the C engine's portable core translated to Go by ccgo (no cgo, no C compiler) |
| [`hashicorp`](hashicorp) | the HashiCorp go-plugin (gRPC) backend |
| [`runtime`](runtime) | how Go reaches C shared libraries: `library` (purego, with or without cgo) and the `cshared` backend for C shared guests |
| [`wasm`](wasm) | the engine-facing WebAssembly backend, built on the reusable `res/web/host/engine/wasm` runtime |

CTN owns the `inference.provider/v1`, `model.tensor/v1`,
`intelligence.perception/v1`, and `intelligence.realtime/v1` bridges in nested
modules under `src/ctn`. The Ext Go library
provides the generic plugin engine they build on.

The umbrella package itself, `plugingo`, holds what needs more than one part:
`PlanGraph` joins a package plan to the host graph (`res/system`). The `core` package
chooses between the three engines (see below). The `cmd/ext-plugin-bridge`
command and the cross-language conformance tests (`crosslang`, `bench`) also live
here, because they need the engine and a backend together.

The `wasm` module requires `res/web/host` for protocol-neutral WebAssembly inspection,
WASI execution and reactor hosting, then binds those mechanisms to
`engine.Backend`. `res/web/host` never imports the plugin engine.

## The C engine from Go

Go binds lib/plugin like every other language (decision 8 of
[the library opener](../../docs/adr-library-opener.md)). `engine/plugin/host/clib` is the binding: the
Go host over lib/plugin's library interface (`ext_plugin_open`, `_call`, `_close`). It is part of the
`engine` module and uses only the standard library, so it loads nothing itself: a driver hands it the
four functions as a `clib.Library`. For purego that is `cengine/library`:

```go
lib, err := library.OpenProvided(clib.Prefix, "") // the copy ext lib fetch embeds; or library.Open(path)
if err != nil { ... }
ext, err := library.BindInterface(lib, clib.Prefix, clib.ABIVersion)
if err != nil { ... }

session, err := clib.OpenProcess(ctx, ext, descriptor, clib.Launch{Executable: ...},
	engine.Options{Verify: ..., Authorize: ..., Reverse: ...})
out, err := session.CallProgress(ctx, contract, "run", payload, onProgress)
```

The rules stay the C engine's: the binding turns Go values into the interface's JSON and back. A
host's callbacks are events there, served by `clib.ServeEvents`, which knows no library: the binding
answers the policies and reverse calls with the Go functions for them, and a call with progress or an
observer returns only once its events are delivered. Progress events do not name their call, so a
session runs one call with progress at a time. Sessions run over a child process the engine starts;
a Go backend (`engine.Options.Connect`) would need C to call Go, which it never does.

`cgoengine` and `c2goengine` keep their own APIs until they reach the same interface and register
with `cengine`; there is no package that chooses between the engines.

See [module versioning](../../docs/module-versioning.md) for the migration table from the
earlier import paths.
