# cshared

`github.com/webong/ext/pkg/ext-go/cshared` is the C side of every library of `lib/`: it serves the C
drivers that [`core`](../core) picks between (purego, cgo and ccgo). It knows no library by name and
requires purego and no ext module. See
[the library opener decision record](../../../docs/adr-library-opener.md).

| Package | What it does |
| --- | --- |
| `library` | The opener purego needs. It loads a C shared library (`.so`, `.dylib`, `.dll`) once per canonical path, never unloads it, and binds its exports to Go function variables through [purego](https://github.com/ebitengine/purego), the same with and without cgo, on Linux, macOS and Windows for amd64 and arm64; on Windows it never searches the working directory for dependent DLLs. Finding, fetching and verifying the built copy (`Cite`, `Browse`, `Reserve`, `Borrow`) grows here. |

`cgoengine` and `c2goengine` stay their own modules and register with `cshared`, so a program downloads
the ccgo translation only when it imports it.

Calls go one way, from Go into C: purego's callbacks are limited in number and platform, so a C API
meant for `library` takes no function pointer it would call on a Go program's behalf.
