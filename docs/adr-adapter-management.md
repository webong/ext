# ext adapter management and product consumption

Status: implemented. `ext package` and `ext ctx adapter` use the same
shared library management command.

## Ownership

Adapters are **ext adapters**, regardless of whether they are invoked through
`ext`, its CTX or CTN package, or another consumer. An adapter owns
integration with an external product, provider, browser, or native tool.

Ext libraries own generic adapter contracts, validation, package trust, and
invocation. CTX and CTN own their selection, policy, and command dispatch. They
consume only capabilities they understand and allow, from adapters installed
and available in their configured home. An adapter can serve both products;
calling it from CTN does not turn it into a separate CTN adapter.

## Current commands

`ext` is the main installer and combined entry point. Adapter management is
exposed directly through the `adapter` builtin:

| Entry point | Responsibility |
| --- | --- |
| `ext package install <source>` | Install an ext adapter |
| `ext package build`, `pack`, `inspect`, `trust`, `verify`, `remove` | Build, package, review, and manage ext adapters |
| `ext ctx adapter …` | CTX's project-aware adapter management |
| `ext ctx …` | Consume capabilities for project contexts |
| `ext ctn …` | Consume capabilities for content workflows |
| `ext ctn adapter ls` | List installed adapters and their trust state; read-only |
| `ext package install <source>` | Install command packages in the command store, not the adapter store |

For example, with a ready-to-run evaluator package:

```sh
ext package install ./my-evaluator
ext package inspect my_evaluator
# Review the installed files before trusting the package.
ext package trust my_evaluator
ext ctn adapter ls
ext ctn verify --engine my_evaluator example.wasm
```

The evaluator must expose the capabilities and sandbox isolation CTN requires.
Installation and trust do not bypass those product checks.

CTX and CTN are separate installed packages using the same library mechanisms
and configured adapter home. CTN does not need CTX running to consume
an installed adapter, and does not currently provide installation commands.
With the same home configuration, an adapter installed through either management
entry point is visible to both products. Different home overrides may deliberately
isolate installations.

## Storage and compatibility

The shared library is `pkg/ext-go/engine/plugin/host/adapter`. The default adapter
store is the shared `$EXT_HOME/packages` root, with `EXT_ADAPTER_HOME` as an adapter-only
override. An existing `~/.config/ctx` directory keeps being used until
`~/.config/ext` exists. See the
[API reference](adapter-api.md) for home resolution and scoped trust.

The contract is `ext.adapter/2.0`. Every variable the host passes to an adapter or
reads for itself uses the `EXT_` prefix (`EXT_ADAPTER_API`, `EXT_ADAPTER_NAME`,
`EXT_ADAPTER_REAL_COMMAND`, `EXT_PROGRAMS`, `EXT_EXECUTABLE`, `EXT_HOME`, and so on),
because an adapter is an ext adapter and the product that invokes it is only one of
its hosts. CTX and CTN provide their own hooks and listen; they define no `CTX_`
binding of their own. This is a hard cut: the former `CTX_` names are not read, so an
adapter installed before the change must be reinstalled. CTX and CTN use the
same home through Ext. `EXT_EXECUTABLE` names the Ext binary for adapter calls
from the CTX package. `ctx.adapter/v1` metadata and `.ctxadapter`
suffixes are serialized names and are unchanged.

## Shared implementation

`pkg/ext-go/engine/plugin/host/adapter/adaptercmd` owns the management command,
catalog activation and refresh, and declared launcher installation. It is part
of the standard-library-only engine module and imports no product code.
`cmd/ext` delegates adapter-specific package operations from `ext package` to it. The installed CTX package calls
the same implementation with CTX diagnostics and its project-aware doctor callback.

`ext package doctor NAME` uses the adapter's default selection. CTX supplies
its own selection and invocation policy for `ext ctx adapter doctor NAME`. Both
paths enforce trust before invoking the installed adapter. CTN continues to
read the shared store and apply its evaluator and isolation policy.

`ext package` is the host's package-management command. There is no `USED BY` column: capability
compatibility is not evidence of actual usage.

The cross-product acceptance test in `cmd/ext/adapter_test.go` builds Ext and
its product packages, installs through Ext, trusts through Ext, and verifies
content through CTN. It also checks that untrusted and changed
packages are refused, and that removal through Ext is visible through ext.

## Related

- [Adapter API](adapter-api.md)
- [Build an ext adapter](adapter-authoring.md)
- [The ext command](adr-ext-command.md)
- [Ownership rules](../AGENTS.md#adapter-ownership)


Legacy command and adapter stores move together with `ext package migrate`.
The original directories are preserved as backups. The shared trust ledger is
`EXT_HOME/packages/.package-trust.json`; approval scopes remain distinct. A
custom `EXT_ADAPTER_HOME` deliberately selects an isolated adapter store and is
not automatically migrated. The host's `ext package` always manages its shared
package root, including when such a consumer override is set.
