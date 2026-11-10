# ext for TypeScript (`ext-io`): plugin SDK

> **Why `ext-io`:** the bare name `ext` is taken on crates.io, PyPI and npm, so `ext-io` is the one package for all of ext's APIs, one namespace per area. Only `plugin` is built; `system`, `web` and `int` are reserved names, added when someone needs them.

Portable host and guest APIs for `ext.plugin/v1`. The runtime is dependency-free
ES modules with `.d.mts` declarations; Node-specific stream wiring is a separate
export. It has not been published to npm yet.

Use a local `file:` dependency on this directory, or import `index.mjs` directly.
The `ext-io/plugin` and `ext-io/plugin/node` export maps provide runtime and type
entry points, and `ext-io/plugin/engine` binds the C engine (see below). Modern browser bundlers can use the portable entry point with a
caller-supplied authenticated duplex transport. Node examples require Node 22.

```ts
import { Registry, schemaCodec, call, type Method } from 'ext-io/plugin';

type Echo = { message: string };
const codec = schemaCodec<Echo>({
  type: 'object',
  properties: { message: { type: 'string', maxLength: 256 } },
  required: ['message'],
});
const echo: Method<Echo, Echo> = {
  contract: { name: 'example.echo', version: 'v1' },
  operation: { name: 'echo', surface: 'observation' },
  input: codec,
  output: codec,
};
const registry = new Registry({ id: 'example/echo', revision: 'artifact-digest' });
registry.register(echo, async input => input);
const guest = registry.guest({ authorize: checkGuestPermission });
// await serveGuest(authenticatedIO, guest, shutdownSignal);
// const output = await call(admittedSession, echo, { message: 'hello' });
```

Hosts use `Session.open(descriptor, {verify, connect, authorize})`. All three
callbacks are mandatory. `connect` returns a `Backend`; `JSONLineBackend(io)`
supplies one over explicit I/O. The transport must authenticate its peer and
unblock pending reads/writes when `close()` is called. It must honor abort
signals. Host callbacks and guest handlers must also honor their signals.

Identity/descriptor snapshots are detached copies. Transport errors, invalid
responses and invocation cancellation fail the session. `close()` drains active
calls; a drain cancellation restores admission. `abort()` closes immediately.
No action or stream read is retried. Only explicit `RemoteError` instances expose
public guest errors; other errors become `operation_failed`.

`Registry` freezes handlers for each created guest. Method codecs perform runtime
validation; TypeScript types alone are insufficient. `schemaCodec` implements
ext's bounded schema vocabulary, with no external schema engine. Large exact
integers and decimals should use strings and domain validators: JavaScript cannot
preserve arbitrary JSON numeric precision. The parser rejects unsafe integers,
nonfinite numbers, duplicate keys, invalid UTF-8, trailing values, excessive
nesting and frames over 24 MiB. IO adapters must return nonempty byte chunks of
at most MaxFrameBytes+1, or null for EOF. Canonical envelope field spelling is
required. Contract schemas should avoid relying on decoder-specific coercions.

`healthMethod` and `openStream` consume the same optional contracts as Go. Host
callbacks use a separately admitted reverse session. Application UI frameworks,
translations, credentials and process launch policy belong to the embedding
application. Go's instance manager and package/graph tooling currently live in
Go; they are not duplicated in this JavaScript package.

## Report progress and call the host

A guest can report progress while a call runs and call back into its host, over a
transport that carries frames (`serveGuest` on a process or a socket; not the C ABI).
An operation that reports declares `progress: true`; the host operations a guest may
call are declared with `registry.declareReverse`. A typed handler gets the call's
`frames` as its fourth argument.

```ts
const registry = new Registry(identity)
  .declareReverse({ name: 'host.svc', version: 'v1' }, [{ name: 'sign' }])
  .register({ ...method, operation: { name: 'echo', progress: true } }, async (input, signal, request, frames) => {
    // Events reach the host in order, before the result. 'dropped' is routine: the
    // rate limit the host set dropped it and counts it for the next event.
    await frames.progress({ kind: 'log', text: 'starting' });
    await frames.progress({ kind: 'progress', completed: 1, total: 3 });
    // The host's refusal or error comes back as a RemoteError with its code
    // (denied, busy, too_large, expired, ...).
    return frames.reverse!.call({ name: 'host.svc', version: 'v1' }, 'sign', input, signal);
  });
```

`frames.progress` rejects with a `FrameError` whose `code` is `unsupported` (the
operation did not declare progress, or the transport has no frames), `invalid` (the
event breaks the rules; nothing is sent) or `closed` (the call is over);
`frames.progressLimits` is the rate the host set, and `splitProgressText` cuts a long
line into events that each fit. `serveGuest` keeps reading while a handler runs, so a
handler may have several reverse calls outstanding and `frames.sessionReverse` is a
caller that background work keeps after the call returned (a host answers it only
while the session lives). `frames.reverse.call` honors the signal and the call's
deadline. A frame that breaks the rules ends the session: `serveGuest` rejects with a
`FrameError` whose code is `protocol`, and the call in flight is not answered. A
transport with no frames (`Guest.invoke` called directly) gives `frames.reverse` and
`frames.sessionReverse` as null.

The shared fixtures (`lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases,model-cases}.json`,
computed by the Go contract) are reproduced by `frames.test.mjs`, with the exceptions
that follow from JavaScript numbers: an integer beyond 2^53 is refused where Go
accepts it, and a count written `1.0` reads as `1`. The list of exceptions is
checked in both directions, so a case that starts agreeing fails the test until it
is removed. `router.test.mjs` covers the router itself, and `examples/frames/guest.mjs`
is the frames conformance guest of
[docs/plugin-guest-frames.md](https://github.com/webong/ext/blob/main/docs/plugin-guest-frames.md), which
`scripts/plugin-crosslang.sh` runs through the Go session and the C engine.

## Native engine (`ext-io/plugin/engine`)

The portable entry point and `ext-io/plugin/node` are plain ES modules and need nothing native. The
`ext-io/plugin/engine` subpath is different: `loadEngine(absoluteAddonPath)` loads a Node-API addon that
**you build yourself** from `engine/addon.c` (shipped in the package) against the ext engine headers,
the Node headers and `libext_host_static` or `libext_host`. No prebuilt addons are published for any
platform, so installing this package does not give you a working engine binding: `loadEngine` fails
until you supply an addon. See [engine/README.md](engine/README.md); the repository's
`scripts/plugin-cengine.sh` builds and exercises both link choices.

## Run

From the repository root:

```sh
node --test pkg/ext-ts/test.mjs pkg/ext-ts/frames.test.mjs pkg/ext-ts/router.test.mjs
tsc --noEmit --strict --module NodeNext --moduleResolution NodeNext --target ES2022 pkg/ext-ts/typecheck.mts
go run ./pkg/ext-go/examples/plugin-typescript
go build -o /tmp/ext-plugin-example ./pkg/ext-go/examples/plugin-typescript
node pkg/ext-go/examples/plugin-typescript/host.mjs /tmp/ext-plugin-example
```

Use a platform-appropriate executable path on Windows. The two process examples
exercise both host/guest language directions. Both trust only the explicitly
selected local development fixture; replace that policy in production.
