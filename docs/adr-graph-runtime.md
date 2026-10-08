# ADR: Generic graph storage and process supervision

Status: accepted for the CTX library.

## Ownership boundary

CTX supplies reusable mechanics through the public `github.com/webong/ext/pkg/graph`
and `github.com/webong/ext/pkg/graph/supervisor` packages. CTX graph records are generic
vertices and directed edges. A consumer registers a namespace and schema version,
supplies an optional validation callback, and owns its kind and relationship
vocabulary. Namespace-qualified names use the form `<namespace>/<name>`.

CTX does not assign meaning, authority, permission, trust, or membership to a
record or relationship. A schema callback may reject a proposed transaction
based on a transaction-scoped read-only view. It must not be used to create a
second authorization system inside CTX. Cycles are allowed unless a consumer
validator rejects them.

The `supervisor` package owns local process mechanics. Callers decide why a
process may run and pass an already-approved `Spec`. The supervisor starts,
observes, restarts, and stops that process. Endpoint and connection records are
declarations; consumer hooks verify readiness and protocol handshakes. Runtime
facts describe observations and never imply consumer authority.

## Graph API contract

Consumers call `Register(Schema)` before use, then `Apply(Transaction)` to make
an atomic change. All records in a transaction belong to one namespace. Vertices
must exist before edges can reference them; node and edge creation can happen in
the same transaction. An invalid endpoint, name, secret-like field, or consumer
constraint rejects the whole transaction without advancing its revision or
publishing a change.

IDs are caller supplied and stable. Repeated IDs upsert records. `CreatedAt` is
preserved on update and `UpdatedAt` plus `Revision` identify the commit that
last changed the record. `ExpectedRevision` compares against that namespace's
current revision. `IdempotencyKey` is scoped to a namespace and binds to the
canonical JSON form of the transaction: replaying the same request returns its
original commit cursor; reusing the key with a different request is an error.

Edges cannot dangle. Deleting a vertex with incident edges fails unless the
transaction sets `DeleteIncidentEdges`, which explicitly detaches all incident
edges. Deleting records and adding replacements in the same transaction is
supported. Each successful transaction advances one namespace revision and one
store-wide monotonically increasing change cursor.

Queries require a positive result limit. Traversal is breadth first, follows
outgoing edges, and takes explicit maximum depth and vertex bounds. Results are
deterministically ordered by IDs where ordering is not traversal order. A
snapshot returns a consistent namespace view and its revision.

`Watch(ctx, namespace, afterCursor, buffer)` replays retained changes newer than
the cursor and then streams later commits in cursor order. An empty namespace
watches all namespaces. If its bounded channel fills, the stream closes; the
consumer should reconnect from the last cursor it processed. Stores retain at
most 4096 commits or 16 MiB of serialized changes by default;
`NewMemoryWithOptions` and `OpenFileWithOptions` can set `MaxChanges` and
`MaxChangeBytes`. The latest commit is retained even if it alone exceeds the
byte limit. Expired cursors return `ErrCursorExpired`, so consumers
must take a fresh snapshot. `Snapshot.HistoryFloor` reports the oldest valid
resume point. Idempotency records expire with their commits.

## Persistence and limits

`graph.NewMemory` is the reference implementation. `graph.OpenFile` stores the
complete state as JSON and commits by writing a mode-0600 temporary file followed
by atomic rename while holding an OS file lock. Every operation refreshes from
the latest committed snapshot under that lock, so short-lived CTX commands and
long-lived local consumers do not overwrite each other's commits. File-backed
watches poll the durable cursor every 150 ms and publish changes in order.
Callers register their schema callback each time a store is opened; the durable
file remembers the namespace schema version and rejects a version mismatch.
The backend does not provide replication. It compacts change history and
idempotency records during commits; live graph records remain until deleted.

The current implementation uses in-memory maps and bounded result slices, and
rewrites the full JSON snapshot on every commit. It does not provide a
distributed index or an unbounded query API. Large graph installations should
use a backend with indexed persistence behind the same interface.

## CTX system context

The public `github.com/webong/ext/pkg/graph/system` package owns the `ext.system`
namespace. Short-lived CTX
commands record the invoking shell session, current directory, detected project,
active profile, and selected adapter context IDs. Optional Bash, Zsh, and
PowerShell prompt hooks refresh shell location after directory changes.
Successful browser opens add the provider, profile, and URL origin. URL paths,
query strings, fragments, arbitrary environment values, and shell history are
not collected. Host shell executables, mounted filesystems, and shared webview
runtimes are discovered directly by the public system graph package,
independently of adapters.
`ctx graph shells`, `ctx graph filesystems`, and `ctx graph webviews` refresh and
expose those host inventories, including executable paths, mounted filesystem
space, and evidence of shared web rendering runtimes. Native
host discovery stays in `pkg/graph/system`; product-specific provider discovery
stays in adapters. `ctx graph scan` records all host inventories and installed
adapters, their declared capabilities and surfaces, and versioned observations
from trusted adapters.
`ctx graph processes` enumerates caller-visible processes; `ctx graph process <pid>`
collects targeted file, mapping, socket, IPC, and usage evidence. The native
collectors live in `pkg/graph/system`, and operate without adapters. Process IDs
include boot and start identity; coverage and observation timestamps distinguish
missing resources from denied, partial, or unsupported inspection. Product
protocols and capabilities remain adapter-owned. See [host discovery](host-discovery.md)
for platform coverage, limits, and reconciliation semantics.
Host consumers declare operation requirements through `ResolveHost` and
`PrepareHost`. Queries refresh stale required categories; preparation performs
current discovery and live validation, preserves explicit selections, and
rejects ambiguous choices. Native webview compatibility is validated by the
embedding backend. Shell launches and protected file exports consume this API.
The fallback `list` collector handles existing browser and manager adapters.
Adapters also project declared resource support, such as `virtualizer` and
`container`, separately from executable capabilities.
The CLI owns native adapter probing; the public package accepts generic
contexts, resources, and relationships and projects them under the machine and
adapter. Named aliases are recorded as declarations. Each scan reconciles
removed inventory records and refreshes an observation timestamp. Core commands
query this projection to find contexts and capabilities before invoking an
adapter.
The graph does not authorize use of an adapter or prove a remote context is
reachable; operations still validate the live endpoint. `ctx graph` is a
short-lived inspection and export interface over the persistent store. Other
services register separate namespaces and cannot change CTX's schema rules.

## Security

Graph records may contain ordinary metadata only. Common secret-bearing field
names (including password, token, credential, private key, and API key) and
explicit `secret-value:` / `private-key:` markers are rejected in attributes and
labels. This catches accidental credential serialization; it cannot identify
arbitrary secret bytes hidden in a generically named string. Consumers must
store only opaque scoped references and resolve them at the point of use.

`supervisor.Spec.Environment` contains ordinary process configuration.
`SecretReferences` maps environment names to opaque `{scope, id}` references;
an injected resolver materializes values only for the child environment. The
resolver result is never returned in an `Instance`, log record, or graph fact.
If a process receives resolved secret references, CTX discards its stdout and
stderr rather than attempting partial redaction. Other output is kept only as
an in-memory tail bounded by `Options.LogLimit`. Hook errors are reduced to
generic lifecycle reasons before graph projection. The supervisor projects
artifact ID/revision/checksum, process state, health, crashes, runtime identity,
endpoint and connection metadata, and generic relationships.

## Process lifecycle contract

When `Artifact.Checksum` is supplied, CTX verifies the selected executable's
SHA-256 before launch. `Options.RequireChecksum` rejects unpinned executables.
The artifact path must be protected against replacement by the embedding
service or its installer; mutation between verification and `exec` remains an
operating-system race.

The default launcher creates an isolated Unix process group or a Windows Job
Object with kill-on-close, and termination targets the process tree. A consumer
can install platform resource or sandbox controls for each attempt through
`Options.ProcessPolicyFactory`; these run before launch and immediately after
start, while CTX still controls the process tree. Windows Job assignment occurs
immediately after `exec.Cmd.Start`; a service requiring containment before the
first instruction must supply a suspended-launch policy through that hook.

Each active process has a renewable lease in `ext.runtime` with a supervisor
owner ID, expiration, PID, and process identity where the OS permits reading
it. On restart, a live unexpired lease returns `ErrRuntimeLeased`. Expired
leases follow `Options.OrphanPolicy`: the default marks the process orphaned
and blocks new starts until `ResolveOrphan` terminates it; `OrphanTerminate`
automatically terminates identity-matched process trees. CTX never kills a PID
whose identity cannot be verified. A dead process is marked stopped. The
supervisor retains 256 terminal instance vertices per runtime by default;
`Options.RetainTerminal` changes that bound.

`EndpointReady` and `ProtocolHandshake` run in order with a bounded context.
The process enters `ready` only after both succeed. Failure stops the process
tree and follows its restart policy. `Health` is false before readiness and
after stopping, crashing, or process exit, even without a custom probe.

## Consumer integration

A consumer registers its own schema, a namespace and version with its kinds and
exact relationship vocabulary. Its validator enforces those semantics using the
proposed transaction and `View`. Authorization and product decisions remain in
the consumer. See `examples/consumer-shaped` for a small consumer-owned example.

The integration boundary is:

1. Open a `graph.Store` and register the consumer's schema and validator.
2. Translate consumer-owned entities to stable vertex IDs and namespace-qualified
   kinds; translate relationships to namespace-qualified edge types.
3. Apply related changes in one `Transaction`, setting `ExpectedRevision` for
   concurrent writers and a stable `IdempotencyKey` for retried requests.
4. Use namespace-scoped bounded queries, `Snapshot`, and `Watch` cursors for
   reads and projections; revalidate and authorize in the consumer at the
   product boundary.
5. Start only processes the consumer has already authorized. Translate selected
   binary identity/revision/checksum, ordinary environment, opaque secret
   references, endpoints, and dependencies to `supervisor.Spec`. Supply the
   consumer's IPC readiness and handshake hooks, an orphan policy, and any
   platform resource controls; consume lifecycle and health observations without
   interpreting them as permission.
6. Keep the consumer's IDs, roles, vocabulary, validation, graph migration,
   trust, approvals, and authorization out of CTX core.

CTX has no database migrations to run for a consumer. The consumer's graph
adapter supplies its own semantic validation and projection, and its process
host continues to own supervision until a separate adapter maps admission,
permits, IPC hooks, and lifecycle projections to the generic CTX supervisor.
