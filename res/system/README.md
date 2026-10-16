# system

`github.com/webong/ext/res/system` is how CTX observes the machine it runs on: the operating
system, its file systems, its shells and web views, and the processes running on it. It reports
what is there without starting anything, and can record it in a graph.

| Package | What it is |
| --- | --- |
| `system` (the module root) | Host, shell, filesystem and web view discovery, process inspection, and the requirements a host has to meet |
| `system/graph` | A generic, namespaced, directed graph store with transactions, in memory or in a file |
| `system/supervisor` | Process supervision and recovery |

The graph store is domain-neutral: callers own their node and edge vocabulary, and the discovery
code in the module root records what it finds through it. CTX and other consumers share this
library rather than each implementing their own.

Product-specific provider discovery belongs in the owning adapter under `adapters/<name>/`. This
module supplies the portable collectors and vocabulary only.

The module was called `res/graph` until it was renamed to say what it is for. The generic store
is now `res/system/graph`, the discovery package that lived in `res/graph/system` is now the
module root, and `res/system/supervisor` is unchanged. See the migration table in
[module versioning](../../docs/module-versioning.md).

See [host discovery](../../docs/host-discovery.md) and the
[graph runtime decision record](../../docs/adr-graph-runtime.md).
