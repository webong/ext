# ADR: Package, source, bundle and resource commands

Status: proposed, 2026-11-24. This records the requested command direction;
the responsibilities and example syntax below are for review, not implemented
commands except the first increment below.

Implemented first increment: `ext package` installs and trusts packages without
command declarations. `ext bundle fetch` and `ext bundle bind` replace the
former top-level library command directly. Command and adapter packages now share
`EXT_HOME/packages`; `ext package migrate` combines their old stores. Sources,
grouped bundle planning and generic resource discovery remain proposed.

## Context

Ext hosts a network of plugins. A plugin may expose commands and capabilities,
consume another plugin's contracts, and have extension points of its own.
An adapter is a plugin with integration responsibilities, not a separate kind
of software users should have to manage through `ext adapter`.

Previously `ext package` managed command packages, while `ext adapter` managed a
separate adapter store with catalog, build, archive and diagnostic operations.
The top-level adapter command is now removed; its operations are reached through
package management. Catalog operations remain `package available|add|refresh`
until source management is implemented.
The new interface should organize operations by what the user is managing.

## Proposed command model

| Command family | Object | Responsibilities |
| --- | --- | --- |
| `ext package` | An installable plugin package | Install, inspect, verify, trust, upgrade and remove packages, including adapters and command providers |
| `ext source` | A configured package catalog or repository | Add, list, inspect, refresh and remove places from which packages can be discovered |
| `ext bundle` | Distributable artifacts and their composition | Fetch verified native library artifacts and generate bindings now; grouped package planning remains proposed |
| `ext resource` | A resource exposed through a plugin contract | Discover and inspect resources available from installed providers; invoke only explicitly declared operations |

These are distinct concepts: a source locates packages, a package provides
capabilities, a bundle groups package selections, and a resource is exposed by
a provider. A bundle is not another archive format. A source is not permission
to execute what it lists. A resource is not an executable artifact or an
implicit grant to use it.

Illustrative syntax, subject to the contract and migration decisions below:

```text
ext source add <name> <location>
ext package install <package-reference>
ext bundle inspect <bundle-reference>
ext resource list
```

Plugin commands remain dynamically discovered. CTX, CTN, CTO and other command
providers are ordinary packages; their names and domain operations are not a
fixed command catalog in Ext's root documentation. Programs may also expose
these capabilities to software through public APIs.

## Reuse existing mechanisms

The native library workflow is now `ext bundle fetch` and `ext bundle bind`,
with no intermediate `lib` subcommand. Libraries describe their interfaces in
`interface.json`; citations identify release artifacts by target and driver.
Loading a library through its C interface remains different from running a
plugin through `ext.plugin/v1`. Sharing the bundle command family does not
make a library description a plugin descriptor or grant execution authority.

Keep `ext.package/v1`, descriptors, declared dependencies, exact contract
versions, artifact integrity and explicit trust. Reuse sealed packages,
digest-pinned archive installation, bounded extraction and receipts; see
[adapter packages](adr-adapter-package.md), [the plugin contract](adr-plugin-contract.md)
and [archive evidence](plugin-engine-parity.md#evidence-for-the-current-increment).

Package dependency resolution must expose missing or conflicting requirements
before activation. Installing a dependency does not authorize it. Removing or
upgrading a package must make its effect on dependents reviewable. Do not assume
that installation of a bundle is atomic; define failure and recovery behavior
before offering such a guarantee.

Resources need an explicit discovery contract before generic commands can
operate on them. Providers own resource types, identifiers and domain behavior.
Ext must not guess a native action from a provider name or add product-specific
switches to resource dispatch. Discovery and inspection must distinguish
manifest metadata from live observations that require an authorized invocation.

## Implementation and remaining design decisions

1. Implemented: one default root, `EXT_HOME/packages`, and a scoped ledger at
   `EXT_HOME/packages/.package-trust.json`. `ext package migrate` stages verified
   packages, receipts and trust records and publishes them with one rename.
   Different content under the same identity, malformed packages and conflicting
   trust records abort before publication. Old roots remain as backups and are
   never read after publication, including after revocation. Run migration with
   other package writers stopped; a migration lock serializes migration calls.
   Custom `EXT_ADAPTER_HOME` roots remain explicit isolated stores and are not
   automatically moved. Old unsealed adapter templates require reinstalling from
   reviewed source; migration will not reseal changed content and retain trust.
2. Implemented: `package install|upgrade|list|inspect|verify|trust|untrust|remove`
   manages packages. `build|pack|index|test|doctor` retains adapter-specific
   operations through existing handlers. Archive installs use `--sha256` and
   preserve receipts. There is no top-level adapter compatibility alias.
3. Define source identity, provenance, update and authentication rules. Review
   whether the existing catalog/index format supplies them before adding one.
4. Define bundle composition, pinning, dependency conflicts and partial failure
   behavior. Reuse existing package dependency data where sufficient.
5. Validate the resource meaning with real consumers, then specify enumeration,
   pagination, authorization, operation schemas and errors. Do not present a
   universal resource API as already available.

Contract or manifest changes follow C-first implementation, shared fixtures and
Go parity. Product-specific behavior remains in its plugin or adapter.

### Implementation sequence

1. Separate generic package management from command registration. Today
   `cmd/ext/internal/pkgcmd` can install a package without commands, but its
   trust operation previously refused that package. This increment supports reviewed
   packages independent of whether they expose commands; command registration
   remains a consumer of trusted capabilities.
2. Design and test store migration before changing the public commands. Existing
   identities, verified artifacts, receipts and scoped trust must be accounted
   for. Produce a migration report for collisions or changed content rather
   than silently picking a winner.
3. Move catalog configuration and refresh to source management. Keep source
   selection distinct from runtime provider selection and command-name routing.
4. Add bundle planning over package dependencies, then resource discovery over
   an explicit provider contract. Neither should require a product-name branch.
5. Switch documentation and remove the old adapter command as a deliberate
   development cut once equivalent operations and migration checks pass. Do not
   introduce compatibility shims by default.

New host mechanics belong in `lib/plugin` and its described library interface,
then the language bindings. Under decision 8 of the
[library opener ADR](adr-library-opener.md), the pure-Go host is being retired;
do not build the new management foundation on it. Existing code is a reference
for behavior and migration, not the destination for new host features.

Acceptance must cover a package with no commands, a command plugin, an adapter,
a dependency chain, ambiguous and missing providers, tampering, trust-scope
separation, and interrupted migration or bundle installation. A declared
dependency does not itself establish a live session or authorize a reverse call.

## Documentation

The root README explains Ext's purpose, shell and embedded use, dynamic plugin
discovery, and the repository layout. CLI installation points to
`https://webong.dev/ext/install.sh` and `https://webong.dev/ext/install.ps1`.
Source builds belong in contributor documentation, and plugin usage belongs
with each plugin. Planned commands are documented here until implemented.

This proposal complements [extension-author DX](adr-extension-authoring-dx.md):
that ADR defines the author workflow; this one defines the proposed management
vocabulary. Neither creates another package format or changes existing commands
by documentation alone.


## Unified package-management checklist (2026-11-24)

This increment is the shared store and existing management operations, not the
future source and resource APIs.

- [x] Install and approve plugins without command declarations.
- [x] Expose native fetch and bind directly under `ext bundle`.
- [x] Share `EXT_HOME/packages` between command discovery, adapter consumers and
  default intelligence-provider discovery.
- [x] Migrate legacy stores with verified content, identity collision detection,
  preserved executable artifacts, receipts and unchanged authorization scopes.
- [x] Keep migration failures unpublished and old directories intact. A second
  migration does not resurrect removed packages or revoked grants.
- [x] Move top-level adapter operations under package management, including
  adapter sealing, archive installation, catalog operations, diagnostics,
  shim activation and dependency-aware adapter removal.
- [x] Preserve per-consumer trust: migration copies scopes unchanged. Explicit
  `package trust` records host approval and, for adapters, adapter approval;
  this does not grant CTN inference/model or other consumer-specific authority.
- [x] Exercise command and adapter migration through the real CLI, then prove
  CTX/CTN can consume the adapter and still reject tampered content.

Run migration with other package writers stopped. An interrupted process may
leave `.package-migration.lock` in `EXT_HOME`: after confirming no migration is
running, remove that lock and rerun `ext package migrate`. Unpublished
`.packages-*` staging directories are never treated as installed state. Retained
legacy stores are backups, not fallbacks. Migration intentionally fails on an
unsealed legacy template; reinstall it from reviewed source rather than granting
trust to newly sealed bytes.

The next architecture increments remain source catalogs, bundle composition and
resource-provider contracts. Existing catalog commands are temporarily exposed
under `ext package`; they retain their existing explicit activation/trust behavior.
The existing store and trust APIs are reused here; no C ABI, manifest or wire
contract changed and no new pure-Go host engine feature was added.
