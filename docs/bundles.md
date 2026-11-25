# Pinned package bundles

`ext bundle plan <bundle.json>` and `ext bundle inspect <bundle.json>` validate
and report a reproducible package selection. `ext bundle install <bundle.json>`
installs that selection without granting trust. Native library `fetch` and `bind`
remain direct `ext bundle` commands.

A bundle is a selection document, not a new archive format:

```json
{
  "apiVersion": "ext.bundle/v1",
  "target": "linux/amd64",
  "packages": [
    {
      "manifest": {"...": "the complete sealed ext.package/v1 manifest"},
      "archive": {
        "source": "provider.zip",
        "sha256": "the archive's 64-character hexadecimal SHA-256"
      }
    }
  ]
}
```

The schematic `manifest` above must be replaced by a real sealed manifest.
Package artifacts may contain executables, source, native libraries or data;
the ordinary manifest and artifact verification rules apply. A bundle does not
turn a native library into a plugin or make its installed path a Go build input.
Native citation jobs can now be included in a plan using `native`. Their
installation uses `ext bundle install bundle.json` and keeps native artifacts
under `EXT_HOME`. No consuming Go project or `go.mod` is required.
Direct `bundle fetch` and `bundle bind` remain available.

The C engine's `bundle.plan` validates the document and calls its existing
package resolver. Each package ID appears once, and every declared dependency
must resolve uniquely within the bundle. Missing providers, ambiguity and cycles
fail planning. Exact manifests pin identities, revisions, artifacts, capabilities
and dependencies; archive SHA-256 pins bind their delivered bytes. Plans include
the bundle digest, full selection, dependency order and provider bindings.

Inspection supports any target. Installing packages requires the exact host
`os/arch`; native-only bundles may prepare a different build target.
A document contains at most 256 packages and 64 native jobs, with at least one
selection overall, and is bounded to 4 MiB by the CLI. Archive
reads, extraction and artifact checks retain the existing store limits. Local
archive paths are relative to the document and cannot escape its directory;
HTTPS URLs cannot include credentials, query strings or fragments. HTTPS fetching
uses the existing bounded downloader and its redirect policy. Private registry
authentication is not implemented.

## Publication and recovery

Installation first downloads/copies every archive to a temporary area, verifies
its pin, installs it into a temporary C store, and compares its complete manifest
with the pinned manifest. None of these steps publishes packages to the user's
store. It then resolves the projected installed graph, including existing
packages outside the bundle, to detect dependency conflicts.

Packages are published in dependency order using the existing C archive store.
**This is not a transaction across packages.** An I/O error or interruption after
publication starts can leave a prefix installed. Progress is written atomically
to `EXT_HOME/bundles/<bundle-digest>.json` after each successful package, with
`installing`, `partial` or `complete` state. An error may occur after the store has
published bytes but before the journal was updated; the journal is evidence of
progress, never an authorization record or definitive installed-state inventory.

Rerun the same bundle after correcting the failure. Every archive and installed
package is rechecked; successful store receipts allow identical content to be
reused. A changed or tampered package is not silently treated as completed.
A stale or missing journal does not suppress validation. Installation never adds
trust records, and changed content invalidates existing digest-based approval.

Built-in package mutations, bundle installation, migration and legacy trust
import serialize with `EXT_HOME/.package-write.lock`. A competing writer fails
immediately. CTX adapter mutations and catalog setup/refresh share this lock.
Direct library clients do not yet participate, so run those separately. After a process crash, confirm no package
writer is running before removing the stale lock and retrying. Abandoned temporary stages
are not installed packages. There is no automatic rollback of already published
packages.

Acceptance covers real CLI planning/installation, archive-pin and full-manifest
mismatch rejection before publication, approval separation, invocation after
approval, receipt-based replay, tampering, and journal reconciliation after an
interrupted publisher. Shared C and C-to-Go cases cover dependency conflicts,
cycles, duplicate package IDs and malformed bundle documents.


## Native job planning

A `native` job contains the complete pinned `ext.library.citation/v1` document,
explicit drivers, an artifact-relative output directory, and an optional generated
Go package name. The bundle target selects each driver's asset:

```json
{
  "citation": {"schema": "ext.library.citation/v1", "library": "ext_demo",
               "version": "1.0.0", "assets": ["replace with real pinned assets"]},
  "drivers": ["purego"],
  "dir": "demo",
  "package": "demo"
}
```

The schematic citation must be replaced with a valid citation; it is embedded
rather than read from a mutable external citation file. Native-only documents
use `packages: []`. Planning reports exact asset URLs, roots and SHA-256 pins
for every requested driver. It performs no download or cache access.

The C engine validates the bundle/native-job envelope. The existing native
library citation parser and `fetch.Plan` validate citation semantics and resolve
the target/driver selections. Duplicate libraries, overlapping directories
(including case differences), missing builds and invalid Go package names fail
the CLI plan. Output directories are portable relative paths beneath
`EXT_HOME/artifacts/native/<os>/<arch>`. For example, `dir: "demo"` on
`linux/amd64` publishes to `EXT_HOME/artifacts/native/linux/amd64/demo`.
Different targets have separate installed artifact roots.

Native installation stages all extracted libraries and generated support files
inside that artifact root, using the existing pinned fetcher, before publishing
any native output or package. It does not write to the invoking directory or a
consumer module. Hidden paths, symlinked components and paths escaping the
artifact root are refused. The ordinary Ext home writer lock covers publication
and receipts; no project lock is needed.

Native jobs publish before packages and appear as `native:<library>` in the
journal. Native journals include `artifactRoot`; their filename suffix derives
from that canonical root. Generated Go support files remain managed artifacts,
not automatically imported packages in a consumer's module. Projects that need
local build inputs can explicitly use the separate `ext bundle fetch` / `bind`
workflow. Bundle installation does not edit their `go.mod` or source tree.

Replaying identical native output verifies its complete directory digest and
reuses it. A new version can replace managed output only when its current digest
matches the receipt in `<artifact-root>/.ext-bundle-state`. Unmanaged different content
and user edits are refused. Matching existing output can be adopted by a receipt
without rewriting it.

Before replacement, the receipt records the old and pending digests. Publication
moves the verified old output into a private backup, renames the prepared output
into place, verifies its digest, removes the verified backup and finalizes the
receipt. After an interrupted swap, rerun the original bundle: recovery checks
both locations and completes the pending replacement. It refuses a different
bundle while replacement is pending, a modified backup, or missing previous
output that the pending receipt says should still exist. Receipts and backups
are not trusted as substitutes for hashing their actual contents. This covers
process interruption; it does not promise a transaction across filesystems or
power-loss durability. Native publication is a directory rename on the artifact-root
filesystem, but a whole mixed bundle is not atomic: a later error may leave
native outputs and a prefix of packages published. Retry revalidates all outputs
instead of trusting journal entries. Preparation failures publish neither.

The acceptance gate also runs `TestNativeUpgradeProcessLoss`: a child exits abruptly
at each native upgrade publication boundary, bypassing defers. The parent verifies
that another writer cannot acquire the abandoned lock, confirms the child has
terminated, and deliberately removes that lock before retrying. The retry must
reconcile the pending receipt and backup and produce the expected artifact bytes.
This is subprocess recovery evidence for native publication; it does not authorize
age-based lock stealing or prove interruption handling for every platform.
