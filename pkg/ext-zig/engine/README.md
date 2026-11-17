# Zig binding to the CTX C engine

Build with Zig 0.17.0 and a prebuilt C engine:

```sh
zig build --build-file pkg/ext-zig/engine/build.zig \
  -Dengine-include=/absolute/ctx/lib/plugin/include \
  -Dengine-lib=/absolute/lib/plugin/build test
```

Add `-Dshared=true` for optional shared linkage. The public module is
`ext_plugin_engine`. Its `ffi` module is generated from all four C headers by
`translate-c`, so foreign layouts and resource APIs are not manually duplicated.
`Host`, `Guest`, `Instances`, `Lease`, `Streams`, `Cancellation`, `Buffer`, and service helpers provide the
common ownership operations. Call `deinit` exactly once after concurrent uses
have joined; owned values must not be copied. Callbacks and their user data must
outlive handles and must not reenter the same host.

`Host.callProgress` is `call` for an operation that declares progress: the
callback in `Progress.deliver` receives the JSON of each event, in order, before the
call returns. `Host.setServices` offers the plugin's `reverse` operations to it:
the engine admits or refuses each request with a stable code, and the `authorize`
and `handle` callbacks of `Services` are the host's policy; they run on engine
threads, receive the request JSON and an engine-owned cancel signal, and emit one
payload or public error. `endpoint.check` and `endpoint.validate` admit loopback
data endpoints and fail with `error.Invalid`, `error.Denied` or `error.Expired`,
never text; `error.Protocol` is a plugin that broke the framing. The tests of these
read `EXT_REVERSE_TEST_GUEST`, `EXT_REVERSE_TEST_DESCRIPTOR` and
`EXT_ENDPOINT_CASES` (see `scripts/plugin-cengine.sh`) and are skipped without them.

`Store` is the engine's package store (`ext_store_*`): `install`, `upgrade`, `replace`,
`remove`, `load` and `ids` over reviewed `ext.package/v1` packages under a root you
supply, as the Go store does. Failures are `error.Invalid` (not a valid package, id or
directory, or an artifact over a `StoreLimits` bound), `error.Mismatch` (a digest that
differs, or an upgrade that changes the id or contracts), `error.Exists` (an install over
an installed id) and `error.NotFound`. `install`, `upgrade`, `replace`, `load` and `ids`
return a `Buffer` that is freed with `deinit`; `ids` is a JSON array of strings. A missing
root is an empty store, made by the first install; calls on one `Store` are serialized by
the engine, several processes mutating one root are yours to serialize.

`Store.list`, `verify` and `listUnverified` are the whole-root reports, and `Layered` is
the read-only view over several roots in priority order (`init` with a slice of
`[*:0]const u8` roots, then `load`, which returns a `Served` with the manifest and the
layer, `list`, `verify` and `listUnverified`). Each report is a `Buffer` of the engine's
JSON, freed with `deinit`; the shapes are in `ext_store_list` of
`lib/plugin/include/ext_host.h`. `listUnverified` reads no artifact: its result is
unverified and must not be launched or trusted as it stands. A damaged winner in a layered
view fails closed instead of falling back to a lower root.

`Store.resolveEntrypoint` is the discovery of what a reviewed package runs. Given an
`EntrypointRequest` (the id, the contract name and version and the protocol it must serve,
the `reviewed_digest` the caller recorded, and an optional `os` and `arch` in Go's `GOOS` and
`GOARCH` names, the platform the engine was built for when null) it reads and verifies the
whole package and returns a `Buffer` of the JSON `{"name","artifact","path","digest"}` of the
best `argv` entrypoint that lists the protocol. A changed or empty digest is `error.Denied`,
so an installed package is never trusted for being installed; a package that does not serve
the contract, or has no entrypoint that fits the platform, is `error.Unsupported`.

`Store.installArchive` installs or upgrades from a local zip, gzip-compressed tar or plain tar
after checking it against a pinned SHA-256, optionally requiring the package id it must hold, and
returns the installed `plugin.json`; `Store.receipt` returns the receipt that install left, the JSON
`{"id","revision","artifact_sha256"}`, or `error.NotFound`. The archive file is bounded by
`StoreLimits.max_archive_bytes` (512 MiB when zero) and its extraction by the tree limits. Besides the
statuses of `install` and `upgrade`, a digest that differs or an archive of another id is
`error.Mismatch`, an unsafe or over-limit entry or a zip64 archive `error.Invalid`, a format that is not
read (zstd, xz, bzip2) `error.Unsupported` and an archive that is not there `error.Missing`.
`src/archive_tests.zig` runs every case of `lib/plugin/testdata/v1/archive-cases.json`.

The engine has no HTTPS, so `download.Downloader` fetches an archive with `std.http.Client` under the
rules every binding follows: `https` only, with a host and without user information or a fragment, for
the first request and every redirect; at most `download.max_redirects` (5) redirects; `Authorization`,
`Cookie` and `Referer` dropped at the first redirect; a body no larger than the limit, whatever
`Content-Length` says; and no file left behind, nor an existing one changed, on failure. `init` trusts
the system roots and `trustOnly` the PEM certificates of one file; there is no proxy or timeout (cancel
the `Io` operation instead). `download` writes a new file, and `install` downloads into a private
temporary directory under the store's archive limit, calls `Store.installArchive` and removes the
download. A refusal by the rules is `error.Invalid`, another status than 200 `error.HttpStatus` and any
other failure `error.Transfer` or `error.DestinationExists`. `src/download_tests.zig` runs every case
of `download-cases.json` against a local TLS origin, `testdata/origin.py`, which needs `python3` and
`openssl` and is skipped without `python3`. The fixtures are found through `-Dfixtures`, by default
`lib/plugin/testdata/v1` of the repository; both tests skip when it is not there.

The guest handler uses the C copying result sink. It must cooperate with its
remaining time and must not retain borrowed pointers. Instance/stream APIs are
available through both ownership wrappers and `ffi`. Release all leases and join
calls before destroying a manager. Failed instance close can be retried; failed
`deinit` retains ownership. `Host.setHooks` installs contextual policy/observation. No Go runtime
is linked. The engine currently supports Linux/macOS and is not yet the default
implementation of the parent Zig SDK.
