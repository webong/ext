# Rust binding to the CTX C engine

This development crate embeds the shared engine without a Go runtime or Rust
protocol implementation. Build the C engine first, then set
`CTX_ENGINE_LIB_DIR` to its library directory when building this crate. Static
linkage is the default; `--features shared` uses the optional shared library and
requires a configured loader path. Nothing is published to a registry yet.

`Host::process` takes explicit artifact-verification and call-authorization
callbacks. `Host::call` generates envelope metadata; `invoke` accepts a complete
wire request. Both return full response JSON, including public domain errors.
`Cancellation` is thread-safe. `drain` rejects new calls and waits for admitted
work; `close` aborts it. Rust ownership keeps handles alive during borrowed calls.
Policy callbacks must not reenter the same host.

`Guest` snapshots its descriptor and dispatches through a Rust handler. Handlers
receive borrowed requests, remaining time and optional cancellation. Public
errors are explicit; panics/private failures are sanitized by the engine. Native
callbacks must cooperate with deadlines. Dropping a Guest requires that all
borrowed calls have finished, enforced by the safe API.

`service`, `sha256`, `verify_artifacts` and `directory_digest` bind shared C
services. `Host::with_observer` installs synchronous metadata observation.
`resources::Instances<T>` and borrowed `Lease<T>` pin values across replacement;
`resources::Streams<T>` owns scoped readers implementing `Stream`. Explicit close
reports errors; Drop joins cleanup. Factories and close callbacks must cooperate
and must not reenter their manager. `ffi` also exposes raw extension/resource APIs. See the
[shared service contract](../../../pkg/plugin/cengine/services.md).

The crate is additive while parity is completed. The existing parent Rust SDK
still implements the wire protocol independently. Linux/macOS are the current
C engine platforms.

## Guest-only and WASM builds

Use `--features guest-only` to link `libctx_guest_static.a`; combine with `shared`
for the native shared guest library. Host and resource wrappers are excluded.
Build C with `CTX_BUILD_HOST=OFF` for WASI, then point `CTX_ENGINE_LIB_DIR` at that
Wasm archive and use `--target wasm32-wasip1`. `examples/guest.rs` executes the same
C dispatcher natively and in WASI. The repository's `scripts/plugin-wasi-core.sh`
reproduces both builds and runs them. Raw FFI users must only call symbols present
in their selected library. No language SDK is required for a wire-only guest.
