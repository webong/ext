# Native ledger binding

`cengine/ledger` binds `lib/ledger` through `cengine/library`. `Bind` accepts an
explicitly loaded library; `BindInterface` accepts an explicit ABI driver.
`Library.OpenStore` opens a C store, `NewGraph` opens its recorder, and
`NativeStore.OpenWatch` provides typed change observation.

`cengine/ledger/supervisor.OpenNative` binds the C supervisor. Shared records,
errors and interfaces come from `core/ledger`; observation wire records come from `core/ledger/observation`. Consumers own host selection. `ObserveRequirements` collects through C; an explicit shell search path is passed to C without mutating process PATH.

CTX and execution-graph publication use this binding. It never silently falls
back to the Go store. Existing native users of `engine/ledger` must update their
import to `cengine/ledger`; the Go memory store remains in `engine/ledger`.
