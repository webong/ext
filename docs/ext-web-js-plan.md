# ext-web-js browser extension peer library plan

Status: initial package and Chromium examples implemented; browser smoke tests,
Firefox validation, and Xallet migration remain. The local Chrome command-line
smoke attempt opened an isolated profile, but Chrome did not register either
unpacked example extension, so it did not verify browser messaging.

`pkg/ext-web-js` will be a standalone JavaScript/TypeScript package for browser
extension builders. It lets independently built extensions act as hosts,
guests, or both, without requiring Xallet, CTX, the ext plugin engine, or a
particular extension framework. The first deliverable includes working host
and guest extension examples.

## Ownership

`pkg/ext-web-js` owns the peer contract, host and guest APIs, validation, lifecycle,
and implementations of browser extension APIs. Browser-specific compatibility
code stays beside that portable contract inside the library. Builders do not
install CTX adapters to use it.

Existing `adapters/<browser>` continue to own external browser installation,
profile discovery, signing, and extension management. `res/web/extension`
continues to own its artifact inspection and packaging workflows.

Consumers choose permitted peers and capabilities, persistence policy, UI,
and application behavior. Xallet retains entity identity, pairing, grants,
control-plane events, outbox, and remote routing. An optional future binding
in `pkg/ext-ts` may connect browser peers to ext plugin sessions; the browser
package must not depend on that binding or the plugin engine.

## Peer model

Host and guest are roles in a connection, not permanent extension types. A host
can admit several guests. An extension may also host capabilities while
consuming another extension's capabilities. Initial examples demonstrate one
host and one guest; automatic mesh discovery and transitive routing are outside
the first version.

Peers register versioned capabilities and exchange validated requests,
responses, events, and state snapshots. Bind peer identity to the sender
information supplied by the browser, never to an ID claimed in a message.
Keep extension identity separate from application user or entity identity.
Discovery produces candidates; an explicit consumer policy admits them and
authorizes operations. A restored registration does not restore authority.

Disconnect fails outstanding calls. Reconnect establishes a fresh session and
registration, without automatically replaying actions. Registry readers receive
detached snapshots rather than mutable internal state.

## Browser integration scope

| Area | Planned behavior |
| --- | --- |
| Runtime messaging | Internal and external runtime messages and port connections, with request correlation and bounded payloads |
| Port lifecycle | Disconnect handling, pending-call rejection, cancellation, timeouts, listener cleanup, and explicit start/stop |
| Sender context | Browser-supplied extension ID, origin, tab, and frame information when available, exposed to authorization hooks |
| API compatibility | Normalize supported `chrome` and `browser` differences; explicitly report missing features |
| Background lifecycle | Prompt listener registration, fresh admission after restart, stale-session invalidation, and bounded reconnect behavior |
| Discovery | Explicit extension IDs by default; optional management-based candidate discovery only when available and permitted |
| Extension-local contexts | Background, popup, side panel, options page, and content-script communication with distinct authorization boundaries |
| Manifest integration | Target-specific examples and documentation of required messaging declarations and permissions |

Persistent configuration and browser storage helpers are optional. Constructors
must not start polling, enroll peers, or grant access. Do not rely on a permanent
background process or uninterrupted timers for correctness.

A webpage-to-content-script bridge is deferred and must be separately opted
into with source/origin validation if added. Tab automation, cookies, bookmarks,
downloads, and script execution are capabilities consumers may implement, not
requirements of the peer library. Remote transport, native messaging, store
publication, and Xallet enrollment are outside the first milestone.

## Source extraction

The starting point is Xallet's `pkg/frontend/exts/browser-ext/src`:
`types.ts`, `hub.ts`, `spoke.ts`, and `index.ts`. Preserve the useful registration,
state, and event concepts while replacing product-specific assumptions:

- Replace discovery by the display name `Xallet` with explicit peer selection.
- Replace unvalidated `any` payloads with runtime-validated contracts.
- Replace constructor-owned polling with an explicit lifecycle.
- Add admission and per-operation authorization hooks.
- Handle host restart even when its extension ID remains unchanged.
- Move reusable messaging out of Xallet application handlers while leaving
  pairing, identity stamping, and outbox delivery in Xallet.
- Resolve the existing external identity-request/handler mismatch in the
  Xallet compatibility layer rather than making user identity a base feature.

Check source licensing and attribution before copying code. Final public names,
npm package name, wire envelopes, and release setup are implementation decisions
to settle before publishing; this plan does not rename existing SDK packages.

## Package layout

```text
pkg/ext-web-js/
  README.md
  package.json
  index.mjs
  index.d.mts
  test.mjs
  examples/
    host/
    guest/
```

Ship browser-consumable JavaScript and TypeScript declarations. Keep browser
globals behind the browser implementation boundary so contract and lifecycle
tests can run without a browser. No Go module, C engine, or native compiler is
required. WXT may consume the library but is not a runtime dependency.

## Host and guest examples

Both examples build independently against the library and produce separately
loadable extension artifacts. Supply target-specific manifests and reproducible
instructions for selecting development extension IDs. Start with Chromium;
validate a Firefox build separately before claiming support. Document unsupported
features explicitly; Safari support requires its own validation milestone.

The host example:

- Accepts an explicitly configured guest ID through a visible admission policy.
- Shows connected peers, their capabilities, and current state in a small popup.
- Calls the guest's typed echo capability and displays its result or error.
- Exposes one narrowly scoped capability the guest may call back into.
- Shows connection loss and fresh registration after restart.

The guest example:

- Connects to an explicitly configured host ID and registers an echo capability.
- Publishes a small state snapshot and a sample event to the host.
- Calls the host's permitted capability and displays the response in its popup.
- Demonstrates a denied operation and a cancellable delayed request.
- Reconnects and registers again after either background context restarts.

Use only permissions needed for these examples. Default examples must not need
management permission, broad website access, Xallet credentials, a daemon, or
the ext plugin engine. Popup/background communication exercises the initial
extension-local transport; other context integrations receive separate tests
as they are added.

## Implementation sequence

1. Define the versioned peer contract, public API, identity binding, message
   limits, state-update semantics, and authorization hooks. Specify error and
   cancellation behavior and a browser support matrix.
2. Implement the framework-independent protocol and host/guest lifecycle with
   fake transport tests. Cover registration, duplicate/stale sessions, immutable
   snapshots, denied operations, malformed messages, and bounded queues.
3. Implement runtime-message and port integration in `pkg/ext-web-js`, including
   sender verification, serialization, API compatibility, and listener cleanup.
   Validate actual browser behavior before committing to transport assumptions.
4. Implement restart recovery and explicit-ID discovery. Add optional management
   discovery and persistence helpers separately so they do not become default
   permissions or dependencies.
5. Build the host and guest examples and exercise bidirectional calls, state,
   events, denial, timeout, cancellation, disconnection, and background restart
   in real browsers. Publish repeatable local build and smoke-test instructions.
6. Migrate Xallet through a temporary `XalletHubSDK`/`XalletSpokeSDK` compatibility
   wrapper, keeping product policy and remote-host integration in Xallet.
   Migrate spokes incrementally after the standalone examples pass.

## Acceptance

The first milestone is complete when two independently built extensions use
only `pkg/ext-web-js` to establish an admitted connection, exchange validated calls
in both directions, publish state and events, reject unauthorized operations,
and recover after host or guest background restart without replaying actions.

Verification includes protocol/lifecycle unit tests, strict TypeScript consumer
checks, independent example builds, and actual browser smoke tests. Exercise
wrong sender IDs, malformed/oversized messages, unknown protocol versions,
disconnect during a request, cancellation, repeated start/stop, and stale
registration. Distinguish tested browser capabilities from planned support.
