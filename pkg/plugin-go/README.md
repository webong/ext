# Go plugin library (umbrella)

`github.com/webong/ext/pkg/plugin-go` requires every part of the Go plugin library at
matching versions, so a program that wants all of it takes one module. Each part is a
module of its own and a program that wants less takes less:

| Module | For |
| --- | --- |
| [`contract`](contract) | the contract, sessions, guests, packages, bridges (standard library only) |
| [`cengine`](cengine) | the C engine binding, with a pure-Go build of the same API |
| [`wasm`](wasm) | the WebAssembly (wazero) backend |
| [`hashicorp`](hashicorp) | the HashiCorp go-plugin (gRPC) backend |

The umbrella package itself, `plugingo`, holds what needs more than one part:
`PlanGraph` joins a package plan to the host graph (`res/system`). The `cmd/ext-plugin-bridge`
command and the cross-language conformance tests (`crosslang`, `bench`) also live
here, because they need the engine and a backend together.

See [module versioning](../../docs/module-versioning.md) for the migration table from the
earlier import paths.
