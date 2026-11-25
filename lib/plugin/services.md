# Shared engine services

`ext_plugin_services_call(operation, input, length, &out)` runs a pure bounded JSON service.
It never discovers, launches, authorizes, or changes files. Input obeys the same
24 MiB, UTF-8, duplicate-key and nesting rules as plugin envelopes. Unknown fields
in service-owned structures are rejected. Unknown operations return
`EXT_PLUGIN_UNSUPPORTED`. Free every successful output with `ext_plugin_buffer_free`.

| Operation | Input | Successful output |
| --- | --- | --- |
| `descriptor.validate` | Descriptor | Detached descriptor |
| `descriptor.match` | `{selected,actual}` | `null` |
| `descriptor.select` | `{candidates:[Descriptor],requirement}` | Unique descriptor |
| `descriptor.requirements` | `{descriptor,requirements:[]}` | `null` |
| `protocol.negotiate` | `{preferred:[string],offered:[string]}` | First common preferred protocol |
| `request.validate` | `{descriptor,request}` | Validated request |
| `response.validate` | `{response,requestID:string}` | Validated response |
| `schema.validate` | Schema | Detached schema |
| `schema.check` | `{schema,value}` | `null` |
| `route.resolve` | `{hosts,guests,bridges?,requirements?}` | Route |
| `package.validate` | Manifest | Detached manifest |
| `package.select` | `{manifest,name,environment}` | `{entrypoint,artifact,protocol}` |
| `package.resolve` | Array of manifests | `{order,bindings}` |
| `frame.validate` | One tagged frame (`progress` or `reverse_request`) | The frame |
| `endpoint.check` | Endpoint | The endpoint |
| `endpoint.validate` | `{endpoint,policy}` | The endpoint |

Requirements use `{contract:{name,version},operation,identity?,provider?}`.
`provider` selects a package ID across revisions; a nonempty selector and an
exact `identity` pin are mutually exclusive. Missing, null or empty `provider`
means no named selection. Matching multiple revisions remains ambiguous; the
resolver never chooses a latest revision. Package `requires` uses the same rule.
Package requirements additionally allow optional `os` and `arch` conditions,
using the same exact tokens as artifacts; empty or omitted conditions match any
target. These conditions are package-planning fields, not descriptor selectors.
`package.resolve_for` accepts `{packages:[Manifest],os,arch}` with both target
tokens required, and resolves only matching requirements. It validates every
manifest and requirement before selection, including inactive requirements.
The original array-based `package.resolve` rejects conditional requirements:
callers must provide a target rather than accidentally resolve all platforms.
`bundle.plan` uses its declared target for the same selection. Resolution never
changes stored manifests, verifies execution compatibility or grants approval.
Route profiles
use the public BackendProfile JSON fields. Route requirements use lowercase
`concurrent`, `nativeStreaming`, and `nativeCallbacks`. Direct routes are preferred;
a bridge is considered only when declared, with exact common protocol and the
intersection of all four endpoint feature sets. This performs no discovery or
version translation.

A profile may state its sandbox `boundary`: `in-process`, `process`, `confined`,
`runtime`, `container` or `kernel` ([the sandboxing ADR](../../docs/adr-sandboxing.md)).
Requirements may list the `boundaries` a consumer accepts, distinct levels; an empty
or absent list accepts any. A route's `boundary` is its guest's, and a bridge does not
change it. A route that meets the transport requirements but not the boundaries is
skipped; when every such route was skipped for its boundary the result is
`EXT_PLUGIN_DENIED`, otherwise `EXT_PLUGIN_UNSUPPORTED`. `testdata/v1/route-cases.json` is the
contract, run by `tests/fixtures.c` and by every engine binding's `RouteFixture`.

Endpoints are the loopback listeners a plugin's result declares (`pkg/ext-go/
engine/plugin/endpoint`). `endpoint.check` reads one strictly (unknown and duplicate
fields and wrong types are refused) and applies the rules no policy changes: a
scheme of `ws`, `wss`, `http`, `https` or `tcp`; an address that is a host and a
port, the host being `localhost` or a loopback IP literal (`net.SplitHostPort` and
`net.ParseIP` are followed, so `[::1]:80`, `[::ffff:127.0.0.1]:80` and `127.0.0.1:80`
pass and `127.1:80`, `10.0.0.1:80` and a zone do not); a path of at most 1024 bytes
that begins with `/` and holds no `?`, `#`, `\`, control character or space; a token
of at most 512 bytes with no control character or space; and an expiry. It answers
`EXT_PLUGIN_INVALID` for a malformed endpoint and `EXT_PLUGIN_DENIED` for one that is not on a
loopback host, in the order of the Go rules (the first rule broken decides).
`endpoint.validate` takes `{endpoint,policy}` with `policy` =
`{schemes?:[string],maxLifetimeMilliseconds?:int,now?:RFC3339}` and adds what a host
admits: `EXT_PLUGIN_DENIED` when no scheme is admitted or this one is not, `EXT_PLUGIN_INVALID`
for a policy naming a scheme there is none of or an expiry beyond the lifetime (an
hour unless the policy says otherwise), and `EXT_PLUGIN_EXPIRED` for one already past. A
policy with no `now` reads the wall clock. Expiry is compared to the nanosecond.
Times are RFC 3339 and nothing else: Go also accepts a few non-RFC spellings (an
offset of 24 hours, a comma before the fraction, year 0000), which the engine
refuses. An outcome is a status, never text: an endpoint is a credential, and no
part of one is copied into an error. The grant (a lifetime tied to a session,
one-time tokens, dialing) is the binding's.
`testdata/v1/endpoint-cases.json` states 213 outcomes every binding must reproduce.

Package environments use `{os,arch,runtimes,sharedVersions}`. `runtimes` maps names
to installed backend profiles; `sharedVersions` maps host libraries to exact
versions. Dependency plans reject cycles, duplicate identities and ambiguous
providers. Providers precede consumers; otherwise traversal is ordered by the
SHA-256 of canonical identity JSON, matching the Go reference.

Schemas support the bounded Ext vocabulary, not arbitrary JSON Schema. The
reference is `plugin/schema/schema.go`; required fields, unknown properties,
Unicode string length, item limits, enums and numeric constraints are checked by
C. Integer-valued schema bounds must use integer JSON syntax and fit int64.

Artifact paths reject traversal, empty components, backslashes, colons, NUL,
line breaks, Windows device names, and trailing dots/spaces. Duplicate paths use
Unicode 17.0.0 simple lowercase, pinned in `src/unicode_lower.h`; the Go module
owns the generator, `pkg/ext-go/cgoengine/tools/unicode_lower.go`. Unicode versions
in older Go toolchains can differ.
Do not interpret a validated path or checksum as a trust grant.

Duplicate names and paths, and tree overlap, are decided by sorting the
artifacts, so validation costs O(n log n) in the artifact count, and
`package.resolve` finds providers through an index built once per call. Both used
to compare every pair or scan every manifest per requirement, which took minutes
on a manifest that fits in one frame. `tests/limits.c` keeps that from coming
back: it checks verdicts against a naive reference and bounds CPU time at the
documented limits, and `tests/fuzz.c` is a libFuzzer target for the parsers and
validators (see the comment at its top for how to run it with a time limit).

Package and plugin identifiers may contain `/` and `.` after the first character,
so an identifier such as `a/../b` or `a//b` is valid. Nothing in the engine
builds a filesystem path from one. A binding or application that does, for
example to place an installed package in a directory, must encode it first (the
Go store replaces `/` with `%2f`) and must not join it into a path as written.

## Native integrity functions

- `ext_plugin_sha256`: bytes to a 32-byte digest.
- `ext_plugin_package_verify`: validate a manifest and hash its listed artifacts.
- `ext_plugin_directory_digest`: hash sorted slash-relative file paths and file digests
  using the existing CTX trust-record format.
- `ext_plugin_store_digest`: verify one installed package under a caller-supplied store
  root, reject unlisted files, and return its directory digest.
- `ext_plugin_store_open`, `ext_plugin_store_close`, `ext_plugin_store_install`, `ext_plugin_store_upgrade`,
  `ext_plugin_store_replace`, `ext_plugin_store_remove`, `ext_plugin_store_load`, `ext_plugin_store_ids`: the
  package store. A package is verified at its source, copied into a staging
  directory below the root, verified again, and renamed into place; an upgrade
  moves the installed directory aside first and puts it back if the new one
  cannot take its place. `plugin.json` is copied byte for byte. `EXT_PLUGIN_EXISTS` is
  an install over an installed id, `EXT_PLUGIN_NOT_FOUND` an id that is not installed,
  `EXT_PLUGIN_MISSING` a source, manifest or artifact that is not there (the file layer's
  reads give it for a name that does not exist and `EXT_PLUGIN_IO` for anything else),
  `EXT_PLUGIN_MISMATCH` an upgrade that changes the id or contracts or an artifact that
  differs from its digest, and `EXT_PLUGIN_INVALID` anything that is not a valid package,
  id or directory. The limits (one artifact, one tree's files and bytes) default
  to the Go store's. A store serializes its own calls; several processes mutating
  one root are the application's to serialize. It makes no trust decision.
- `ext_plugin_store_list`, `ext_plugin_store_verify`, `ext_plugin_store_list_unverified`: whole-root
  reports as JSON. `list` verifies every package and fails closed on the first
  entry that is not a package directory or does not verify; `verify` checks every
  entry and reports each one's status, so a caller can show everything that is
  wrong; `list_unverified` reads only the manifests (no artifact is read or
  hashed, so what it returns is not trusted) and reports the entries it could not
  read separately. A staging directory (a leading ".") is skipped and a missing
  root is empty.
- `ext_plugin_store_resolve_entrypoint`: entrypoint resolution (the Go store's
  `ResolveEntrypoint`, "discovery"). It reads and verifies the whole package,
  requires that it serves the exact contract name and version, compares its
  directory digest with the digest the caller reviewed (an empty or changed one
  is `EXT_PLUGIN_DENIED`, so nothing is selected without a trust decision) and then
  selects, among the `argv` entrypoints that list the protocol and are not named
  for another operating system, the one whose artifact fits the platform best
  (2 for an artifact that names an `os`, 1 for an `arch`, 2 for a ".`<os>`"
  entrypoint name; the first in manifest order wins a tie). `os` and `arch` are
  Go's `runtime.GOOS` and `runtime.GOARCH` names, and `NULL` takes those of the
  platform the engine was built for. It answers `{"name","artifact","path",
  "digest"}`; `EXT_PLUGIN_UNSUPPORTED` means no served contract or no compatible
  entrypoint.
- `ext_plugin_layered_open`, `ext_plugin_layered_close`, `ext_plugin_layered_load`, `ext_plugin_layered_list`,
  `ext_plugin_layered_verify`, `ext_plugin_layered_list_unverified`: a read-only view over
  several roots in priority order, such as a user root before a system root. An
  id is served from the first root that holds it and a shadowed copy is never
  read. A damaged winning copy fails closed instead of falling back to a lower
  one, and an entry that is not a package in any root fails `list`. The view
  never writes and has no default roots; a repeated root (compared as a cleaned
  path), an empty one or none at all is `EXT_PLUGIN_INVALID`.
- `ext_plugin_trust_state_of`, `ext_plugin_trust_record`, `ext_plugin_trust_revoke`: persist scoped
  reviews of exact package digests in a caller-selected trust file. The host
  serializes writers and protects the file's parent directory; installation
  does not grant trust.

POSIX filesystem functions use descriptor-relative opens. Windows functions
validate opened paths and reject reparse points. Both reject links and special
files. Directory traversal is bounded to 1024 nested levels,
and one directory digest to 250,000 files (the Go store's default `MaxTreeFiles`)
and 64 MiB of path text; beyond either it fails with `EXT_PLUGIN_INVALID` instead of
holding every path in memory. The Go store applies its own configurable limits
first, and `plugin.DirectoryDigest` in the engine module does not cap the count.
Applications must protect reviewed content against concurrent writes and
separately establish publisher trust. Neither function launches code.

## Resource engines

`ext_plugin_instance.h` manages configuration revisions, raw-byte digests, replacement,
leases and bounded capacity. Retired values remain alive until their leases are
released. Factory and cleanup callbacks execute outside locks. Close stops
admission permanently, cancels factory lifetimes and waits within the supplied
call budget; a timed-out close can be retried. Callback code and user data must
remain loaded until destroy succeeds.

`ext_plugin_stream.h` supplies a bounded pull-stream lifecycle. Applications supply the
authenticated scope; the engine enforces scope matching, random IDs, monotonic
sequence numbers, serialized reads, item/byte limits, capacity and maximum age.
Close can run concurrently with read and must unblock it. `release` runs only
after close and all readers have joined. A stream's lifetime is independent of
the successful open invocation. These callbacks are cooperative native code,
not an isolation boundary.

## Bindings

- Go: `cgoengine.EngineCall`, package/schema/route helpers, `NewInstances`,
  `NewStreams`, and `GuestFromRegistry`.
- Rust: `ext-plugin-engine` in `pkg/ext-rust/engine`, with safe host/guest/service
  wrappers and raw resource FFI.
- Zig: `ext_plugin_engine` in `pkg/ext-zig/engine`, with host/guest/service wrappers
  and the complete header-generated FFI.
- Node: `loadEngine` in `pkg/ext-ts/engine`, with asynchronous host/guest
  callbacks, cancellation and pure JSON services. Node resource bindings and
  filesystem helpers are still missing.

See `docs/plugin-engine-parity.md` for the remaining migration requirements.

## Native routing through the described host

A described `host` may include `routes`, a nonempty array of at most 256
`{contract:{name,version},operation,target:<host handle>}` entries. Each entry
forwards that declared reverse operation unchanged to an already-started target
host through the native registry. The source must declare the reverse operation;
the target must offer that exact contract and operation. Duplicate operations
fail with `exists`. Different operations of one contract may use different
targets. No name-based fallback occurs.

`reverse_authorize` remains an application event and defaults to denial when
unhandled. Forward authorization still runs on the target. Routed calls do not
raise a `reverse` handling event: the C registry carries the remaining deadline,
cancellation and public response across the call. Optional `services` settings
still bound time, payload size and concurrency. Routes neither load packages nor
grant trust; applications supply already-reviewed host handles.

The source pins target storage until the source is destroyed. Closing a target
handle still shuts it down immediately and subsequent forwarding reports
`reverse_unavailable`; it does not leave a dangling reference. Closing the source
destroys its session and registry before releasing its target references. Targets
must already exist, so these ownership references cannot form cycles.

`package.routes` compiles route candidates from
`{packages:[Manifest],os,arch,consumer:Identity}`. It resolves the complete graph
for the explicit target, finds the exact consumer identity, then returns
`[{contract,operation,provider:Identity}]` for its resolved requirements that
also appear in its reverse declarations. Admission-only requirements and
host-provided reverse operations without package requirements are not routes.
Overlapping requirements selecting the same provider are deduplicated; different
providers for the same operation are ambiguous. Distinct operations may select
different providers of one contract. Invalid inactive declarations still fail.
Shared cases are in `testdata/v1/routing-cases.json`.

This service creates no hosts and grants no authority. The application selects
the candidates it permits, verifies their packages and trust, opens providers,
and maps each full provider identity to a reviewed host handle before supplying
`routes` to the source. It must still authorize each reverse call.

## Catalog indexes

`catalog.validate` accepts the existing artifact index object:
`{"format_version":1,"name":"example/tools","packages":{"linux/amd64":{"url":"tools.zip","sha256":"<64 hexadecimal characters>"}}}`.
It returns the validated index. It permits 1–256 target entries, each an exact
`os/arch` token pair, URLs of 1–4096 bytes without whitespace/control bytes, and
SHA-256 pins. Unknown fields are invalid. Package names follow the identity ID
vocabulary. The caller bounds the encoded document and enforces URL transport,
origin, authentication and relative-path policy before downloading.

`catalog.select` takes `{"index":<index>,"target":"linux/amd64"}` and returns
that target's `{url,sha256}`; an absent target returns `not_found`, malformed
input `invalid`. Neither operation downloads, installs, resolves runtime
providers or grants trust. Shared cases are `testdata/v1/catalog-cases.json`.

## Bundle planning

`bundle.plan` takes `{apiVersion:"ext.bundle/v1",target:"os/arch",packages:[{manifest:<sealed package manifest>,archive:{source:<location>,sha256:<pin>}}]}`.
It validates 1–256 selections, one revision per package ID, archive source text
and pins, then returns the existing `package.resolve` `{order,bindings}` result.
The complete dependency graph must be present. It does not download, install,
interpret native library interfaces or grant trust. Unknown fields are rejected.
Target and location policy at installation time belong to the caller. Shared
cases are in `testdata/v1/bundle-cases.json`; see [bundle workflow](../../docs/bundles.md).

### Resource discovery request validation

`resource.request.validate` accepts `{"operation":"list|inspect","request":{...}}`
and returns the request unchanged. It validates `ext.resource/v1` request shape
only; it does not discover, invoke or authorize a provider. Unknown operations
return `unsupported`; malformed envelopes and requests return `invalid`.

A list request permits only:

- `type`: optional nonempty contract-style token (maximum 256 bytes).
- `limit`: optional integer from 1 through 100. Omission leaves the provider's
  bounded default in effect; the validator does not insert a default.
- `cursor`: optional nonempty opaque string, at most 4096 UTF-8 bytes, without
  ASCII controls or DEL. Explicit null and empty values are invalid; omit it
  for the first page.

An inspect request requires exactly `resource`, containing `provider` (the
ordinary identity, requiring ID and revision), `type` (the same token rule) and
`id` (opaque, nonempty, at most 1024 UTF-8 bytes without ASCII controls or DEL).
The ID is neither interpreted as a filesystem path nor treated as authority.
Provider version follows the existing identity's optional-version rule.

The shared `testdata/v1/resource-cases.json` fixtures cover these boundaries.
Response validation is described below. Live authorization, continuation-token
lifecycle and domain-error mappings remain separate work.

### Resource discovery response validation

`resource.response.validate` accepts
`{"operation":"list|inspect","provider":identity,"request":{...},"response":{...}}`.
The host supplies the selected provider identity independently of the response.
The service validates the original request first and returns the response
unchanged. Shape/bounds/duplicate errors are `invalid`; a different provider,
revision, resource or requested type is `mismatch`. Unknown operations are
`unsupported`. These are validation results, not permission grants.

A list response is `{"items":[resource-reference,...],"next":"optional-token"}`.
It has at most the requested limit (100 when omitted), no duplicate references,
and every reference has the selected provider identity and requested type, if
filtered. Omit `next` at the end: explicit null or empty is invalid. A continuing
page must contain an item and must not repeat the request cursor. This checks
one page only: the provider remains responsible for snapshot consistency,
authorization, token expiry and avoiding duplicates across pages.

An inspect response contains exactly `resource` and `operations`. Its resource
must equal the requested reference and match the selected provider. Operations
is an array of 0–64 entries, each containing exactly `contract` (name/version),
`operation`, `input` and `output`. Contract/operation pairs are unique. Input
and output use the engine's existing inline schema vocabulary, with depth 16
and a shared 1024-node budget for the entire response. No external schema lookup
or provider-specific dispatch is needed. Before calling one of these operations,
a host must still check the live provider descriptor and its own authorization
policy; this validator does neither.

Successful responses have no arbitrary metadata extension in v1. Domain failures
use the existing plugin response error envelope rather than embedding an error
in these success payloads. The detailed domain mapping remains consumer-owned.

### Native job envelopes in bundles

`bundle.plan` accepts an optional `native` array of up to 64 jobs. Each job has
`citation` (an object identifying `ext.library.citation/v1`), `drivers` (1–3
distinct names among cgo, c2go and purego), `dir` (a portable relative path),
and optional `package`. A bundle may have an empty `packages` array when it
has native jobs. The service still returns the package dependency plan.

The C service validates this envelope only. Full citation validation, native
target/driver resolution, generated language package naming and destination
overlap checks belong to the existing native-library layer and CLI composition.
The CLI completes those checks before reporting a plan. No native archive is
loaded, fetched or executed by this service.

### Resource discovery errors

`resource.error.validate` accepts `{"operation":"list|inspect","request":{...},"error":{...}}`.
It validates the request and the existing plugin remote-error envelope, returning
the error unchanged. The envelope's message and retry-hint bounds still apply;
messages are untrusted diagnostics. No error grants authority or triggers a retry.

| Code | Discovery meaning |
| --- | --- |
| `invalid` | Request parameters are invalid for this provider. |
| `not_found` | The requested resource no longer exists or is not exposed. |
| `denied` | The caller lacks permission for this discovery operation. |
| `unsupported` | The discovery operation or requested resource type is unsupported. |
| `mismatch` | A reference or continuation context does not match this request. |
| `expired` | A supplied list cursor has expired; only valid on list requests with a cursor. |
| `closed` | The provider's discovery session is closed. |
| `canceled` | The operation was canceled. |
| `timeout` | The operation exceeded its deadline. |
| `capacity` | The provider cannot admit more work. |
| `io` | Discovery failed due to an I/O error. |
| `operation_failed` | An otherwise unclassified discovery failure. |

Unknown categories and malformed envelopes return `invalid`. Providers may use
`not_found` instead of revealing whether an inaccessible resource exists. These
categories describe discovery only: a database key missing, unavailable codec,
or backend mismatch belongs to the database operation's domain contract. The
engine does not infer those mappings from a provider name or error text.

### Resource operation selection and call validation

`resource.operation.select` accepts `{descriptor, inspection, requirement}`.
`requirement` contains `{resource, contract, operation}`. It validates the
inspection's complete operation list and schemas, matches the exact resource
reference and provider identity, and requires the requested contract operation
in both the inspection and provider descriptor. It returns the inspected
operation (including its schemas). Missing declarations return `not_found`,
identity/reference disagreement returns `mismatch`, and malformed declarations
return `invalid`. Selection never authorizes or invokes an operation.

`resource.call.validate` accepts `{selection, payload}`, where `selection` is
the input above. It repeats selection and validates the payload against the resource operation's input schema. Provider
descriptors establish operation availability; they do not contain schemas. It
returns the payload unchanged. An explicit JSON null payload
is permitted only when the applicable schemas accept null; omitting payload is
invalid.

These are pure validation services. The consuming application's domain contract
must bind the resource reference to the actual operation, authorize that resource
and action, and validate its result. Ext does not inject a resource ID into an
arbitrary payload or infer an operation from a resource type. A validated
inspection is a live observation, not a durable grant: the provider must enforce
resource existence and authority again during invocation. The CLI resource call uses an explicit `{resource, input}` payload envelope;
see the resource discovery design for its invocation and authority rules.

`resource.call.request.validate` checks the CLI call request
`{resource, contract, operation, input}` before provider launch and returns it
unchanged. Every field is required, input may be any JSON value, and extra fields
are invalid. This structural validation does not authorize a call.

`resource.result.validate` accepts `{selection, payload}` like call validation,
but checks the selected resource operation's output schema. It returns the
validated success payload unchanged. Domain errors use the plugin error envelope
and are validated by the host protocol, not as success payloads or discovery errors.

## Cooperating package writers

The described `package_writer` object opens with `{root: "<ext-home>"}` and holds
`root/.package-write.lock` exclusively until close. It creates the home if
needed. A competing C or CLI writer receives `exists`; no waiting, timeout,
owner inference or stale-lock stealing occurs. The object has no call operations.
Close checks directory identity, removes only that empty directory, and reports
`mismatch` if another directory replaced it. A failed close still consumes the
handle and leaves the replacement intact. Process loss leaves a stale lock;
recover it only after establishing that the prior writer stopped.

The portable store also exposes `ext_plugin_store_writer_acquire`,
`ext_plugin_store_writer_release` and `ext_plugin_store_writer_free`. These use
exactly the same lock and ship wherever the store does, including mobile SDKs
without the process host. Release is idempotent; free releases if needed and
consumes the pointer. Calls on one pointer must be serialized. The described
object delegates to this API. Language bindings must reject embedded NULs before
passing paths to the C-string API.

Hold this handle across dependency planning, package publication and approval
changes, then close it. Store/trust APIs deliberately do not acquire it implicitly:
a caller can perform several operations under one writer without recursive
locking. The directory convention interoperates with the CLI and CTX locks.
It coordinates cooperating processes, not hostile writers; the application must
protect the home and its ancestors from unrelated mutation. Callers which ignore
the protocol still bypass it. This object is implemented by the native C host
library, so described-interface hosts in any language can use it without a new
language-specific lock implementation.

### Reviewed package snapshots

The described `store` object exposes `snapshot {source, id, reviewed_digest}`;
`ext_plugin_store_snapshot` is the portable C equivalent. The receiver is the
private destination store. The source must be a verified package directory with
the requested ID and the exact lowercase SHA-256 directory digest reviewed by
the caller. Existing destination IDs are never replaced.

The store copies the declared artifacts under its usual bounds, preserves
executable bits, verifies the staged package and compares its directory digest
to the reviewed digest before publication. A review mismatch is `denied`; an ID
or copied-content mismatch is `mismatch`; an existing destination is `exists`.
Invalid IDs or malformed digest strings are `invalid`. No trust record or
installation receipt is created. The result is the copied manifest.

The caller must own a private destination, coordinate source review and copying
with the package writer, and remove the destination after its consumers finish.
The copy is independent of later source changes; it is not filesystem-enforced
immutable storage. Snapshotting does not grant authority, choose dependencies,
copy approvals or manage child processes. Consumers must separately approve and
snapshot every package they intend to execute. Nested command and adapter
adoption remains application work.
# Ext graph projection

`package.graph {packages, os, arch}` resolves the manifests for the explicit
target and returns `{schema, transaction}` in the Ext graph data model. Pass
these unchanged to ledger's `store.register` and `store.apply` if persistence is
wanted. The libraries remain independent. Projection grants no authority and
does not launch or install anything. Missing/ambiguous dependencies and invalid
manifests retain the resolver's errors.

The `ext.plugin` namespace contains `ext.plugin/package` vertices and
`ext.plugin/requires` edges. See [the contract and reconciliation limits](../../docs/adr-ext-graph.md).

`package.execution {packages, os, arch, root, max_hosts?}` compiles a routed
execution plan. `root` contains the exact `identity` and selected `contract`.
The optional host limit defaults to 128 and must be 1–1024. The result contains
`nodes` (identity, contract and routes), `order` (provider-first indices into
nodes), and `root` (the root node index). Nodes are unique by full identity and
contract; repeated routes to that pair share a node. Only providers reachable
through declared reverse routes are scheduled. All supplied package requirements
must resolve. No package is reviewed, copied, started or authorized by planning.

## Execution graph object

The described `execution_graph` object owns process hosts and private snapshots;
its `plan` is the `package.execution` result. Unlike the pure services above,
`start` verifies and copies reviewed packages and launches hosts. `call` invokes
its root; `close` shuts down the entire graph and removes owned copies after
active native calls finish. See [configuration, policy and lifetime](../../docs/adr-ext-graph.md#c-execution-lifecycle).

`execution_graph.observe` returns the complete ready session set and child PID
locators. `snapshot` correlates that set with caller-supplied ledger process
references and emits a schema/transaction for a dedicated namespace, with
session/package/process links and routed session edges. See
[Ext graph](../../docs/adr-ext-graph.md#live-sessions-and-reconciliation) for
ownership, correlation and reconciliation requirements.
