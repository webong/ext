# ledger

`github.com/webong/ext/pkg/ext-go/engine/ledger` is the Go API for
[`lib/ledger`](../../../../lib/ledger/README.md). `Bind` binds an explicitly loaded
C library through `cengine/library`; `Library.OpenStore` opens its memory, file
or optional SQLite backend, and `NewGraph` opens its recorder. CTX uses this path.
The binding never silently chooses the old Go implementation. Synchronous calls
check cancellation before entry and forward context deadlines to C; canceling
a context without a deadline does not interrupt a call already inside C.

`Library.Objects()` exposes all described C objects, including supervisors and
watches. Typed helpers cover CTX's graph operations. Shared observation types and
portable host-selection policy remain Go code; collection, recording, inventory
resolution and persistent graph mechanics execute in C.

The older `Store` interface, `NewMemory`, Go supervisor and recorder are still
compatibility APIs for unmigrated users. They are not the implementation used by
CTX. See [the migration decision](../../../../docs/adr-library-opener.md#ctx-ledger-adoption).

| Package | What it is |
| --- | --- |
| `ledger` | C binding, shared model and selection policy; compatibility store API |
| `ledgertest` | Runs the shared store fixtures in [`lib/ledger/testdata/store/v1`](../../../../lib/ledger/testdata/store/v1/README.md) against any `ledger.Store` |
| `supervisor` | Generic local component processes with recovery for unexpected exits, projecting their state into a `ledger.Store` ([the supervisor](supervisor/README.md)) |
| `recorder` | A nested module: the recorder of `res/system`'s observations and the SQLite store ([the recorder](recorder/README.md)) |

The store's contract is data: `lib/ledger/testdata/store/v1` holds its entities, rules, error
codes, layouts and operation cases, and every implementation runs them
([the fixtures](../../../../lib/ledger/testdata/README.md)). The C library is the main
implementation; this package is the fallback for programs that load no C.
