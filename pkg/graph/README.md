# graph

`github.com/webong/ctx/pkg/graph` provides a generic, namespaced, directed graph
store with transactions, plus `graph/system` for host, shell, filesystem and
webview discovery and process-graph inspection.

The store is domain-neutral: callers own their node and edge vocabulary. CTX,
Xallet, Cymonkey and other consumers share this library rather than each
implementing their own graph.

Product-specific provider discovery belongs in the owning adapter under
`adapters/<name>/`. `graph/system` supplies the portable collectors and
vocabulary only.

See [host discovery](../../docs/host-discovery.md) and the
[graph runtime decision record](../../docs/adr-graph-runtime.md).
