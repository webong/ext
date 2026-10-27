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
| bounded clipboard contracts and native access | contract | contract | macOS and iOS | — | yes |
| credential framing/client contract | yes | yes | — | — | — |
| local interface/address/route/resolver observation | yes | yes | interfaces and addresses | — | yes |
| share and keyboard-extension normalization | contract | — | iOS | — | yes |

Missing cells are implementation gaps, not a reason to place the mechanism in
a consumer. New native implementations should preserve the same bounded,
transport-free contract. Credential-store implementations are deliberately not
gaps in this table: Keychain, Credential Manager and Secret Service remain
selectable native-service adapters.
