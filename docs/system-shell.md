# Local system shell library

`res/system` is ext's base layer for extending a machine's graphical and
textual shell. It owns portable contracts and native implementations for local
OS mechanisms. It is not restricted to observation.

The ownership test is:

- If the operation is a reusable local OS mechanism—surface discovery,
  capture, accessibility, atomic input, image/media processing and playback,
  clipboard access, local interface/address/route/resolver observation,
  filesystem or volume mechanics, process lifecycle, permission state, or
  shell discovery—it belongs in `res/system`.
- If behavior selects a product, provider, browser, or native service, it
  belongs in that product's adapter.
- If behavior grants authority, asks for approval, exposes a remote interface,
  maps semantic commands, routes a session, retains credentials, or applies
  product policy, it belongs to the consumer. Cymonkey is one such consumer.

Calling an OS framework does not make a platform implementation an adapter.
Core Graphics, Accessibility, ScreenCaptureKit, AVFoundation, User32, GDI and
WASAPI implementations of `res/system` contracts stay beside those contracts.
Likewise, application-owned WKWebView, Android WebView and WebView2
implementations stay in `res/web`; browser-profile discovery and product
installation stay in browser adapters.

## Safety shape

Local actions are intentionally narrower than a control product:

- callers pass an explicitly observed target;
- process generations and opaque surface identities are revalidated;
- capture allocation and encoded output are bounded;
- input operations are atomic and require a focused target;
- media playback reports backpressure;
- media framing and capture processing impose byte, pixel, frame and time limits;
- archive restore uses a pinned empty destination and decompression limits;
- private-directory removal revalidates the inspected native identity and refuses links, reparse points, roots, home/temp/system directories, and mounted volume roots; on Windows it also refuses a DACL that grants write access to Everyone, Authenticated Users, Users, Guests, or Anonymous;
- network observation opens no connection, performs no lookup and sends no traffic;
- native bytes are returned directly, without a base64 or network envelope;
- libraries never add allowlists, remote endpoints, approval UI or grants.

Consumers compose these mechanisms with their own trust and session model.
For example, Cymonkey can approve a surface, invoke `res/system`, encode the
result for its interface, and record an audit event without maintaining another
copy of the native implementation.

## Current platform coverage

| Mechanism | Go/macOS | Go/Windows | Apple SDK | .NET/Windows | Android |
| --- | --- | --- | --- | --- | --- |
| host/process/filesystem observation | yes | yes | platform subset | — | platform subset |
| host resource capacity, container, uptime | yes | yes | — | — | — |
| surface/accessibility/permission observation | yes | yes | yes | — | app-visible subset |
| window capture and atomic input | yes, where the legacy Core Graphics symbol remains | yes | macOS, ScreenCaptureKit/Accessibility | — | — |
| accessibility actions and typed Apple Events | — | — | macOS | — | — |
| screen/system-audio/microphone capture | — | — | macOS and iOS (ReplayKit) | WASAPI audio | MediaProjection |
| PCM playback | — | — | macOS and iOS | WASAPI | yes |
| bounded image crop, annotation and contact sheets | yes | yes | through Go consumers | — | — |
| bounded volume snapshot/restore | yes | yes | through Go consumers | — | — |
| bounded private-directory create/remove | yes | yes | through Go consumers | — | — |
| bounded clipboard contracts and native access | contract | Win32 backend | macOS and iOS | Win32 | yes |
| credential framing/client contract | yes | yes | — | — | — |
| local interface/address/route/resolver observation | yes | yes | interfaces and addresses | — | yes |
| share and keyboard-extension normalization | contract | — | iOS | — | yes |
| per-user service definitions and lifecycle | launchd | Task Scheduler | — | — | systemd adapter |
| audio-device observation and bounded PCM streams | contract | — | playback | WASAPI audio | playback |
| desktop capture, input, recording via external tools | — | — | — | — | X11 and Wayland adapters |

## Per-user services

`res/system/service` renders and publishes per-user launchd property lists,
systemd units, and Task Scheduler XML. Publication stages a complete file and
links it into place, so an existing file is never replaced silently and a
filesystem without hard links fails closed. Registration, unregistration,
start, stop, and status go through a `service.Manager` the consumer selects;
the shared package never picks a backend by name.

launchd and Task Scheduler are the operating system's own mechanism and ship in
`res/system/service/launchd` and `res/system/service/taskscheduler`. systemd's
`systemctl --user` is a selectable native tool, so its backend lives in
`adapters/systemd`. The consumer supplies the service name, executable,
arguments, environment, working directory, restart policy, definition
directory, and an optional readiness callback; the library supplies no product
name, installation layout, port, or health endpoint.

## Linux audio and desktop tools

`res/system/audio` defines device observation and bounded PCM16 capture and
playback interfaces. `adapters/pulseaudio` implements them with `pactl`,
`parec`, and `pacat`; `adapters/alsa` implements them with `aplay -L`,
`arecord -L`, `aplay`, and `arecord`. Neither selects a device or adds routing,
mixing, or transport policy. PipeWire's PulseAudio compatibility presents the
PulseAudio protocol; a native PipeWire backend would be a separate adapter.

`res/system.DesktopDriver` is the portable seam for desktop-scoped operations by
external tools. `adapters/x11` implements capture, keyboard and pointer input,
and bounded `ffmpeg` recording with `import` and `xdotool`; `adapters/wayland`
implements capture, keyboard input, and optional pointer input through an
explicitly configured `ydotool` (`ydotoold`/uinput). Wayland window targeting
and recording stay explicit gaps: targeting is denied to arbitrary clients, and
recording needs a compositor recorder or an XDG portal approval flow that
belongs to the consumer. Window-scoped
capture and input remain in `res/system` because command-line tools cannot
address its opaque window identities.

## Commands

`ctx` exposes the contracts that are available to a Go program:

```sh
ctx service render --platform linux --name example-agent --executable /usr/local/bin/agent
ctx service status --name com.example.agent --executable /usr/local/bin/agent --dir ~/Library/LaunchAgents
ctx desktop capture --window window:<opaque-id> --out capture.png
ctx desktop input --window window:<opaque-id> --text "hello"
ctx clipboard generation
ctx clipboard read --format CF_UNICODETEXT --json
ctx clipboard write --generation <generation> --format CF_UNICODETEXT --text "hello"
```

`ctx service` renders every platform and runs the lifecycle on macOS and Windows,
where launchd and Task Scheduler are the operating system mechanisms. Linux
lifecycle is intentionally not linked into `ctx`: the `systemctl --user` backend
is `adapters/systemd`, and a product may depend on it without ctx core choosing
a native tool. `ctx desktop` uses window-scoped native capture and input where
`res/system` implements them; Linux desktop tools are the X11 and Wayland
adapters. `ctx clipboard` currently has only the Windows Go backend; the Apple
and Android bindings ship in the SDKs.

## On-device models

`res/ml` defines portable model manifests, verified artifact installation,
model inspection, and bounded inference through a caller-selected provider.
The iOS CoreML binding ships through `ExtML` in `pkg/ext-ios`; the Android
NNAPI/TensorFlow Lite binding compiles into the `pkg/ext-android` AAR. The
shared library owns no model download, provider selection, authorization, or
remote protocol.

Missing cells are implementation gaps, not a reason to place the mechanism in
a consumer. New native implementations should preserve the same bounded,
transport-free contract. Credential-store implementations are deliberately not
gaps in this table: Keychain, Credential Manager and Secret Service remain
selectable native-service adapters.
