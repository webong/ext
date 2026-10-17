# ADR: reverse calls, progress frames and data endpoints

Status: proposed. Target: `contract`, `cengine` and the umbrella at v0.5.0, as one release.

## Problem

A plugin can only answer calls. Three needs follow from that:

1. A plugin must call back into its host for services the host owns (credential
   use, endpoint validation, environment preparation, redaction). Today the
   host has to hand prepared material to the plugin, and the plugin repeats the
   host's policy, which can drift.
2. A call returns once, at the end. A long call cannot show progress or logs.
3. JSON lines cannot carry continuous data (media, relays, capture frames). A
   plugin can only point at a loopback endpoint, and every consumer invents the
   shape of that pointer and its secret handling.

`docs/plugin-sdk.md` already promises (1) for the TypeScript SDK through "a
separate reverse `Session`". The Go `contract` module has nothing for it.

## Decisions

### Frames stay on the one connection

The JSON-line connection stays one duplex stream with one outstanding forward
call (the existing `gate`). It gains tagged frames, all bounded by
`MaxFrameBytes`. A frame without `"frame"` is exactly today's request or
response, so nothing changes for a plugin or host that uses none of this.

| `frame`            | direction     | meaning |
| ------------------ | ------------- | ------- |
| (absent)           | host → guest  | request (as today) |
| (absent)           | guest → host  | response (as today) |
| `reverse_request`  | guest → host  | a call from the plugin to a host service |
| `reverse_response` | host → guest  | the answer to one `reverse_request` |
| `progress`         | guest → host  | one bounded progress or log event of the current call |

Frames that are not requests or responses are legal only while a forward call
is in flight and carry its `parent` id. A reverse call or progress event with no
live parent is a protocol violation and fails the session (the child is killed,
as for any malformed frame).

A separate connection per direction was rejected: a child process has only
stdin and stdout on every platform the host supports, and extra inherited
descriptors do not exist on Windows without per-platform handle plumbing. It
would also leave the C engine, the TypeScript SDK and the mobile SDKs each
inventing their own second channel.

### Declaration: nothing is implicit

`Descriptor` gains two optional fields, compared exactly by `MatchHandshake`
(expansion and reduction are both mismatches, as for contracts today):

- `reverse`: the host contracts and operations this plugin may call, the same
  shape as `contracts`, with the same count and name limits.
- `Operation.progress`: the operation may emit progress frames.

Both default to absent. An older host rejects a descriptor that uses them at
the handshake (its decoder rejects unknown fields), so the new frames are never
sent to a peer that does not know them. That is a fail-closed break of the
handshake for plugins that opt in, and a minor-version change for the module.

### Reverse calls

Host side (`contract`, `contract/host`):

- `Options.Reverse` registers the services the host offers: a handler
  `func(ctx, ReverseRequest) (json.RawMessage, error)`, an `Authorize` hook with
  the semantics of `Options.Authorize` (called for every reverse call, receives
  copies, required whenever `Reverse` is set), `Timeout`, `MaxPayloadBytes`
  (default 1 MiB, never above `MaxFrameBytes`) and `MaxInFlight` (default 4).
- A reverse call is admitted only if (contract, operation) is in the plugin's
  declared `reverse` set, the parent call is live, the payload is within bound,
  and `Authorize` allows it. Otherwise the plugin gets a `reverse_response` error
  with a stable code (`denied`, `unsupported`, `too_large`, `busy`, `expired`);
  the call and the session continue.
- The reverse deadline is the earliest of the plugin's deadline, the parent
  call's deadline and `Timeout`. When the parent call ends, still-running
  reverse handlers are canceled and their late results are discarded.
- A host that sets no `Reverse` denies every reverse call.

Guest side (`contract/transport/jsonline`):

- The server runs one reader goroutine that demultiplexes frames: forward
  requests go to the (still sequential) call loop, `reverse_response` frames go
  to the waiting reverse call. A handler that blocks on a reverse call therefore
  never blocks the reader, which is what removes the deadlock on a serialized
  channel.
- The handler obtains the caller with `plugin.ReverseFrom(ctx)`; it satisfies
  `author.Caller`, so `author.Call` works unchanged. Writes use one mutex so
  frames never interleave.
- Cancellation of the parent call cancels the handler's context and its pending
  reverse calls.

In-process and bridge backends implement the same interfaces over direct calls
(no framing).

### Progress

- `plugin.ProgressFrom(ctx)` returns a reporter for the current call.
  `Report(Progress)` sends `{kind: "progress"|"log", text, completed, total}`.
- Bounds, enforced by the sender first and by the host as a backstop:
  text 2048 bytes, 1024 events and 256 KiB of text per call. Beyond them
  `Report` returns `ErrProgressLimit` and sends nothing; a host that receives
  more fails the session.
- The host receives events through `Session.CallProgress(ctx, contract, op,
  payload, func(Progress))`. `Session.Call` is `CallProgress` with no callback
  and discards events. The callback runs on the connection's reader and must
  not block; a slow callback delays only that call.
- The final response is unchanged and always last. An operation that did not
  declare `progress` cannot emit it.
- Progress text is untrusted diagnostics. Hosts must not treat it as policy
  input.

### Data endpoints

A new package `contract/endpoint` defines the declared shape that a result may
carry:

```
{ "scheme": "ws"|"wss"|"http"|"https"|"tcp", "address": "127.0.0.1:PORT",
  "path": "/optional", "token": "…", "expiresAt": "RFC3339" }
```

- `Validate(e, Policy)` enforces: the scheme is in the policy's required
  allow-list; the host is `localhost` or a loopback IP literal; a port is
  present; no credentials, query or fragment; length limits; `expiresAt` within
  `Policy.MaxLifetime`.
- `Admit(session, e, Policy)` returns a `Grant` whose lifetime ends at the
  earlier of `expiresAt` and the session closing (`Session.Done()` is new).
  `Grant.Redeem()` returns the token exactly once; later calls return
  `ErrUsed`. A host can refuse by returning an error from its own check before
  `Admit`.
- Endpoint values are secrets. `String`, `GoString`, `Format` (`%v %+v %#v`)
  and every error from this package print a redacted form (scheme and
  loopback host class only, never address, path or token). JSON encoding
  carries the real value because the protocol needs it. Tests assert that no
  value appears in any error or formatted string.
- This is the standard form of what `session.endpoint` returns for browsers;
  that protocol is unchanged.

### The C engine and other SDKs

`lib/plugin` gets the same frames, descriptor fields, bounds and error codes,
and the same endpoint validation, so the conformance fixtures
(`lib/plugin/testdata`) run against both engines. `pkg/plugin-go/cengine`
re-exports the new Go API with identical signatures (the arch test already
requires `cengine/host` to mirror `contract/host`). The TypeScript, Rust, Zig,
Swift and Java bindings follow the C engine and are not part of v0.5.0.

## Limits and failure model

| Case | Result |
| ---- | ------ |
| Reverse call outside a live parent | session fails |
| Reverse call not declared | `reverse_response` error `denied`; session continues |
| No host `Reverse` configured | every reverse call `denied` |
| Payload over bound | `too_large` |
| More than `MaxInFlight` reverse calls | `busy` |
| Reverse handler outlives parent | canceled; late result dropped |
| Progress on an op without `progress` | session fails |
| Progress over bound (sender) | `ErrProgressLimit`, nothing sent |
| Progress over bound (receiver) | session fails |
| Unknown `frame` value | session fails |
| Frame over `MaxFrameBytes` | session fails (unchanged) |

Everything not listed keeps today's behavior, including: cancelling a call kills
the process, and a failed Session cannot be reused.

## Compatibility

All additions are optional. A plugin that declares nothing new behaves
identically on old and new hosts. A host that uses nothing new behaves
identically with old and new plugins. A plugin that declares `reverse` or
`progress` needs a v0.5.0 host. The wire version stays `ext.plugin/v1`.

## Non-goals

Unsolicited guest-initiated calls outside any forward call, bidirectional
streams, multiplexed concurrent forward calls, and carrying bulk data in
frames.
