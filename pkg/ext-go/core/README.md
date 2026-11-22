# core

`github.com/webong/ext/pkg/ext-go/core` is the switcher: it decides how a program runs each library of
`lib/` (the plugin engine, the ledger, and whatever comes next). It knows no library by name; each
library's Go package lists its own drivers. See
[the library opener decision record](../../../docs/adr-library-opener.md).

| Driver | Where the code is |
| --- | --- |
| `go` | the library's pure-Go implementation in `pkg/ext-go/engine` |
| `purego` | the engine's bindings to a built copy of the C library, which `core/library` opens |
| `cgo` | `pkg/ext-go/cgoengine`, linked against the C library when the program is built |
| `c2go` | `pkg/ext-go/c2goengine`, the C library translated to Go |

| Package | What it does |
| --- | --- |
| `driver` | Picks one driver: by name, by `EXT_ENGINE`, or with `Auto` the best available one in the order cgo, c2go, purego, go. A name that cannot be served is an error, never another driver. |
| `library` | The opener purego needs. It loads a C shared library (`.so`, `.dylib`, `.dll`) once per canonical path, never unloads it, and binds its exports to Go function variables through [purego](https://github.com/ebitengine/purego), the same with and without cgo, on Linux, macOS and Windows for amd64 and arm64; on Windows it never searches the working directory for dependent DLLs. Finding, fetching and verifying the built copy (`Cite`, `Browse`, `Reserve`, `Borrow`) grows here. |

The module requires purego and no ext module, and it has no root package: the umbrella published its
engine selection package at the import path `pkg/ext-go/core` up to v0.12.0 (it is now
`pkg/ext-go/plugin`), and a root package here would give a program that requires both an ambiguous
import.

Calls go one way, from Go into C: purego's callbacks are limited in number and platform, so a C API
meant for `library` takes no function pointer it would call on a Go program's behalf.
