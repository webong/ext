# Node binding to the CTX C engine

`loadEngine(absoluteAddonPath)` loads an application-selected Node-API addon.
Build `addon.c` against the installed engine headers, Node headers and
`libctx_host_static` (default) or `libctx_host`. The repository's
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

Call `close` explicitly. It stops new work and disposes native resources after
outstanding calls finish; GC finalizers and environment cleanup are fallbacks.
Guest close allows active calls to finish within their budgets. Pure shared
services are available through `service(operation, input)`.

This covers hosts, guests, pure services, integrity and observation.
Instance/stream manager wrappers are still pending. The
parent TypeScript wire SDK remains available independently. Linux/macOS are the
current C engine platforms.

`directoryDigest(root)` and `verifyArtifacts(manifest, root)` run C filesystem
integrity operations on a worker thread. They reject symbolic links and operate
on application-selected roots; digests do not authenticate publishers.
`HostOptions.observe` receives metadata only, asynchronously, with a bounded
1024-event queue. Observation is best effort: saturated queues and shutdown may
drop events, and observer exceptions never affect plugin calls. Use it for
telemetry, not authorization or durable audit records. Instance and stream
manager bindings are still pending. Import this module as `@ctx/plugin/engine`
when consuming the package, and supply the explicitly selected addon path.
