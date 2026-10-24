# Android plugin SDK (guest)

`pkg/ext-android` is the plugin SDK for Android. It lets Java code, and Kotlin
through the same API, author an `ext.plugin/v1` guest on the JVM.

**Status:** two parts, verified differently.
- The guest SDK (below) is plain JVM and JNI; its tests run on a desktop JDK 17. An
  Android build of the JNI library (NDK) has not been compiled or run.
- The WebView host (`WebViewPluginHost`) is plain Java built against `android.jar` and
  has run in a real `WebView` on an Android 14 emulator. See Hosting a plugin in a WebView. A small JNI library binds the guest half of the
shared [C engine](../../lib/plugin/README.md), which does the strict JSON parsing,
request validation against the descriptor, deadlines and error sanitizing. The
application supplies a descriptor and a `Handler`.

This package covers both halves for an app. Authoring a guest is the first part. The
second is hosting a WebAssembly reactor in a hidden WebView (see Hosting a plugin in a
WebView), the first route of [the mobile decision](../../docs/adr-mobile-wasm.md). The
fallback route, an in-process interpreter, is the C backend in
[`lib/plugin/wamr`](../../lib/plugin/wamr/README.md), bound here as an optional
`WamrPluginHost` (see Hosting a plugin in WAMR). Nothing is published yet.

## Writing a plugin

```java
Descriptor descriptor = new Descriptor(
    new Identity("com.example/greeter", "1"),
    List.of(new Contract("example.greeter", "v1", List.of(new Operation("greet")))));

try (Guest guest = new Guest(descriptor, call -> {
        if (call.payload().length == 0) {
            throw new RemoteError("invalid_name", "name is required");
        }
        return call.payload(); // one JSON value
    })) {
    JsonLine.serveStdio(guest);   // a guest process over the JSON-line transport
}
```

- `Guest.invoke(request)` validates a complete request and returns the complete
  response, so an application can put the guest behind its own transport, such as
  an Android bound service.
- `JsonLine` is the ext.plugin/v1 JSON-line transport for a guest process: it
  serves the `plugin.hello` handshake and then one response per request line.
  Stdout belongs to the protocol; log to stderr.
- `Call.payload()` is the request payload exactly as sent, so numbers keep their
  original text. Throw `RemoteError` for a failure callers may see; any other
  throwable is private and becomes a generic `operation_failed`.
- Long-running handlers call `Call.checkDeadline()`. Native code cannot be
  interrupted from outside.

## What a JVM guest cannot do

The `ext_plugin_*` C ABI that `pkg/plugin-go/cengine/cshared` loads would need a JVM inside
the host process, so a Java library cannot be loaded that way. A JVM guest speaks
JSON lines in its own process, or runs inside an application that provides the
transport. Kotlin/Native, which compiles to a native library, could export that
ABI; it is not attempted here.

## Data endpoints

`Endpoint.check` and `Endpoint.validate` run the engine's rules for the loopback endpoint
a host hands a plugin (see `lib/plugin/services.md`): a plugin checks what it receives
before dialing, a host validates what it grants. A refusal is a `GuestException` whose
`status()` is `Endpoint.INVALID`, `Endpoint.DENIED` or `Endpoint.EXPIRED`, and nothing of
the endpoint, which is a credential.

## Report progress and call the host

A guest process can report progress while a call runs and call back into its host, over
`JsonLine` (not the WAMR host, which runs reactors, and not `Guest.invoke` on its own: those have
no frame channel). An operation that reports is declared with `Operation.reporting(name)`; the
host operations a guest may call are the descriptor's `reverse` list.

```java
Descriptor descriptor = new Descriptor(
    new Identity("com.example/signer", "1"),
    List.of(new Contract("example.signer", "v1", List.of(Operation.reporting("sign")))),
    List.of(new Contract("host.svc", "v1", List.of(new Operation("sign")))));

Guest guest = new Guest(descriptor, call -> {
    // Events reach the host in order, before the result. DROPPED is routine: the rate
    // limit the host set dropped it and counts it for the next event.
    call.progress(Progress.log("starting"));
    call.progress(Progress.fraction(1, 3));
    // The host's refusal or error comes back as a RemoteError with its code (denied,
    // busy, too_large, expired, ...).
    return call.reverse().call(new ContractRef("host.svc", "v1"), "sign", call.payload());
});
```

`Call.progress` throws a `FrameException` whose `code()` is `UNSUPPORTED` (the operation
did not declare progress, the host asked for no events, or there is no frame channel: stop
reporting), `INVALID` (the event breaks the rules; nothing is sent) or `CLOSED` (the call
is over or the connection ended). `Call.progressLimits()` is the rate the host set, and
`Progress.split` cuts a long line into events that each fit. `JsonLine` reads its input on
a thread of its own, so a handler may have several reverse calls outstanding (from several
threads) and `Call.sessionReverse()` is a caller that background work keeps after the call
returned; `ReverseCaller.call` waits for the answer until the call's deadline (30 seconds
for the session's), or the timeout given. A handler can poll `Call.connectionEnded()` to
stop early. Both `Call.reverse()` and `Call.sessionReverse()` are null, and `progress`
fails with `UNSUPPORTED`, when the guest is invoked directly.

A frame the host may not send (a second request while a call is in flight, an unknown
frame, an answer to a request that was never made, a malformed answer) ends the session:
`JsonLine.serve` throws an `IOException`, and the call in flight is not answered. When the
input ends, the call in flight is answered and the session ends; a reverse call still
waiting fails with `CLOSED`. One request may follow the hello without waiting for its
answer. The engine validates the descriptor and each request; `Frames` judges the frames
the guest writes and reads by the Go contract's rules, which `FramesTests` reproduces from
the shared fixtures, and `RouterTests` covers the router.
`src/test/java/.../FramesGuest.java` is the frames conformance guest of
[docs/plugin-guest-frames.md](../../docs/plugin-guest-frames.md), which
`scripts/plugin-crosslang.sh` runs through the Go session and the C engine.

## Building and testing

```sh
scripts/ext-android.sh          # needs a JDK and CMake
```

The script builds the JNI library with CMake, compiles the classes with
`javac -Xlint:all -Werror`, runs the tests with `java -ea` (no test framework is
assumed), and writes launchers for `ConformanceGuest`, the shared
`ext.conformance/v1` fixture, and `FramesGuest` (each names `java` by absolute path,
since the engine starts a guest with an empty environment). `scripts/plugin-crosslang.sh`
runs them through the same Go suite as the Rust and Zig guests, and requires them when a
JDK is present.

## Hosting a plugin in a WebView

`io.github.webong.ext.plugin.webview.WebViewPluginHost` runs a WebAssembly reactor
([the ABI](../../docs/plugin-reactor-abi.md)) in a hidden `WebView`, the phone's own web
engine. This is the first route of [the mobile decision](../../docs/adr-mobile-wasm.md),
with `restricted` isolation: no network, files or process, but the browser's rules and
not a capability sandbox.

```java
WebViewPluginHost host = new WebViewPluginHost(context, reactorBytes, null, log);
host.start(60_000).get();                         // null shim: the embedded copy
byte[] descriptor = host.handshake(10_000).get();
byte[] response = host.invoke(requestJson, 10_000).get();
host.close();
```

- It uses the page API of `res/web/bundle`: the `ExtHost` JavaScript interface carries
  the page's frames to the app, and `window.ext.host._deliver` carries the app's frames
  back. The shim and the page glue are embedded in `GeneratedAssets.java`, generated by
  `scripts/plugin-mobile-assets.sh` from the same sources the iOS host embeds.
- Futures complete on the main thread: never block the main thread on one.
- A running WebAssembly call cannot be interrupted. A call that overruns its timeout
  destroys the WebView and closes the host.
- It sets `setBlockNetworkLoads(true)` and needs no INTERNET permission.
- API level 24 and up. The sources are in `src/android` and need only `android.jar`.

`scripts/ext-android-webview.sh` builds a test app by hand (`javac`, `d8`, `aapt2`,
`apksigner`; no Gradle, Kotlin or NDK), installs it on a running emulator or starts the
first AVD headless, and runs the nine checks the iOS suite runs. All pass on a Pixel 7
API 34 emulator, not on a physical device. A running emulator needs several GiB of free
disk.

## Hosting a plugin in WAMR (optional)

`WamrPluginHost` runs a reactor in-process on WAMR's interpreter, through the engine's host
half and a second JNI library, `libextwamrjni`, which is built only when `EXT_WAMR_ROOT` names
a WAMR checkout. The default `libextjni` and its guest SDK carry neither the host nor an
interpreter. It needs API 28 or later.

```java
byte[] descriptor = WamrPluginHost.descriptorOf(module, 30_000, null);   // inspection only
try (WamrPluginHost host = new WamrPluginHost(module, descriptor)) {
    host.start(30_000);
    byte[] response = host.invoke(request, 10_000);   // blocks: call from a background thread
}
```

- The constructor also takes `Options` (memory pages, interpreter stack, an instruction
  budget per call, diagnostic bytes) and `verify`, `authorize` and diagnostic callbacks.
  A denial from `authorize` throws `WamrException` with status `DENIED` and leaves the
  host usable; a timeout, trap or exhausted budget ends the instance.
- `close()` is safe from any thread and interrupts a running call.
- The build disables WAMR's hardware bound checks (`EXT_WAMR_DISABLE_HW_BOUND_CHECK`): they
  install signal handlers and probe the native stack, which the JVM and ART own, and the JVM
  aborted with SIGILL on first use. The interpreter checks memory in software instead.
- `scripts/ext-android-wamr.sh` builds it and runs the 12 checks on a desktop JDK;
  `scripts/ext-android-wamr-device.sh` cross-builds with the NDK and runs them in a test
  app on an emulator or device. WAMR is fetched at its pinned commit
  (`scripts/plugin-wamr-source.sh`) and its license must ship with any binary that links it.

## Distribution

Releases are GitHub releases tagged `ext-android-vX.Y.Z` (a dash form, not a Go module tag;
see [module versioning](../../docs/module-versioning.md)). Each carries:

- `ext-android-X.Y.Z.aar`: the guest SDK and the WebView host as `classes.jar` (Java 17, which the
  app's D8 desugars), `libextjni` for arm64-v8a and x86_64, and consumer R8 rules (`proguard.txt`).
  Minimum SDK 24.
- `ext-android-wamr-X.Y.Z.aar`: the same plus `libextwamrjni` and the `WamrPluginHost` classes.
  Minimum SDK 28. WAMR's license is inside it (`META-INF/WAMR-LICENSE`) and beside it
  (`WAMR-LICENSE`), and must ship with any app that links it.
- `checksums.txt` (SHA-256) and a build-provenance attestation for every file.

The release workflow builds both AARs, tests each one as shipped in an app with R8 on, on an
emulator, and only then publishes. Verify a download with `sha256sum -c checksums.txt` and
`gh attestation verify <file> --repo webong/ext`. To use one, put it in a local flat repository:

```groovy
repositories { flatDir { dirs 'libs' } }          // libs/ext-android-X.Y.Z.aar
dependencies { implementation name: 'ext-android-X.Y.Z', ext: 'aar' }
```

It is not on Maven Central: that waits until an outside app needs it. To build locally,
`scripts/ext-android-release.sh [--with-wamr] <version>` stages the same AARs under
`dist/ext-android-<version>/` and publishes nothing; `scripts/ext-android-consumer.sh
[--with-wamr] [--aar <file>]` tests an AAR in an app.

## Kotlin and Android

Kotlin calls this API directly; there is no separate Kotlin layer yet.
`CMakeLists.txt` and `ext_jni.c` are written to build unchanged with the Android
NDK toolchain, but **neither the Kotlin usage nor an Android build has been
compiled or run**: this repository's checks have no Kotlin compiler, Gradle or
NDK. Packaging as an AAR, and distribution through Maven, are not done.

Apple's App Store rule 2.5.2 and Google Play policy forbid downloading executable
code, so a native plugin must ship inside the signed app bundle or package.

## Related

- [Engine adapters](../../docs/engine-adapters.md) are adapters that run programs
  (`jvm`, `wasm`, `evm`). They are a different thing from this SDK, which binds the
  shared plugin engine, `lib/plugin`; the page explains the two senses of
  "engine".
- [The web engine](../../docs/web-engine.md) runs web content in a browser the host
  chooses, including the iOS Simulator and Android devices. It does not author plugins.
- A WASM interpreter reachable from the C engine does not exist yet. Until it does,
  a mobile app that hosts third-party plugins after release has no route that
  satisfies the no-downloaded-code rules. That is open work shared with
  `pkg/plugin-go/wasm`, which is Go and wazero.
