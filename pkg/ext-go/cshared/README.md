# cshared

`github.com/webong/ext/pkg/ext-go/cshared` is the plugin backend for guests built as C shared libraries:
the `ext_plugin_*` ABI of [`lib/plugin/include/ext_plugin.h`](../../../lib/plugin/include/ext_plugin.h).
It is a backend like `yaegi`, `hashicorp` and `wasm`, not a way of reaching a library of `lib/`.

| Package | What it does |
| --- | --- |
| `cshared` | The loader, as an `engine.Backend`, with a cgo build and a purego build that opens guests through [`core/library`](../core/README.md). |
| `cshared/abi` | The ABI's constants and wire envelope; no cgo. |
| `cshared/guest` | The Go guest lifecycle that exports a plugin through the ABI (`-buildmode=c-shared`). |

The module requires `pkg/ext-go/engine`, `pkg/ext-go/core` and purego, never `cgoengine`, so loading a
C shared guest needs neither the C engine nor a C toolchain. On darwin/amd64 both builds refuse a Go
c-shared guest, since two Go runtimes there share the TLS slot for g. `scripts/cshared-purego.sh` runs
the tests and the shared host cases with and without cgo.
