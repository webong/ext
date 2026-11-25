# Package-management implementation work

This tracks the next increments after the unified store. The command and
ownership decisions are in [the command ADR](adr-cli-package-model.md).

## Acceptance

Run `bash scripts/package-acceptance.sh` from any directory. It fails on any
failed case and disables the Go test result cache. The real CLI and product
guests are built in the checkout, staged into temporary directories, and invoked
from a separate temporary working directory with `GOWORK=off`. Runtime processes
receive an isolated `EXT_HOME`, without inherited Ext provider/store overrides.
No user packages or trust records are changed. Required CLI lifecycle and bundle
recovery tests must emit passing test and package events; a missing selection or
skipped required test (including a subtest) fails the gate. The runner requires Go, Python 3, CMake and
a C compiler; a missing C library is a failure, not a skipped acceptance case.

The command lifecycle covers install, refusal before approval, invocation,
upgrade invalidating approval, reapproval, migration and removal. The adapter
lifecycle covers approval, consumption through installed CTX/CTN packages,
diagnostics, migration, tampering rejection and removal. A non-command provider is installed from a SHA-256-pinned archive, then invoked
by a staged software consumer using the described C engine interface. It checks
approval, revocation, tampering and removal, and confirms the operation is not a
CLI command. A second installed guest makes a reverse call that the acceptance
consumer forwards to the C-resolved provider through a second C host. The case
checks both approvals, revocation, missing and ambiguous providers, and recovery.
This proves live calls under explicit application policy, not automatic routing
for every product. Bad archive pins cannot publish a package; successful installs
retain receipts. Manager-level tests cover migration collisions and trust scopes.

This is runtime independence evidence, **not** evidence that current SDK paths
build against published module versions. Dependency scenarios and interrupted bundle operations still need expansion.

## Archive and installer rehearsal

Run `python3 scripts/rehearse-cli-install.py` on macOS or Linux. It builds only the
current host target into temporary directories, stages the canonical C engine,
creates a release archive and installs it through a copied installer outside the
checkout. It executes the installed package planner and all three bundled product
guests, then verifies that bad checksums, broken native libraries and engine-free archives
are rejected without changing installed files, permissions or approvals. It publishes nothing and uses an
isolated `EXT_HOME`. Product guests require local socket access.

## Remaining implementation

- [ ] Complete external acceptance: dependency chains, missing/ambiguous providers
      and interrupted operations. Command, adapter, C-host provider and pinned
      archive cases are implemented.
- [x] Reuse artifact indexes with C catalog validation/selection; implement named
      source add/list/inspect/refresh/remove and pinned installation without trust.
- [x] Verify HTTPS source fetch and refresh against controlled TLS servers, including
      redirect policy, size bounds and preservation after rejected refreshes.
- [x] Import verified directory catalogs as pinned archive sources; retire
      top-level legacy catalog activation with migration guidance.
- [x] Run the full package acceptance gate on Linux arm64 in a disposable
      container with the checkout mounted read-only. This proves runtime acceptance,
      not published-module independence or Windows support.
- [x] Rehearse current-target archive installation on macOS arm64 and Linux
      arm64, including bundled product execution and preservation of the installed
      tree after bad checksums, broken engines and missing engines.
- [ ] Collect Linux/Windows distribution execution evidence and migrate
      product-owned adapter compatibility workflows. Private registry
      authentication remains a separate design.
- [x] Require native artifacts for every CLI release target; bundle the C engine
      and verify it in both installers. The repeatable POSIX archive/install rehearsal
      passed on macOS arm64 and Linux arm64; Windows runtime evidence remains open.
- [x] Expose the existing C dependency resolver through package plan, with
      removal/replacement previews, approval reporting and command admission.
- [x] Check directory and archive replacements against installed requirements
      before publication, including named-source replacements.
- [x] Verify three-package transitive command admission, revocation and provider
      replacement with the real CLI outside the workspace.
- [x] Apply generic dependency-removal preflight to adapter targets before
      adapter cleanup; retain explicit force semantics.
- [x] Prove live provider-to-provider calls through C hosts in the staged
      acceptance consumer, including missing/ambiguous providers and revocation.
- [x] Project legacy adapter constraints into the C-resolved graph for package
      planning, admission, replacement/removal and bundle workflows.
- [x] Add persisted named provider selectors in C and Go contract parity, keeping
      exact identity pins separate. Shared fixtures cover missing and ambiguous
      revisions; outside-workspace CLI acceptance covers equivalent providers,
      compatible upgrades, approval invalidation and removal preflight.
- [x] Add OS/architecture conditions to package requirements, explicit target
      resolution in C and Go parity, and target selection in CLI and bundle
      planning. Validate inactive declarations and reject target-free conditional
      resolution. This enables, but does not complete, adapter declaration migration.
- [x] Migrate first-party adapter declarations to generic `requires` and
      adapter-owned share-space mappings. Keep legacy loading, reject mixed or
      conflicting mappings, and verify CLI/bundle lifecycle and CTX handoff.
- [ ] Finish reusable routing integration in product consumers. The resource CLI now opens
      routed graphs; other product consumers still need adoption.
      See [planning](package-planning.md).
- [x] Expose native forwarding through described host routes with application
      authorization, inherited deadlines/cancellation and pinned target lifetime.
      Exercise real provider calls, denial and closed targets, plus C lifetime
      checks for both close orders. Broader product adoption remains open.
- [x] Compile route candidates from target-resolved requirements and consumer
      reverse declarations in C, with shared fixtures. Support separate providers
      for different operations of one contract; reject competing providers for
      one operation. Use the compiler in the standalone acceptance consumer.
- [x] Open resource provider graphs through the shared CLI native-host workflow.
      Preflight all routed packages before launch, require exact invocation-only
      dependency grants, and close consumers before providers. The required
      three-process acceptance covers untrusted preflight, default denial,
      identity mismatch, successful chaining, process cleanup, and POSIX
      cancellation followed by a fresh call.
- [x] Verify partial graph startup failure: a trusted package whose executable
      announces a different identity fails the native handshake; previously
      started providers terminate, the root never starts, and restoration plus
      a fresh call succeeds. Require this subtest in the acceptance gate.
      The graph lifecycle cases and full gate pass on Linux arm64; the graph
      cases also pass on macOS arm64.
- [ ] Prove Windows graph process cleanup/cancellation and adopt the workflow
      in real product consumers.
- [x] Snapshot all routed resource providers into one private C store before
      launching any guest. Keep provider identities and routes pinned, retain
      copies through host shutdown, and remove them on copy/startup failure or
      cancellation. Required acceptance checks every guest's executable path;
      a changed-source regression rejects a partially copied graph and cleans it.
      The resource command retains its writer through invocation; this does not
      implement snapshot handoff across nested command/product dispatch.
- [x] Add pinned package bundle composition, inspection and installation, with
      staged verification, progress journals and reconciled retries; preserve
      direct native fetch/bind.
- [x] Coordinate built-in package mutations, bundles, migration and legacy trust
      import with one cross-process CLI writer lock.
- [x] Expose offline native target/driver planning; share it with fetch and
      distinguish extraction roots within the same pinned archive.
- [x] Include native citation jobs and resolved assets in bundle planning,
      without downloads or partial installation.
- [x] Stage and publish native jobs under EXT_HOME/artifacts/native/<os>/<arch>,
      with full-content replay checks and artifact-root progress journals.
- [x] Add receipt-verified native upgrades and interruption recovery; exercise a
      mixed bundle with a real C engine and executable packages.
- [x] Coordinate CTX adapter mutations and catalog setup/refresh with the
      package writer; share the directory-lock mechanism through res/system.
- [x] Expose a native C `package_writer` handle interoperable with CLI locks;
      verify cross-process exclusion, abandoned locks and ownership-safe release.
- [x] Use the C writer handle in the staged standalone provider consumer,
      including its live dependency-call path.
- [x] Expose Python `Engine.package_writer` as a native context manager and
      run its contention/crash/ownership cases in the host conformance runner.
- [x] Expose PHP `Engine::packageWriter` and `withPackageWriter` over the C
      handle, with cross-process and ownership checks in host conformance.
- [x] Expose Ruby `Engine#package_writer` over the C handle with scoped cleanup
      and writer regressions in the host conformance suite.
- [x] Expose .NET `Engine.PackageWriter` with `IDisposable` cleanup and native
      cross-process contention, replacement ownership and concurrent-dispose tests.
- [x] Expose Lua `Engine:package_writer` and `with_package_writer` over the C
      handle, with scoped cleanup and cross-process ownership tests in host conformance.
- [x] Add Rust `store::Writer` and `Store::with_writer`, exercising real store
      mutations, panic/error cleanup and separate-process contention. Native writer
      roots reject embedded NULs before filesystem access (C and Rust regressions).
- [x] Add TypeScript `PackageWriter`, `withPackageWriter` and store `withWriter`
      scopes; verify awaited store mutations, rejection cleanup, separate-process
      contention and ownership protection in the native engine suite.
- [x] Expose the same writer in the portable C store API, without process-host
      dependencies; add Swift `PackageWriter` and scoped store mutation tests.
- [x] Bind the portable writer through Android JNI as Java `PackageWriter`
      (`AutoCloseable`); verify actual store mutations and second-JVM contention
      in the desktop Java SDK suite. Device execution remains separate evidence.
- [x] Exercise abrupt subprocess exit after native backup and publication; verify
      the lock survives, then explicitly recover after confirmed process termination
      and reconcile the pending receipt on retry.
- [x] Bind the portable writer as Zig `PackageWriter`; verify store mutation,
      scoped cleanup and replacement ownership with the full Zig engine suite
      (23 tests, including reverse, registry, endpoint, archive and download cases).
- [x] Coordinate CTN inference, perception and model-provider install/trust/untrust
      with the shared package writer, including legacy intelligence-store
      overrides that still write the EXT_HOME trust ledger. Required regressions
      cover contention before store access and writer release after failed installs.
- [x] Hold CTN's package writer through inference, perception and tensor-provider
      admission, invocation and backend shutdown. Abort invocation-owned sessions
      even when their request context is canceled. Required cases prove contention,
      failed-admission release, live trust-change refusal and canceled perception
      guest termination. CTN tests pass with both fallback and C-backed engines on
      macOS arm64; Windows process termination still needs runtime evidence.
- [x] Audit in-repository store clients and distinguish explicit SDK mechanisms
      from application transactions; record boundaries and remaining execution
      gaps in [the writer audit](package-writer-audit.md).
- [x] Add C-first reviewed package snapshots with bounded verified copying,
      exact ID/digest checks before publication, no replacement, and no copied
      trust or receipts. Test source-change independence through C and the
      generated Go binding; require the binding test in the package gate.
- [x] Snapshot user-installed dynamic command packages before launch. Recheck
      discovery metadata, trust and dependency admission under the writer; copy
      with the C store, release the writer, and run the private entrypoint.
      Require stdio/socket cleanup, guest writer acquisition, revoked-trust and
      source-removal execution tests. Bundled release packages retain their
      existing installation trust boundary.
- [x] Execute CTX's central adapter dispatch from C-verified private snapshots.
      The CLI passes its canonical engine path to socket guests; embedded CTX
      can use a provided library. Recheck selected metadata and adapter trust
      under the writer, release it before launch, and clean up after execution.
      Required POSIX acceptance performs a real nested credential adapter call,
      proves both guests can acquire the writer and both snapshots are removed.
- [x] Share CTX snapshot preparation in `internal/adaptersnapshot` and use it
      for browserhost discovery, query and normalization. Each invocation reloads
      its selected store, rechecks trust and rejects changed selection metadata.
      Required POSIX tests cover private execution, explicit-store contention,
      revocation after selection, replacement rejection and cancellation cleanup.
      Browserhost tests pass on macOS arm64; the full package gate, including
      command and adapter snapshots, passes on Linux arm64 after the main update.
- [x] Share declared dependency-environment preparation across CTX central
      dispatch and browserhost. Remove inherited hints case-insensitively and
      emit only selected manifest declarations. Required tests cover duplicate
      and undeclared hints plus live browser invocations with forged overrides.
- [x] Share CTX host executable/prefix setup with browserhost and make the
      credential client honor the host's validated command prefix. Reject prefix
      overrides in native selector settings. Real nested acceptance uses the
      Go credential client from both ordinary and browser adapters through ext;
      preserve snapshot cleanup and writer availability in both paths.
      The full package acceptance gate passes on macOS arm64 with these paths;
      this remains runtime evidence, not published-module build evidence.
- [x] Move resource execution-graph lifetime into the C `execution_graph`
      object. The CLI supplies review and reverse-call policy; C owns private
      copies, provider hosts, startup, cancellation and teardown. Required tests
      cover copy failure, partial startup failure, live cancellation and POSIX
      cancellation during the initial handshake.
- [x] Expose C execution session observations and graph snapshots, using ledger's
      process identities; require real routed-child correlation with the recorder.
- [x] Add revision-checked whole-namespace reconciliation in C ledger; require
      stale-revision, atomic rejection, isolation, explicit clear and file reopen checks.
- [ ] Adopt graph publication/reconciliation in applications with explicit ownership,
      shutdown ordering and restart policy; do not treat snapshots as liveness leases.
- [ ] Integrate private execution snapshots into nested command/adapter dispatch,
      including whole-graph review, private snapshot ownership and cleanup.
- [ ] Complete writer adoption for remaining consumers, including CTX adapter
      and dynamic-command execution. Preserve nested dependency dispatch and
      product management authority; the exclusive writer cannot wrap these
      paths indiscriminately. External SDK callers still require their own audit.
- [x] Inspect KVLite's extension and database APIs and record the resource
      discovery design in [the consumer design](resource-discovery-design.md).
- [x] Expose offline resource-provider declarations through C store verification
      and descriptor selection; acceptance checks exclusion and tampering.
- [x] Validate list/inspect request envelopes in C with shared boundary fixtures.
- [x] Validate resource pages and inspections against the selected provider and
      original request in C, with shared response fixtures.
- [x] Add explicit live list/inspect through the C host, with approval, request
      and response checks, paging, remote errors, revocation and deadline tests.
- [x] Pin discovery error categories in C fixtures and validate live provider
      errors, including absence, denial, expiry and rejection of domain codes.
- [x] Add C resource operation selection and payload validation against
      inspection schemas and provider operation declarations; pin matching and rejection in fixtures.
- [x] Expose explicit resource-bound CLI calls through one C host, with live
      inspection, schema validation before/after invocation and provider denial.
- [x] Handle resource-call cancellation through native host shutdown; verify
      Ctrl-C on macOS arm64 and Linux arm64 during discovery and a domain call,
      provider termination, writer release and a successful subsequent call.
      The full Linux package gate passes with required cancellation subtests;
      Windows amd64 cross-compiles, but console interruption evidence remains open.
- [ ] Pin domain error mappings and validate a real consumer bridge; synthetic
      resource calls are protocol evidence, not KVLite integration.
- [ ] Continue driver convergence through the described C library interface;
      validate shared host fixtures across affected bindings. Do not add new
      host mechanics to the retiring pure-Go engine.

Do not mark this list complete from CLI help or unit tests alone. Each increment
needs its contract fixtures, actual invocation evidence and updated documentation.
