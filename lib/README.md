# Native libraries

`lib/` is a container for native libraries that several language bindings share.
Nothing lives at this level: no source, no build file, no repository-wide policy.

| Library | Built with | Contents |
| --- | --- | --- |
| [`plugin`](plugin/README.md) | CMake | Portable plugin engine: contract validation, host sessions, guests, streams, instances, package services and runtime backends, as static and shared libraries with public C headers |

The language bindings live under `pkg/plugin-*` (Go, Rust, Zig, TypeScript, iOS and
Android) and build on `lib/plugin`. A library here is not a Go module and is not
listed in `go.work`. The Go implementation in `pkg/plugin-go` remains the behavioral
baseline until the engine reaches parity; see
[the parity checklist](../docs/plugin-engine-parity.md).
