# iOS plugin SDK (guest)

`pkg/plugin-ios` is the plugin SDK for Apple platforms. It lets Swift code, and
Objective-C through the C headers, author an `ext.plugin/v1` plugin on iOS and macOS. It binds the guest half of the shared [C engine](../plugin-engine/README.md),
which does the strict JSON parsing, request validation against the descriptor,
deadlines and error sanitizing. A Swift author supplies a descriptor and a handler.

Only the **guest** is here. The host half needs an in-process backend, because iOS
cannot spawn processes; that is separate work. Nothing is published yet.

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
`ext_plugin_*` symbols of [`ext_plugin.h`](../plugin-cshared/ext_plugin.h), so any
`pkg/plugin-cshared` host can load it. Forgetting `ext_plugin_guest_factory` is a
link error, not a run-time surprise.

Use `ExtPluginGuest` alone, with `Guest.invoke`, to run a guest inside a process
that has its own transport.

- A handler returns one JSON value. `Call.payload` is the request payload exactly
  as sent, so numbers keep their original text; `decodePayload` is a convenience.
- Throw `RemoteError` for a failure callers may see. Any other thrown error is
  private and becomes a generic `operation_failed`.
- **A handler must not trap** (`fatalError`, out-of-range index, forced unwrap):
  nothing can recover a crashed process. Long loops call `Call.checkDeadline()`.

## Building and testing

```sh
scripts/plugin-ios-xcframework.sh     # macOS, iOS and iOS Simulator slices
swift test --package-path pkg/plugin-ios
xcodebuild test -scheme ExtPlugin-Package \
  -destination 'platform=iOS Simulator,name=iPhone 16 Pro'   # in pkg/plugin-ios
```

`scripts/plugin-ios-xcframework.sh` compiles the engine's guest core (no
pthreads, no `posix_spawn`) with the repository's strict flags and writes
`Frameworks/CExtEngine.xcframework`, which is not committed. `ExtConformancePlugin`
is the shared `ext.conformance/v1` fixture. `scripts/plugin-crosslang.sh` loads
its dynamic library through the Go `cshared` host, with the same suite that checks
Rust and Zig, and requires it on macOS.

## Platform and policy limits

Apple's App Store rule 2.5.2 and Google Play policy forbid downloading executable
code, so a native plugin must ship inside the signed app bundle. Third-party
plugins that arrive after release need an interpreter, such as the WASM backend.

## Distribution

SwiftPM resolves a package from a repository root, which this monorepo's root is
not, so the manifest here is for development against the local xcframework. A
release publishes the xcframework as a checksummed archive and a manifest whose
binary target points at it. That release step is not automated yet.

## Objective-C

Objective-C reaches the same C headers directly. A Swift-free Objective-C
authoring API is not provided yet.

## Related

- [Engine adapters](../../docs/engine-adapters.md) are adapters that run programs
  (`jvm`, `wasm`, `evm`). They are a different thing from this SDK, which binds the
  shared plugin engine, `pkg/plugin-engine`; the page explains the two senses of
  "engine".
- [The web engine](../../docs/web-engine.md) runs web content in a browser the host
  chooses, including the iOS Simulator and Android devices. It does not author plugins.
- A WASM interpreter reachable from the C engine does not exist yet. Until it does,
  a mobile app that hosts third-party plugins after release has no route that
  satisfies the no-downloaded-code rules. That is open work shared with
  `pkg/plugin-wasm`, which is Go and wazero.
