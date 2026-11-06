# Handoff: move Cymonkey onto ext

For the chat that works in `~/Workspace/Projects/Akive/cymonkey`. Written 2026-11-04 from the ext
repository at `~/Workspace/Utils/ext` (main at `846af9b9`). Facts marked *verified* were checked against the
code or the published tags; anything marked *unverified* is a guess to confirm first.

## The decision

Ext is the reusable mechanism base. Cymonkey consumes it and never the reverse. Cymonkey keeps only what is
its own: the `display`, `content` and `device` contracts and their plugins, operator policy and approval,
the `cymonkey/v1alpha1` wire protocol, Jangolova, the Console SDK (tool and model composition, MCP
attachments), browser-automation protocol clients (WebDriver Classic, BiDi, Safari MCP), streaming (WebRTC,
VNC gateway) and the runtime integrations (Three.js, Godot, Unity, Unreal, Blender, Lens Studio, Effect
House).

Ext will not take: OCI or other guest-computer provisioning, any network or remote transport (streaming,
WebRTC, VNC), or Cymonkey's Console and browser-automation clients. Do not propose moving those into ext.

So there is nothing left to extract. The work is migration: replace Cymonkey's copies of what ext already
owns with ext's versions, and update the old import paths.

## Rules for this work

- Do not commit, push, tag or open a PR until the user says so in that chat.
- All changes are in the Cymonkey repository. Ext is read-only from there; if ext needs a change, say so and
  the user will switch to the ext chat.
- Keep the `cymonkey/v1alpha1` protocol and all wire behavior unchanged. Run each module's existing tests
  before and after each step.
- Never describe code that ext owns as belonging to Cymonkey, in code, docs or commit messages.

## 1. Go: old import paths to new (verified)

Cymonkey imports ext in these module roots: the root module (71 files), `lib/jangolova` (20), `pkg/board` (5),
`pkg/console` (8), `pkg/content-onnx`, `pkg/content-webllm` and `pkg/content-endpoints` (2 each). They all
require `github.com/webong/ext/pkg/plugin-go/contract v0.7.0`; the root also requires
`github.com/webong/ext/res/web v0.5.0`. Both module paths are retired. `pkg/lens-studio`, `pkg/effect-house`
and `tests/jangolova-blender-live` import no ext code.

| Old import | New import | Module |
| --- | --- | --- |
| `pkg/plugin-go/contract` | `pkg/ext-go/engine` | `github.com/webong/ext/pkg/ext-go/engine` v0.2.0 |
| `.../contract/host`, `host/store`, `host/process`, `host/adapter` | `.../engine/host`, `host/store`, `host/process`, `host/adapter` | same module |
| `.../contract/package` | `.../engine/package` (declared package name `packagekit`) | same module |
| `.../contract/transport/jsonline` | `.../engine/transport/jsonline` | same module |
| `.../contract/validate/jsonschema` | `.../engine/validate/jsonschema` | same module |
| `.../contract/endpoint` | `.../engine/endpoint` | same module |
| `res/web/userscript` | `res/web/host/artifact/userscript` | `github.com/webong/ext/res/web/host` v0.1.0 |
| `res/web/contract` | `res/web/host/contract` | same module |
| `res/web/extension` | `res/web/host/artifact/extension` | same module |
| `res/web/bookmarklet` | `res/web/host/artifact/bookmarklet` | same module |

Proof that all of these resolve: a scratch module outside any workspace, with the default checksum database,
requiring only `pkg/ext-go/engine v0.2.0` and `res/web/host v0.1.0` and importing every new path above,
`go mod tidy` and `go build` both pass, and no old path appears in its graph.

Notes:

- Cymonkey does not use the root `res/web` package (ctx's `browser` alias), so `manage/cookie` is not needed.
- The progress helpers that moved from `res/web/contract` to `artifact/extension` are not used by Cymonkey.
- If Cymonkey wants the engine-routed (C engine) behavior it swaps `engine/host` and `engine/host/store` for
  `pkg/ext-go/cgoengine/host` and `.../host/store`, which have an identical API. Pure Go (`engine`) is the
  baseline and needs no C toolchain.
- Ext's modules declare `go 1.26.0`; Cymonkey's `go 1.26.5` is fine.
- `GOWORK=off` builds of ext modules now work against the tags. Cymonkey has no `go.work`; its sub-modules
  are joined by local replacements, so update each sub-module's `go.mod` and keep the replacements consistent.
- Docs that still use old names: `docs/bookmarklets.md`, `architecture.md`, `augmentation-validation.md`,
  `browser-extension-installation.md`, `cymonkey-plugins.md`, `tachikoma-peekaboo-extraction.md`, the
  `lib/browser-helper/test/fixtures/ext` READMEs.

## 2. Python SDK

`pkg/content-ultralytics/python/ext_plugin` is a vendored copy of the old guest SDK (ext commit 9b323d7, tag
`pkg/plugin/v0.5.0`). The SDK is now the package `ext-io`: `from ext_io import plugin`, with `plugin.serve_stdio`,
`plugin.Guest` and `plugin.RemoteError`; the manifest file is `plugin.json`. Source: `pkg/ext-python` in ext.
Nothing is published on PyPI, so the options are to vendor `ext_io` from a newer ext checkout (the same way) or
install it by path. The old `ext_plugin` import name is gone. *Unverified:* whether the current `ext_io.plugin`
API matches how `content-ultralytics` calls the old one; run its tests after the swap.

## 3. Native helpers: overlap with ext

Each Cymonkey helper wraps the `cymonkey/v1alpha1` protocol (`hello`, `capabilities`, `describe`, `act`,
`events`) around native mechanisms. The mechanisms overlap ext; the protocol, the outward connection and the
policy stay in Cymonkey. Matched on the native APIs each side calls and on file structure, *not* line by line.

| Cymonkey helper | Replace with (ext) | Keep in Cymonkey |
| --- | --- | --- |
| `lib/macos-helper` (2.4k lines): `Accessibility.swift`, `Viewer.swift` (window capture, input), `Pointer.swift`, `Clipboard.swift`, `AppleEvents.swift`, `DeviceMedia.swift`, `DeviceAudio`, `MacOSWebViewScripts` | `ExtSystem` (`Accessibility`, `Surface`, `SurfaceControl`, `MediaCapture`, `DesktopPointer`, `AppleEvent`), `ExtClipboard`, `ExtAudio`, `ExtWebKit` | `Runtime.swift`, `ControlClient.swift`, `Configuration.swift`, `Models.swift` |
| `lib/ios-helper` (620): `WebViewRuntime.swift` (register `WKWebView`s, authorized cookie reads) | `ExtWebKit` (`BrowserWorkspace`, `WorkspaceCookies`), `ExtWeb` | `Protocol.swift`, `DeviceTargetTransport.swift` |
| `lib/android-helper` (1.0k): `AndroidUserScripts.kt` (476), `AndroidWebViewRuntime.kt` | `res/web/android` in the ext AAR (`DocumentStart`, `BrowserWorkspace`, `WorkspaceCookies`) | `DeviceTargetTransport.kt`, `BrowserSurface.kt`, device capture, projection and playback glue |
| `lib/windows-helper` (486): `WebView2BrowserHelper.cs`, `BrowserScriptPolicy.cs`, `NativePointer.cs`, `DeviceAudio.cs` | `Ext.Web.Windows` (WebView2 workspace), `Ext.Audio.Windows`; capture and pointer in `res/system` | `Program.cs` (outward WebSocket, owner-configured executables) |

Seen in both code bases: `AXUIElement`, `CGEvent`, ScreenCaptureKit (`SCStream`), `NSPasteboard` (macOS);
`addDocumentStartJavaScript` and `CookieManager` (Android); `AddScriptToExecuteOnDocumentCreated` and
`CoreWebView2` (WebView2). The iOS match is weaker: ext uses `WKUserScript`, the Cymonkey helper does not.

**First check, before swapping the macOS helper:** Cymonkey uses ScreenCaptureKit in 5 files and ext in 2.
Diff the macOS capture path against `ExtSystem`'s `MediaCapture` and list anything Cymonkey does that ext
cannot (formats, region capture, audio, permissions). If there is something, report it to the user: it is the
one case where ext would take code from Cymonkey. Do not port it yourself.

Suggested order: Android (its AAR is released), then macOS, then iOS and Windows once they are consumable
(see section 5).

## 4. JavaScript: `lib/browser-helper`

Cymonkey's `lib/browser-helper` (4.7k lines) is the origin of ext's `pkg/ext-web` (peer messaging, in-browser
management, workspace, userscript registry, page bridge, themes). Ext owns it now. Cymonkey-specific parts
(application commands, active-tab selection, package policy, external installation actions) stay in Cymonkey
on top of it. *Verified:* ext's `pkg/ext-web` has 26 tests and its examples pass ext's Go packager.

Known behavior to design around: the userscript registry derives a browser registration ID from a namespaced
prefix plus the script ID and caps it at 128 characters, while the Go userscript rules allow a 128-character
script ID. Call `registry.registrationId(id)` inside `prepare` to refuse an over-long script early.

`pkg/ext-web` is `private: true` and is not on the npm registry. Ext is adding a release workflow
(`ext-web-release.yml`) that attaches `ext-web-X.Y.Z.tgz` to a GitHub release `ext-web-vX.Y.Z`; until the first
such tag exists Cymonkey cannot install it. See section 5.

## 5. How to consume each ext SDK today (verified from the published releases)

| SDK | Where it is | Notes |
| --- | --- | --- |
| Go modules | Go proxy, tags `<module>/vX.Y.Z` | `pkg/ext-go/engine` v0.2.0, `cgoengine` v0.2.0, `pkg/ext-go` v0.10.1, `wasm` v0.4.0, `res/web/host` v0.1.0, `res/system` v0.3.0, `res/graph` v0.2.0, `res/int` v0.1.0, `pkg/ext-go/inference`, `model`, `perception`, `realtime` v0.1.0 |
| Swift (iOS, macOS) | GitHub release `ext-ios-v0.1.0`: `ext-ios-0.1.0-package.tar.gz` plus `CExtEngine` and `CExtWamr` xcframeworks | Package `Ext`, platforms macOS 12 and iOS 13. Products include `ExtWeb`, `ExtWebKit`, `ExtSystem`, `ExtClipboard`, `ExtAudio`, `ExtHost`, `ExtShare`, `ExtML`, `ExtNetwork`. **On SwiftPM since `ext-ios-v0.1.1`:** `.package(url: "https://github.com/webong/ext", from: "0.1.1")` resolves through the `dist/ext-ios` branch and the bare `0.1.1` tag (verified). The `v0.1.0` release has no SwiftPM tag; use 0.1.1 or later. |
| Android | GitHub release `ext-android-v0.1.0`: `ext-android-0.1.0.aar` and `ext-android-wamr-0.1.0.aar` | Not on Maven. Add the AAR as a local file dependency. The AAR ships the web workspace and `io.github.webong.ext.system`. |
| .NET (Windows) | GitHub release `ext-dotnet-v0.1.0`: `Ext.Web.Windows`, `Ext.Audio.Windows`, `Ext.Clipboard.Windows`, `Ext.ML.Windows` as `.nupkg` | No NuGet feed. Point a local NuGet source at the downloaded packages. |
| Python | `pkg/ext-python` (`ext-io`) | Not published. Vendor or install by path. |
| JavaScript | `pkg/ext-web` | **Released as `ext-web-v0.1.0`** (2026-11-04): `npm install https://github.com/webong/ext/releases/download/ext-web-v0.1.0/ext-web-0.1.0.tgz`, checksum in `checksums.txt` beside it (verified; all six entry points import). Not on the npm registry. |

Checksums are in each release's `checksums.txt`.

## 6. Plan

1. In Cymonkey, run every module's tests to record a green baseline.
2. Go migration, one module at a time (start with a small one, `pkg/content-onnx`): rewrite imports with the
   table in section 1, change the `require` lines to the new modules and versions, `go mod tidy`, run tests.
   Then `pkg/content-webllm`, `pkg/content-endpoints`, `pkg/console`, `pkg/board`, `lib/jangolova`, and the root
   last. Update the docs listed in section 1.
3. Python: swap the vendored SDK (section 2).
4. Native helpers in the order of section 3, after the ScreenCaptureKit check.
5. `lib/browser-helper` on top of `pkg/ext-web` once ext can ship it.
6. Delete each Cymonkey copy only after its tests pass on the ext version.

## 7. What ext still has to do (the ext chat will handle these)

- Done 2026-11-04: `ext-ios-v0.1.1` (SwiftPM via `dist/ext-ios`) and `ext-web-v0.1.0` (tarball release).
- Still open: the macOS ScreenCaptureKit comparison in section 3, and the coordinator's naming plan for
  `ext-web` (the tag family `ext-web-vX.Y.Z` is the one in use today).
- A NuGet feed, or documented local-source instructions, for the Windows libraries.
- Decide whether to publish ext-io on PyPI.

If you hit a missing API in ext, report it with the exact symbol and the Cymonkey call site that needs it.
