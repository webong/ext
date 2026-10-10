# plugin

`github.com/webong/ext/pkg/plugin` defines the portable contract for
independently provided host and guest implementations, and supplies the runtime
backends that carry it.

| Package | Role |
| --- | --- |
| `pkg/plugin` | Contract, validation, selection, requirements, session lifecycle and observation |
| `pkg/plugin/adapter` | Host side of adapters, usable by any ext product: manifests, the installed store, packaging and indexes, trust and invocation. The process protocol adapter executables speak is in the root package (`adapter.go`). |
| `pkg/plugin/inprocess` | Trusted endpoints with connection lifetime cancellation |
| `pkg/plugin/jsonline` | Bounded JSON-line transport over a supplied duplex connection |
| `pkg/plugin/host` | Opens an installed package as a running plugin and calls it by dotted method name (`Open`, `Call`, `Close`) |
| `pkg/plugin/jsonschema` | Bounded offline JSON Schema (2020-12 subset) validation of arbitrary caller schemas; `schema` is the small typed vocabulary for manifests |
| `pkg/plugin/process` | Child-process backend over JSON lines: scrubbed environment, bounded stderr, consumer-supplied launch command, kill on cancel |
| `pkg/plugin/store` | Generic package store under a caller-supplied root: install, upgrade, list, remove, digest-verified |
| `pkg/plugin/nativego` | Native Go plugin loading |
| `pkg/plugin-cshared` | Versioned C ABI header and runtime loader |
| `pkg/plugin/author`, `schema`, `stream`, `instance`, `capability`, `packagekit` | Typed authoring, bounded schemas, streams, instances, capabilities and packaging |
| `pkg/plugin/plugintest` | Shared conformance fixtures |
| `pkg/plugin/bridge`, `interop` | Route resolution and bridges between backends |
| `pkg/plugin-engine` | Portable C engine and its public headers |

Runtime backends that own a heavy runtime are separate modules:

| Module | Backend | Runtime |
| --- | --- | --- |
| [`plugin/hashicorp`](hashicorp/README.md) | Native go-plugin startup with net/rpc or gRPC | `go-plugin`, `grpc` |
| [`plugin/wasm`](wasm/README.md) | WASI Preview 1 guests | `wazero` |

Runtime backends may use the runtime that owns their mechanism. HashiCorp and
native Go remain Go integrations; a non-Go host reaches them through an
explicit bridge rather than by embedding Go.

Language SDKs and bindings live alongside this library under `pkg/<language>/`.
See [the plugin SDK guide](../../docs/plugin-sdk.md),
[the contract decision record](../../docs/adr-plugin-contract.md),
[interoperability](../../docs/plugin-interoperability.md), and the
[shared-engine parity checklist](../../docs/plugin-engine-parity.md).
