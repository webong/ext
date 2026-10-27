# system

`github.com/webong/ext/res/system` is the reusable local operating-system layer
for extending graphical and textual shells. It observes hosts, processes,
filesystems, web views, displays, windows, accessibility and permission state;
it also supplies bounded native mechanisms such as surface capture and input,
media capture/playback, volume snapshot/restore, local network-state
observation, and process supervision.

The boundary is local mechanism, not read-only behavior. Consumers choose the
target and own authorization, approval UI, remote protocols, routing, retention
and product policy.

| Package | What it is |
| --- | --- |
| `system` (the module root) | Host resource capacity and container state, native desktop, display/window, accessibility, permission-state, shell, filesystem and web view discovery; process inspection; bounded window or caller-selected driver capture and atomic input |
| `system/audio` | Bounded, transport-free audio-device observation and caller-selected PCM16 capture/playback interfaces; native PulseAudio, WASAPI, and platform playback implementations select no device |
| `system/clipboard` | Bounded, transport-free clipboard contracts, the Apple and Android native implementations shipped by the platform SDKs, and the Win32 backend for Go consumers |
| `system/credential` | Portable credential-store adapter framing and the local CTX credential client; native stores remain product adapters |
| `system/graph` | A generic, namespaced, directed graph store with transactions, in memory or in a file |
| `system/media` | Bounded PCM16 and RIFF/WAVE framing; image sampling/change detection, crop, annotation and contact sheets; caller-supplied video frame sampling; recording options and stream contract |
| `system/network` | Bounded, read-only local interface, assigned-address, route and resolver observation; opens no connection and supplies no remote service |
| `system/service` | Portable per-user service definitions (launchd, systemd, Task Scheduler), non-overwriting publication, registration, start/stop and status through a caller-selected manager; no product identity, layout, port, or health protocol |
| `system/share` | Bounded, transport-free share and keyboard-extension model: text, links, and attachment references; no extension registry, event schema, inbox, or application identity |
| `system/supervisor` | Process supervision and recovery |
| `system/volume` | Bounded local snapshot, restore, archive digest, and pinned private-directory creation/removal for a caller-selected volume or directory |
| `ios/` (`ExtSystem`) | Apple share-extension normalization and keyboard-extension editing helpers, surface/accessibility/permission APIs, macOS capture and input, Accessibility actions, typed Apple Events, ScreenCaptureKit and ReplayKit capture, and bounded PCM playback |
| `android/` | App-visible share-intent and `InputMethodService` helpers, surface/accessibility/permission APIs, MediaProjection capture, and bounded PCM playback |
| `windows/` (`Ext.System.Windows`) | Windows WASAPI endpoint discovery, capture, bounded PCM playback, and generation-checked Win32 clipboard access for .NET consumers |

The graph store is domain-neutral: callers own their node and edge vocabulary, and the discovery
code in the module root records what it finds through it. CTX and other consumers share this
library rather than each implementing their own.

Product-specific provider discovery belongs in the owning adapter under
`adapters/<name>/`. Calling Core Graphics, ScreenCaptureKit, AVFoundation,
User32, GDI or WASAPI to implement a public system API is a platform
implementation, not a product adapter.

The Apple and Android bindings live under `ios/` and `android/` and ship as the
`ExtSystem` and `io.github.webong.ext.system` products in the native SDKs.
The network package's native bindings live under `network/ios` and
`network/android` and retain the `ExtNetwork` and
`io.github.webong.ext.network` product names for source compatibility.
Android observes app-visible surfaces, accessibility structure and permission
state and exposes caller-consented MediaProjection capture and bounded PCM
playback. Apple provides bounded playback and ReplayKit app capture, while
macOS additionally provides desktop capture, audio and input. Accessibility
text is opt-in and surface operations use opaque observed identities rather
than exposing native handles.

No implementation prompts for permission, opens settings, selects a target,
defines grants, shows approval UI, or provides remote routing. Those decisions
remain with CTX, Cymonkey or another consumer.

The Go surface collector uses Core Graphics on macOS and User32 on Windows.
The same Go API revalidates an observed window before capture or atomic input;
Windows capture uses GDI and macOS capture uses the system Core Graphics symbol
when the OS still provides it. Apple clients should prefer `ExtSystem`'s
ScreenCaptureKit implementation on macOS 14 and newer.
Linux X11 builds dynamically inspect root geometry and visible windows without
a link-time X11 dependency; monitor partitions are reported as partial. A
Wayland-only session reports global surface observation as unsupported because
the compositor does not expose that state to arbitrary clients.

The module was called `res/graph` until it was renamed to say what it is for. The generic store
is now `res/system/graph`, the discovery package that lived in `res/graph/system` is now the
module root, and `res/system/supervisor` is unchanged. See the migration table in
[module versioning](../../docs/module-versioning.md).

See [host discovery](../../docs/host-discovery.md) and the
[graph runtime decision record](../../docs/adr-graph-runtime.md). The broader
ownership and safety boundary is documented in the
[local system shell library](../../docs/system-shell.md).
