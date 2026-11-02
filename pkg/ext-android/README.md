# Android SDK

`pkg/ext-android` is the ext SDK for Android. Its plugin, web, clipboard, system,
and network-observation packages do not depend on each other. Java is the API, and
Kotlin calls it directly.

- **Plugin** (`io.github.webong.ext.plugin`): author an `ext.plugin/v1` guest on the JVM over a small JNI
  library that binds the guest half of the shared [C engine](../../lib/plugin/README.md), keep reviewed
  `ext.package/v1` packages in the engine's package store, and host a WebAssembly reactor in a WebView or
  in WAMR.
- **Web** (`io.github.webong.ext.web`): the cookie, userscript and bookmarklet rules of
  [`res/web`](../../res/web/README.md), in plain Java with no native code, run against the same fixtures as
  Go. The code is `res/web/android`; this AAR ships it. See Web rules.
- **System** (`io.github.webong.ext.system`): bounded display metadata and the
  calling application's visible window and view structure, plus permission-state
  observation, from `res/system/android`. Memory observation distinguishes
  physical memory (`memory_bytes`, from `ActivityManager.MemoryInfo.totalMem`)
  from effective capacity (`memory_capacity_bytes`, equal to physical on Android
  because no container memory limit is exposed at this level). Missing
  `ActivityManager` or zero total memory yields a non-complete memory status
  rather than guessing host values. Storage and battery absence are reported
  explicitly.
- **Network** (`io.github.webong.ext.network`): bounded local interfaces,
  assigned addresses, routes and DNS configuration from `res/system/network/android`.

The web package lives in [`res/web/android`](../../res/web/android/README.md), imports nothing of the plugin
package and loads no native library. It is still in the same AAR as the plugin code, so an app that wants only
the web rules takes the native libraries and the minimum SDK with it.

**Status:** verified in three ways.
- The guest SDK is plain JVM and JNI; its tests run on a desktop JDK 17.
- The package store runs the same way, on a desktop JDK against the host build of the C engine. Its JNI
  code cross-compiles with the NDK for arm64-v8a and x86_64 against API 24, but it has not run on an
  emulator or a device.
- The JNI library is built with the NDK for arm64-v8a and x86_64 by `scripts/ext-android-release.sh`,
  and the release workflow tests each AAR as shipped, in an app with R8 on, on an emulator.
- The WebView host (`WebViewPluginHost`) is plain Java built against `android.jar` and has run in a
  real `WebView` on an Android 14 emulator. Nothing has run on a physical device.
- The web package has run on a desktop JDK, and dexes with D8 for API 24. The release workflow also runs
  `WebRoundtrips` (examples/android-consumer) on an emulator under R8, with answers Go gave, because Android's
  `Character` case tables come from ICU; that job has not run yet, so check id derivation and the lower-casing
  of non-ASCII hosts on a device before relying on them.
- The system and network contracts run on a desktop JDK, compile against
  `android.jar`, and the release consumer test observes the emulator's app-owned
  UI and local network state.

The application supplies a descriptor and a `Handler`. The engine does the strict JSON parsing, request
validation against the descriptor, deadlines and error sanitizing.

The plugin part covers both halves for an app. Authoring a guest is the first part. The
second is hosting a WebAssembly reactor in a hidden WebView (see Hosting a plugin in a
WebView), the first route of [the mobile decision](../../docs/adr-mobile-wasm.md). The
fallback route, an in-process interpreter, is the C backend in
[`lib/plugin/wamr`](../../lib/plugin/wamr/README.md), bound here as an optional
`WamrPluginHost` (see Hosting a plugin in WAMR). Nothing is on Maven yet.

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

## Web rules

`io.github.webong.ext.web` is the Android module of [`res/web`](../../res/web/README.md): the cookie,
userscript and bookmarklet rules in plain Java, with no dependency, no native code and no use of the plugin
package. Its code, its tests and its usage are in [`res/web/android`](../../res/web/android/README.md); the AAR
of this package carries its classes beside the plugin ones.

```java
Site site = Site.parse("https://Example.com:8443/a?b=1");
CookieRules.validate(Cookie.builder("sid", token, ".example.com", "/").secure(true).build());
String url = Bookmarklet.encode("alert(document.title)");
```

```sh
scripts/web-android.sh        # javac -Xlint:all -Werror and the tests of res/web/android; needs a JDK only
```

## What a JVM guest cannot do

The `ext_plugin_*` C ABI that `pkg/ext-go/cgoengine/cshared` loads would need a JVM inside
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

## Package store

`PackageStore` and `LayeredPackageStore` bind the engine's package store (`ext_store_*` and
`ext_layered_*` in [`ext_host.h`](../../lib/plugin/include/ext_host.h)): the same store the Go, Rust,
Node and Zig bindings use, held to the pure-Go store by the differential tests described in
[the parity page](../../docs/plugin-engine-parity.md). A package is its `plugin.json` manifest and the
artifacts it lists, in a directory named by the package id with `/` encoded as `%2f`. The store copies and
re-verifies content; it never runs it or grants authority, and it has no default location, so the app
supplies the root (for example under `Context.getFilesDir()`).

```java
try (PackageStore store = new PackageStore(new File(context.getFilesDir(), "packages").getPath())) {
    byte[] manifest = store.install(sourceDir);          // plugin.json, byte for byte as the source had it
    store.upgrade(newerDir);                             // same id and contracts; the old copy survives a failure
    store.replace(otherDir);                             // installs or replaces, whatever the contracts
    List<String> ids = store.ids();                      // byte order; nothing is read or verified
    byte[] loaded = store.load("com.example/greeter");   // reads, verifies, returns plugin.json
    store.remove("com.example/greeter");
}

// A read-only view, highest root first. It never writes, so a system root is never created through it.
try (LayeredPackageStore view = new LayeredPackageStore(List.of(userRoot, systemRoot))) {
    LayeredPackageStore.Loaded found = view.load("com.example/greeter");   // found.layer() is 0 for userRoot
}
```

- A failure is a `GuestException`; `status()` is one of the constants of `PackageStore`: `INVALID` (a package,
  id or directory that is not valid, a directory that is not a package, an artifact over a limit), `MISMATCH`
  (a changed artifact, a directory that is not what its name says, an upgrade that changes the id or the
  contracts), `EXISTS` (an install over an installed id), `NOT_FOUND`, `MISSING` (a source directory, a manifest
  or a listed artifact that is not there), `CLOSED`, `IO` and `NOMEM`. A `null` argument is a
  `NullPointerException`; a string with no UTF-8 spelling (a lone surrogate) or with a NUL is `INVALID`.
- `StoreLimits` bounds one artifact file, the files in a tree and the bytes of a tree; zero takes the engine's
  default (4 GiB, 250000 files, 16 GiB). The install and upgrade calls take a `preserveExecutable` flag: a
  file artifact keeps its execute bit only when asked, while entrypoints and the files of a tree always do.
- Manifests and the reports come back as the engine's bytes, not as a Java model. `load` returns `plugin.json`
  verbatim, which is what a trust record's digest covers, and `list()`, `verify()` and `listUnverified()`
  return the JSON of `ext_store_list`, `ext_store_verify` and `ext_store_list_unverified` as UTF-8, to be read
  with `org.json` or any parser the app already has; the SDK has no JSON model of its own to commit to.
  Only `ids()` is parsed, into a `List<String>`. `listUnverified()` reads manifests alone, so its result must
  not be launched or trusted as it stands.
- A missing root is an empty store, made (owner-only) by the first install; asking a store or a view for ids or
  a report never creates it. In a layered view an id is served from the first root that holds it, a shadowed
  copy is never read, and a winner that does not verify fails the call instead of falling back to the copy
  below. A repeated root (however spelled), an empty root, or no root at all is `INVALID`.
- Both classes are safe to share between threads: the engine serializes calls on one store, and `close()` waits
  for calls in flight, is idempotent, and makes later calls fail with `CLOSED`. Several processes mutating one
  root are the app's to serialize.
- Paths reach the engine as UTF-8, not as the modified UTF-8 of a JNI `jstring`, so a name with an emoji in it
  is the name on disk. `libextjni` carries the store: it links the host half of the engine as a static archive,
  so only the objects the store needs come with it (the file layer, integrity checks and SHA-256, about 50 KB
  per ABI), and no process or WebAssembly hosting code.
- The store holds reviewed content; it does not make downloaded native code launchable. See the store rules
  under Kotlin and Android.

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
scripts/ext-android.sh          # the plugin part; needs a JDK and CMake
scripts/web-android.sh          # the web part (res/web/android); needs a JDK only
```

## Clipboard

The AAR ships `io.github.webong.ext.clipboard` and
`io.github.webong.ext.clipboard.android.SystemClipboard` from
[`res/system/clipboard`](../../res/system/clipboard/README.md). The binding provides bounded,
generation-checked access to the app-visible Android system clipboard without
transport or approval UI.

The AAR also ships `io.github.webong.ext.system` and
its native surface, accessibility, and permission observers. Android public APIs
expose displays and the calling app's window and view tree; these observers do
not enumerate other applications, prompt, capture, or send input.

## Share and keyboard extensions

The AAR ships `io.github.webong.ext.system.share` and
`io.github.webong.ext.system.keyboard`, with the
`io.github.webong.ext.system.android.SystemShare` and `SystemKeyboard` bindings
from [`res/system/android`](../../res/system/android/README.md). `SystemShare`
normalizes `ACTION_SEND` and `ACTION_SEND_MULTIPLE` intents and copies
attachments into a caller-supplied directory; `SystemKeyboard` adapts an
`InputMethodService` connection. The SDK ships no input view, capability inbox,
event schema, extension registry, application identity, or launch policy.

## Network observation

The AAR ships `io.github.webong.ext.network` and
`io.github.webong.ext.network.android.SystemNetworkObserver` from
[`res/system/network`](../../res/system/network/README.md). It reads interfaces and assigned
addresses through the runtime, and reads local routes and DNS configuration
through `ConnectivityManager` when `ACCESS_NETWORK_STATE` is granted. It does
not use `INTERNET`, perform a lookup, or open a connection.

The script builds the JNI library with CMake, compiles the classes with
`javac -Xlint:all -Werror`, runs the tests with `java -ea` (no test framework is
assumed; `StoreTests` is the package store's, run from `GuestTests`), and writes launchers for `ConformanceGuest`, the shared
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
a WAMR checkout. The default `libextjni` and its guest SDK carry no interpreter and none of the host's
process or WebAssembly hosting code (only the package store, see Package store). It needs API 28 or later.

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

- `ext-android-X.Y.Z.aar`: the guest SDK, the package store, the WebView host and the web rules as `classes.jar`
  (Java 17, which the app's D8 desugars), `libextjni` for arm64-v8a and x86_64, and consumer R8 rules (`proguard.txt`).
  Minimum SDK 24.
- `ext-android-wamr-X.Y.Z.aar`: the same plus `libextwamrjni` and the `WamrPluginHost` classes.
  Minimum SDK 28. WAMR's license is inside it (`META-INF/WAMR-LICENSE`) and beside it
  (`WAMR-LICENSE`), and must ship with any app that links it.
- `checksums.txt` (SHA-256) and a build-provenance attestation for every file.

The release workflow ([`ext-android-release.yml`](../../.github/workflows/ext-android-release.yml))
checks the web rules on a JDK, builds both AARs, tests each one as shipped in an app with R8 on, on an
emulator (guest, WebView host, web rules, native libraries per ABI), and only then publishes. Verify a download with `sha256sum -c checksums.txt` and
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

Kotlin calls this API directly; there is no separate Kotlin layer yet. **No Kotlin has been compiled
here**: the consumer app and the tests are Java, and this repository's checks have no Kotlin compiler.
The NDK build and the packaging are done (see Distribution); Maven Central is not.

Apple's App Store rule 2.5.2 and Google Play policy forbid downloading executable
code, so a native plugin must ship inside the signed app bundle or package.

## Related

- [Evaluator adapters](../../docs/evaluator-adapters.md) are adapters that run programs
  (`jvm`, `wasm`, `evm`, `php`). They are a different thing from this SDK, which binds the
  shared plugin engine, `lib/plugin`; the page explains the two senses of
  "engine".
- [The web engine](../../docs/web-engine.md) runs web content in a browser the host
  chooses, including the iOS Simulator and Android devices. It does not author plugins.
- A WASM interpreter reachable from the C engine exists as the optional WAMR host
  (`lib/plugin/wamr`, `WamrPluginHost` here), run on an Android emulator; it has not run on a
  physical device. `res/web/wasm`, which is Go and wazero, is the separate Go route.
- [`res/web`](../../res/web/README.md) is the Go home of the rules the web package ports.

## Browser workspace

The AAR includes `io.github.webong.ext.web.android.BrowserWorkspace` from
[`res/web/android`](../../res/web/android/WORKSPACE.md). It provides
a native page and navigation/state callbacks, using `res/web` for reviewed
userscript preparation. Document-start scripts require the consuming app to add
AndroidX WebKit 1.9+ and an installed WebView supporting that feature; navigation
itself has no AndroidX dependency. The SDK ships the necessary R8 consumer rules.
