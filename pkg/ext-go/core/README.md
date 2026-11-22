# core

`github.com/webong/ext/pkg/ext-go/core` is the switcher and the joint between the two sides of each
library of `lib/` (the plugin engine, the ledger, and whatever comes next): Go, in [`engine`](../engine),
and C, in [`cengine`](../cengine). `core/driver` decides how a program runs each library and knows none of
them by name; `core/<library>` bridges a library's C side to its Go contract, as
[`core/plugin/cguest`](plugin/cguest/README.md) makes a C ABI guest a plugin backend. The module requires
`engine` and `cengine`.
See [the library opener decision record](../../../docs/adr-library-opener.md).

| Driver | Where the code is |
| --- | --- |
| `go` | the library's pure-Go implementation in [`engine`](../engine) |
| `purego` | a built copy of the C library, opened by [`cengine`](../cengine) |
| `cgo` | [`cgoengine`](../cgoengine), linked against the C library when the program is built |
| `c2go` | [`c2goengine`](../c2goengine), the C library translated to Go |

`driver.Choose` takes one by name, by `EXT_ENGINE`, or with `Auto` the best available one in the order
cgo, c2go, purego, go. A name that cannot be served is an error, never another driver.

The module has no root package: the umbrella published its engine selection package at the import
path `pkg/ext-go/core` up to v0.12.0 (it is now `pkg/ext-go/plugin`), and a root package here would give
a program that requires both an ambiguous import.
