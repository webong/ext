# One CTX contract, interoperable implementations

CTX's plugin library supports independently built hosts and guests. Go, C,
Rust, Zig and TypeScript implementations can coexist. There is no requirement
to migrate every host to one engine or to write a full SDK for every language.
The public contract and conformance suite define interoperability. A language
binding is a convenience; it is not permission to use a plugin.

## Boundaries

| Layer | Responsibility |
| --- | --- |
| `ctx.plugin/v1` | Identity, exact descriptor, requests, responses, errors and deadlines |
| Host implementation | Verify selection, authorize invocations, own sessions and choose installed bindings |
| Guest implementation | Declare contracts, authorize domain work and execute handlers |
| Runtime backend | Implement launch/ABI/transport mechanics behind `plugin.Backend` |
| Bridge | Expose a verified backend as another transport's `plugin.Endpoint` |
| `plugin/interop` | Resolve an explicitly declared direct route or one installed bridge |
| Conformance tooling | Exercise behavior across hosts, guests, runtimes and bridges |

A CTX host API can hide whether a guest is reached through JSON lines, HashiCorp,
WASI or a native ABI. Their execution properties remain observable: a native
call cannot become sandboxed or forcibly cancellable merely by wrapping it.
Applications retain control of allowed runtimes and permissions.

## Compatibility planning

`interop.Resolve(hosts, guests, bridges, requirements)` accepts reviewed
`BackendProfile` metadata and installed bridge declarations. It prefers direct
connections, then one bridge, using caller preference order. It returns the
chosen protocol, bindings and bridge name without launching anything.

Transport names describe wire bindings, not source languages. Both ends must
share an implemented protocol version. Native concurrency, streaming and callback
requirements must be satisfied across every hop. JSON-line transport serializes
calls; wrapping a concurrent HashiCorp backend does not make that path concurrent.
CTX pull-stream operations can use unary calls and are separate from native
transport streaming. No speculative bridge chains, protocol downgrades, retries
or executable discovery are performed.

```go
translator, err := hashicorp.JSONLineBridge("grpc")
if err != nil { return err }
route, err := interop.Resolve(
    []plugin.BackendProfile{jsonline.Profile()},
    []plugin.BackendProfile{hashicorp.GRPCProfile()},
    []interop.Bridge{translator},
    interop.Requirements{},
)
// Application binds route.Bridge to a reviewed installed executable/factory.
```

A missing route fails with `plugin.ErrUnsupported`. Declaring a profile does
not install its implementation, establish trust, or translate domain methods.

## Generic relay

`bridge.Open(ctx, descriptor, plugin.Options{...})` creates a verified upstream
Session and returns an Endpoint. It can be exposed through `jsonline.ServeGuest`,
`bridge.ServeJSONLine`, or `hashicorp.Plugin{Guest: relay}`. The latter now accepts
any `plugin.Endpoint`; existing `*plugin.Guest` callers continue to work.

- Verify selection before connecting; match the complete upstream handshake.
- Validate incoming requests and preserve the selected identity/domain contract.
- Apply upstream authorization on each call. The incoming ID is correlation only.
- Keep the external response ID while assigning independent upstream IDs.
- Preserve payloads and deliberate public errors, including retry hints.
- Bound upstream work by the incoming deadline and configured session timeout.
- Close on upstream transport failure rather than mislabeling it a domain error.
- Return a public `permission_denied` on local bridge policy denial, without
  revealing policy diagnostics or destroying a healthy connection.
- Never retry an operation automatically: it may already have had effects.

`Relay.Close` aborts; `Relay.Drain` uses Session's graceful-close behavior.
The frontend owner must close the relay on disconnect. `ServeJSONLine` owns
both relay and connection and monitors EOF during active work with bounded
read-ahead. Policy credentials are local to the bridge; caller identity and
credentials are not automatically delegated. Use separate sessions/policies
where callers have different authority.

## HashiCorp bridge executable

`pkg/plugin-hashicorp/cmd/ctx-plugin-bridge` exposes a reviewed CTX guest through a JSON-line or
HashiCorp frontend. Its default exposes a HashiCorp CTX guest as a JSON-line command. A Rust, Zig, JS, C or Go host uses its normal CTX JSON-line binding.
The bridge contains Go and runs in its own process; the host does not need to
embed the Go runtime. Both HashiCorp gRPC and net/rpc are supported explicitly.

```text
Host in any language
  → ctx.plugin/v1 JSON lines
  → ctx-plugin-bridge (Go)
  → HashiCorp gRPC or net/rpc
  → guest implementing the selected CTX domain contract
```

Build and invoke:

```sh
go build -o /tmp/ctx-plugin-bridge ./pkg/plugin-hashicorp/cmd/ctx-plugin-bridge
/tmp/ctx-plugin-bridge --config /absolute/path/bridge.json
```

Configuration uses `ctx.bridge/v1` and is reviewed local launch policy. The
`descriptor` is the complete selected CTX descriptor, unchanged by the bridge:

```json
{
  "apiVersion": "ctx.bridge/v1",
  "descriptor": {
    "apiVersion": "ctx.plugin/v1",
    "identity": {"id": "example/echo", "revision": "build-1"},
    "contracts": [{"name": "example.echo", "version": "v1",
      "operations": [{"name": "echo"}]}]
  },
  "process": {
    "executable": "/absolute/path/to/guest",
    "sha256": "REPLACE_WITH_REVIEWED_64_HEX_DIGEST",
    "protocol": "grpc",
    "arguments": [],
    "environment": []
  },
  "allow": [{"contract": {"name": "example.echo", "version": "v1"},
             "operation": "echo"}]
}
```

Both allowlist and digest pin are required. A digest is content equality, not
publisher authentication. The embedding application authenticates/protects the
bridge binary, its configuration, the guest and any native/interpreter dependencies.
Use immutable artifact paths. The bridge does not expand descriptor permissions
from request data. The child environment is explicit; go-plugin adds its native
handshake/authentication variables. Standard output is protocol-only.

For launchers that cannot supply arguments, the default configuration path is
`<bridge-executable>.json`. This sidecar has the same trust requirements as an
explicit config file. A validated configuration snapshot is sent privately to
the watchdog, avoiding a second read of mutable launch policy.

The Unix executable uses a parent-liveness pipe and a small worker subprocess.
The worker owns the actual go-plugin client and doubles as the watchdog. On
abrupt launcher death, it cancels serving and kills/reaps the direct HashiCorp
guest. go-plugin never owns or kills its own watchdog.
Normal disconnect also cancels upstream work and closes its owned go-plugin
client. This is not arbitrary process-tree containment. The library connector
alone does not supply the executable's watchdog. The executable is currently
implemented for Linux, macOS and FreeBSD; Windows support remains pending.

## Reverse direction and other implementations

The production executable also supports a HashiCorp host talking to a JSON-line
guest. Set `frontend` to `hashicorp-grpc` or `hashicorp-netrpc`, omit `process`,
and provide a pinned `jsonline` selection:

```json
{
  "frontend": "hashicorp-grpc",
  "jsonline": {
    "executable": "/absolute/path/to/jsonline-guest",
    "sha256": "REPLACE_WITH_REVIEWED_64_HEX_DIGEST",
    "arguments": [],
    "environment": []
  }
}
```

These fields belong in the same `ctx.bridge/v1` configuration with the complete
`descriptor` and explicit `allow` list shown above. Select exactly one upstream
process. The frontend uses CTX's HashiCorp handshake and plugin binding. Its
worker owns the JSON-line guest; launcher death closes the private lifetime pipe
so the worker can close and reap that guest even if go-plugin kills the frontend.

Build with `-tags ctx_cengine` and the C library linker flags to make the relay
use C-owned sessions. Add `ctx_cengine_shared` for shared linkage. The bridge's
HashiCorp runtime code stays Go in its separate process.

A Go HashiCorp server can expose a relay backed by a Rust/Zig JSON-line guest:

```go
relay, err := bridge.Open(ctx, selected, optionsForJSONLineGuest)
if err != nil { return err }
// Server owner arranges relay.Close during its lifecycle.
binding := &hashicorp.Plugin{Guest: relay}
```

Any backend implementing the shared Endpoint/Backend contract can participate
in a relay. Existing non-CTX HashiCorp interfaces still require an explicit
domain translation through `hashicorp.ConnectInterface`; the bridge cannot
infer the meaning of arbitrary native methods. HashiCorp's Go package is one
implementation, while its gRPC protocol can also be implemented in other
languages with the proper startup/service bindings.

## Checked combinations

`scripts/plugin-interop.sh` runs the proof using real subprocesses:

| Host/front | Bridge/backend | Guest | Coverage |
| --- | --- | --- | --- |
| Go Session | generic Relay | in-process fixture | reusable backend conformance |
| Go JSON-line | bridge executable / gRPC and net/rpc | Go CTX HashiCorp guest | conformance, allowlist, digest rejection, startup-failure and abrupt-death cleanup |
| Go/Rust/Zig/Node through C engine | bridge executable / gRPC and net/rpc | Go CTX HashiCorp guest | echo and structured errors |
| Independent TypeScript SDK | bridge executable / both transports | Go CTX HashiCorp guest | echo and structured errors |
| Independent Rust/Zig SDKs | bridge executable / both transports | Go CTX HashiCorp guest | optional prebuilt-host checks |
| Go HashiCorp host | production HashiCorp bridge frontend / JSON lines | Go guest | reverse-route conformance with gRPC and net/rpc |
| Go HashiCorp host | production HashiCorp bridge frontend / JSON lines | Rust/Zig guests | optional prebuilt-guest conformance |

The local proof includes the optional Rust/Zig rows when their artifact paths
are supplied. Ordinary Go test runs skip executable-based tests unless
`CTX_BRIDGE_REQUIRED=1` and artifact paths are set. The runner builds the required
Go binaries and C-language examples; set `ZIG_BIN`/`NODE_INCLUDE_DIR` as described
in the C prototype guide. `CTX_INTEROP_REUSE_CENGINE=1` reuses an existing verified
runner output. Set `CTX_INTEROP_ENGINE=c` to repeat the bridge checks with
C-owned sessions and `CTX_CENGINE_LINKAGE=shared` for the optional shared library.
Both production directions have passed locally with Go and C session engines.
It never enables unreviewed executable discovery.

This is a tested interoperability path, not a claim that every arbitrary host,
guest, runtime feature or native interface is interchangeable.

## Engine embedding and plugin execution

Consumers choosing the experimental C engine get static embedding by default,
with an optional shared library built from the same implementation. This choice
is independent of the execution backend. A statically embedded engine can still
launch a JSON-line guest or a reviewed HashiCorp bridge. A Node addon can contain
the static engine while remaining dynamically loaded by Node.

The C ABI separates host selection/policy from backend configuration. Its current
built-in backend remains JSON-line subprocess. Runtime extensions may supply
additional mechanics through a versioned vtable; unknown kinds are unsupported.
Native Go consumers can continue using the Go implementation without cgo. A
central engine service is deferred. See the [C embedding guide](../pkg/plugin-engine/README.md)
for packaging, ABI migration and ownership rules.
