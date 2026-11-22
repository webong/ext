# cguest

`github.com/webong/ext/pkg/ext-go/cengine/cguest` is the plugin backend for guests built as C shared libraries:
the `ext_plugin_*` ABI of [`lib/plugin/include/ext_plugin.h`](../../../../lib/plugin/include/ext_plugin.h).
It is a backend like `yaegi`, `hashicorp` and `wasm`, not a way of reaching a library of `lib/`.

| Package | What it does |
| --- | --- |
| `cguest` | The loader, as an `engine.Backend` (its backend name is `cshared`), with a cgo build and a purego build that opens guests through [`cengine/library`](../README.md). |
| `cguest/abi` | The ABI's constants and wire envelope; no cgo. |
| `cguest/guest` | The Go guest lifecycle that exports a plugin through the ABI (`-buildmode=c-shared`). |

It is part of the `cengine` module, which requires `pkg/ext-go/engine` and purego, never `cgoengine`, so loading a
C shared guest needs neither the C engine nor a C toolchain. On darwin/amd64 both builds refuse a Go
c-shared guest, since two Go runtimes there share the TLS slot for g. `scripts/cshared-purego.sh` runs
the tests and the shared host cases with and without cgo.
