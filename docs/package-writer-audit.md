# Package writer adoption audit

This audit covers production callers in this repository. SDK store methods are
mechanisms: they do not implicitly acquire an Ext-home lock. An application must
hold a writer across the whole review/publication/trust transaction, using the
same home as every cooperating client. A lock around each method alone leaves
races between methods. Isolated test stores are not shared production clients.

| Caller | Coordination now | Remaining work |
| --- | --- | --- |
| CLI package install/upgrade/trust/remove and migration | `cmd/ext/internal/packagelock`, held through preflight and mutation | Discovered commands snapshot their own package under the writer; dependency graph isolation remains open |
| CLI source import | Source lock; archive verification uses a private staging store | Staging does not publish into the installed store |
| CLI bundle install | Package writer covers publication and receipts; native artifacts have their own journals | Retain separate explicit project fetch/bind operations |
| CLI resource invocation | Package writer covers graph preflight, verified graph snapshot, invocation and reverse-order host shutdown; private copies are removed last | Only process entrypoints; product adoption remains incomplete |
| CTX adapter management and catalog setup | `acquireAdapterWriter` covers mutation, trust and shim changes | Central dispatch and browserhost share snapshot preparation; whole-graph isolation remains open |
| CTN inference, perception and tensor providers | Common writer covers mutations and live session admission through backend shutdown | Same-home coordination; independent homes sharing an overridden store require application coordination |
| CTO | No installed-plugin store mutation found in its production Go callers | Reaudit if CTO adds management |
| Standalone provider acceptance consumer | Native `package_writer` held through review and invocation | Acceptance evidence, not a production consumer |
| Language SDK store/trust APIs | Explicit writer scopes available in C, Python, Ruby, PHP, Lua, .NET, Rust, TypeScript, Swift, Java and Zig | Callers outside this repository remain responsible for adoption |
| Go compatibility store/trust APIs | Low-level APIs remain explicit; CTX/CTN use `src/ctx/system` directory locking and described-host consumers can use native `package_writer` | Do not add new host machinery to the retiring pure-Go engine |

The production callsites inspected are `cmd/ext/internal/{pkgcmd,bundlecmd,
sourcecmd,resourcecmd}`, `src/ctx/internal/app/{adapters,setup}.go`,
`src/ctn/internal/app/{inference,perception,model_provider}.go`, and SDK store
bindings under `pkg/`. Native download helpers also leave transaction ownership
with their caller; their documentation shows the writer spanning download and
installation. Generated bindings expose mechanisms and cannot infer an
application's Ext home or transaction boundary.

## Nested adapter execution is a distinct gap

`src/ctx/internal/app/adapters.go:invokeAdapterIOWithEnv` verifies adapter trust
and runs its entrypoint from a private per-invocation copy. It supplies `EXT_EXECUTABLE` and
`EXT_DEPENDENCY_*` for provider-owned dependency dispatch. Chromium's credential
integration invokes that executable to reach its credential adapter;
`TestChromiumSecretServiceIdentityFallback` demonstrates the nested dispatch.

Adding the exclusive package writer around every invocation would reject that
nested call. Similarly, holding it around every dynamically loaded Ext command
would prevent a product guest from running its authorized package-management
commands. Neither path should silently inherit an environment flag that bypasses
coordination: arbitrary guests can forge environment values.

The remaining execution protocol must preserve nested dispatch and concurrent
readers while preventing reviewed installed bytes from changing before use.
The first implementation uses verified execution snapshots: the C store now
provides `snapshot {source,id,reviewed_digest}` into a caller-owned private store.
It verifies the source and staged-copy digests, publishes without replacement,
and grants no trust. Discovered user-installed commands now execute their own package from such a
copy and clean it up after the guest exits. CTX central adapter dispatch and `internal/app/browserhost` now use the same
`internal/adaptersnapshot` preparation. Browser source selection is not a lasting
approval: invocation rechecks the selected store and refuses revoked trust or a
changed manifest. Whole-graph isolation remains open.
Integration must review and snapshot the execution graph under the writer, then
release it before starting nested commands and retain copies until all consumers
finish. Snapshot paths and approvals must not be accepted from untrusted guest
environment variables. Reader leases remain a possible future alternative.
New shared host mechanics belong in C first, with fixtures and binding parity.

Required acceptance before closing this gap:

1. A browser adapter reaches its credential dependency through a real nested
   command while a concurrent package replacement is either excluded or leaves
   both calls on their verified snapshots.
2. Independent invocations can run concurrently.
3. Product management commands retain their intended write authority without
   lock bypasses or deadlocks.
4. Cancellation and abnormal process exit do not silently permit mutation of
   still-running consumers, and do not reclaim live ownership based on age.

The existing exclusive writer is sufficient for the explicitly scoped resource
and CTN sessions tested today. That evidence does not prove safe coordination for
whole dependency graphs or compatibility adapters.


The CLI supplies its canonical engine path as `EXT_PLUGIN_LIBRARY` to socket
product guests. CTX uses that library's described store interface, or an
application-provided `ext_plugin` library when embedded. This selects runtime
code, not a package approval or lock-bypass token. Adapter selections, capability
checks and dependency dispatch still belong to CTX. The POSIX nested acceptance
case runs `ext` → CTX → outer credential adapter → `ext` → CTX → leaf adapter;
both adapter processes can acquire the writer and their private copies are
removed after completion. This proves independent per-invocation snapshots,
not one pinned dependency graph across the entire chain.


CTX central dispatch and browserhost also share `internal/adapterenv` when
passing dependency hints to an adapter. Inherited `EXT_DEPENDENCY_*` entries are
removed case-insensitively, including duplicates and undeclared spaces; only the
selected manifest's dependencies are emitted. Live browserhost tests cover both
an adapter with a credential dependency and one with none. Hints remain selectors,
not grants: the receiving product still checks the provider's trust. This closes
the browserhost path's previous reliance on inherited dependency overrides; it
does not pin all nested calls to one package revision.


Nested credential calls use the executable and optional command prefix supplied
by the host. Both CTX invocation paths prepare those values consistently; when
CTX is an Ext product, the credential client calls `ext ctx credential ...`.
The client validates the prefix as one bounded command name and prevents native
selector settings from overriding it. The nested acceptance fixture uses the
actual Go credential client for both ordinary and browser adapters, and checks
private-copy cleanup after the dependency returns.
