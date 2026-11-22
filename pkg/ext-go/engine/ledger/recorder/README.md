# recorder

> The pure-Go recorder: [`lib/ledger`](../../../../../lib/ledger/README.md), the ledger in C, is
> the main implementation and this one follows it as the fallback for programs that load no C.
> New graph behaviour lands in `lib/ledger` and its fixtures in
> [`lib/ledger/testdata`](../../../../../lib/ledger/testdata/README.md) first, which stay the contract.

`github.com/webong/ext/pkg/ext-go/engine/ledger/recorder` records what is observed about a
machine, its adapters and its shell sessions in a ledger. It is its own Go module, nested in
the engine module, because it projects `res/system`'s types: the engine module uses only the
standard library, and this module requires it and [`res/system`](../../../../../res/system/README.md).

| Package | What it is |
| --- | --- |
| `recorder` (the module root) | The recorder. It projects observations onto the `ext.system` vocabulary and reads them back: `Graph`, `Open` and `New`; `ObserveShell` and `ObserveWeb`; `ScanHost` and `ObserveHost`; `ScanProcesses` and `ObserveProcesses`; `ObserveInventory` and `ResolveInventory` for adapters, contexts, installers and programs; and `ReadHost`, `ResolveHost` and `PrepareHost` for host requirements |
| `sqlite` | The durable store: the ledger contract over one SQLite database, in pure Go (`modernc.org/sqlite`). `recorder.Open` uses it, at `<config>/graph.db`. It goes when the Go file backend on the C library's layout (`ledger.json`) replaces it |

The store itself is [`engine/ledger`](../README.md) and the supervisor
[`engine/ledger/supervisor`](../supervisor/README.md), both in the engine module.

The recorder takes its observations from the collectors in `res/system` (`Scan*` calls a
collector and then records the result; `Observe*` records a result the caller already holds).
`res/system` never requires this module.

The recorder currently describes the local machine, under the vertex `machine/local`. It does not
authorize use of an adapter or prove that a remote context is running; callers still check trust
and validate native selections. The graph namespace and the vertex and edge identifiers are
persisted in the graph database and do not change between versions. The `ext.system` vocabulary is
the declarative schema `recorder.Schema()`; change `recorder.SchemaVersion` whenever its kinds or
relationships change.

This module was `res/graph` until v0.3.0; see [module versioning](../../../../../docs/module-versioning.md),
the [graph runtime decision record](../../../../../docs/adr-graph-runtime.md) and
[host discovery](../../../../../docs/host-discovery.md).
