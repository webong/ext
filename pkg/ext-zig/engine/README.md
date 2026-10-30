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

The guest handler uses the C copying result sink. It must cooperate with its
remaining time and must not retain borrowed pointers. Instance/stream APIs are
available through both ownership wrappers and `ffi`. Release all leases and join
calls before destroying a manager. Failed instance close can be retried; failed
`deinit` retains ownership. `Host.setHooks` installs contextual policy/observation. No Go runtime
is linked. The engine currently supports Linux/macOS and is not yet the default
implementation of the parent Zig SDK.
