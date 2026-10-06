# CTX plugin SDK for TypeScript

Portable host and guest APIs for `ctx.plugin/v1`. The runtime is dependency-free
ES modules with `.d.mts` declarations; Node-specific stream wiring is a separate
export. The package is private and has not been published to npm.

Use a local `file:` dependency on this directory, or import `index.mjs` directly.
The `@ctx/plugin` and `@ctx/plugin/node` export maps provide runtime and type
entry points. Modern browser bundlers can use the portable entry point with a
caller-supplied authenticated duplex transport. Node examples require Node 22.

```ts
import { Registry, schemaCodec, call, type Method } from '@ctx/plugin';

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
CTX's bounded schema vocabulary, with no external schema engine. Large exact
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

## Run

From the repository root:

```sh
node --test pkg/plugin-ts/test.mjs
tsc --noEmit --strict --module NodeNext --moduleResolution NodeNext --target ES2022 pkg/plugin-ts/typecheck.mts
go run ./examples/plugin-typescript
go build -o /tmp/ctx-plugin-example ./examples/plugin-typescript
node examples/plugin-typescript/host.mjs /tmp/ctx-plugin-example
```

Use a platform-appropriate executable path on Windows. The two process examples
exercise both host/guest language directions. Both trust only the explicitly
selected local development fixture; replace that policy in production.
