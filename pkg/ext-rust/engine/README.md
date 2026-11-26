# Rust binding to the ext C engine

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

`Registry` composes guests (`ext_registry_*`): a source host, made with
`Host::with_registry` and a `RegistrySource` (an `authorize` and the bounds, no
handler), has its plugin's reverse calls served by forward calls to the hosts
`Registry::register` names, along the edges `Registry::route` declares. Undeclared
edges are `reverse_undeclared`, unregistered targets `reverse_unavailable`, and a
call that would re-enter a host on its call chain `denied`. A host keeps the
registry alive and leaves it before it is destroyed. `tests/registry.rs` runs the
shared registry cases (set `EXT_REGISTRY_TEST_GUEST` to the built
`ext_registry_test`).

`store::Store` is the engine's package store (`ext_store_*`): `install`, `upgrade`,
`replace`, `remove`, `load` and `ids` over reviewed `ext.io/v1` packages under a
root you supply, as the Go store does. `install`, `upgrade` and `replace` copy the
package, verify it at the source and again once copied, and return the installed
`plugin.json` byte for byte. Failures are `Error::INVALID` (not a valid package, id or
directory, or an artifact over a `Limits` bound), `Error::MISMATCH` (a digest that
differs, or an upgrade that changes the id or contracts), `Error::EXISTS` (an install
over an installed id) and `Error::NOT_FOUND`. A missing root is an empty store, made
by the first install; calls on one `Store` are serialized by the engine, several
processes coordinate using `store::Writer` or `Store::with_writer`. `tests/store.rs`
covers store operations and cross-process writer contention.

```rust,ignore
store.with_writer(ext_home, |locked| {
    // Keep dependency checks, package writes and trust updates in this scope.
    locked.install(source, false)?;
    locked.load("example/provider")
})?;
```

Pass the shared `EXT_HOME`, not the `plugins` store subdirectory. For operations
spanning multiple stores, `store::Writer::acquire(ext_home)` provides the same
native lock as a guard. Drop releases it on errors or panic unwinding; `close()`
consumes it and reports release errors. Acquisition fails immediately with
`Error::EXISTS` when another writer holds the lock. Abandoned locks are not
recovered by age. Release detects replacement ownership and preserves the other
writer's lock. Abrupt termination leaves the lock behind. Low-level store calls
remain available; callers must use a writer scope when sharing their home.

`Store::list`, `verify` and `list_unverified` are the whole-root reports, and
`store::Layered` is the read-only view over several roots in priority order (`open`,
`load`, `list`, `verify`, `list_unverified`). They return the engine's JSON bytes as
`load` returns `plugin.json`, so you parse them with your own JSON library (the crate has
no dependencies without the `download` feature); the shapes are in `ext_plugin_store_list` of `lib/plugin/include/ext_plugin_host.h`.
`list_unverified` reads no artifact: its result is unverified and must not be launched or
trusted as it stands. A damaged winner in a layered view fails closed instead of falling
back to a lower root.

`Store::resolve_entrypoint` is the discovery of what a reviewed package runs: it reads and
verifies the whole package, requires that it serves the exact contract name and version,
compares its directory digest with the `reviewed_digest` the caller recorded (an empty or
changed digest is `Error::DENIED`, so an installed package is never trusted for being
installed) and returns the JSON `{"name","artifact","path","digest"}` of the best `argv`
entrypoint that lists the protocol for a `store::Platform` (Go's `GOOS` and `GOARCH`
names; `Platform::default()` is the platform the engine was built for). A package that
does not serve the contract, or has no entrypoint that fits, is `Error::UNSUPPORTED`.

`Store::install_archive` installs, or upgrades to, the package in a local archive file
(`ext_plugin_store_install_archive`): a zip, gzip-compressed tar or plain tar, told apart by its
content, that must have the pinned SHA-256 before anything is extracted. Extraction is bounded
by the store's `Limits` (`max_archive_bytes` bounds the file, 512 MiB by default) and writes
only directories and regular files. A successful install leaves a receipt, which
`Store::receipt` returns as `{"id","revision","artifact_sha256"}` JSON and which lets a repeat
install of the same archive skip it. Failures are the store's statuses plus `Error::MISMATCH`
for a digest that differs, `Error::UNSUPPORTED` for a format that is not read (zstd, xz,
bzip2) and `Error::MISSING` for an archive that is not there; a link entry, an unsafe path, a
zip64 zip or an archive over a limit is `Error::INVALID`. The receipt file is the one the Go
stores read and write. `tests/archive.rs` runs every case of
`lib/plugin/testdata/v1/archive-cases.json`.

The engine has no HTTPS, so downloading is the binding's: the `download` feature adds
`download::Downloader` (ureq 3 over rustls with ring; nothing else is added to the crate's
dependencies when the feature is off). `Downloader::download` fetches an `https` URL into a
new file under the shared rules: no user information, fragment or missing host, at most five
redirects, each checked by the same rules, `Authorization`, `Cookie` and `Referer` dropped at
the first redirect, a body limit enforced whatever `Content-Length` says, and no file left
behind on failure (an existing destination is never changed). `Downloader::install` downloads
into a private temporary directory under the store's archive limit, calls
`Store::install_archive` and removes the download. `DownloadError::Invalid` is a refusal by
the rules (`Error::INVALID` from `status()`), `Status` an HTTP status other than 200, `Io` a
transfer or file failure and `Store` the engine's status. The client trusts the Web PKI roots
bundled by webpki-roots unless `with_root_certificates` names others, uses a proxy named by
the environment, and has no timeout unless `with_timeout` sets one. `tests/download.rs` runs
every case of `lib/plugin/testdata/v1/download-cases.json` against a local TLS server with a
certificate made by rcgen for the test; the tests always build the feature.

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

Use `--features guest-only` to link `libext_plugin_guest_static.a`; combine with `shared`
for the native shared guest library. Host and resource wrappers are excluded.
Build C with `EXT_BUILD_HOST=OFF` for WASI, then point `EXT_ENGINE_LIB_DIR` at that
Wasm archive and use `--target wasm32-wasip1`. `examples/guest.rs` executes the same
C dispatcher natively and in WASI. The repository's `scripts/plugin-wasi-core.sh`
reproduces both builds and runs them. Raw FFI users must only call symbols present
in their selected library. No language SDK is required for a wire-only guest.
