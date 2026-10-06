# CTX plugin SDK

CTX owns the reusable plugin library alongside and `pkg/graph` (including `pkg/graph/supervisor`).
Hosts and guests share one protocol and can select a supported transport.
Adapters and applications own their domain contracts, grants, native behavior,
distribution and activation. HashiCorp go-plugin remains a dependency of
`pkg/plugin-hashicorp` inside the library.

This change is entirely within CTX. Xallet and Cymonkey migration is deferred.
Their existing native protocols are not automatically compatible with CTX.

## Packages

| Package | Responsibility |
| --- | --- |
| `plugin` | Immutable descriptors, exact selection, verification, per-call admission, lifecycle, compatibility preflight and metadata observation |
| `pkg/plugin/author` | Typed methods, runtime validation, descriptor generation and frozen guest dispatch |
| `pkg/plugin/schema` | Bounded `ctx.schema/v1` payload/configuration schemas |
| `pkg/plugin/inprocess` | Trusted endpoints with connection lifetime cancellation |
| `pkg/plugin/jsonline` | Bounded JSON-line transport on a supplied duplex connection |
| `pkg/plugin-hashicorp` | Native go-plugin startup and net/rpc or gRPC bindings |
| `pkg/plugin/nativego` | Dynamic Go `.so` loading through a standard guest factory |
| `pkg/plugin-wasm` | Embedded wazero execution of WASI Preview 1 command guests |
| `pkg/plugin-cshared` | Dynamic C ABI loading with separate guest handles and owned buffers |
| `pkg/plugin-go/cshared/guest` | Go guest lifecycle and cgo export bridge for C shared libraries |
| `pkg/plugin/instance` | Configuration revisions, resource leases, replacement and disposal |
| `pkg/plugin/capability` | Optional health and configuration contracts |
| `pkg/plugin/stream` | Optional, scoped, bounded pull streams |
| `pkg/plugin/packagekit` | Portable manifests, artifact verification, preflight and graph projection |
| `pkg/plugin/plugintest` | Reusable backend conformance tests |
| `pkg/plugin-go` | Go binding to the C engine; static by default, shared optional |
| `pkg/plugin-ts` | Portable JavaScript runtime and TypeScript declarations for hosts and guests |
| `pkg/plugin-rust` | Rust typed registry, host sessions, JSON-line/WASI guests and C ABI host/guest bindings |
| `pkg/plugin-zig` | Zig typed registry, host sessions, JSON-line/WASI guests and C ABI host/guest bindings |
| `pkg/plugin-wasm/crosslang` | Real Rust/Zig/Go host and guest interoperability tests |
| `pkg/plugin-hashicorp/cmd/ctx-plugin` | Read-only package inspection and dependency resolution |

## Compatibility policy

The wire protocol, domain contract versions, SDK/package releases and immutable
artifact revisions are independent. The currently implemented wire version is
`ctx.plugin/v1`. The optional contracts are `ctx.health@v1`,
`ctx.configuration@v1` and `ctx.stream@v1`. Package manifests use
`ctx.package/v1`; their embedded schemas use the `ctx.schema/v1` vocabulary.

* Existing v1 fields and meanings remain stable. Unknown envelope fields,
  duplicate JSON keys, oversized frames and trailing JSON remain errors.
* New capabilities use new named contracts. Changing a payload's accepted shape
  requires a new domain version when old peers cannot safely accept the change.
* A new wire shape requires a new protocol implementation. Adding SDK helpers
  does not require changing the protocol or existing guest executables.
* A new SDK release must pass the frozen v1 fixtures and backend conformance
  suite. Protocol versions are removed only through a documented SDK breaking
  release, after at least 12 months of deprecation and an overlapping release
  supporting both versions. No v1 retirement is scheduled.
* Go source compatibility follows the containing Go module's release policy;
  the TypeScript SDK currently has a development `0.1.0` version. Use keyed Go
  option literals. These source APIs and the stable wire format have distinct
  compatibility obligations.

`NegotiateProtocol(preferred, offered)` selects an exact common version using
local preference. Offers come from reviewed manifests or an authenticated
native bootstrap, before choosing the corresponding binding. It does not add a
new negotiation message to an old v1 guest, infer implementations that are not
installed, or authorize downgrade/retry after handshake failure. `Open` still
checks the entire reviewed identity and descriptor exactly.

`Options.Requirements` checks required domain operations before verification or
connection. `packagekit.SelectEntrypoint` additionally checks the explicitly
named runtime, platform artifact, protocol and exact host dependency versions.

### Backend behavior

| Binding | Concurrent execution | Cancellation mechanism | Process owner |
| --- | --- | --- | --- |
| In-process | Yes, when handler supports it | Request context | Embedding host |
| JSON-line | Serialized per connection | Close connection | Host/supervisor |
| HashiCorp net/rpc | Yes | Close dispensed RPC stream | go-plugin client when using `Connect` |
| HashiCorp gRPC | Yes | Cancel request | go-plugin client when using `Connect` |
| Native Go | Yes, when handler supports it | Cooperative request context; loader/init cannot be interrupted | Embedding host; image stays loaded |
| WASI Preview 1 | Serialized per module | Close pipes and terminate module execution | wazero runtime |
| C shared library | Serialized per handle | Release caller; clean up after native call returns | Embedding host; image stays loaded |

All bind the same `Guest`. A transport error or cancellation after dispatch
fails its CTX session; start a fresh verified session to reconnect. Actions are
never replayed automatically. The standard bindings expose unary calls. The
SDK's pull stream capability works over those unary calls; native gRPC streams,
HashiCorp broker callbacks and reattachment still require explicit bindings.
An in-process handler must honor cancellation and has no isolation boundary.
Subprocess separation also does not constitute an OS sandbox.

The [runtime authoring guide](plugin-runtimes.md) contains build recipes and one
typed implementation shared across native Go, WASI and C shared libraries.
WASI guests use the same `jsonline.ServeStdio` helper as standalone command
guests. The C binding has its own versioned ABI header, while domain envelopes
continue to use `ctx.plugin/v1`. Native loaders require explicit verified paths;
they do not discover, install or authorize artifacts.

## Typed authoring

Define an `author.Method[Input, Output]` with its contract reference, operation,
optional input/output schemas and domain validators. Register it once with
`author.Register`, then use `Registry.Descriptor` and `Registry.Guest`.
Registration rejects duplicate methods and invalid schemas. Descriptor ordering
is deterministic. Guests freeze their declaration and handlers, so subsequent
registration cannot expand a live guest's authority.

The guest's `author.Options.Authorize` is mandatory. The host independently uses
`plugin.Options.Verify` and `Authorize`. `author.Call` applies the same typed
method to a host `Session`. Invalid inputs do not reach the handler. Undeclared
public errors become the existing generic `operation_failed` response. Return
`plugin.RemoteError` only for deliberately public errors.

Schemas are opt-in at the method level; Go field types alone do not express
required fields or all domain constraints. Schema v1 supports objects, arrays,
strings, booleans, numbers, integers and null, with required fields, enums and
size/range constraints. Objects reject undeclared properties by default. It
rejects unsupported schema constructs instead of silently approximating JSON
Schema. Limits include depth 16, 1024 schema nodes and 256 properties per object.
Use strings for integers outside JavaScript's exact integer range in contracts
consumed by the TypeScript SDK. Its parser rejects unsafe integers and nonfinite
numbers. In Go, `schema` numeric comparisons use float64; use a domain validator
for exact decimal arithmetic.

Payload schemas live in reviewed package metadata and typed method definitions;
v1 handshake descriptors intentionally retain their existing fields. Updating a
schema changes the selected package revision and, when necessary, its contract
version. The host must bind the reviewed metadata to installed artifacts.

Runnable template:

```sh
go run ./examples/plugin-sdk --backend jsonline
go run ./examples/plugin-sdk --backend inprocess
```

Both print the configured echo, health and a three-item stream. The existing
`examples/plugin-hashicorp` shows native subprocess ownership for both RPCs.

## Instances and configuration

Keep these lifetimes distinct:

1. Package identity identifies an immutable installed selection.
2. Process ownership belongs to one supervisor or native runtime client.
3. Session ownership covers admission and one transport connection.
4. An instance owns resources for one authenticated subject and configuration.

`instance.Manager[T]` requires a bounded capacity, validator and factory.
`Configure` validates and creates a new instance before replacing the current
one. A failed factory retains the old instance. Reusing a revision with different
configuration bytes is rejected. Acquired leases retain old resources until
released; each retiring instance is disposed once. Configure may report an old
instance's disposal error after committing the replacement. The manager also
reports the first disposal error from `Close` without retaining an unbounded
error history.

Capacity counts current, retiring and creating instances. Reserve headroom for
replacements while old leases remain active. Concurrent updates of one key
return `ErrUpdating`. `Close(ctx)` stops admission, cancels pending factories and waits for leases,
factories and cleanup; a timeout leaves admission closed and permits waiting
again. Factories honor their context; cleanup functions must finish promptly.
The manager does not kill processes or implement durable configuration storage.

`RegisterConfiguration` exposes `ctx.configuration@v1/update`. Its scope mapper
must derive a key from authenticated subject/tenant context and the requested
instance ID. The host controls the allowed configuration and revision. Credentials
use the host's explicit secret-delivery mechanism, not this payload. Manager
observers expose configured/retired/disposed lifecycle metadata. They are local
callbacks, not a durable event journal.

## Streams and host services

`stream.Service` registers `open`, `read` and `close`. It requires authenticated
scope derivation, a capacity and maximum lifetime. Handles are random, ephemeral
and scoped. Each read is independently authorized and carries its expected
sequence and item limit. Pulling supplies backpressure without a producer queue.

Batches contain at most 256 items and 1 MiB of item JSON. Readers must honor the
requested limit, keep allocations bounded, and allow concurrent `Close` to
interrupt a read. EOF disposes resources; unused streams expire. Service closure
cancels factories and closes readers. A read failure terminates the client stream;
there is no replay, resume cursor or implicit retry. Cancellation can fail the
whole underlying session, so expiry is the final cleanup backstop.

For guest-to-host calls, expose the host service as another `Guest` and give the
plugin an explicitly admitted reverse `Session` implementing `author.Caller`.
Apply verification and per-call authorization in that direction too. A supplied
payload or manifest dependency cannot mint a host-service caller. Use a separate
connection for reverse calls to avoid deadlock on serialized JSON-line channels.
The SDK example demonstrates the reverse session through an injected in-process
binding; network endpoint authentication and HashiCorp broker setup are supplied
by the consumer.

## Packaging and graph composition

`packagekit.Manifest` describes artifact digests, runtime entry points,
platforms, provided/required contracts, configuration and payload schemas,
frontend assets/locales and exact supported versions of host-provided frontend
libraries. Its validation is portable and launches nothing. It does not select
React, SystemJS, a browser, a credential store or a signing program.

`VerifyArtifacts` checks listed files and rejects symlinks and traversal paths.
The host authenticates the manifest, protects the installation against concurrent
replacement and decides trust. Checksums alone provide content equality.
Unlisted files are not authenticated by this function; hosts can use
`plugin.DirectoryDigest` for an entire protected installation.

`Resolve` rejects missing or ambiguous providers and cycles, returning a
provider-before-consumer order. `Plan.Graph(namespace)` produces a transaction
for the existing graph library. Applying the transaction and activating the plan
remain host-owned. Graph records do not grant permissions; restart generations,
transactional activation and rollback remain part of the host's composition.

```sh
go run ./pkg/plugin-hashicorp/cmd/ctx-plugin inspect --entry main examples/plugin-package/plugin.json
go run ./pkg/plugin-hashicorp/cmd/ctx-plugin resolve examples/plugin-package/plugin.json
```

Add `--root DIR` to verify artifact contents. `inspect` reports `trusted: false`
because this read-only tool has no publisher trust policy. It never executes or
installs the inspected code. Host runtime requirements and dependency versions
are checked before launch; errors identify the missing contract or dependency.

## Observability and conformance

`Options.Observer` receives verification, connection, handshake and completed
invocation events. Events include bounded identity/operation metadata, duration,
correlation ID and stable outcome code. They contain no payload or error message.
Observers can integrate the host's logger, metrics and tracing through context.
Callbacks must be concurrency-safe, fast, nonblocking and must not panic. No
exporter, background telemetry or payload logging is enabled automatically.

To qualify a new backend, implement a factory that connects to
`plugintest.Guest()` and call `plugintest.Run(t, factory)`. The suite exercises
concurrent invocation, exact handshake rejection, domain-error sanitization,
authorization, cancellation, closed admission and interruption of I/O. Runtime-
specific process cleanup remains covered by each runtime's own tests. Parser
fixtures and domain/schema tests complement the behavioral backend suite.

```sh
go test -race ./pkg/plugin-hashicorp/... ./pkg/plugin-hashicorp/cmd/ctx-plugin
node --test pkg/plugin-ts/test.mjs
go run ./examples/plugin-typescript
```

The cross-language example runs a real JavaScript guest from a Go host. The
reverse direction is available by building that Go example and passing the
executable to `examples/plugin-typescript/host.mjs`. Type declarations have a
separate `typecheck.mts` fixture. CI exercises both directions and the native
backend conformance suite.

## Scope and remaining ecosystem work

### Rust and Zig SDKs

The [Rust SDK](../pkg/plugin-rust/README.md) and [Zig SDK](../pkg/plugin-zig/README.md)
provide typed method registration, immutable guest snapshots, explicit policy
callbacks, envelope validation, host sessions and C ABI export helpers. Their
command guests also compile to WASI Preview 1. Both use the existing wire/ABI
versions, so Go hosts do not need language-specific dispatch.

`scripts/plugin-crosslang.sh` builds real artifacts, runs SDK unit tests, applies
the Go conformance suite to both languages over JSON lines, C shared libraries
and WASI, and exercises Rust/Zig hosts against Go guests. It also checks the
shared malformed-JSON fixtures against both command guests. The dedicated CI
job is configured to run this on Linux and macOS; Go-only test runs skip foreign-artifact tests
unless the artifact environment variables are provided.

The new language SDKs cover v1 authoring and transport sessions. Portable schema
interpretation, instance managers, package graphs and high-level wrappers for
optional capabilities remain in the existing Go/TypeScript packages as
documented there. Foreign peers can call those declared capabilities through
the raw contract APIs. Native host cancellation mechanics are documented per
SDK; a synchronous Zig C call requires the guest to cooperate with deadlines.
The Rust crate and Zig package are local development packages, not published
registry releases. Current examples/CI pin Rust 1.98.1 and Zig 0.17.0.

### Ecosystem adoption

This is the first implemented SDK layer covering the six planned areas. CTX
adapters retain their existing shared descriptor/integrity integration and native
argv/stream behavior. Xallet and Cymonkey are reference consumers for future
adoption; no migration or dependency change is included in their repositories.

Future releases can add remote RPC backends, WASI component-model bindings,
native multiplexed streaming, richer schema tooling and publisher-specific distribution through
the same boundaries. A Grafana plugin or arbitrary HashiCorp interface still
needs an explicit domain translation. Source release/tagging and npm publication
are separate steps; the TypeScript package remains private during development.

### C host embedding prototype

`pkg/plugin-engine` is an opt-in experiment in sharing one native host engine
between Go, Rust, Zig and Node.js. It implements the JSON-line subprocess
backend and preserves the existing guest wire contract. It does not replace
the Go plugin library or port its other runtimes/capabilities. See the
[prototype guide](../pkg/plugin-engine/README.md) and
[evaluation](../pkg/plugin-engine/evaluation.md) for ownership, scope and measured
tradeoffs. Ordinary Go builds do not depend on this C engine.


### Interoperability across implementations

Go, C and language-native SDK hosts can coexist behind the same contract.
`pkg/plugin/interop` plans explicit direct/bridged routes; `pkg/plugin/bridge` exposes a
verified backend as an Endpoint. `pkg/plugin-hashicorp/cmd/ctx-plugin-bridge` connects JSON-line hosts
to HashiCorp gRPC/net/rpc guests, while HashiCorp servers can expose relay
Endpoints in the reverse direction. See [interoperability](plugin-interoperability.md)
for configuration, ownership and the tested host/guest matrix. This extends
compatibility without replacing the existing Go engine.
