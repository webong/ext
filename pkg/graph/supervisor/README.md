# supervisor

`github.com/webong/ext/pkg/graph/supervisor` runs generic local component processes and
projects their state, with recovery for unexpected exits.

CTX uses it when a selected backend does not already own its process lifecycle,
for example local process plugins. Platform-specific process identity and
signals live in the `_unix` and `_windows` files.

See [the graph runtime decision record](../../../docs/adr-graph-runtime.md) for how
supervision relates to process discovery.
