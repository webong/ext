# graph

`github.com/webong/ext/res/graph` records what is observed about a machine, its
adapters and its shell sessions in a graph, and supervises local processes while
projecting their state into it.

| Package | What it is |
| --- | --- |
| `graph` (the module root) | The recorder. It projects observations onto the `ext.system` vocabulary and reads them back: `Graph`, `Open` and `New`; `ObserveShell` and `ObserveWeb`; `ScanHost` and `ObserveHost`; `ScanProcesses` and `ObserveProcesses`; `ObserveInventory` and `ResolveInventory` for adapters, contexts, installers and programs; and `ReadHost`, `ResolveHost` and `PrepareHost` for host requirements |
| `store` | A generic, namespaced, directed graph store with transactions and declarative namespace schemas. It is domain-neutral: callers own their node and edge vocabulary, and several services can keep their own namespaces in one store. `store.NewMemory` keeps it in memory |
| `store/sqlite` | The durable store: the same contract over one SQLite database, in pure Go (`modernc.org/sqlite`). `graph.Open` uses it, at `<config>/graph.db` |
| `store/storetest` | Runs the shared store fixtures against any `store.Store` |
| `supervisor` | Generic local component processes with recovery for unexpected exits, projecting their state into a `store.Store` |

A state seen through `supervisor.Instance` is already in the graph: the supervisor
applies each lifecycle fact before it releases the instance. See
[the supervisor](supervisor/README.md).

The recorder takes its observations from the collectors in
[`res/system`](../system/README.md) (`Scan*` calls a collector and then records the result;
`Observe*` records a result the caller already holds). `res/graph` requires `res/system`;
`res/system` never requires `res/graph`. The store and the supervisor use the standard
library only; `store/sqlite` adds `modernc.org/sqlite`.

The store's contract is data: [`testdata/store/v1`](testdata/store/v1/README.md) holds its
entities, rules, error codes, SQLite layout and operation cases, and every implementation runs
them ([the fixtures](testdata/README.md)).

The recorder currently describes the local machine, under the vertex `machine/local`. It
does not authorize use of an adapter or prove that a remote context is running; callers
still check trust and validate native selections.

CTX and other consumers share this library rather than each implementing their own. The
graph namespace and the vertex and edge identifiers are persisted in the graph database and do
not change between versions. The `ext.system` vocabulary is the declarative schema
`graph.Schema()`; change `graph.SchemaVersion` whenever its kinds or relationships change.

The path `res/graph` was used for an earlier form of the module that is now `res/system`
(v0.1.x, deprecated); this module reuses the path from v0.2.0. See the migration table in
[module versioning](../../docs/module-versioning.md), the
[graph runtime decision record](../../docs/adr-graph-runtime.md) and
[host discovery](../../docs/host-discovery.md).
