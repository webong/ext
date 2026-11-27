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

The module has no root package. Its existing Go packages sit in three groups,
which are plain directories. Their Swift, Java and C# implementations now live
in `lib/system` and ship through the platform SDKs. The Go import paths stay
available during the migration to Ext resource guests.

## service: what the host provides

| Package | What it is |
| --- | --- |
| `service/network` | Versioned local interface, address, route and resolver records and validation; native observation runs in `lib/system/net` through Ext resource guests |
| `service/credential` | Portable credential-store adapter framing (`adapterkit`) and the local CTX credential client (`client`); native stores remain product adapters |
| `service/user` | Go manager contract and OS manager integrations. Rendering and safe publication execute in `lib/ledger` through the injected `Definitions` interface |

The Go host inventory, exact path checks, and executable checks moved to
`lib/ledger` and its `pkg/ext-go/cengine/ledger` binding. CTX owns installer
declaration expansion and candidate merging in `src/ctx/internal/program`.
`service/host` and `service/program` have no Go packages.
`service/process` has no Go package: the C ledger discovers processes, inspects
resources, and supplies birth tokens. CTX desktop actions take window observations
and process birth tokens from the ledger. `capture/surface` retains the portable
snapshot and action contracts, and accepts a caller-supplied birth-token lookup
when it revalidates a target before native capture or input.

## capture: content and events crossing the device edge

| Package | What it is |
| --- | --- |
| `capture/surface` | Portable display and window records, bounds, and the `ext.system.surface/v1` capture/input request contract. The C ledger observes windows; installed macOS and Windows resource guests use native libraries in `lib/system/act/capture/surface` to act on them. `DesktopDriver` remains the seam for caller-selected desktop tools |
| `capture/audio` | Bounded, transport-free audio-device observation and caller-selected PCM16 capture/playback interfaces; native implementations select no device |
| `capture/clipboard` | Portable clipboard contract and bounds; the native Swift, Java and C# implementations live in `lib/system/act/clipboard`, with a Windows Ext resource guest under `capture/clipboard/windows/Guest` |
| `capture/share` | Bounded, transport-free share and keyboard-extension model: text, links, and attachment references; no extension registry, event schema, inbox, or application identity |
| `capture/media` | Bounded PCM16 and RIFF/WAVE framing; picture-image sampling/change detection, crop, annotation and contact sheets; caller-supplied video frame sampling; recording options and stream contract |

## device: volumes and disk images

| Package | What it is |
| --- | --- |
| Private directories | The C ledger owns create, inspect and bounded removal; Go consumers use `pkg/ext-go/cengine/ledger` |
| `device/dirlock` | A shared directory lock utility used by package and adapter writers |
| `device/image` | Bounded snapshot (`Export`), restore and archive digest of a caller-selected directory tree. It is a tar.gz snapshot today; real disk-image tooling (`hdiutil`, VHD) would be a native tool owned by an adapter |

"Device" here means storage devices. Audio devices are `capture/audio`.

## Native code

| Path | What it is |
| --- | --- |
| `lib/system/act/capture/surface/ios` (`ExtSystem`) | Apple surface/accessibility/permission APIs, macOS capture and input, Accessibility actions, typed Apple Events, ScreenCaptureKit and ReplayKit capture |
| `pkg/ext-darwin/Sources/ExtHost` | Swift binding to C ledger host-resource observation |
| `lib/system/act/share/ios` (`ExtShare`) | Apple share-extension normalization and keyboard-extension editing helpers |
| `lib/system/act/capture/audio/ios` (`ExtAudio`) | Bounded PCM playback |
| `lib/system/act/clipboard/ios`, `lib/system/net/ios` | `ExtClipboard`, `ExtNetwork` |
| Android implementations beside those libraries | App-visible share-intent and `InputMethodService` helpers, surface/accessibility/permission/resource APIs, MediaProjection capture, bounded PCM playback, clipboard and network observation. Java packages stay `io.github.webong.ext.system.*`, `...clipboard`, `...network` |
| `lib/system/act/capture/audio/windows` (`Ext.Audio.Windows`) | Windows WASAPI endpoint discovery, system or microphone capture and bounded PCM playback for .NET consumers (NAudio) |
| `lib/system/act/clipboard/windows` (`Ext.Clipboard.Windows`) | Generation-checked Win32 clipboard access for .NET consumers, with no package dependencies |

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

Product-specific provider discovery belongs in the owning adapter, outside ext
(see [adapter repositories](../../docs/adapter-repositories.md)). Calling Core Graphics, ScreenCaptureKit, AVFoundation,
User32, GDI or WASAPI to implement a public system API is a platform
implementation, not a product adapter.

### Streaming Apple capture audio

`ExtSystem.Float32WAVStream` accepts borrowed `CMSampleBuffer` and
`AVAudioPCMBuffer` values directly from capture callbacks. Append each buffer
while it is valid and send the returned bytes to the destination in order. For
example, a `SystemMediaSink` can feed system audio and microphone buffers to
separate streams and write their chunks to reader-owned pipes. Serialize
appending and writing for each stream if callbacks arrive on different queues.

The first chunk includes an IEEE Float32 WAV header with unknown lengths for
streaming readers. Samples are little-endian and interleaved in the input
channel order. Each append is bounded by frame count and output bytes, including
the first header. The first successful append fixes the sample rate and channel
layout. Open a new stream if they change. An unsuccessful append throws
`Float32WAVStreamError` and leaves the stream usable. At capture end or
cancellation, call `close()` and signal EOF on the destination.

This covers PCM conversion and streaming framing when a consumer needs to send
native capture audio to a reader such as FFmpeg. Go's `capture/media.WAV`
frames finite PCM16 data.

## The graph

The graph store, the recorder that writes what these collectors find into it, and the
process supervisor are the ledger: [`lib/ledger`](../../lib/ledger/README.md) in C and
[`pkg/ext-go/cengine/ledger`](../../pkg/ext-go/cengine/ledger/README.md) for its Go binding.
The Go ledger uses its own wire records in `core/ledger/observation` and does not require this module. Nothing here depends on the ledger. The path `res/graph` was used briefly
for an earlier form of this module (v0.1.x, deprecated), and then for the Go graph module until it moved
into `pkg/ext-go/engine/ledger`.
See the migration table in [module versioning](../../docs/module-versioning.md).

See [host discovery](../../docs/host-discovery.md) and the
[graph runtime decision record](../../docs/adr-graph-runtime.md). The broader
ownership and safety boundary is documented in the
[local system shell library](../../docs/system-shell.md).
