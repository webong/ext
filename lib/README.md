# Native libraries

`lib/` is a container for reusable implementation libraries. Its platform code
uses the native language of the API it calls: C for portable engines, Swift for
Apple frameworks, Java for Android frameworks, and C# for .NET platform APIs.
Nothing lives at this level: no source, no build file, no repository-wide policy.

| Library | Built with | Contents |
| --- | --- | --- |
| [`plugin`](plugin/README.md) | CMake | Portable plugin engine: contract validation, host sessions, guests, streams, instances, package services and runtime backends, as static and shared libraries with public C headers |
| [`ledger`](ledger/README.md) | CMake | Portable graph store, recorder and supervisor |
| [`system`](system/README.md) | SwiftPM, Android source sets, .NET projects | OS-provided web, intelligence, action and network APIs, plus host and capture mechanisms; Ext guest contracts and entrypoints stay under `res/` |

The language bindings live under `pkg/`. Ext guests under `res/` adapt these
libraries to versioned capabilities; a library can also be used directly by a
native application. `lib/system` carries no Go module or Go implementation.
