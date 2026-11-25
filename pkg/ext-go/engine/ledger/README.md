# ledger

`github.com/webong/ext/pkg/ext-go/engine/ledger` holds the compatibility Go memory
store. Shared types and validation are defined in `core/ledger` and retained here
as aliases. Observation types and portable selection live in
`core/ledger/observation`. The Go supervisor and recorder remain for existing users.

Native users import `pkg/ext-go/cengine/ledger` for `Bind`, `Library.OpenStore`,
`NewGraph` and typed watches, and `cengine/ledger/supervisor` for `OpenNative`.
CTX uses that C-backed path. Engine never imports the native loader.

| Package | What it is |
| --- | --- |
| `ledger` | Go memory store and compatibility aliases of core contracts |
| `ledgertest` | Runs the shared store fixtures in [`lib/ledger/testdata/store/v1`](../../../../lib/ledger/testdata/store/v1/README.md) against any `ledger.Store` |
| `supervisor` | Generic local component processes with recovery for unexpected exits, projecting their state into a `ledger.Store` ([the supervisor](supervisor/README.md)) |
| `recorder` | A nested module: a thin entry point to the C recorder ([the recorder](recorder/README.md)) |

The store's contract is data: `lib/ledger/testdata/store/v1` holds its entities, rules, error
codes, layouts and operation cases, and every implementation runs them
([the fixtures](../../../../lib/ledger/testdata/README.md)). The C library is the main
implementation; this package is the fallback for programs that load no C.
