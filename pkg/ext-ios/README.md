# iOS SDK (plugin and web)

`pkg/ext-ios` is the ext SDK for Apple platforms: one Swift package with two parts that
do not depend on each other.

- **Plugin** (`ExtPluginGuest`, `ExtPluginExports`, `ExtPluginWebView`, `ExtPluginWamr`): author an
  `ext.plugin/v1` plugin in Swift, and Objective-C through the C headers, on the guest half of the
  shared [C engine](../../lib/plugin/README.md); host a WebAssembly reactor in a web view or in WAMR.
- **Web** (`ExtWeb`): the cookie, userscript and bookmarklet rules of [`res/web`](../../res/web/README.md),
  ported to Swift and run against the same fixtures as Go. See Web rules.

`ExtWeb` imports nothing of the plugin code or the engine, and `scripts/ext-ios-web.sh` fails if it
ever does. An app that uses only `ExtWeb` links no engine code; it still resolves the package, whose
manifest declares the engine's binary target.

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
`pkg/plugin-go/cengine/cshared` host can load it. Forgetting `ext_plugin_guest_factory` is a
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

`ExtWeb` is the part of `res/web` a phone needs: the portable rules for browser cookies, userscripts
and bookmarklets, with no engine and no native code.

```swift
import ExtWeb

let site = try parseSite("https://Example.com:8443/a?b=1")        // host lower-cased, escapes as Go's net/url
let cookie = Cookie(name: "sid", value: token, domain: ".example.com", path: "/", secure: true)
try CookieRules.validate(cookie)                                   // field rules; the error never has the value
CookieRules.matches(site, cookie)                                  // domain, path, Secure and expiry

var record = Userscript.inferMetadata(UserscriptRecord(target: "app", id: "", name: "", source: source))
record.revision = Userscript.revision(of: record)                   // sha256 over the reviewed fields
try Userscript.validate(record)                                     // document-start, @grant none, explicit matches
let script = try Userscript.replaySource(id: record.id, revision: record.revision, source: record.source,
                                         matches: record.matches, excludes: record.excludeMatches)
// App code: webView.configuration.userContentController.addUserScript(
//     WKUserScript(source: script, injectionTime: .atDocumentStart, forMainFrameOnly: false))

let url = try Bookmarklet.encode("alert(document.title)")           // a javascript: URL, wrapped and percent-encoded
let source = try Bookmarklet.decode(url)                            // back to source for review; never evaluated
```

- `Cookie`, `CookieBundle` and `UserscriptRecord` are `Codable` with the wire names of `res/web`; a cookie's
  `description`, `debugDescription` and `dump` print its name, domain and path and never its value.
- `UserscriptRecord.matches` and `excludeMatches` are optional because the revision digest tells `null`
  from `[]`.
- The rules are Go's, written out for Swift. They are checked two ways: the language-neutral fixtures in
  `res/web/testdata` (`cookie/v2`, `userscript/v1alpha1`, `bookmarklet/v1alpha1`), which the Go tests run too,
  and `scripts/ext-web-differential.sh`, which compares the port with Go on about 94,000 generated URLs,
  cookie domains, names and bookmarklet strings (all agree; Go 1.27 with `GODEBUG=urlstrictcolons=1`).
- Not ported: the userscript file store (`Load`, `Save`, `List`; an app owns its storage) and the SQLite
  cookie readers and extension packaging, which read desktop browser profiles. Also different on purpose:
  the wording of descriptions, JSON member names match exactly where Go ignores case, and a host with several
  colons is refused (Go's default for that follows the consuming module's `go` line).

```sh
scripts/ext-ios-web.sh        # swift test of ExtWeb alone; needs Swift, not the engine xcframework
```

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
scripts/ext-ios-web.sh                # just ExtWeb, without the xcframework
```

`scripts/ext-ios-xcframework.sh` compiles the engine's guest core (no
pthreads, no `posix_spawn`) with the repository's strict flags and writes
`Frameworks/CExtEngine.xcframework`, which is not committed. `ExtConformancePlugin`
is the shared `ext.conformance/v1` fixture. `scripts/plugin-crosslang.sh` loads
its dynamic library through the Go `cshared` host, with the same suite that checks
Rust and Zig, and requires it on macOS.

## Data endpoints

`Endpoint.check(_:)` and `Endpoint.validate(_:policy:)` run the engine's rules for the
loopback endpoint a host hands a plugin (see `lib/plugin/services.md`): a plugin checks
what it receives before dialing, a host validates what it grants. A refusal is an
`EndpointError` (`invalid`, `denied` or `expired`) and never carries the endpoint, which
is a credential. This SDK does not report progress or call a host's reverse services:
a guest exported through the `ext_plugin_*` C ABI has no frame channel for them, and a
WebAssembly reactor has none either.

## Platform and policy limits

Apple's App Store rule 2.5.2 and Google Play policy forbid downloading executable
code, so a native plugin must ship inside the signed app bundle. Third-party
plugins that arrive after release need an interpreter, such as the WASM backend.

## Distribution

**Not distributed yet.** There is no iOS release job: the tag form `ext-ios-vX.Y.Z` is
reserved and documented, but SwiftPM needs a repository root or a dedicated distribution
repository to resolve a binary-target package from, and until that is decided a release job
would publish something nobody can consume. The staging script below produces the pieces.

SwiftPM resolves a package from a repository root, which this monorepo's root is not,
so the manifest here is for development against the local xcframework.
`scripts/ext-ios-release.sh <version>` stages a release under
`dist/ext-ios-<version>/`:

- `CExtEngine.xcframework.zip`, the engine's guest core for macOS, iOS and the iOS
  Simulator, to attach to a GitHub release tagged `ext-ios-v<version>`; and
- `package/`, a release manifest whose binary target points at that archive by URL and
  `swift package compute-checksum` checksum, plus the Swift sources, to publish at a
  repository root SwiftPM can resolve.

`scripts/ext-ios-release.sh --with-wamr <version>` also stages the optional WAMR host:
`CExtWamr.xcframework.zip` as a second release asset, `WAMR-LICENSE` beside it (and in the
package's `LICENSES/`), and the `ExtPluginWamr` product pinned to that archive by checksum.
Without the flag the package carries neither the product nor its sources.

The archive is not byte-reproducible, so its checksum changes with every run: upload the
archive from the same run as the manifest. The script checks that the staged package builds
against the archive. It publishes
nothing: it does not tag, upload, or create the distribution repository, and the
release URL does not exist until someone attaches the archive. No CI job does this yet.

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
  run on a physical device. `pkg/plugin-go/wasm`, which is Go and wazero, is the separate Go route.
- [`res/web`](../../res/web/README.md) is the Go home of the rules `ExtWeb` ports.
