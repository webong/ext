# Shared engine services

`ctx_engine_call(operation, input, length, &out)` runs a pure bounded JSON service.
It never discovers, launches, authorizes, or changes files. Input obeys the same
24 MiB, UTF-8, duplicate-key and nesting rules as plugin envelopes. Unknown fields
in service-owned structures are rejected. Unknown operations return
`CTX_UNSUPPORTED`. Free every successful output with `ctx_buffer_free`.

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

Requirements use `{contract:{name,version},operation,identity?}`. Route profiles
use the public BackendProfile JSON fields. Route requirements use lowercase
`concurrent`, `nativeStreaming`, and `nativeCallbacks`. Direct routes are preferred;
a bridge is considered only when declared, with exact common protocol and the
intersection of all four endpoint feature sets. This performs no discovery or
version translation.

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
Unicode 17.0.0 simple lowercase, pinned in `src/unicode_lower.h`; regeneration is
in `tools/unicode_lower.go`. Unicode versions in older Go toolchains can differ.
Do not interpret a validated path or checksum as a trust grant.

## Native integrity functions

- `ctx_engine_sha256`: bytes to a 32-byte digest.
- `ctx_package_verify`: validate a manifest and hash its listed artifacts.
- `ctx_directory_digest`: hash sorted slash-relative file paths and file digests
  using the existing CTX trust-record format.

Filesystem functions currently use POSIX descriptor-relative opens and reject
symlinks and special files. Directory traversal is bounded to 1024 nested levels.
Applications must protect reviewed content against concurrent writes and
separately establish publisher trust. Neither function launches code.

## Resource engines

`ctx_instance.h` manages configuration revisions, raw-byte digests, replacement,
leases and bounded capacity. Retired values remain alive until their leases are
released. Factory and cleanup callbacks execute outside locks. Close stops
admission permanently, cancels factory lifetimes and waits within the supplied
call budget; a timed-out close can be retried. Callback code and user data must
remain loaded until destroy succeeds.

`ctx_stream.h` supplies a bounded pull-stream lifecycle. Applications supply the
authenticated scope; the engine enforces scope matching, random IDs, monotonic
sequence numbers, serialized reads, item/byte limits, capacity and maximum age.
Close can run concurrently with read and must unblock it. `release` runs only
after close and all readers have joined. A stream's lifetime is independent of
the successful open invocation. These callbacks are cooperative native code,
not an isolation boundary.

## Bindings

- Go: `goengine.EngineCall`, package/schema/route helpers, `NewInstances`,
  `NewStreams`, and `GuestFromRegistry`.
- Rust: `ctx-plugin-engine` in `pkg/rust/engine`, with safe host/guest/service
  wrappers and raw resource FFI.
- Zig: `ctx_plugin_engine` in `pkg/zig/engine`, with host/guest/service wrappers
  and the complete header-generated FFI.
- Node: `loadEngine` in `pkg/typescript/engine`, with asynchronous host/guest
  callbacks, cancellation and pure JSON services. Node resource bindings and
  filesystem helpers are still missing.

See `docs/plugin-engine-parity.md` for the remaining migration requirements.
