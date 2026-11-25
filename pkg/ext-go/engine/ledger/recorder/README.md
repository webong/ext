# Recorder entry point

This former Go recorder module now delegates to `pkg/ext-go/cengine/ledger`.
`New(ctx, store)` requires an explicitly opened `*ledger.NativeStore`. Collection,
recording, storage and inventory resolution execute in `lib/ledger`; there is no
Go collector or SQLite store implementation here.

This is an API migration, not a drop-in replacement: the old `Open`, `DiscoverHost`,
`Schema` helpers and `New` accepting a Go store were removed. New consumers should
use `cengine/ledger.Bind`, `Library.OpenStore` and `ledger.NewGraph` directly.
Host-selection and preparation policy belongs to the consuming application.

CTX retains a read-only importer for existing `graph.db` files. Frozen legacy SQL
fixtures test that migration and native SQLite compatibility without keeping the
retired Go implementation. The C recorder fixtures remain the behavioral contract.
