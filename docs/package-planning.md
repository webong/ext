# Package dependency planning

`ext package plan` verifies installed packages with the C engine and runs its
existing `package.resolve` service. The JSON report includes:

- `status`: `ok`, `not_found`, `ambiguous`, or the engine error code. A cycle is
  `invalid`, as in the canonical package contract.
- `plan.order`: provider-before-consumer identities.
- `plan.bindings`: the provider selected for every declared requirement.
- `issues`: consumer identity, requirement and missing/ambiguous selection code.
- `unapproved`: packages without current host approval for their verified digest.

A successful resolution is not authorization: planning returns success when the
graph resolves even if `unapproved` is nonempty. It neither installs packages nor
creates sessions or reverse-call routes. Each consumer still owns invocation
policy; domain-specific scopes remain separate from host package approval.

```text
ext package plan
ext package plan --without example/provider
ext package plan --with /path/to/sealed/replacement
```

Previews verify the candidate's artifacts, replace or omit its identity only in
memory, and include previous resolved bindings for comparison. A failed preview
exits nonzero. No package or trust record is changed. Plans cover the complete
installed store: unrelated unresolved requirements also make the overall plan
fail. A malformed or tampered package fails verification rather than being
silently omitted from the graph.

Approval refuses a store with unresolved requirements. Approval of one package
never approves its providers. Before starting an installed command that declares
requirements, Ext rechecks resolution and host approval of its transitive
providers, including the command itself. Revoking a provider therefore prevents
subsequent command starts. This admission applies to both stdio and socket
commands; it does not terminate already-running processes or authorize their
reverse calls.

User-installed dynamic commands execute from a per-invocation private snapshot.
Before copying, Ext takes the package writer, reloads the package, rejects a
manifest changed since command discovery, and rechecks current trust and
configured dependency admission. The C store verifies the snapshot against the
reviewed digest. Ext then releases the writer before launching, so product guests
can still perform authorized management commands. The copy is removed after the
guest finishes, for both stdio and socket transports. Launch overrides outside
the package are rejected on this discovery path. These tests require the canonical
engine; `scripts/package-acceptance.sh` supplies it.

This isolates the command package itself. It does not redirect nested commands
to a snapshot dependency graph, transfer trust to child processes, or change
bundled release-package dispatch. Those boundaries remain separate work.

Package removal checks the resulting plan if remaining packages declare
requirements, including when the target is an adapter. To deliberately leave unresolved dependents, use
`ext package remove <id> --force`. Adapter removal runs both this generic preflight
and its existing specialized dependency checks before removing shims or content. Directory upgrades and pinned archive replacements (including
named sources and archives passed to `install`) now preview the replacement
automatically when installed packages declare requirements. A rejected change
leaves the installed package and its approval intact. Archive preparation checks
the pin before planning, and publication checks it again. New packages may still
be staged with unresolved requirements; approval and invocation remain gated.

Built-in package mutations hold `EXT_HOME/.package-write.lock` across preflight
and publication; bundle installation and explicit trust changes use the
same lock. CTX adapter install/trust/remove/add/refresh and its catalog setup
helpers participate too, including when CTX runs as an installed guest. They use
the shared filesystem utility `pkg/ext-go/core/dirlock`; the lock remains
held through dependency checks, trust and shim updates. An adapter-root override
uses the same lock filename beside that store. Direct library clients do not yet
participate automatically and must coordinate explicitly. Native C described
hosts can open `package_writer` with the Ext home as `root` and hold it across
planning, publication and trust changes; close releases it.
CTN inference, perception and model-provider mutations and invocation-owned
sessions also participate. Their session shutdown precedes lock release, including
when the request context is canceled. This does not establish lifetime locking
for arbitrary command plugins or CTX adapters: their nested dispatch needs a
separate protocol. See the [writer adoption audit](package-writer-audit.md).
Adapter metadata now participates through the compatibility projection below;
the stored declaration formats remain separate.

The planner needs the canonical C shared library described in
[source management](source-management.md). Its public interface now accepts the
manifest-array input already specified for `package.resolve`; the interface
regression test checks that entry point directly.


## Legacy adapter dependency constraints

An adapter's platform-selected metadata can require a named adapter, an exact
adapter API version and a share space. Generic `requires` selects a
contract operation, optionally naming a package with `provider` or pinning its
full `identity` including revision. The selectors are mutually exclusive.
Named selection permits compatible upgrades but rejects multiple matching
revisions as ambiguous. These declarations are not interchangeable: dropping the named
provider or share-space requirement would weaken the existing constraint, and
inventing a revision would prevent ordinary provider upgrades.

For example, a generic manifest can declare:

```json
"requires": [{
  "provider": "example/provider",
  "contract": {"name": "example.values", "version": "v1"},
  "operation": "read"
}]
```

The named package must still offer that exact contract version and operation.
Changing its revision invalidates its approval even when resolution succeeds.

A package requirement may also declare `"os":"linux"` and/or
`"arch":"arm64"`. Empty conditions match every target; nonempty conditions
must match exactly. Package planning and admission use the running host's target,
and bundle planning uses the bundle's target. All declarations are validated,
including those for other targets. Library callers use C `package.resolve_for`
with `{packages,os,arch}`, Go `packagekit.ResolveFor`, or
`packages.ResolvePackagesFor`. Target-free resolution rejects conditional
requirements. This does not choose an executable or authorize a provider.

The adapter library projects these requirements into an ephemeral generic graph.
It validates all platform declarations, selects the current platform, requires
the named adapter at its exact adapter API version, and checks the `share`
operation and declared share space. Only after those checks does it add a
`requires` edge naming the provider's package ID in this verified
snapshot. It never invents capabilities or revisions and never writes the edge
back to a manifest. A compatible upgrade therefore selects the new revision.

The C resolver evaluates the resulting graph, including cycles and dependencies
that cross between adapter and generic packages. Package planning, trust,
invocation admission, removal and replacement preflight use this projection.
Bundle planning applies it for the bundle target; installation rechecks the
combined installed graph and publishes selected packages in its resolved order.
Missing adapters or incompatible share metadata fail with adapter diagnostics.
Generic manifests can persist `requires.provider`; shared C/Go fixtures and the
outside-workspace CLI acceptance check cover selection and compatible upgrades.
Adapter API, platform and share-space validation still belongs to the adapter
layer. First-party manifests now persist these requirements and use adapter
metadata `share_dependencies` to link a share space to its required provider.
Both the loader and planner read this form; provider/version/platform selection
has one authority in `requires`. Legacy declarations remain readable and are
projected for compatibility, but cannot be mixed with the new mappings. The
planner checks the share space before resolution even when an equivalent generic
edge already exists. Bundle checks use the explicit OS and architecture.

The real CLI acceptance exercises both forms through install, trust, removal,
compatible/incompatible upgrades and bundles. CTX's catalog acceptance exercises
the new form through dependency installation, trust, repair, removal protection
and the `EXT_DEPENDENCY_*` handoff. This does not implement automatic routing of
generic reverse calls in product consumers.

## Live dependency acceptance

The staged software consumer in `cmd/ext/testdata/providerhost` resolves the
installed graph through C, checks both packages' current approvals before either
launch, and obtains route candidates from C `package.routes`. It supplies one
explicitly authorized route to the described C host.
The native registry forwards the reverse operation to the selected provider,
carrying its remaining deadline and cancellation state. The acceptance gate verifies the provider's
result reaches the dependent guest, rejects missing or ambiguous providers and
revoked approval on either package, application denial and a closed target,
and checks recovery after restoration.

This fixture coordinates with CLI writers for the duration of the call. It is
an application policy example using reusable native forwarding and route
candidate compilation. It still selects one route itself. The resource CLI now
opens complete routed graphs with explicit invocation grants and owns their
shutdown; adoption by other product consumers remains work. Route compilation
matches declared reverse operations to resolved requirements, without granting
authority. Different operations of a contract can target different providers;
competing providers for the same operation are rejected.

The repeatable `TestLegacyAdapterPlan` acceptance case also installs pinned
adapter bundles. It checks missing named providers, provider-before-dependent
ordering when lexical order differs, restoration through bundle installation,
and refusal of a provider-only bundle that breaks an installed adapter's share
space. The rejected bundle leaves the existing provider approval intact.
