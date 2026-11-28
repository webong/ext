# Native libraries

`lib/` holds libext, the portable C libraries every binding in `pkg/` is built on.
Nothing lives at this level: no source, no build file, no repository-wide policy.

| Library | Built with | Contents |
| --- | --- | --- |
| [`plugin`](plugin/README.md) | CMake | Portable plugin engine: contract validation, host sessions, guests, streams, instances, package services and runtime backends, as static and shared libraries with public C headers |
| [`ledger`](ledger/README.md) | CMake | Portable graph store, recorder and supervisor, with host-resource collection and private volumes |

The language bindings and platform packages live under `pkg/`. A platform package
also carries that platform's native mechanisms (web, intelligence, actions and
networking) in the platform's own language: `pkg/ext-darwin` in Swift,
`pkg/ext-android` in Java, `pkg/ext-windows` in C# and `pkg/ext-linux` in C. Ext
guests under `res/` adapt both to versioned capabilities, and an application can
use either directly.
