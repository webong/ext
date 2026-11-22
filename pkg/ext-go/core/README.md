# core

`github.com/webong/ext/pkg/ext-go/core` is the switcher: `core/driver` decides how a program runs each
library of `lib/` (the plugin engine, the ledger, and whatever comes next). It knows no library by name,
uses only the standard library and requires no module; each library's Go package lists its own drivers.
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
