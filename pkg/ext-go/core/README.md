# core

`github.com/webong/ext/pkg/ext-go/core` is how a Go program reaches the libraries of `lib/` (the
plugin engine, the ledger, and whatever comes next) without ext-go carrying them. It knows no library
by name: each library's Go package lists its own drivers and declares what it needs. See
[the library opener decision record](../../../docs/adr-library-opener.md).

| Package | What it does |
| --- | --- |
| `driver` | The driver switcher. A library runs through one of four drivers: `cgo` (the C library linked through cgo), `c2go` (the C translated to Go by ccgo), `purego` (the C library loaded as a shared library at run time) and `go` (its pure-Go implementation). `Choose` takes a driver by name, or the best available one in that order with `Auto`; `FromEnvironment` reads the name from `EXT_ENGINE`. A name that cannot be served is an error, never another driver. |
| `library` | Loads a C shared library (`.so`, `.dylib`, `.dll`) once per canonical path, never unloads it, and binds its exports to Go function variables through [purego](https://github.com/ebitengine/purego). It works the same with and without cgo, on Linux, macOS and Windows for amd64 and arm64; on Windows it never searches the working directory for dependent DLLs. |
| `cshared` | The C plugin ABI (`lib/plugin/include/ext_plugin.h`) as an `engine.Backend`: the loader for guests built as C shared libraries, with a cgo build and a purego build on `library`. |
| `cshared/abi` | The ABI's constants and wire envelope; no cgo. |
| `cshared/guest` | The Go guest lifecycle that exports a plugin through the ABI (`-buildmode=c-shared`). |

The module has no root package. The umbrella published its engine selection package at the import
path `pkg/ext-go/core` up to v0.12.0 (it is now `pkg/ext-go/plugin`), and a root package here would
give a program that requires both an ambiguous import.

The module depends on `pkg/ext-go/engine` and purego only, never on `cgoengine`, so loading a C
shared guest needs neither the C engine nor a C toolchain. `scripts/cshared-purego.sh` runs the
`cshared` and `library` tests and the shared host cases with and without cgo.

Calls go one way, from Go into C: purego's callbacks are limited in number and platform, so a C
API meant for `library` takes no function pointer it would call on a Go program's behalf.
