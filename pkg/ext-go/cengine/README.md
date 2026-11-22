# cengine

`github.com/webong/ext/pkg/ext-go/cengine` is the C side of every library of `lib/`: it serves the C
drivers that [`core`](../core) picks between (purego, cgo and ccgo). `cgoengine` and `c2goengine` stay
their own modules and register with it, so a program downloads the ccgo translation only when it
imports it. See [the library opener decision record](../../../docs/adr-library-opener.md).

| Package | What it does |
| --- | --- |
| `library` | The opener purego needs. It loads a C shared library (`.so`, `.dylib`, `.dll`) once per canonical path, never unloads it, and binds its exports to Go function variables through [purego](https://github.com/ebitengine/purego), the same with and without cgo, on Linux, macOS and Windows for amd64 and arm64; on Windows it never searches the working directory for dependent DLLs. It knows no library by name. Finding, fetching and verifying the built copy (`Cite`, `Browse`, `Reserve`, `Borrow`) grows here. |
| `cguest` | Loads C ABI guests (the `ext_plugin_*` exports, with `cguest/abi` for the ABI's constants) once per canonical path, never unloads them, and exchanges frames with them as bytes, one call at a time. It knows nothing of the plugin protocol: [`core/plugin/cguest`](../core/plugin/cguest/README.md) makes it a plugin backend. |

It knows no library by name, requires purego and no ext module, and needs no C toolchain.

Calls go one way, from Go into C: purego's callbacks are limited in number and platform, so a C API
meant for `library` takes no function pointer it would call on a Go program's behalf.

The module path is new. The `ext_cengine` build tag and the old module `pkg/plugin-go/cengine` name the
C plugin engine binding, which is now [`cgoengine`](../cgoengine).
