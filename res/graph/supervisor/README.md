# supervisor

`github.com/webong/ext/res/graph/supervisor` runs generic local component processes and
projects their state, with recovery for unexpected exits.

Deprecated: it is now part of [`lib/ledger`](../../../lib/ledger/README.md#the-supervisor)
(`ext_ledger_supervisor_*`), and this package is kept working only until its Go consumers move
to the C library and `res/graph` is removed.

CTX uses it when a selected backend does not already own its process lifecycle,
for example local process plugins. Platform-specific process identity and
signals live in the `_unix` and `_windows` files.

A state seen through `Instance` is already in the graph: the supervisor applies each
lifecycle fact before it releases the instance, so a caller that waits for `ready` or
`stopped` can read the matching graph fact straight away. The graph store is therefore
written to while the instance lock is held and should not block for long.

See [the graph runtime decision record](../../../docs/adr-graph-runtime.md) for how
supervision relates to process discovery.
