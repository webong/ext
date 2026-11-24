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

## Choosing an engine

The engine's rules exist as the pure-Go `engine` (the oracle, on every platform), as the C
engine through cgo (`cgoengine`, at native speed), as the C translated to Go (`c2goengine`, no
cgo) and as the built C engine loaded at run time through purego (no cgo and no C compiler: the
library `plugin.UseLibrary` names, or the copy `ext-go fetch` embeds). `pkg/ext-go/plugin` is the one
place that knows all four, and it chooses with `core/driver`, the switcher every library of `lib/`
shares:

```go
import "github.com/webong/ext/pkg/ext-go/plugin"

// The best available engine that carries what the program needs, or the one EXT_ENGINE names
// (cgo, c2go, purego, go or auto).
e, err := plugin.FromEnvironment(plugin.Sessions | plugin.Backends | plugin.Progress)
if err != nil { ... }

session, err := e.Open(ctx, descriptor, engine.Options{Verify: ..., Authorize: ..., Connect: ...})
fmt.Println(session.Engine()) // which engine runs this session

leases, err := plugin.NewInstances(e, instance.Options[T]{...})
```

`Engines` lists them in the order `Choose` prefers: cgo, c2go, purego, go. A name is never served by
another engine: an engine that is not in this build (cgo needs cgo, the `ext_cengine` tag and the C
library; c2go needs a platform it was translated for; purego needs a library named or carried), or lacks a feature the program asked for, is an
error wrapping `engine.ErrUnsupported` (the `engine/plugin` package). The features are `Services` (the engine's pure services by
name; the go engine has none), `Sessions` (`OpenProcess`; c2go has them except on Windows),
`Backends` (`Open` over a Go backend and `NewInstances`; every engine with sessions but purego, which
never has C call Go), `Progress` and `Reverse` (cgo, purego and go; c2go does not carry them yet). The
choice is made once, when a session or instance manager is opened.

The purego engine binds only lib/plugin's library interface (`ext_plugin_open`, `_call`, `_close`),
and the rules stay the C engine's: the binding turns Go values into the interface's JSON and back.
A host's callbacks are events there, served by `cengine/library.ServeEvents`, which knows no library:
the binding answers the policies and reverse calls with the Go functions for them, and a call with
progress or an observer returns only once its events are delivered. Progress events do not name
their call, so a session runs one call with progress at a time.

See [module versioning](../../docs/module-versioning.md) for the migration table from the
earlier import paths.
