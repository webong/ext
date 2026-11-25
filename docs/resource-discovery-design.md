# Resource discovery: consumer evidence and implementation contract

Status: offline provider discovery and C request/response validation implemented;
explicit live list/inspect and resource-bound calls are implemented. A real
KVLite bridge remains unfinished.

`ext resource providers` verifies installed packages through the C store and
uses C descriptor selection to find `ext.resource/v1` declarations containing
both `list` and `inspect`. Its JSON array is sorted by provider identity and
labels each descriptor `observation: "manifest"`. It never launches providers
or grants approval. This reports protocol declarations, not whether a provider
can currently run or whether any resources are accessible. Tampered installed
packages fail discovery instead of being silently hidden. Live discovery requires an explicit provider and JSON request:

```text
ext resource list example/provider '{"limit":10}'
ext resource inspect example/provider '{"resource":{"provider":{"id":"example/provider","revision":"r1"},"type":"example.database","id":"primary"}}'
```

Each live request requires existing host approval for the selected package and
its transitive dependencies. The CLI's explicit invocation authorizes that one
discovery operation; it does not supply a database grant or credentials. Reverse
services require explicit invocation-only dependency grants, described below. The provider must enforce application resource policy. The C host
receives no inherited environment. Startup and call each have a five-second
deadline. Only installed process entrypoints are supported by this CLI path.
The shared CLI writer lock is held until the host closes, so participating
package writers cannot change approval or content during the call. Direct
library writers do not yet participate.

The CLI validates requests before launch and success payloads after invocation,
validating typed discovery errors through the C engine before preserving them. Pagination is explicit: it does not follow
tokens automatically. `ext resource call` explicitly inspects and invokes one
resource operation, as described below.

## Consumer evidence

The local KVLite checkout was inspected at revision
`aaaca524970ac3ab8ba24c132bc53d9fd2c63761`, using `MODULES.md`,
`src/db.go`, `src/engine.go`, `src/transport.go` and `src/errors.go`.

KVLite has independently versioned engine and transport modules. Discovery
reads module metadata without loading libraries, starting listeners or changing
databases. Engine selection belongs to the caller; database manifests preserve
the selected driver. Remote clients select only server-owned driver mappings,
never a server filesystem path. A transport starts explicitly.

The database API provides get, put and delete; engine/protocol APIs expose
callback-based prefix scans and atomic ordered batches. Existing scan cursors
in the native ABI are runtime handles, not portable continuation tokens. Ext
must not manufacture pagination by exposing those handles as stable IDs.

This leads to two distinct resources: installed extension metadata and
host-exposed database instances. A database resource is not an engine package.
Installing a package does not open a database or grant access to its records.

## Discovery contract

Implement `ext.resource/v1` with `list` and `inspect` operations first. A package
advertises this contract through its ordinary descriptor. Generic selection
uses descriptors and contracts, never package-name dispatch.

- A resource reference contains provider package identity (including revision),
  provider-defined type and opaque ID. It contains no host path by default.
- `list` accepts an optional type filter, a limit of 1–100, and an optional opaque
  continuation token of at most 4096 bytes. It returns items and either a next
  token or no continuation.
- The provider owns pagination consistency. A token is bound to provider
  revision, filter and authorized caller; expired or mismatched tokens fail
  explicitly. Providers must not silently restart a scan and return duplicates.
- `inspect` accepts one resource reference and returns its resource reference and declared
  operations, each with contract, operation name and inline input/output schemas.
  Inspection does not invoke any of those operations.
- Responses distinguish manifest declarations from live observations. Listing
  providers from installed manifests needs no guest invocation. Enumerating
  database instances is a live authorized request, even if it is read-only.
- Schemas bound strings, array sizes and response bytes within the existing
  plugin frame limit. Unknown fields fail validation in the contract fixtures.

The first implementation should expose provider discovery from verified
manifests separately from live `list` and `inspect`. This prevents a harmless
looking discovery command from executing every installed plugin.

Request and response validation are implemented by the C engine's
`resource.request.validate` and `resource.response.validate` services, plus `resource.error.validate`, with shared cases in
`lib/plugin/testdata/v1/resource-cases.json`. See the exact bounds in
[services](../lib/plugin/services.md#resource-discovery-request-validation).
They return validated payloads unchanged and do not validate a cursor's runtime
authority or expiry. Operation schemas are
inline instances of the existing engine schema vocabulary, avoiding a new
external schema registry; provider-specific metadata is not part of v1.

## Invocation and authority

Package approval is necessary to launch a provider but does not authorize a
database, prefix, mutation or listener. The consuming application supplies a
resource authorization decision before invocation. The provider must enforce
the same resource boundary; IDs are not bearer credentials.

A subsequent `resource call` resolves only an operation explicitly advertised
for that resource, validates its schema, and uses the ordinary plugin session.
It never infers a method from a resource type. KVLite would own its database
contract and map that contract to its DB API; Ext supplies discovery, schemas
and transport only. No code in Ext chooses RocksDB or opens KVLite paths.

Use the existing plugin error envelope for protocol errors (invalid input,
missing operation/resource, denied access, unsupported contract, cancellation
and deadline). Keep provider-specific details as structured domain data rather
than inventing an alternative transport error vocabulary. KVLite's missing key,
closed database, unavailable codec, unexposed driver and backend mismatch remain
distinct domain outcomes. The fixture design must pin the exact mapping before
implementation rather than collapse them into a generic missing-resource error.

## Required implementation evidence

1. C-first descriptor/discovery validation with positive and negative fixtures;
   bindings run the same cases.
2. Offline discovery proves no provider process is started.
3. An authorized live provider exercises pagination, expiry, schema rejection,
   revocation, deadline and cancellation.
4. A real consumer bridge demonstrates a database operation without core
   provider-name branches. Consumer integration needs its own repository change;
   an in-repository synthetic guest is only protocol evidence.
5. Discovery, inspection and a read-only call precede mutation support. No
   Kubernetes-style reconciliation loop is implied by this resource protocol.

Discovery error categories are now pinned in the shared resource fixtures and
[service contract](../lib/plugin/services.md#resource-discovery-errors). Live
acceptance covers resource absence, denied access, expired cursors, deadline and
rejection of a domain error used as a discovery category. KVLite domain mappings
and its real consumer bridge remain separate unfinished work.

Live CLI operations handle Ctrl-C by closing their C host, waiting for the call
and cancellation handler to finish, and then releasing the package writer lock.
`resourcecmd.Manager.RunContext` supplies the same behavior to callers with a
context. Cancellation reports `context canceled`; the closed session is never
reused. POSIX acceptance interrupts an active list and a resource operation,
checks that the provider process has exited, and runs a fresh call afterward.
Windows console interruption still needs its own execution harness.

Operation selection and payload checks are implemented as C services
`resource.operation.select` and `resource.call.validate`. Both require a valid
inspection and the exact selected provider descriptor; an operation must appear
in both. Payloads must satisfy the inspected input schema. Provider descriptors declare
operation availability, not schemas. The CLI uses these services before invocation and validates the result with
`resource.result.validate`. None of these checks grants resource authority.

## Explicit resource calls

`ext resource call <provider-id> <request-json>` takes:

```json
{"resource":{"provider":{"id":"example/provider","revision":"r1"},"type":"example.record","id":"one"},"contract":{"name":"example.record","version":"v1"},"operation":"read","input":"name"}
```

The CLI checks the request and provider identity before launch, then performs
inspection and the requested operation within one host session under the package
writer lock. It passes `{resource, input}` as the domain operation payload; the
inspected input schema must describe that full envelope. This binds the requested
resource to the call without injecting fields into an arbitrary existing API.
The provider must check the reference and its own resource policy at invocation,
even if inspection succeeded. The CLI supplies no credentials. It opens routed
provider dependencies only after every reachable package passes review and
entrypoint verification. Reverse calls are denied by default. Each invocation
can explicitly grant an operation:

```sh
ext resource call example/resources '<request-json>' \
  --allow-dependency '{"provider":{"id":"example/provider","revision":"provider-v1","version":"1.0.0"},"contract":{"name":"example.value","version":"v1"},"operation":"read"}'
```

A grant pins the full provider identity, contract and operation. It grants no
persistent trust and permits only reverse calls attached to an active call.
Each hop of a dependency chain needs its own grant. The CLI closes consumers
before providers on completion, startup failure or cancellation.

Before any host starts, the CLI copies every reachable reviewed provider into
one private store through the C snapshot operation and resolves all executable
paths there. A failed copy removes the partial store without launching guests.
The graph retains these copies until its hosts close, then removes the store.
The existing writer still spans the invocation; snapshots do not add authority
or change revocation policy. This graph is local to resource invocation and does
not yet supply nested product commands with a pinned graph.

The explicit command authorizes this exact operation from the CLI side; it does
not infer safety from an operation name and may execute a mutating operation if
the provider advertises one and permits it. The acceptance fixture exercises a
read operation first, with invalid input blocked before invocation, invalid
output rejected, and a provider denial after successful inspection preserved.
Calls use the same process entrypoint as inspection. Separate-entrypoint or
non-process resource integrations still need consumer-specific support.
Domain errors preserve the ordinary plugin error envelope and are not coerced
into discovery categories. Domain-specific success/error mappings and the real
KVLite integration remain work.
