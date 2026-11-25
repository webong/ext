# Native ledger binding

`cengine/ledger` binds `lib/ledger` through `cengine/library`. `Bind` accepts an
explicitly loaded library; `BindInterface` accepts an explicit ABI driver.
`Library.OpenStore` opens a C store, `NewGraph` opens its recorder, and
`NativeStore.OpenWatch` provides typed change observation.

`cengine/ledger/supervisor.OpenNative` binds the C supervisor. Shared records,
errors and interfaces come from `core/ledger`; observation wire records come from `core/ledger/observation`. Consumers own host selection. `ObserveRequirements` collects through C; an explicit shell search path is passed to C without mutating process PATH.

CTX and execution-graph publication use this binding. It never silently falls
back to the Go store. Existing native users of `engine/ledger` must update their
import to `cengine/ledger`; memory storage is opened through `Library.OpenStore(ctx, "memory", "", options)`.

Driver coverage is explicit:

| Driver | Ledger support |
| --- | --- |
| purego | `Bind` loads the native shared library. |
| cgo | `cgoengine/ledger.Bind`, built with `ext_ledger_cgo` and the fetched C headers and library. |
| c2go | `c2goengine/ledger.Bind`, generated from the C portable profile with `ext_ledger_c2go`; memory/file stores, watches, recorder projections and fingerprints on 64-bit Linux/macOS. |
| go | Removed; record aliases do not provide an engine. |

`BindInterface` accepts a described ABI supplied by a driver; it does not create
a missing driver or substitute another implementation. Platform collector and
supervisor support is determined by the loaded C library.

The translated profile returns `unsupported` for native collection, machine
environments and process supervision. Windows translation is unavailable;
`Bind` returns `driver.ErrUnavailable` on targets without that driver. Use the
native purego or cgo library for those platform operations. Run
`bash scripts/ledger-c2go.sh` from the repository to regenerate and validate the
profile; generated translations are not committed.
