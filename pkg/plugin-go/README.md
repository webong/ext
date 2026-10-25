# Go plugin library (umbrella)

`github.com/webong/ext/pkg/plugin-go` requires every part of the Go plugin library at
matching versions, so a program that wants all of it takes one module. Each part is a
module of its own and a program that wants less takes less:

| Module | For |
| --- | --- |
| [`engine`](engine) | the pure-Go engine: the contract, sessions, guests, packages, bridges (standard library only) |
| [`cgoengine`](cgoengine) | the C engine binding, with a pure-Go build of the same API |
| [`c2goengine`](c2goengine) | the C engine's portable core translated to Go by ccgo (no cgo, no C compiler) |
| [`wasm`](wasm) | the WebAssembly (wazero) backend |
| [`hashicorp`](hashicorp) | the HashiCorp go-plugin (gRPC) backend |

The umbrella package itself, `plugingo`, holds what needs more than one part:
`PlanGraph` joins a package plan to the host graph (`res/system`). The `core` package
chooses between the three engines (see below). The `cmd/ext-plugin-bridge`
command and the cross-language conformance tests (`crosslang`, `bench`) also live
here, because they need the engine and a backend together.

## Choosing an engine

The engine's rules exist as the pure-Go `engine` (the oracle, on every platform), as the C
engine through cgo (`cgoengine`, at native speed) and as the C translated to Go (`c2goengine`, no
cgo). `engine` is the one place that knows all three:

```go
import "github.com/webong/ext/pkg/plugin-go/core"

// The best available engine that carries what the program needs, or the one EXT_ENGINE names
// (cgo, c2go, go or auto).
e, err := core.FromEnvironment(core.Sessions | core.Progress)
if err != nil { ... }

session, err := e.Open(ctx, descriptor, plugin.Options{Verify: ..., Authorize: ..., Connect: ...})
fmt.Println(session.Engine()) // which engine runs this session

leases, err := core.NewInstances(e, instance.Options[T]{...})
```

`Engines` lists them in the order `Choose` prefers: cgo, c2go, go. A name is never served by
another engine: an engine that is not in this build (cgo needs cgo, the `ext_cengine` tag and the C
library; c2go needs a platform it was translated for), or lacks a feature the program asked for, is an
error wrapping `plugin.ErrUnsupported`. The features are `Services` (the engine's pure services by
name; the go engine has none), `Sessions` (`Open`, `OpenProcess`, `NewInstances`; c2go has them
except on Windows), `Progress` and `Reverse` (cgo and go; c2go does not carry them yet). The choice is
made once, when a session or instance manager is opened.

See [module versioning](../../docs/module-versioning.md) for the migration table from the
earlier import paths.
