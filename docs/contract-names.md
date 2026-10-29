# Contract names

A plugin contract is a name and a version, such as `content.provider` at `v1`,
declared in a package descriptor with its operations. This page says who owns
those names and lists the ones that are recorded here.

## Who owns what

Consumers own their contracts: the names, the payload schemas, the semantics
of each operation and how they change. The plugin library does not interpret
payloads. It validates the shape of a name, resolves a dotted method to a
declared contract and operation, and checks calls against what a descriptor
declares. See [the plugin contract ADR](adr-plugin-contract.md).

This page is a registry of names only. It exists so that two consumers do not
claim the same name for different payloads. It holds no schemas and no code.
Listing a name here does not make the plugin library responsible for it.

The plugin library owns the `ext.*` namespace for its own formats, such as
`ext.plugin/v1` and `ext.package/v1`. No consumer contract uses it.

## What a name may look like

The library accepts a contract or operation name that is 1 to 256 bytes, starts
with a lowercase letter or digit, and otherwise uses lowercase letters, digits
and `.`, `_`, `/` and `-`. Contract names may contain dots.

A host or guest that addresses operations as one string uses
`<contract name>.<operation>`, for example `content.provider.observe`. The
library resolves the longest declared contract name that prefixes the method;
the rest is the operation (`Descriptor.ResolveMethod`).

The operation name `plugin.hello` is reserved by the library for the handshake
and cannot be declared.

## Choosing a name

- A short, generic name such as `device` or `display` is the most likely to
  collide with another consumer's contract later. Use one only if it is
  recorded below with its owner.
- If a name is not recorded here, qualify it with an owner prefix that is
  unlikely to be reused.
- Keep a name stable. A change that old plugins cannot handle is a new version
  of the contract, never a silent change under the same version.
- A contract becomes a shared contract only when a second consumer needs the
  same payloads. Then it moves to an agreed owner and this page records the
  change. Until then it stays with the consumer that defined it.

## Recorded names

| Contract | Version | Owner | Status | Schema |
| --- | --- | --- | --- | --- |
| `content.provider` | `v1` | Cymonkey | in use | Go constants and doc comments in `lib/blockade/plugin/adapter.go`; JSON schemas for its documents in `lib/blockade/protocol/v1alpha1/` |
| `device.provider` | `v1` | Cymonkey | in use | Go constants and doc comments in `lib/board/plugin/provider.go` |
| `display.engine` | `v1` | Cymonkey | in use | Go constants and doc comments in `lib/jangolova/plugin/adapter.go` |
| `inference.provider` | `v1` | Ext (`res/inference`) | implemented in Ext | JSON schemas and fixtures in `res/inference/testdata/`; Go bridge in `pkg/ext-go/inference` |

The owner's repository is `github.com/webong/cymonkey`. Its operations, as
declared by the owner: `content.provider` has `start`, `observe`, `interpret`,
`capabilities`, `health`, `close` and `model.inspect`; `device.provider` has
`list`, `open`, `invoke`, `stream.open`, `stream.read`, `stream.close` and
`close`; `display.engine` has `inspect`, `connect`, `call`, `authorize`,
`health`, `disconnect`, `events` and `material`. The owner commits to additive
changes only within `v1`.

Status is one of `proposed`, `in use` and `retired`. The owner updates its own
rows through a change to this page.

## Adding or changing a row

Open a change to this page with the name, version, owner, status and where the
schema is published. Name collisions with a recorded row are resolved by
renaming the newer claim, not by changing the older one.
