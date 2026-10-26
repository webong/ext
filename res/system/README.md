# system

`github.com/webong/ext/res/system` is how CTX observes the machine it runs on: the operating
system, its native desktop displays and windows, accessibility structure,
permission state, file systems, shells and web views, and the processes running on it. It reports
what is there without starting anything, and can record it in a graph.

| Package | What it is |
| --- | --- |
| `system` (the module root) | Host, native desktop, display/window, accessibility, permission-state, shell, filesystem and web view discovery, process inspection, and the requirements a host has to meet |
| `system/graph` | A generic, namespaced, directed graph store with transactions, in memory or in a file |
| `system/supervisor` | Process supervision and recovery |

The graph store is domain-neutral: callers own their node and edge vocabulary, and the discovery
code in the module root records what it finds through it. CTX and other consumers share this
library rather than each implementing their own.

Product-specific provider discovery belongs in the owning adapter under `adapters/<name>/`. This
module supplies the portable collectors and vocabulary only.

The Apple and Android bindings live under `ios/` and `android/` and ship as the
`ExtSystem` and `io.github.webong.ext.system` products in the native SDKs. They
observe surfaces, app-visible accessibility structure and permission state.
Accessibility text is opt-in and native handles are never exposed. The library
does not capture pixels, send input, prompt for permission, open settings, show
approval UI, or provide remote routing.

The Go surface collector uses Core Graphics on macOS and User32 on Windows.
Linux X11 builds dynamically inspect root geometry and visible windows without
a link-time X11 dependency; monitor partitions are reported as partial. A
Wayland-only session reports global surface observation as unsupported because
the compositor does not expose that state to arbitrary clients.

The module was called `res/graph` until it was renamed to say what it is for. The generic store
is now `res/system/graph`, the discovery package that lived in `res/graph/system` is now the
module root, and `res/system/supervisor` is unchanged. See the migration table in
[module versioning](../../docs/module-versioning.md).

See [host discovery](../../docs/host-discovery.md) and the
[graph runtime decision record](../../docs/adr-graph-runtime.md).
