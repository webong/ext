# ADR: reverse calls, progress frames and data endpoints

Status: accepted. Ships in `contract`, `cengine` and the umbrella at v0.5.0, as one release: the Go side is implemented, and the C engine refuses it (see "Status in v0.5.0" below).

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

A `progress` frame is legal only while a forward call is in flight and carries
its `parent` id. A `reverse_request` is legal at any time after the handshake: it
names the forward call it belongs to in `parent`, or omits `parent` for a
session-scoped call (background work such as a renewal loop). A `progress` frame
with no live parent, or a `reverse_request` whose `parent` is not the live call,
is a protocol violation and fails the session (the child is killed, as for any
malformed frame).

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
  declared `reverse` set, its `parent` (if named) is the live call, the payload
  is within bound, and `Authorize` allows it. `ReverseRequest` tells the handler
  whether the call is call-scoped or session-scoped. Otherwise the plugin gets a
  `reverse_response` error with a stable code; the call and the session continue.
  The codes separate "not offered, fall back" from "forbidden" and make a missing
  declaration easy to debug:

  | code | meaning |
  | ---- | ------- |
  | `reverse_undeclared` | the descriptor's `reverse` list does not name the operation; fix the descriptor (a guest also gets this locally, without sending) |
  | `reverse_unavailable` | the host offers no reverse services, or not yet (before the handshake matched) |
  | `denied` | the host's `Authorize` refused this call |
  | `too_large`, `busy`, `expired` | payload over the bound, too many in flight, deadline already past |

  Which operations a plugin declared is the plugin's own information, so
  `reverse_undeclared` reveals nothing about the host.
- **What the guest sees from a handler's error.** A reverse call fails in the
  guest with a `*plugin.RemoteError` (`errors.As` finds it). When `Handle`
  returns a `*plugin.RemoteError`, the guest receives that code and message
  unchanged, so a host may define its own codes (for example `policy`) and
  guests may branch on them. The code must match the identifier rule (lowercase
  letters, digits and `. _ / -`, at most 256 bytes), the message must be at most
  4096 bytes and the retry hint within range. An error that breaks those rules,
  and any error that is not a `RemoteError`, reaches the guest as
  `operation_failed` with a generic message, and the connection stays up. The
  same holds for a guest handler's error on a forward call.
- **Per-session host state.** There is no per-session value in the API: a host
  keeps it in the context. A call-scoped handler's context derives from the
  context given to `Call`, and a session-scoped handler's from the context given
  to `Open` (its values, not its cancellation).
- The reverse deadline is the earliest of the plugin's deadline, the parent
  call's deadline (if call-scoped) and `Timeout`. When the parent call ends,
  its still-running reverse handlers are canceled and their late results are
  discarded. Session-scoped handlers end when the session does.
- **Context chain (nested hosting).** The context passed to the handler is
  derived from the context of the `Session.Call` that is in flight, so its
  values (including `plugin.ReverseFrom`) and its deadline and cancellation
  carry through: a host that is itself a plugin can answer a child's reverse call
  by making its own reverse call upstream, and deadlines nest child < this call
  < the top call. A session-scoped handler's context is derived without
  cancellation from the context given to `plugin.Open` (values kept), canceled
  when the session ends.
- A host that sets no `Reverse` denies every reverse call.

Guest side (`contract/transport/jsonline`):

- The server runs one reader goroutine for the life of the connection that
  demultiplexes frames: forward requests go to the (still sequential) call
  loop, `reverse_response` frames go to the waiting reverse call. The host
  client likewise reads continuously, so a session-scoped `reverse_request` is
  served even while no call is in flight. A handler that blocks on a reverse call therefore
  never blocks the reader, which is what removes the deadlock on a serialized
  channel.
- The handler obtains the caller with `plugin.ReverseFrom(ctx)`; it satisfies
  `author.Caller`, so `author.Call` works unchanged. `plugin.SessionReverseFrom(ctx)`
  returns a caller for session-scoped work that a guest can keep after its
  call returns; it is valid until the connection closes. Writes use one mutex so
  frames never interleave.
- Cancellation of the parent call cancels the handler's context and its pending
  reverse calls.

In-process and bridge backends implement the same interfaces over direct calls
(no framing).

### Progress

- `plugin.ProgressFrom(ctx)` returns a reporter for the current call.
  `Report(Progress)` sends `{kind: "progress"|"log", stream: ""|"out"|"err",
  text, completed, total, dropped}`. `stream` tells a program's stdout from its
  stderr for `log` events.
- A call can run for days, so there is no cumulative cap. Events are limited by
  **rate** (a token bucket) and size: text at most 2048 bytes per event, and by
  default 50 events and 64 KiB of text per second with a burst of twice that.
  A host may set other limits per call with `CallOptions.Progress`; the limits
  travel to the guest in the request (`progress`, present only for operations
  that declare progress) so both ends apply the same ones.
- Each event's text is valid UTF-8 of at most 2048 bytes. **Splitting a longer
  line into events is the reporter's job**, and so is not cutting a multi-byte
  character in half: `plugin.SplitProgressText` cuts at rune boundaries (and
  replaces invalid bytes with U+FFFD) so every piece is a legal event. Where to
  split (at newlines, say) stays with the guest.
- **The handler can read the rate.** `plugin.ProgressLimitsFrom(ctx)` returns the
  event rate the host set for the call (defaults filled in), and false for an
  operation that did not declare progress, so a streamer can coalesce lines to
  stay inside it instead of letting the limit drop events.
- **What `Report` returns.** Only `ErrProgressDropped` is routine (the event was
  dropped and counted; treat it as sent or slow down). `ErrUnsupported` means the
  operation did not declare progress, so no event will ever flow: stop reporting
  and return the text in the result. `ErrInvalid` means the event breaks the
  rules and nothing was sent. `ErrClosed` means the call is over or the
  connection closed. Any other error means the connection is failing. An
  operation that declared progress always gets a working reporter, whether or
  not the host asked for the events (a host using plain `Call` discards them).
- **A long call has two bounds, and both need raising.** The host's
  `Options.Timeout` (and the caller's context deadline) bounds how long the host
  waits; the guest's own `MaxCallDuration` (default 30 seconds) is a limit the
  guest sets for itself, independent of the host, because a guest decides what it
  is willing to run. A command that runs for minutes or days must raise both, or
  whichever is lower ends the call with a deadline error. The handler's context
  also ends at the request's deadline, so a handler that honors its context stops
  there too.
- Over the limit, events are **dropped and counted, never an error**: the next
  event that is delivered carries `dropped: N` (events lost since the last
  delivered one), and a final marker with the remaining count is delivered
  before the call returns. `Report` reports a drop with `ErrProgressDropped`
  (informational) so a guest can slow down. The host applies the same limits
  as a backstop and drops-and-counts what a guest sends beyond them.
  An oversize frame still fails the session.
- The host receives events through `Session.CallProgress(ctx, contract, op,
  payload, func(Progress))`. `Session.Call` is `CallProgress` with no callback
  and discards events. The callback runs on the connection's reader and must
  not block; a slow callback delays only that call.
- The final response is unchanged and always last. An operation that did not
  declare `progress` cannot emit it (a protocol violation).
- Progress text is untrusted diagnostics. Hosts must not treat it as policy
  input.

### Data endpoints

A new package `contract/endpoint` defines the declared shape that a result may
carry:

```
{ "scheme": "ws"|"wss"|"http"|"https"|"tcp", "address": "127.0.0.1:PORT",
  "path": "/optional", "token": "…", "oneTime": false, "expiresAt": "RFC3339" }
```

- `Validate(e, Policy)` enforces: the scheme is in the policy's required
  allow-list; the host is `localhost` or a loopback IP literal; a port is
  present; no credentials, query or fragment; length limits; `expiresAt` within
  `Policy.MaxLifetime`.
- `Admit(session, e, Policy)` returns a `Grant` whose lifetime ends at the
  earliest of `expiresAt`, `Revoke()` and the session closing
  (`Session.Done()` is new). A host that wants to refuse an endpoint returns an
  error before calling `Admit`.
- **One grant per session is the norm, and reconnects are the host's.** The
  token is *reusable*: `Grant.Token()` returns it any number of times while the
  grant is active, so the host can hand it to successive or concurrent clients.
  An endpoint that must be single-use declares `"oneTime": true`; then the
  first `Token()` succeeds and later calls return `ErrUsed`.
- **Rotation:** the plugin publishes a new endpoint (any result or reverse
  call can carry one), the host `Admit`s it and calls `Revoke()` on the old
  grant. A revoked grant returns `ErrExpired` for everything. How the plugin
  announces it is the consumer's contract; the recommended pattern is a reverse
  operation named `endpoint.rotate` whose payload is `{"endpoint": <Endpoint>}`,
  sent before `expiresAt`. The library standardizes no announcement contract yet.
- **Presenting the token.** One convention for every consumer: for `ws`, `wss`,
  `http` and `https`, the header `Authorization: Bearer <token>` on the handshake
  or request; for `tcp`, the token followed by one `\n` as the first line of the
  stream, before any other data. A listener closes the connection on a missing or
  wrong token, and needs none when `Token` is empty. The token is never put in a
  URL.
  `Grant.AuthorizationHeader()` returns the header value for the first group and
  `Grant.DialTCP(ctx)` connects and sends the token line for `tcp`, so a host
  follows the convention without writing it. `DialTCP` reads a one-time token
  only after the connection exists, and its errors (`ErrDial`) never name the
  address.
- **Declaring an endpoint in a result.** A result carries the object under the
  key `endpoint` (`endpoint.Key`). `endpoint.Decode(raw)` reads it strictly
  (unknown fields and duplicate keys are refused) and applies `Check`, the shape
  rules no policy changes; `Admit` and `Validate` add the host's policy. Errors
  never include a value from the input.
- `host.Process.Done()` returns the session's done channel, so a host that opened
  a plugin with `host.Open` passes the process to `endpoint.Admit` as the
  lifetime.
- Endpoint values are secrets. `String`, `GoString`, `Format` (`%v %+v %#v`)
  and every error from this package print a redacted form (scheme and
  loopback host class only, never address, path or token). JSON encoding
  carries the real value because the protocol needs it. Tests assert that no
  value appears in any error or formatted string.
- This is the standard form of what `session.endpoint` returns for browsers;
  that protocol is unchanged.

### The C engine and other SDKs

**Status in v0.5.0: the Go side ships; the C engine refuses.** The pure-Go
baseline in `pkg/plugin-go/contract` implements everything above, and the
pure-Go build of `pkg/plugin-go/cengine` (the build used without cgo or without
the `ext_cengine` tag) inherits it because it opens sessions through `contract`.
`cengine/host` mirrors `contract/host` exactly (`Options.Reverse`,
`Process.CallProgress`), as the architecture test requires.

The C engine in `lib/plugin` does not carry these frames yet, so it fails
closed instead of ignoring them: its descriptor parser rejects the `reverse` and
`progress` fields, and the C build of `cengine` returns `plugin.ErrUnsupported`
from `Open` and `OpenProcess` when `Options.Reverse` is set and for an operation
that declares progress. `Session.Done` and `Session.CallProgress` exist in both
builds so code compiles against either. The remaining C work, and the Rust, Zig,
TypeScript, Swift and Java bindings that follow it, is listed in
[engine parity](plugin-engine-parity.md).

## Identifiers

Request and call identifiers (`Request.ID`, `Request.Parent`, reverse ids) are
opaque correlation tokens. They are unique within one session, are not stable
across sessions or restarts, and carry no meaning a host may rely on. Do not
persist them or use them as keys in policy.

## Progress markers

The last event of a call that lost events is a marker: `kind: "log"`, empty
`text`, empty `stream`, and `dropped` set. A consumer must accept an event whose
only content is `dropped`. `dropped` on every event, the marker included, counts
events lost since the previous delivered event, not since the start of the call,
so the sum over a call's events is the total lost. Delivered events plus that sum
equal the events reported by the guest.

## Limits and failure model

| Case | Result |
| ---- | ------ |
| `reverse_request` naming a parent that is not the live call | session fails |
| Session-scoped reverse call | same checks as a call-scoped one |
| Reverse call not declared | `reverse_response` error `denied`; session continues |
| No host `Reverse` configured | every reverse call `denied` |
| Payload over bound | `too_large` |
| More than `MaxInFlight` reverse calls | `busy` |
| Reverse handler outlives parent | canceled; late result dropped |
| Progress on an op without `progress` | session fails |
| Progress over the rate or size limit (sender) | dropped, counted, `ErrProgressDropped` |
| Progress over the limit (receiver) | dropped, counted, reported in the next event and at the end |
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

Bidirectional streams, multiplexed concurrent forward calls, and carrying bulk data in
frames.
