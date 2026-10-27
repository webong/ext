# Apple SDK

`pkg/ext-ios` is the ext SDK for Apple platforms. Its plugin, web, clipboard,
system-observation, and network-observation libraries do not depend on each other.

- **Plugin** (`ExtPluginGuest`, `ExtPluginExports`, `ExtPluginWebView`, `ExtPluginWamr`): author an
  `ext.plugin/v1` plugin in Swift, and Objective-C through the C headers, on the guest half of the
  shared [C engine](../../lib/plugin/README.md); host a WebAssembly reactor in a web view or in WAMR.
- **Web** (`ExtWeb`): the cookie, userscript and bookmarklet rules of [`res/web`](../../res/web/README.md),
  in Swift, run against the same fixtures as Go. The code is `res/web/ios`; this package ships it. See Web rules.

`ExtWeb` lives in [`res/web/ios`](../../res/web/ios/README.md) and imports nothing of the plugin code or
the engine. An app that uses only `ExtWeb` links no engine code; it still resolves the package, whose manifest
declares the engine's binary target.

The plugin part covers both halves for an app. Authoring a guest binds the engine, which does the
strict JSON parsing, request validation against the descriptor, deadlines and error sanitizing; a Swift
author supplies a descriptor and a handler. Hosting is the second half: a WebAssembly reactor in a
hidden web view (see below), the first route of [the mobile decision](../../docs/adr-mobile-wasm.md),
or in-process on the interpreter of [`lib/plugin/wamr`](../../lib/plugin/wamr/README.md) (the optional
`ExtPluginWamr`, see Hosting a plugin in WAMR). iOS cannot spawn processes, so a host cannot use the
engine's subprocess backend. Nothing is published yet.

## Writing a plugin

```swift
import ExtPluginExports
import ExtPluginGuest
import Foundation

let descriptor = Descriptor(
    identity: Identity(id: "com.example/greeter", revision: "1"),
    contracts: [Contract(name: "example.greeter", version: "v1",
                         operations: [Operation(name: "greet")])])

func handle(_ call: Call) throws -> Data {
    struct Input: Decodable { var name: String }
    let input = try call.decodePayload(Input.self)
    if input.name.isEmpty {
        throw RemoteError(code: "invalid_name", message: "name is required")
    }
    return Data(#"{"greeting":"hello \#(input.name)"}"#.utf8)
}

@_cdecl("ext_plugin_guest_factory")
public func ext_plugin_guest_factory() -> UnsafeMutableRawPointer? {
    exportGuest { try Guest(descriptor: descriptor, handler: handle) }
}
```

Build it as a `.library(type: .dynamic, ...)` product that depends on
`ExtPluginGuest` and `ExtPluginExports`. The library then exports the four
`ext_plugin_*` symbols of [`ext_plugin.h`](../../lib/plugin/include/ext_plugin.h), so any
`pkg/ext-go/cgoengine/cshared` host can load it. Forgetting `ext_plugin_guest_factory` is a
link error, not a run-time surprise.

Use `ExtPluginGuest` alone, with `Guest.invoke`, to run a guest inside a process
that has its own transport.

- A handler returns one JSON value. `Call.payload` is the request payload exactly
  as sent, so numbers keep their original text; `decodePayload` is a convenience.
- Throw `RemoteError` for a failure callers may see. Any other thrown error is
  private and becomes a generic `operation_failed`.
- **A handler must not trap** (`fatalError`, out-of-range index, forced unwrap):
  nothing can recover a crashed process. Long loops call `Call.checkDeadline()`.

## Web rules

`ExtWeb` is the iOS module of [`res/web`](../../res/web/README.md): the cookie, userscript and bookmarklet
rules in Swift, with no dependency on the plugin code or the engine. Its code, its tests and its usage are in
[`res/web/ios`](../../res/web/ios/README.md); `Sources/ExtWeb` here is a link to it, so an app that adds this
package gets the `ExtWeb` product beside the plugin ones, and `scripts/ext-ios-release.sh` stages a real copy.

```swift
import ExtWeb

let site = try parseSite("https://Example.com:8443/a?b=1")
try CookieRules.validate(Cookie(name: "sid", value: token, domain: ".example.com", path: "/", secure: true))
let url = try Bookmarklet.encode("alert(document.title)")
```

```sh
scripts/web-ios.sh            # swift test of res/web/ios; needs Swift, not the engine xcframework
```

## Clipboard

`ExtClipboard` ships the bounded clipboard values and caller-owned Apple native
binding from [`res/system/clipboard`](../../res/system/clipboard/README.md). It supports macOS
and iOS, requires an explicit format and byte policy, and performs no remote
routing or approval UI.

## System observation

`ExtSystem` ships the local display and window observation model from
[`res/system`](../../res/system/README.md). It also observes read-only native UI
structure and accessibility/screen-recording permission state. macOS reports the
focused accessible application and visible desktop windows; iOS reports only the
calling application's views and windows. Text is opt-in. Neither binding exposes
native handles, captures pixels, sends input, prompts, opens settings, or routes remotely.

## Network observation

`ExtNetwork` ships [`res/system/network`](../../res/system/network/README.md)'s bounded local
interface and assigned-address observation. Apple route-table and resolver
collection currently report `unsupported`. It performs no lookup, opens no
connection, and provides no remote discovery or routing service.

## Hosting a plugin in a web view

`ExtPluginWebView` runs a WebAssembly reactor ([the ABI](../../docs/plugin-reactor-abi.md))
in a hidden `WKWebView`, the phone's own web engine. This is the first route of
[the mobile decision](../../docs/adr-mobile-wasm.md), with `restricted` isolation: no
network, files or process, but the browser's rules and not a capability sandbox.

```swift
let host = WebViewPluginHost(module: reactorBytes)   // uses the embedded page shim
try await host.start()
let descriptor = try await host.handshake(deadline: Date().addingTimeInterval(10))
let response = try await host.invoke(requestJSON, timeout: 10)
host.close()
```

- The page API is [`window.ext.host`](../../docs/web-engine.md) of `res/web/bundle`. The
  host registers a script message handler named `ext` and delivers frames with
  `window.ext.host._deliver`; `ext.plugin/v1` frames pass through as strings.
- A running WebAssembly call cannot be interrupted. A call that overruns its timeout
  destroys the web view, and the host is then closed.
- It is main-actor code. One host runs one plugin, one call at a time.
- The page shim and the page glue are embedded in the package
  (`GeneratedAssets.swift`), generated by `scripts/plugin-mobile-assets.sh` from
  `res/web/bundle/shim.js` and `res/web/wasm/reactor.js`, the same sources the
  Android host embeds. `bridge:` is optional; pass a newer shim to override. The script's
  `--check` fails when a generated copy is stale.

Tested on macOS and in the iOS Simulator, not on a physical device. Set
`EXT_RUST_REACTOR` to the Rust shared example built for `wasm32-wasip1`
(`scripts/plugin-crosslang.sh` builds it); the tests skip without it.

## Hosting a plugin in WAMR (optional)

`ExtPluginWamr` is the in-process fallback for a reactor when a web view is not wanted: a
`WamrPluginHost` runs it on WAMR's interpreter (no JIT or AOT, which iOS forbids) through
the engine's host half. It is built only when `Frameworks/CExtWamr.xcframework` exists, so
the default package stays free of it.

```swift
let host = try WamrPluginHost(module: reactorBytes, descriptor: descriptor,
                              verify: { _ in true }, authorize: { _ in true })
try host.start(timeout: 10)
let response = try host.invoke(requestJSON, timeout: 10)
host.close()
```

- The host enforces the request deadline, a linear-memory cap and an optional
  instruction budget; a guest that overruns, traps or exhausts either ends the instance.
- `WamrPluginHost.descriptor(of:)` reads a module's descriptor before it is trusted.
- `scripts/ext-ios-wamr-xcframework.sh` fetches WAMR at the pinned commit
  (`scripts/plugin-wamr-source.sh`) and builds the macOS, iOS and Simulator slices
  (about 0.5 to 0.9 MB each). `CExtEngine.xcframework` declares the `CExtWamr` module and
  header, because two xcframeworks cannot ship the same header or module map. WAMR's
  license is written to `Frameworks/WAMR-LICENSE` and must ship with any binary linking it.
- `scripts/ext-ios-wamr.sh` builds it and runs the tests on macOS.

## Building and testing

```sh
scripts/ext-ios-xcframework.sh     # macOS, iOS and iOS Simulator slices
swift test --package-path pkg/ext-ios
xcodebuild test -scheme Ext-Package \
  -destination 'platform=iOS Simulator,name=iPhone 16 Pro'   # in pkg/ext-ios
scripts/web-ios.sh                    # just ExtWeb (res/web/ios), without the xcframework
```

`scripts/ext-ios-xcframework.sh` compiles the engine's guest core (no
pthreads, no `posix_spawn`) with the repository's strict flags and writes
`Frameworks/CExtEngine.xcframework`, which is not committed. `ExtConformancePlugin`
is the shared `ext.conformance/v1` fixture. `scripts/plugin-crosslang.sh` loads
its dynamic library through the Go `cshared` host, with the same suite that checks
Rust and Zig, and requires it on macOS.

## Report progress and call the host

A guest process can report progress while a call runs and call back into its host, over `JSONLine`
(not the C ABI that `ExtPluginExports` exports, not a WebAssembly reactor, and not `Guest.invoke` on its
own: those have no frame channel, so reporting is `unsupported` there and a call has no reverse caller).
An operation that reports is declared with `Operation.reporting(_:)`; the host operations a guest may call
are the descriptor's `reverse` list.

```swift
let descriptor = Descriptor(
    identity: Identity(id: "com.example/signer", revision: "1"),
    contracts: [Contract(name: "example.signer", version: "v1", operations: [.reporting("sign")])],
    reverse: [Contract(name: "host.svc", version: "v1", operations: [Operation(name: "sign")])])

let guest = try Guest(descriptor: descriptor) { call in
    // Events reach the host in order, before the result. `.dropped` is routine: the rate limit the
    // host set dropped it and counts it for the next event that passes.
    _ = try call.progress(.log("starting"))
    _ = try call.progress(.fraction(1, of: 3))
    // The host's refusal or error comes back as a RemoteError with its code (denied, busy,
    // too_large, expired, ...).
    return try call.reverse!.call(ContractRef(name: "host.svc", version: "v1"), "sign", payload: call.payloadMember("payload"))
}
try JSONLine.serveStandardIO(guest)    // or JSONLine.serve(guest, input: fd, output: fd) over your own pipes
```

`Call.progress` throws a `FrameError` whose `code` is `unsupported` (the operation did not declare progress,
the host asked for no events, or there is no frame channel: stop reporting), `invalid` (the event breaks the
rules; nothing is sent) or `closed` (the call is over or the connection ended). `Call.progressLimits` is the
rate the host set, and `ProgressEvent.split` cuts a long line into events that each fit. The event type is
`ProgressEvent` because Foundation already has a `Progress`.

`JSONLine` reads its input on a thread of its own, so a handler may have several reverse calls outstanding
(from several threads) and `Call.sessionReverse` is a caller that background work keeps after the call
returned; `ReverseCaller.call` waits for the answer until the call's deadline (30 seconds for the session's),
or the `timeout` given. A handler can poll `Call.connectionEnded` to stop early. `Call.reverse` and
`Call.sessionReverse` are nil, and `progress` fails with `unsupported`, when the guest is invoked directly.

A frame the host may not send (a second request while a call is in flight, an unknown frame, an answer to a
request that was never made, a malformed answer) ends the session: `JSONLine.serve` throws
`JSONLineError.protocolViolation`, and the call in flight is not answered. When the input ends, the call in
flight is answered and the session ends; a reverse call still waiting fails with `closed`. One request may
follow the hello without waiting for its answer. The engine validates the descriptor and each request;
`Frames` judges the frames the guest writes and reads by the Go contract's rules, which `FramesFixtureTests`
reproduces from the shared fixtures (`lib/plugin/testdata/v1`), and `RouterTests` covers the router on pipes.
`ExtFramesGuest` is the frames conformance guest of
[docs/plugin-guest-frames.md](../../docs/plugin-guest-frames.md); `scripts/plugin-crosslang.sh` runs it
through the Go session and the C engine on macOS. It is a process, so it runs on macOS, not on iOS, which
cannot spawn one: on iOS the transport is whatever pipes the app opens.

## Data endpoints

`Endpoint.check(_:)` and `Endpoint.validate(_:policy:)` run the engine's rules for the
loopback endpoint a host hands a plugin (see `lib/plugin/services.md`): a plugin checks
what it receives before dialing, a host validates what it grants. A refusal is an
`EndpointError` (`invalid`, `denied` or `expired`) and never carries the endpoint, which
is a credential.

## Platform and policy limits

Apple's App Store rule 2.5.2 and Google Play policy forbid downloading executable
code, so a native plugin must ship inside the signed app bundle. Third-party
plugins that arrive after release need an interpreter, such as the WASM backend.

## Distribution

Releases are GitHub releases tagged `ext-ios-vX.Y.Z` (a dash form, not a Go module tag; see
[module versioning](../../docs/module-versioning.md)), made by
[`.github/workflows/ext-ios-release.yml`](../../.github/workflows/ext-ios-release.yml). It stages
the release with `scripts/ext-ios-release.sh --with-wamr`, runs the web checks, the Swift tests on the
host and the same tests in an iOS Simulator, and only then publishes. Each release carries:

- `CExtEngine.xcframework.zip`, the engine's guest core for macOS, iOS and the iOS Simulator;
- `CExtWamr.xcframework.zip` and `WAMR-LICENSE`, the optional WAMR host. WAMR is Apache-2.0 with the
  LLVM exception; its license must ship with any binary that links it, and the package carries it in
  `LICENSES/`;
- `ext-ios-X.Y.Z-package.tar.gz`, the staged Swift package: a release manifest whose binary targets
  point at the two archives by URL and checksum, the Swift sources, the README and the licenses;
- `checksums.txt` (SHA-256) and a build-provenance attestation for every file.

Verify a download with `shasum -a 256 -c checksums.txt` and
`gh attestation verify <file> --repo webong/ext`.

**SwiftPM needs a repository root.** It resolves a package from a repository root, which this
monorepo's root is not, so the manifest in this directory is for development against the local
xcframework, and a consumer needs the staged package at the root of a repository of its own. The
workflow's last job does that, but only when the repository variable `EXT_IOS_DISTRIBUTION_REPO` names
one (for example `webong/ext-ios`) and the secret `EXT_IOS_DISTRIBUTION_TOKEN` can push to it: it
replaces that repository's contents with the staged package, commits `ext-ios X.Y.Z` and tags `X.Y.Z`,
after the release exists, because the manifest's binary targets are the release's assets. Until that
variable is set, a release is the GitHub release alone and an app takes the package from
`ext-ios-X.Y.Z-package.tar.gz` (or builds from source). Nothing creates that repository.

A consumer then writes:

```swift
.package(url: "https://github.com/<distribution repository>", from: "0.1.0")
// and depends on the products it uses: ExtWeb, ExtPluginGuest, ExtPluginExports, ExtPluginWebView, ExtPluginWamr
```

The archives are not byte-reproducible, so their checksums change with every run: the manifest and
the archives always come from the same run. `scripts/ext-ios-release.sh [--with-wamr] <version>`
stages the same pieces locally under `dist/ext-ios-<version>/` and checks that the staged package
builds against the archives; it publishes nothing. Without `--with-wamr` the package carries neither
the `ExtPluginWamr` product nor its sources.

## Objective-C

The Swift SDK is usable from Objective-C through `EXT*` classes, so an Objective-C
plugin needs no Swift code of its own:

- `EXTGuest` authors a plugin: `initWithIdentifier:revision:contractName:contractVersion:operations:error:handler:`
  takes a block that returns the payload JSON, or returns `nil` and sets its error.
  `+[EXTGuest remoteErrorWithCode:message:retryAfterMilliseconds:]` makes a public error;
  any other error is private. `EXTCall` is what the block receives.
- `EXTPluginExport` is the C ABI hook. Define `ext_plugin_guest_factory` as
  `return [EXTPluginExport retainedPointerFor:MakeGuest()];` and the plugin exports the
  four `ext_plugin_*` symbols.
- `EXTWebViewPluginHost` hosts a reactor in a web view, with completion blocks.

`scripts/ext-ios-objc.sh` compiles and runs an Objective-C program
(`Tests/ObjC/main.m`) that uses all three and calls the C ABI directly; it passes on
macOS. It is not run in the iOS Simulator.

## Related

- [Engine adapters](../../docs/engine-adapters.md) are adapters that run programs
  (`jvm`, `wasm`, `evm`). They are a different thing from this SDK, which binds the
  shared plugin engine, `lib/plugin`; the page explains the two senses of
  "engine".
- [The web engine](../../docs/web-engine.md) runs web content in a browser the host
  chooses, including the iOS Simulator and Android devices. It does not author plugins.
- A WASM interpreter reachable from the C engine exists as the optional WAMR host
  (`lib/plugin/wamr`, `ExtPluginWamr` here), built for and run on the iOS Simulator; it has not
  run on a physical device. `res/web/wasm`, which is Go and wazero, is the separate Go route.
- [`res/web`](../../res/web/README.md) is the Go home of the rules `ExtWeb` ports.

## Browser workspace

The `ExtWebKit` product exposes the native browser workspace owned by
[`res/web/ios`](../../res/web/ios/WORKSPACE.md). It uses `ExtWeb` for reviewed
userscript validation and document-start replay, with navigation/state callbacks
and caller-selected WebKit storage. It has no dependency on the plugin engine.
Its source link is materialized by the existing release staging step.
