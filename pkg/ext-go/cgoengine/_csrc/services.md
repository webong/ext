# Shared engine services

`ext_engine_call(operation, input, length, &out)` runs a pure bounded JSON service.
It never discovers, launches, authorizes, or changes files. Input obeys the same
24 MiB, UTF-8, duplicate-key and nesting rules as plugin envelopes. Unknown fields
in service-owned structures are rejected. Unknown operations return
`EXT_UNSUPPORTED`. Free every successful output with `ext_buffer_free`.

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

Requirements use `{contract:{name,version},operation,identity?}`. Route profiles
use the public BackendProfile JSON fields. Route requirements use lowercase
`concurrent`, `nativeStreaming`, and `nativeCallbacks`. Direct routes are preferred;
a bridge is considered only when declared, with exact common protocol and the
intersection of all four endpoint feature sets. This performs no discovery or
version translation.

Endpoints are the loopback listeners a plugin's result declares (`pkg/ext-go/
engine/endpoint`). `endpoint.check` reads one strictly (unknown and duplicate
fields and wrong types are refused) and applies the rules no policy changes: a
scheme of `ws`, `wss`, `http`, `https` or `tcp`; an address that is a host and a
port, the host being `localhost` or a loopback IP literal (`net.SplitHostPort` and
`net.ParseIP` are followed, so `[::1]:80`, `[::ffff:127.0.0.1]:80` and `127.0.0.1:80`
pass and `127.1:80`, `10.0.0.1:80` and a zone do not); a path of at most 1024 bytes
that begins with `/` and holds no `?`, `#`, `\`, control character or space; a token
of at most 512 bytes with no control character or space; and an expiry. It answers
`EXT_INVALID` for a malformed endpoint and `EXT_DENIED` for one that is not on a
loopback host, in the order of the Go rules (the first rule broken decides).
`endpoint.validate` takes `{endpoint,policy}` with `policy` =
`{schemes?:[string],maxLifetimeMilliseconds?:int,now?:RFC3339}` and adds what a host
admits: `EXT_DENIED` when no scheme is admitted or this one is not, `EXT_INVALID`
for a policy naming a scheme there is none of or an expiry beyond the lifetime (an
hour unless the policy says otherwise), and `EXT_EXPIRED` for one already past. A
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

Schemas support the bounded CTX vocabulary, not arbitrary JSON Schema. The
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

- `ext_engine_sha256`: bytes to a 32-byte digest.
- `ext_package_verify`: validate a manifest and hash its listed artifacts.
- `ext_directory_digest`: hash sorted slash-relative file paths and file digests
  using the existing CTX trust-record format.
- `ext_store_digest`: verify one installed package under a caller-supplied store
  root, reject unlisted files, and return its directory digest.
- `ext_store_open`, `ext_store_close`, `ext_store_install`, `ext_store_upgrade`,
  `ext_store_replace`, `ext_store_remove`, `ext_store_load`, `ext_store_ids`: the
  package store. A package is verified at its source, copied into a staging
  directory below the root, verified again, and renamed into place; an upgrade
  moves the installed directory aside first and puts it back if the new one
  cannot take its place. `plugin.json` is copied byte for byte. `EXT_EXISTS` is
  an install over an installed id, `EXT_NOT_FOUND` an id that is not installed,
  `EXT_MISSING` a source, manifest or artifact that is not there (the file layer's
  reads give it for a name that does not exist and `EXT_IO` for anything else),
  `EXT_MISMATCH` an upgrade that changes the id or contracts or an artifact that
  differs from its digest, and `EXT_INVALID` anything that is not a valid package,
  id or directory. The limits (one artifact, one tree's files and bytes) default
  to the Go store's. A store serializes its own calls; several processes mutating
  one root are the application's to serialize. It makes no trust decision.
- `ext_store_list`, `ext_store_verify`, `ext_store_list_unverified`: whole-root
  reports as JSON. `list` verifies every package and fails closed on the first
  entry that is not a package directory or does not verify; `verify` checks every
  entry and reports each one's status, so a caller can show everything that is
  wrong; `list_unverified` reads only the manifests (no artifact is read or
  hashed, so what it returns is not trusted) and reports the entries it could not
  read separately. A staging directory (a leading ".") is skipped and a missing
  root is empty.
- `ext_layered_open`, `ext_layered_close`, `ext_layered_load`, `ext_layered_list`,
  `ext_layered_verify`, `ext_layered_list_unverified`: a read-only view over
  several roots in priority order, such as a user root before a system root. An
  id is served from the first root that holds it and a shadowed copy is never
  read. A damaged winning copy fails closed instead of falling back to a lower
  one, and an entry that is not a package in any root fails `list`. The view
  never writes and has no default roots; a repeated root (compared as a cleaned
  path), an empty one or none at all is `EXT_INVALID`.
- `ext_trust_state_of`, `ext_trust_record`, `ext_trust_revoke`: persist scoped
  reviews of exact package digests in a caller-selected trust file. The host
  serializes writers and protects the file's parent directory; installation
  does not grant trust.

POSIX filesystem functions use descriptor-relative opens. Windows functions
validate opened paths and reject reparse points. Both reject links and special
files. Directory traversal is bounded to 1024 nested levels,
and one directory digest to 250,000 files (the Go store's default `MaxTreeFiles`)
and 64 MiB of path text; beyond either it fails with `EXT_INVALID` instead of
holding every path in memory. The Go store applies its own configurable limits
first, and `plugin.DirectoryDigest` in the engine module does not cap the count.
Applications must protect reviewed content against concurrent writes and
separately establish publisher trust. Neither function launches code.

## Resource engines

`ext_instance.h` manages configuration revisions, raw-byte digests, replacement,
leases and bounded capacity. Retired values remain alive until their leases are
released. Factory and cleanup callbacks execute outside locks. Close stops
admission permanently, cancels factory lifetimes and waits within the supplied
call budget; a timed-out close can be retried. Callback code and user data must
remain loaded until destroy succeeds.

`ext_stream.h` supplies a bounded pull-stream lifecycle. Applications supply the
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
