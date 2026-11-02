# system

`github.com/webong/ext/res/system` is the reusable local operating-system layer
for extending graphical and textual shells. It observes hosts, processes,
filesystems, web views, displays, windows, accessibility and permission state;
it also supplies bounded native mechanisms such as surface capture and input,
media capture/playback, volume snapshot/restore, and local network-state
observation.

The boundary is local mechanism, not read-only behavior. Consumers choose the
target and own authorization, approval UI, remote protocols, routing, retention
and product policy.

The module has no root package. Its packages sit in three groups, which are plain
directories, plus the .NET library. Each package owns its native Swift, Java and
Windows code beside its Go code, and the SDKs (`pkg/ext-ios`, `pkg/ext-android`) ship
it from there.

## service: what the host provides

| Package | What it is |
| --- | --- |
| `service/host` | Shells, mounted filesystems, web views and host resource capacity (CPU, memory, storage, power, container state). Discovery only; nothing is launched |
| `service/network` | Bounded, read-only local interface, assigned-address, route and resolver observation; opens no connection and supplies no remote service |
| `service/process` | Process discovery and inspection with bounded resource detail; the lightweight `Lookup` identifies the process behind a window |
| `service/program` | Finds executables by name on `PATH` and joins them with what an installer reports about the same file, by resolved path; mechanism only, it never branches on a program or installer name |
| `service/credential` | Portable credential-store adapter framing (`adapterkit`) and the local CTX credential client (`client`); native stores remain product adapters |
| `service/user` | Portable per-user service definitions (launchd, systemd, Task Scheduler), non-overwriting publication, registration, start/stop and status through a caller-selected manager; no product identity, layout, port, or health protocol |

## capture: content and events crossing the device edge

| Package | What it is |
| --- | --- |
| `capture/surface` | Displays, windows, accessibility structure and permission state; window capture and atomic input; the `DesktopDriver` seam that caller-selected tools implement |
| `capture/audio` | Bounded, transport-free audio-device observation and caller-selected PCM16 capture/playback interfaces; native implementations select no device |
| `capture/clipboard` | Bounded, transport-free clipboard contracts, the Apple and Android native implementations shipped by the platform SDKs, and the Win32 backend for Go consumers |
| `capture/share` | Bounded, transport-free share and keyboard-extension model: text, links, and attachment references; no extension registry, event schema, inbox, or application identity |
| `capture/media` | Bounded PCM16 and RIFF/WAVE framing; picture-image sampling/change detection, crop, annotation and contact sheets; caller-supplied video frame sampling; recording options and stream contract |

## device: volumes and disk images

| Package | What it is |
| --- | --- |
| `device/volume` | Pinned private-directory creation, inspection and removal for a caller-selected path |
| `device/image` | Bounded snapshot (`Export`), restore and archive digest of a caller-selected directory tree. It is a tar.gz snapshot today; real disk-image tooling (`hdiutil`, VHD) would be a native tool owned by an adapter |

"Device" here means storage devices. Audio devices are `capture/audio`.

## Native code

| Path | What it is |
| --- | --- |
| `capture/surface/ios` (`ExtSystem`) | Apple surface/accessibility/permission APIs, macOS capture and input, Accessibility actions, typed Apple Events, ScreenCaptureKit and ReplayKit capture |
| `service/host/ios` (`ExtHost`) | Apple host-resource observation |
| `capture/share/ios` (`ExtShare`) | Apple share-extension normalization and keyboard-extension editing helpers |
| `capture/audio/ios` (`ExtAudio`) | Bounded PCM playback |
| `capture/clipboard/ios`, `service/network/ios` | `ExtClipboard`, `ExtNetwork` |
| `*/android` beside each package | App-visible share-intent and `InputMethodService` helpers, surface/accessibility/permission/resource APIs, MediaProjection capture, bounded PCM playback, clipboard and network observation. Java packages stay `io.github.webong.ext.system.*`, `...clipboard`, `...network` |
| `windows/` (`Ext.System.Windows`) | Windows WASAPI endpoint discovery, capture, bounded PCM playback, and generation-checked Win32 clipboard access for .NET consumers. It still serves two packages (`capture/audio`, `capture/clipboard`) from one assembly |

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

Product-specific provider discovery belongs in the owning adapter under
`adapters/<name>/`. Calling Core Graphics, ScreenCaptureKit, AVFoundation,
User32, GDI or WASAPI to implement a public system API is a platform
implementation, not a product adapter.

## The graph

The graph store, the recorder that writes what these collectors find into it, and the
process supervisor live in the module [`res/graph`](../graph/README.md), which requires
this module. Nothing here depends on the graph. The path `res/graph` was used briefly
for an earlier form of this module (v0.1.x, deprecated); it now names the graph module.
See the migration table in [module versioning](../../docs/module-versioning.md).

See [host discovery](../../docs/host-discovery.md) and the
[graph runtime decision record](../../docs/adr-graph-runtime.md). The broader
ownership and safety boundary is documented in the
[local system shell library](../../docs/system-shell.md).
