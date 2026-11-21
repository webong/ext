# runtime

`github.com/webong/ext/pkg/ext-go/runtime` is how Go reaches a C shared library. The module has no
root package, so nothing in it shadows the standard `runtime`.

| Package | What it does |
| --- | --- |
| `library` | Loads a C shared library (`.so`, `.dylib`, `.dll`) once per canonical path, never unloads it, and binds its exports to Go function variables through [purego](https://github.com/ebitengine/purego). It works the same with and without cgo, on Linux, macOS and Windows for amd64 and arm64; on Windows it never searches the working directory for dependent DLLs. |
| `cshared` | The C plugin ABI (`lib/plugin/include/ext_plugin.h`) as an `engine.Backend`: the loader for guests built as C shared libraries, with a cgo build and a purego build on `library`. |
| `cshared/abi` | The ABI's constants and wire envelope; no cgo. |
| `cshared/guest` | The Go guest lifecycle that exports a plugin through the ABI (`-buildmode=c-shared`). |

The module depends on `pkg/ext-go/engine` and purego only, never on `cgoengine`, so loading a C
shared guest needs neither the C engine nor a C toolchain. `scripts/cshared-purego.sh` runs the
`cshared` and `library` tests and the shared host cases with and without cgo.

Calls go one way, from Go into C: purego's callbacks are limited in number and platform, so a C
API meant for `library` takes no function pointer it would call on a Go program's behalf.
