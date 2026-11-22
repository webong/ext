# ledger

`github.com/webong/ext/pkg/ext-go/engine/ledger` is the pure-Go engine of
[`lib/ledger`](../../../../lib/ledger/README.md): a generic, namespaced, directed graph store
with transactions and declarative namespace schemas, using only the standard library. It is
domain-neutral: callers own their node and edge vocabulary, and several services can keep their
own namespaces in one store. `ledger.NewMemory` keeps it in memory.

| Package | What it is |
| --- | --- |
| `ledger` | The store contract and the memory store |
| `ledgertest` | Runs the shared store fixtures in [`lib/ledger/testdata/store/v1`](../../../../lib/ledger/testdata/store/v1/README.md) against any `ledger.Store` |
| `supervisor` | Generic local component processes with recovery for unexpected exits, projecting their state into a `ledger.Store` ([the supervisor](supervisor/README.md)) |
| `recorder` | A nested module: the recorder of `res/system`'s observations and the SQLite store ([the recorder](recorder/README.md)) |

The store's contract is data: `lib/ledger/testdata/store/v1` holds its entities, rules, error
codes, layouts and operation cases, and every implementation runs them
([the fixtures](../../../../lib/ledger/testdata/README.md)). The C library is the main
implementation; this package is the fallback for programs that load no C.
