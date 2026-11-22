# cguest

`github.com/webong/ext/pkg/ext-go/cguest` runs plugin guests built as C shared libraries
(the `ext_plugin_guest_*` ABI of [`lib/plugin/include/ext_plugin_guest.h`](../../../lib/plugin/include/ext_plugin_guest.h))
as a plugin backend, like `yaegi`, `hashicorp` and `wasm`.

| Package | What it does |
| --- | --- |
| `cguest` | The `engine.Backend` (its backend name is `cshared`). It speaks `ext.plugin/v1` over [`cengine/cguest`](../cengine/README.md), which loads the library and exchanges bytes: the frames, the frame limit and default deadline, the descriptor and response checks, and the engine's errors. |
| `cguest/guest` | The Go guest lifecycle that exports a plugin through the ABI (`-buildmode=c-shared`). |

It needs neither the C engine nor a C toolchain: `cengine/cguest` loads with cgo, or without it through
purego. On darwin/amd64 both builds refuse a Go c-shared guest, since two Go runtimes there share the TLS
slot for g. `scripts/cshared-purego.sh` runs the tests and the shared host cases with and without cgo.
