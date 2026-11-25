# Ext graph shared by plugin and ledger

Status: accepted; initial C package projection implemented.

Ext graph is the common model of entities and relationships used by plugin and
ledger. It is not synonymous with either the plugin dependency resolver or the
ledger database. An application can use either library independently, or use
both with the same graph data.

## Ownership

- `lib/plugin` owns package identity, dependency resolution, peer routes and
  execution. It projects its facts into Ext graph data. It does not import ledger.
- `lib/ledger` implements graph storage, queries, history, machine observations
  and supervision. It accepts graph transactions without knowing plugin package
  names or interpreting their execution semantics. It does not import plugin.
- Applications choose which facts to publish and which calls to authorize.
  A graph edge never grants execution, trust or access to a machine resource.
- Language bindings expose the C operations; they do not reimplement graph
  projection or lifecycle rules.

The existing vertex, edge, schema and transaction wire contracts under
`lib/ledger/testdata/store/v1` are the Ext graph data model. Their current physical
location remains unchanged to preserve existing conformance consumers. This
increment adds no third library or duplicate graph store.

## Initial implementation

`lib/plugin` exposes `package.graph` with `{packages, os, arch}`. It resolves
the supplied manifests using the existing C dependency resolver, then returns
`{schema, transaction}`. Both objects can be passed unchanged to the C ledger's
`store.register` and `store.apply` operations.

The namespace is `ext.plugin`, schema version `1`. Package vertices have kind
`ext.plugin/package`; their attributes contain the full plugin identity.
Dependency edges have type `ext.plugin/requires`, point from consumer to provider,
and carry the resolved requirement. Vertex IDs are the plugin identity SHA-256
keys already used by the resolver. Edge IDs include the consumer key and the
binding's index in the plan; they are plan-local, not stable identifiers across
arbitrary declaration changes.

These are declarative package facts. Projection performs no installation,
execution, approval or observation of a running process. Applying a transaction
upserts the supplied records; it does not remove facts omitted by a later plan.
An application must explicitly reconcile deletions or keep separate plan stores.
This operation does not yet implement continuous inventory synchronization.

The shared projection fixtures are `lib/plugin/testdata/v1/graph-cases.json`.
The plugin C fixture runner verifies exact output. `TestExtGraphAcrossLibraries`
loads both C libraries, passes successful projections to ledger unchanged, and
checks the stored vertices and edges. The package acceptance gate builds both
libraries and requires this test and its subtests to pass.

## Execution planning

Execution planning now lives in C as `package.execution`. It takes manifests,
an explicit target, a root `{identity, contract}`, and an optional `max_hosts`
(default 128, range 1–1024). It returns reachable nodes with their routes, a root
index, and provider-first node indices in `order`. A host is keyed by full
identity and selected contract, so several operations can share a host without
merging different contract selections. Unrouted dependencies are resolved but
are not scheduled for execution. Capacity exhaustion returns `capacity`;
invalid input and dependency cycles return `invalid`.

The resource CLI now consumes this plan instead of implementing its own recursive
traversal. The C resolver runs once per plan. Shared fixtures cover limits,
root identity/contract selection, shared providers and transitive startup order;
live resource acceptance covers startup failure and cancellation with the C plan.
Planning grants no authority and starts no hosts.

## C execution lifecycle

The described `execution_graph` object now owns the running hosts and their
private package store. Configuration supplies package rows (`manifest` and
`directory`), an exact root identity and contract, target OS/architecture,
`temp_root`, and optional `max_hosts`. Opening and reading `plan` launch nothing.

The application reviews each reachable package under its writer boundary, then
calls `start` with `reviews: [{identity, digest}]` and a bounded `timeout_ms`.
Every reachable identity requires one review. The engine verifies those digests,
compares copied manifests with the selection, resolves every private entrypoint,
and only then starts providers before consumers. A digest supplied here is the
application's launch approval for those exact bytes; it is not a transferable
trust record and the graph does not independently choose a trust scope.

`call` invokes the root. Reverse authorization events carry exact `consumer` and
`provider` identities plus the `request`; absent or denied application policy
refuses the call. The graph owns all internal host handles. Closing wakes policy
waiters, stops consumers before providers, and retains the copies until in-flight
native operations have returned. Failed startup removes partial copies and
terminates any hosts already started. A failed graph cannot be restarted.

The CLI now supplies selection, source-writer coordination, review and per-call
policy. It no longer owns provider traversal, handles, startup order, snapshot
creation, or snapshot cleanup. The same described C object is available to other
language bindings without a CLI dependency.

## Live sessions and reconciliation

The C `execution_graph.observe` operation reports every ready host's selected
identity, contract, routes, unique session ID and child PID. Session IDs contain
192 random bits per execution plus the node index; reopening a package does not
reuse its session. A PID is only a locator. The channel retains its child until
teardown; it does not reap and then continue advertising that PID.

An application collects each PID through ledger `collect` (`what: process`) and
passes its `host_id`, `pid` and nonempty `start_id` to ledger `process_identity`.
That operation uses exactly the recorder's identity rule and returns an
`ext.system` reference. It does not collect or authorize anything itself.

`execution_graph.snapshot` accepts a dedicated namespace and those references
in `observe` order, each augmented with the original `session`. C checks the
session and PID against its ready hosts and returns the shared graph schema and
transaction: session vertices, package references, process references, and
`instance-of`, `runs-in` and `routes-to` edges. Reference vertices carry the
canonical ID and namespace in `attributes.reference`; edges stay within their
namespace, as the graph contract requires. Package IDs match `package.graph`;
process IDs match the ledger recorder. Route attributes retain the selected
contract and operation. References are caller-supplied observations, never grants.

A graph snapshot is a dated observation, not a liveness lease. The owning
application serializes collection/publication with shutdown and uses a separate
namespace per execution (or combines all executions before publication). A
failed or closing graph cannot produce a partial desired snapshot. After close,
the owner explicitly reconciles an empty namespace. No plugin callback silently
deletes ledger history and no library imports the other.

Ledger `store.reconcile` accepts only `namespace`, `expected_revision`, `vertices`
and `edges`; both arrays and the revision are required. Under the backend's write
lock it computes omitted vertex/edge deletions and applies the entire desired
namespace atomically. A stale revision returns `changed`, with no retry. Invalid
or dangling desired edges leave the namespace untouched. Empty arrays explicitly
clear it. Other namespaces are unaffected. The existing transaction and snapshot
bounds apply; incomplete scans use `apply`, never `reconcile`.

The package acceptance gate requires the cross-library process-identity fixtures,
a real two-host routed execution correlated with the recorder, and reconciliation
on memory and reopened file stores. These checks do not substitute for a consumer
that continuously observes and publishes its live graph.

## Platform portability and capability boundaries

Decision accepted 2026-11-25; the requirements below are implementation work,
not a claim that every platform or backend already satisfies them.

Ext provides one graph model and portable planning contracts. It does not promise
identical execution, observation or background availability on every operating
system and architecture. Plugin owns implementation selection, planning and
execution; ledger owns recorded facts, observations and supervision. Both use
Ext graph. Applications retain authorization and lifecycle policy.

### Execution is a capability, not a consequence of a plan

A valid dependency plan describes what is needed. It does not prove that the
current host can run it. Package trust, target compatibility, runtime availability
and application authorization are separate checks; none substitutes for another.

Before starting any node, execution preparation must check every reachable node
against the actual host's backend capabilities and required grants. Unsupported
requirements must identify the affected node and capability without starting a
partial graph. Availability and permission can change after preparation; startup
must revalidate them and retain the existing partial-start cleanup guarantees.
Preparation must not prompt for permission or grant it implicitly.

Consumers select by capability rather than OS-name branches. Native mechanisms
remain platform-specific underneath the portable interface. Native artifacts
must match the target OS, architecture and ABI; a portable guest still requires
a compatible runtime and supported imports. A backend fallback is permitted only
when it satisfies the declared requirements and application policy, including
isolation and cancellation. It must not silently weaken them.

The current process-based `execution_graph` lifecycle and PID correlation are one
execution profile. Embedded guests need session identities and observations that
do not require a child process. Several guest sessions may share one application
process; they must not be reported as separate OS processes. Preserve the current
process contract and introduce any additional runtime representation through
versioned fixtures before changing bindings.

### Observations have scope and coverage

Ledger accepts application-owned facts and observations available through the
host's permitted APIs. It does not imply machine-wide access. Each collection
must identify its scope, observation time and coverage, using the existing
`complete`, `partial`, `permission-denied`, `unsupported` and `unavailable`
vocabulary from [the architecture ADR](adr-architecture.md). Reuse its reason
and error codes; additions require fixtures rather than platform-specific strings.

An empty complete collection means no matching resources were found within that
scope. Denied, unsupported, interrupted or partial collection does not mean the
resources do not exist. Retained observations keep their original timestamps;
reading stored data must not make it appear freshly observed.

Only a complete authoritative snapshot of the namespace's owned scope may drive
deletion through `store.reconcile`. A collector losing permission or being
suspended must not reconcile an empty namespace. Use `apply` for partial facts
and record coverage separately. Explicit owner teardown may still clear its
execution namespace as described above; that is a lifecycle decision, not an
inference from a failed scan.

### Mobile execution and lifecycle

The baseline is an ordinary sandboxed application. Root access, debugging
privileges, special entitlements and device-management roles are not assumed.
Building the engines in C does not enlarge the embedding application's access.

| Concern | iOS | Android |
| --- | --- | --- |
| Guest execution | Do not assume downloaded native executables or desktop-style helper processes. Use permitted bundled implementations or embedded runtimes. | Subprocess support does not escape the app sandbox. Apps targeting Android 10 or later cannot execute files directly from their writable app home directory. |
| Observation | App-owned state and information exposed by permitted system APIs; no general access to other apps' private state. | App-owned state and permission-scoped system APIs; native code remains subject to app isolation. |
| Background work | OS-managed tasks and background modes; continuous execution is not guaranteed. | OS-managed background work and foreground-service restrictions; continuous execution is not guaranteed. |

The [mobile WebAssembly ADR](adr-mobile-wasm.md) continues to select the system
web engine first and WAMR as the fallback for mobile reactor guests. This decision
does not replace that backend choice or certify distribution-policy compliance.
OS execution support and app-store distribution rules require separate review.

Applications provide the lifecycle hooks for suspend, resume and termination.
Operations remain bounded and cancellable while the host is scheduled; no promise
is made that cleanup or a timeout callback runs while the OS suspends the app.
On resume or restart, revalidate capabilities and session liveness before
publishing fresh runtime facts. Persisted sessions are historical observations,
not proof that a guest survived. No correctness rule may depend on receiving a
final shutdown callback.

### Implementation sequence and acceptance

1. Define versioned fixtures for execution requirements, preparation results and
   scoped observation coverage. Reuse existing capability contracts. Keep plugin
   and ledger independently usable, with no dependency between the C libraries.
2. Implement preparation in `lib/plugin`, then expose it through language
   bindings. Test that a missing capability anywhere in the reachable graph
   starts no nodes, and a capability lost during startup triggers cleanup.
3. Add backend-neutral session projection alongside the process profile. Test
   multiple embedded sessions in one process, provider ordering, authorization,
   cancellation and teardown without inventing child PIDs.
4. Implement coverage-aware collection/publication and lifecycle integration.
   Test complete-empty, partial, denied and unsupported collections; permission
   loss must preserve existing facts rather than delete them. Test resume and
   restart with stale sessions, including termination without a cleanup callback.
5. Exercise the same graph fixtures through desktop and mobile bindings, then
   run native integration checks for each claimed OS/architecture/backend.
   Include physical iOS and Android devices for mobile lifecycle claims. Report
   compilation, simulator/emulator runs and device runs separately. A required
   check that is skipped cannot count as a passing acceptance result.

The first end-to-end proof is an application that plans and runs an embedded
guest, records its session and permitted observations in ledger, survives a
background/resume cycle, and reports an unavailable process capability explicitly.
It must preserve the same graph semantics as the desktop process proof without
claiming the same host access. Until those checks pass, portable design and
verified platform support remain distinct claims.

Platform references checked for this decision:

- [Apple runtime security and sandboxing](https://support.apple.com/en-ie/guide/security-pdf/sec15bfe098e/1/web/1)
- [Apple background execution](https://developer.apple.com/documentation/uikit/about-the-background-execution-sequence)
- [Android application sandbox](https://source.android.com/docs/security/app-sandbox)
- [Android executable-file restrictions](https://developer.android.com/privacy-and-security/security-best-practices)
- [Android background service restrictions](https://developer.android.com/develop/background-work/services/fgs/restrictions-bg-start)

## Remaining work

Adopt publication/reconciliation in consuming applications, including their
ownership and restart policy. Nested product-command handoff still requires its
own ownership protocol. Migrate other consumers before retiring their duplicate
Go implementations.
Implement the platform capability, observation coverage and mobile lifecycle
requirements above before claiming equivalent execution support across platforms.
