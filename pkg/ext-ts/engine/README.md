# Node binding to the CTX C engine

`loadEngine(absoluteAddonPath)` loads an application-selected Node-API addon.
Build `addon.c` against the installed engine headers, Node headers and
`libext_host_static` (default) or `libext_host`. The repository's
`scripts/plugin-cengine.sh` builds and exercises both choices. No prebuilt npm
binaries are published yet.

The returned `Host` has `open`, `start`, `invoke`, `call`, `drain`, and `close`.
Verification and authorization callbacks are mandatory and may return promises;
returning false or throwing denies access. `call` supplies identity, surface,
ID and deadline in C and returns the full response envelope. Domain errors remain
in `response.error`. Call options accept a timeout in milliseconds and AbortSignal.

`Guest` accepts an immutable descriptor and an async handler returning
`{payload}` or `{error}`. Private exceptions are sanitized. Native calls run on
libuv workers; callbacks run on the JS thread with a bounded AbortSignal.
Cancellation and late promise settlement do not retain borrowed C pointers.
Callbacks must cooperate and must not recursively wait on the same host.
The shared libuv pool limits simultaneously blocked native calls; applications
must account for that when choosing their worker concurrency.

`call(contract, operation, payload, {onProgress, progress})` hands each progress
event of an operation that declares progress to `onProgress`, in order, after the
engine has validated it and applied the rate limit and before the promise settles;
`progress` sets the rate the plugin may report at. `HostOptions.reverse`
(`{authorize, handle, timeout, maxPayloadBytes, maxInFlight}`) offers the plugin's
`reverse` operations to it: the engine admits or refuses each request with a stable
code, and the two functions are the host's policy, called for each admitted request
with its JSON; `handle` returns `{payload}` or `{error}`. Their `signal` aborts when
the reverse deadline passes, not when the engine cancels the request (the call ended
or the host closed): the answer is then discarded. `engine.endpoint.check(endpoint)`
and `engine.endpoint.validate(endpoint, policy)` admit loopback data endpoints and
throw an error whose `status` is `status.INVALID`, `status.DENIED` or
`status.EXPIRED`, never text. A plugin that breaks the framing rejects with
`status.PROTOCOL`.

`new engine.PackageStore(root, limits)` is the engine's package store: `install`,
`upgrade`, `replace`, `remove`, `load` and `ids` over reviewed `ext.package/v1` packages
under a root you supply, as the Go store does. Each call returns a promise and runs on a
libuv worker. `install`, `upgrade` and `replace` resolve with the installed `plugin.json`
as text, as the source had it; a rejection carries `status`: `status.INVALID` (not a
valid package, id or directory, or an artifact over a limit), `status.MISMATCH` (a digest
that differs, or an upgrade that changes the id or contracts), `status.EXISTS` (an install
over an installed id) or `status.NOT_FOUND`. A missing root is an empty store, made by
the first install; calls on one store are serialized by the engine, several processes
mutating one root are yours to serialize. `close()` lets calls in flight finish.

Call `close` explicitly. It stops new work and disposes native resources after
outstanding calls finish; GC finalizers and environment cleanup are fallbacks.
Guest close allows active calls to finish within their budgets. Pure shared
services are available through `service(operation, input)`.

This covers hosts, guests, pure services, integrity, observation and the
instance and stream resource managers. The parent TypeScript wire SDK remains available independently. Linux/macOS are the
current C engine platforms.

`directoryDigest(root)` and `verifyArtifacts(manifest, root)` run C filesystem
integrity operations on a worker thread. They reject symbolic links and operate
on application-selected roots; digests do not authenticate publishers.
`HostOptions.observe` receives metadata only, asynchronously, with a bounded
1024-event queue. Observation is best effort: saturated queues and shutdown may
drop events, and observer exceptions never affect plugin calls. Use it for
telemetry, not authorization or durable audit records. Import this module as `@ctx/plugin/engine`
when consuming the package, and supply the explicitly selected addon path.

## Resource managers

`new engine.Instances({create, dispose, validate, capacity})` wraps the C
instance manager: `configure(key, revision, config)`, `acquire(key)` (a lease with
`value`, `revision` and `release()`), `remove(key)` and `close()`. Configuration
is any JSON value; keys and revisions are text without NUL. A leased value is never
disposed, even after replacement or `close`. `close` stops admission and waits for
leases: on timeout it rejects with status 6, and you release leases and call it
again. Always release leases; an unreleased lease is leaked rather than released
from a GC finalizer, because a disposer would have to wait on the JS thread.

`new engine.Streams({open, capacity, maxAge})` wraps the scoped pull-stream manager.
`open(scope, parameters)` returns an opaque ID; `read(scope, id, sequence, limit)`
takes sequences starting at 1, each the previous plus one, and limits of 1..256.
A stream the scope does not own, or one already removed, is reported as denied.

Manager calls run on libuv workers and their callbacks on the JS thread, so, as with
hosts, concurrently blocked calls are bounded by the libuv pool. Callbacks must not
await another call on the same manager.
