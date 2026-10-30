# Rust binding to the CTX C engine

This development crate embeds the shared engine without a Go runtime or Rust
protocol implementation. Build the C engine first, then set
`EXT_ENGINE_LIB_DIR` to its library directory when building this crate. Static
linkage is the default; `--features shared` uses the optional shared library and
requires a configured loader path. Nothing is published to a registry yet.

`Host::process` takes explicit artifact-verification and call-authorization
callbacks. `Host::call` generates envelope metadata; `invoke` accepts a complete
wire request. Both return full response JSON, including public domain errors.
`Cancellation` is thread-safe. `drain` rejects new calls and waits for admitted
work; `close` aborts it. Rust ownership keeps handles alive during borrowed calls.
Policy callbacks must not reenter the same host.

`Host::call_progress` is `call` for an operation that declares progress: the
callback in `Progress::on_event` receives the JSON of each event, in order, before
the call returns, after the engine has validated it and applied the rate limit.
`Host::with_services` offers the plugin's `reverse` operations to it: the engine
admits or refuses each request with a stable code, and the `authorize` and `handle`
hooks of `Services` are the host's policy. They run on engine threads, up to
`max_in_flight` at a time, see a `ReverseCall` with the request JSON and an
engine-owned cancellation to poll, and answer with a payload or a public error.
`endpoint::check` and `endpoint::validate` admit loopback data endpoints and
answer with `Error::INVALID`, `Error::DENIED` or `Error::EXPIRED`, never text.
`Error::PROTOCOL` is a plugin that broke the framing; the session is closed.
`tests/frames.rs` runs the engine's scripted test guest (set
`EXT_REVERSE_TEST_GUEST` to the built `ext_reverse_test`) and the shared endpoint
fixture.

`store::Store` is the engine's package store (`ext_store_*`): `install`, `upgrade`,
`replace`, `remove`, `load` and `ids` over reviewed `ext.package/v1` packages under a
root you supply, as the Go store does. `install`, `upgrade` and `replace` copy the
package, verify it at the source and again once copied, and return the installed
`plugin.json` byte for byte. Failures are `Error::INVALID` (not a valid package, id or
directory, or an artifact over a `Limits` bound), `Error::MISMATCH` (a digest that
differs, or an upgrade that changes the id or contracts), `Error::EXISTS` (an install
over an installed id) and `Error::NOT_FOUND`. A missing root is an empty store, made
by the first install; calls on one `Store` are serialized by the engine, several
processes mutating one root are yours to serialize. `tests/store.rs` covers it.

`Store::list`, `verify` and `list_unverified` are the whole-root reports, and
`store::Layered` is the read-only view over several roots in priority order (`open`,
`load`, `list`, `verify`, `list_unverified`). They return the engine's JSON bytes as
`load` returns `plugin.json`, so you parse them with your own JSON library (the crate has
no dependencies); the shapes are in `ext_store_list` of `lib/plugin/include/ext_host.h`.
`list_unverified` reads no artifact: its result is unverified and must not be launched or
trusted as it stands. A damaged winner in a layered view fails closed instead of falling
back to a lower root.

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
[shared service contract](../../../lib/plugin/services.md).

The crate is additive while parity is completed. The existing parent Rust SDK
still implements the wire protocol independently. Linux/macOS are the current
C engine platforms.

## Guest-only and WASM builds

Use `--features guest-only` to link `libext_guest_static.a`; combine with `shared`
for the native shared guest library. Host and resource wrappers are excluded.
Build C with `EXT_BUILD_HOST=OFF` for WASI, then point `EXT_ENGINE_LIB_DIR` at that
Wasm archive and use `--target wasm32-wasip1`. `examples/guest.rs` executes the same
C dispatcher natively and in WASI. The repository's `scripts/plugin-wasi-core.sh`
reproduces both builds and runs them. Raw FFI users must only call symbols present
in their selected library. No language SDK is required for a wire-only guest.
