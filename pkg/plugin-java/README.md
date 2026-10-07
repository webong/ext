# Java plugin SDK (guest)

`pkg/plugin-java` lets Java code, and Kotlin through the same API, author an
`ext.plugin/v1` guest on the JVM. A small JNI library binds the guest half of the
shared [C engine](../plugin-engine/README.md), which does the strict JSON parsing,
request validation against the descriptor, deadlines and error sanitizing. The
application supplies a descriptor and a `Handler`.

Only the **guest** is here. The host half needs an in-process backend, which is
separate work. Nothing is published yet.

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

The `ext_plugin_*` C ABI that `pkg/plugin-cshared` loads would need a JVM inside
the host process, so a Java library cannot be loaded that way. A JVM guest speaks
JSON lines in its own process, or runs inside an application that provides the
transport. Kotlin/Native, which compiles to a native library, could export that
ABI; it is not attempted here.

## Building and testing

```sh
scripts/plugin-java.sh          # needs a JDK and CMake
```

The script builds the JNI library with CMake, compiles the classes with
`javac -Xlint:all -Werror`, runs the tests with `java -ea` (no test framework is
assumed), and writes a launcher for `ConformanceGuest`, the shared
`ext.conformance/v1` fixture. `scripts/plugin-crosslang.sh` runs that launcher
through the same Go suite as the Rust and Zig guests, and requires it when a JDK
is present.

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
  shared plugin engine, `pkg/plugin-engine`; the page explains the two senses of
  "engine".
- [The web engine](../../docs/web-engine.md) runs web content in a browser the host
  chooses, including the iOS Simulator and Android devices. It does not author plugins.
- A WASM interpreter reachable from the C engine does not exist yet. Until it does,
  a mobile app that hosts third-party plugins after release has no route that
  satisfies the no-downloaded-code rules. That is open work shared with
  `pkg/plugin-wasm`, which is Go and wazero.
