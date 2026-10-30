# ext adapter management and product consumption

Status: implemented. `ext adapter` and standalone `ctx adapter` use the same
shared library management command.

## Ownership

Adapters are **ext adapters**, regardless of whether they are invoked through
`ext`, standalone `ctx`, standalone `ctn`, or another consumer. An adapter owns
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
| `ext adapter install <source>` | Install an ext adapter |
| `ext adapter build`, `pack`, `inspect`, `trust`, `verify`, `remove` | Build, package, review, and manage ext adapters |
| `ctx adapter …` | The same management commands when CTX runs standalone |
| `ext ctx …` or `ctx …` | Consume capabilities for project contexts |
| `ext ctn …` or `ctn …` | Consume capabilities for content workflows |
| `ext ctn adapter ls` or `ctn adapter ls` | List installed adapters and their trust state; read-only |
| `ext package install <source>` | Install command packages in the command store, not the adapter store |

For example, with a ready-to-run evaluator package:

```sh
ext adapter install ./my-evaluator
ext adapter inspect my_evaluator
# Review the installed files before trusting the package.
ext adapter trust my_evaluator
ext ctn adapter ls
ext ctn verify --engine my_evaluator example.wasm
```

The evaluator must expose the capabilities and sandbox isolation CTN requires.
Installation and trust do not bypass those product checks.

Standalone CTX and CTN use the same library mechanisms and configured adapter
home as their embedded counterparts. CTN does not need CTX running to consume
an installed adapter, and does not currently provide installation commands.
With the same home configuration, an adapter installed through either management
entry point is visible to both products. Different home overrides may deliberately
isolate installations.

## Storage and compatibility

The shared library is `pkg/ext-go/engine/host/adapter`. The default adapter
store is under `$EXT_HOME/adapters`, with `EXT_ADAPTER_HOME` as an adapter-only
override and legacy CTX home settings retained. See the
[API reference](adapter-api.md) for home resolution and scoped trust.

The contract is `ext.adapter/2.0`. Existing `ctx.adapter/v1` metadata,
`CTX_ADAPTER_*` environment variables, and `.ctxadapter` archive/index suffixes
remain compatible. Their spelling does not imply CTX ownership. This terminology
change does not rename serialized fields or invalidate existing packages.

## Shared implementation

`pkg/ext-go/engine/host/adapter/adaptercmd` owns the management command,
catalog activation and refresh, and declared launcher installation. It is part
of the standard-library-only engine module and imports no product code.
`cmd/ext` registers it as the `adapter` builtin. Standalone CTX calls the same
implementation with CTX diagnostics and its project-aware doctor callback.
`ext ctx adapter` also remains available through the embedded product.

`ext adapter doctor NAME` uses the adapter's default selection. CTX supplies
its own selection and invocation policy for `ctx adapter doctor NAME`. Both
paths enforce trust before invoking the installed adapter. CTN continues to
read the shared store and apply its evaluator and isolation policy.

`ext adapter` follows the command registry's normal builtin precedence and
explicit source selection rules. There is no `USED BY` column: capability
compatibility is not evidence of actual usage.

The cross-product acceptance test in `cmd/ext/adapter_test.go` builds all three
binaries, installs through ext, trusts through standalone ctx, and verifies
content through standalone ctn. It also checks that untrusted and changed
packages are refused, and that removal through ctx is visible through ext.

## Related

- [Adapter API](adapter-api.md)
- [Build an ext adapter](adapter-authoring.md)
- [The ext command](adr-ext-command.md)
- [Ownership rules](../AGENTS.md#adapter-ownership)
