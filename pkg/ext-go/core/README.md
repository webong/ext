# core

`github.com/webong/ext/pkg/ext-go/core` owns shared contracts and driver selection.
It never imports `engine` or `cengine`. Both implementations use the same types.

| Package | Responsibility |
| --- | --- |
| `plugin` | Plugin identities, descriptors, requests, responses, validation and backend interfaces |
| `plugin/interop` | Shared transport-route metadata and portable capability resolution |
| `ledger` | Graph records, store interfaces and error codes; graph algorithms execute in C |
| `ledger/observation` | Ledger-owned observation wire records |
| `ledger/supervisor` | Shared supervisor state and result types |
| `ledger/ledgertest` | Implementation-independent store conformance fixtures |
| `driver` | Library-independent driver selection, using only the standard library |

Engine implementations and native bindings depend on core; core never depends
on them. Core requires Go 1.25 and uses only the standard library. Consumers own host-selection policy.

| Driver | Where the code is |
| --- | --- |
| `go` | the library's pure-Go implementation in [`engine`](../engine) |
| `purego` | a built copy of the C library, opened by [`cengine`](../cengine) |
| `cgo` | [`cgoengine`](../cgoengine), linked against the C library when the program is built |
| `c2go` | [`c2goengine`](../c2goengine), the C library translated to Go |

`driver.Choose` takes one by name, by `EXT_ENGINE`, or with `Auto` the best available one in the order
cgo, c2go, purego, go. A name that cannot be served is an error, never another driver.

The module has no root package: the umbrella published its engine selection package at the import
path `pkg/ext-go/core` up to v0.12.0 (that package is gone), and a root package here would give
a program that requires both an ambiguous import.

## Contracts, drivers, and plugin backends

`plugin/interop` owns transport-route metadata and portable resolution;
`plugin.JSONLineProfile` owns the standard JSON-line capability description.
Consumers import `core/plugin/interop` directly; the old route forwarding
package has been removed. `engine/plugin/transport/jsonline.Profile` forwards here.
There is one implementation of their validation and defaults.

Drivers select how Go accesses a canonical C library: linked cgo, translated C,
or a shared library through purego. The legacy `go` selection exists only where
an implementation still provides it. Plugin backends select how a guest executes:
Yaegi source interpretation, WASM, or HashiCorp RPC. They consume core contracts;
core imports none of those runtimes and remains standard-library-only.
