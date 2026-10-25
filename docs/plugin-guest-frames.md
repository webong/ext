# Progress events and reverse calls in the guest SDKs

The Go guest (`jsonline.ServeGuest`) can report progress while a call runs and call
back into its host; the C engine hosts both ([ADR](adr-plugin-reverse-progress-endpoints.md),
[engine parity](plugin-engine-parity.md)). This is the plan and the contract for
bringing the same to the guests the language SDKs serve over JSON lines: Rust,
TypeScript, Java, Python and Zig. Swift has no JSON-line guest and is left out until
one is wanted. The C ABI, WebAssembly reactors and HashiCorp have no frame channel
and are out of scope, as are the host sides of these SDKs (they would reject a
guest's frames).

## What a guest must do

These rules come from `jsonline/serve.go` and the host that reads its frames.

- **Model.** An operation may carry `"progress":true`; a descriptor may list host
  contracts under `"reverse"`; a request may carry `"progress":{"eventsPerSecond",
  "bytesPerSecond"}` and `"parent"`. Validation is Go's, pinned by
  `model-cases.json`.
- **Progress.** A call gets a working reporter when its operation declared progress
  and the request carried a rate (the host sends one for every such call, whether or
  not it wants the events). Events are `progress` frames naming the call as `parent`.
  The guest applies the sender-side token bucket (events and bytes of text per second;
  defaults 50 and 65536; a burst of twice the rate), counts what it drops and attaches
  the count to the next event that passes. Before the response it sends a marker,
  `{"kind":"log","dropped":n}`, for drops still unreported; nothing follows the
  response. Text is at most 2048 bytes of valid UTF-8; a longer line is cut on
  character boundaries (`SplitProgressText`).
- **Reverse calls.** Only operations the descriptor lists under `reverse`; others are
  refused locally as `reverse_undeclared` without a frame. A `reverse_request` frame
  has an id `g<N>` (N counts from 1 on the connection), the call's id as `parent` for
  a call-scoped request (none for a session-scoped one, which may be made at any time
  after the handshake), a deadline (the call's, or 30 s on) and the payload verbatim.
  The host answers with a `reverse_response` frame: a payload (`null` for none) or an
  error with a stable code (`denied`, `busy`, `too_large`, `expired`,
  `reverse_unavailable`, `operation_failed`, or its policy's own). A response for a
  request the guest abandoned is dropped; one for an id never issued is a violation.
- **Reading.** The host sends one request at a time and, while a call is in flight,
  nothing but `reverse_response` frames. A request during a call, an unknown frame
  tag, or a malformed frame is a protocol violation: the guest ends the session (the
  process exits non-zero), as today.

## Design

**A frame router per connection**: one lock around writes; a table from request id to
waiter; a read step that classifies a frame as a request (queued for the call loop),
a reverse response (handed to its waiter) or a violation. It runs in one of two modes
behind one API.

- **Threaded or async** (Rust native, Java, TypeScript): a dedicated reader. The
  handler runs on the call loop. Several reverse calls may be outstanding at once, and
  a session-scoped call may be made from background work after a call returned.
- **Inline** (WebAssembly commands, Zig, Python): no reader. The thread that waits for
  a reverse response reads frames itself until it arrives, which is enough because the
  host sends nothing else during a call. A session-scoped call, and a second request
  outstanding at once, are not available: they fail with `unsupported`.

**One API, spelled in each language's idiom**:

| | |
| --- | --- |
| report an event | sent; dropped by the rate limit (routine, the call goes on); `unsupported` (the operation did not declare progress, or there is no frame channel: stop reporting); `invalid` (the event breaks the rules, nothing was sent); `closed` (the call is over or the connection closed) |
| the call's rate | the resolved events and bytes per second, absent when the operation did not declare progress |
| split text | pieces of at most 2048 bytes, cut on characters, invalid UTF-8 replaced |
| call the host | the payload, or the host's error with its code, message and retry hint; also `closed`, deadline and canceled; `unsupported` where the mode cannot |
| the call-scoped caller | present on a call whenever the transport can carry frames |
| the session-scoped caller | present on a threaded or async guest |

A transport with no frame channel (the C ABI, a reactor, an in-process call) gives
the same API with no capability: reporting is `unsupported` and there is no caller, as
Go's `plugin.ProgressFrom` and `ReverseFrom` behave there.

## Fixtures

The Go contract is the oracle. `pkg/plugin-go/engine/sdk_fixtures_test.go`
computes these files from it, checks the committed copies against them, and rewrites
them with `EXT_UPDATE_FIXTURES=1`. Each SDK reproduces every outcome, as for
`endpoint-cases.json`; the C engine does too (`lib/plugin/tests/fixtures.c`).

| File | Judges |
| --- | --- |
| `lib/plugin/testdata/v1/frame-cases.json` | progress frames, reverse requests and reverse responses: strict decoding and the frame's own rules |
| `lib/plugin/testdata/v1/progress-limiter-cases.json` | the sender's token bucket on an injected clock: whether each event passes, the drop count it carries, the count still pending |
| `lib/plugin/testdata/v1/model-cases.json` | descriptors with `progress` and `reverse`, and requests with a rate and a parent: validity, whether the call gets a reporter, the resolved rate |

## The frames conformance guest

`lib/plugin/testdata/v1/frames-descriptor.json` is the descriptor every language's
frames guest serves (identity `ctx/frames`, revision `fixture-1`); `plugintest.FramesGuest`
is the reference. `plugintest.RunFrames` hosts a guest process through the Go session,
and `pkg/plugin-go/crosslang` runs the same suite through the C engine too, over a Go
client and with the engine owning the process (`TestFramesGuests`; a language
joins by setting `EXT_<LANGUAGE>_FRAMES_GUEST` to its guest process). Results are JSON;
key order does not matter.

Contract `ext.frames/v1`, host contract `host.svc/v1` (`sign` has surface `signing`;
`nothere` is deliberately undeclared):

| Operation | Does | Result |
| --- | --- | --- |
| `stream` (progress) | reports `progress 1/3`, then log `out` "hello", then log `err` "oops" | `{"done":true}` |
| `flood` (progress) | payload `{"count":N}` (default 300): reports N log events of text "x" | `{"dropped":D}`, D the number of reports the rate limit dropped |
| `limits` (progress) | reads the call's rate | `{"declared":true,"events":E,"bytes":B}` |
| `split` (progress) | payload `{"text":T}`: reports each piece of T as a log event on `out` | `{"pieces":n}` |
| `plain` | tries to report one event; reads the call's rate | `{"unsupported":true,"limits":false}` |
| `call` | payload `{"op":O,"payload":P}`: calls host `O` with `P`, call-scoped | `{"result":R}`, or `{"error":{"code","message","retry"?}}` for the host's error (`retry` only when non-zero), or `{"error":{"other":"closed"\|"deadline"\|"canceled"\|"failed"\|"unsupported"}}` |
| `twice` | calls `sign` with `1` then with `2` | `{"first":R1,"second":R2}` |
| `idle` | starts background work that, 20 ms later, makes a session-scoped call to `sign` with `{"n":1}` and keeps its outcome | `{"started":true}` |
| `collect` | waits up to 5 s for that outcome | the outcome in `call`'s shape, or `{"timeout":true}` |
| `burst` | six call-scoped calls to `hold` at once | counts by outcome, `ok` or the error's code: `{"busy":2,"ok":4}` |

A guest that is not concurrent answers `idle`, `collect` and `burst` with the public
error `unsupported`. The host's services answer `sign` with `"signed"`, `empty`
with nothing, `secret` with a policy denial, `fail` with a private error, `errok`
with `{policy, "refused for you", retry 5}`, `errretryok` with retry 31536000000,
`big` with a payload over the 64-byte bound, and `hold` when released.

The suite checks: events in order, then the result, and the same result when the host
asks for none; the flood limited, every drop told to the guest and reported to the host
(in an event or the marker); the rate reaching the guest (defaults and explicit);
`plain` unsupported; text cut on characters; every answer shape above; `nothere`
refused by the guest with no frame sent; the request as the host saw it (id `g1`,
the call as parent, plugin, contract, surface, payload, a deadline not after the
call's); ids `g1`, `g2` in sequence; `reverse_unavailable` from a host with no
services; and, for concurrent guests, the session-scoped call after the call returned
and the in-flight bound.

## Plan

| Phase | Work | Status |
| --- | --- | --- |
| 0 | Fixtures, the frames guest contract, the Go reference guest and driver, the C engine reproducing the fixtures | done |
| 1 | Rust: reader thread, inline mode for WASI, `CallContext` progress and reverse | done |
| 2 | TypeScript: `serveGuest` reads while the handler runs | done |
| 3 | Java: reader thread, `Call` progress and reverse, descriptor model | done |
| 4 | Python: inline mode | done |
| 5 | Zig: inline mode, written without a local compiler | done |

Each phase ends with its guest passing the fixtures and `TestFramesGuests` through the
Go session and the C engine, its own tests, and the docs of its SDK.
