# Package sources

A named source points to a local artifact index or a public HTTPS index. The
index is the existing `format_version: 1` format: one package `name`, with a
`packages` map keyed by `os/arch`. Each target contains `url` and `sha256`.
It works for command plugins, adapters and non-command providers.

```text
ext source add tools /path/to/index.json
ext source list
ext source inspect tools
ext source refresh tools
ext package install --source tools
ext package trust example/tools
ext source remove tools
```

Add and refresh read and validate the index, then atomically save its canonical
snapshot and SHA-256 in `EXT_HOME/sources.json`. They do not install or trust
anything. Refresh must preserve the package identity. A failed refresh leaves
the previous snapshot intact. Installation selects the current platform from
the cached snapshot, verifies the archive pin and expected package identity,
and uses the existing verified package installer and receipt. Upgrades use
`ext package upgrade --source tools`; changed content requires approval again.
Removing a source leaves installed packages and their trust records untouched.

The index digest records which metadata was selected; it is not a publisher
signature. HTTPS transport authenticates the endpoint. Indexes and downloads
are bounded, and redirects use the existing downloader's credential stripping.
Source URLs cannot embed credentials, query strings or fragments. Private
registry authentication is not implemented. Archive locations from remote
indexes must resolve to the configured HTTPS origin without embedded
credentials, query or fragment. Local indexes use relative archive paths that
stay within their directory. A source is a location for artifacts, never a
runtime provider choice or permission to invoke a plugin.

The C library owns `catalog.validate` and `catalog.select` and their shared
fixtures. The CLI owns its source settings and download workflow. It loads the
engine through the described library interface: an explicitly configured
`EXT_PLUGIN_LIBRARY`, an embedded provided library, or `ext-lib/ext_plugin_host.dll`,
`ext-lib/libext_plugin_host.dylib` or `ext-lib/libext_plugin_host.so` beside the executable.
A development build without that library reports an error; it does not fall back
to new pure-Go host mechanics. CLI release builds now require a same-revision native engine artifact for every
supported target; installers copy it beside the executable. See
[CLI engine distribution](cli-engine-distribution.md).

Writers serialize through `EXT_HOME/.sources.lock`. Following a crash, confirm
no source writer is running before removing a stale lock. The temporary
`.sources-*` file is unpublished state and never loaded as configuration.

## Import a directory catalog

```text
ext source import-catalog /path/to/catalog/adapters
ext source list
ext package install --source catalog-<printed-id>
ext package trust <package-id>
```

Import verifies every catalog package through the C store, creates a bounded ZIP
snapshot, and verifies the pinned archive through the C installer in a temporary
store. It registers ordinary local indexes for the current host target, under
stable source names derived from package IDs. The original catalog is untouched.
Imports apply to any sealed package, not only adapters.

All sources publish together in one configuration update. A collision or failed
verification leaves existing configuration unchanged and removes unpublished
archives. Existing names are never overwritten: explicitly remove the source
before importing a new catalog snapshot. Imported indexes live under
`EXT_HOME/source-archives`; refreshing one rereads that snapshot, not the original
catalog. Removing a source leaves its archive snapshot on disk, along with any
installed package and approvals. No import or refresh grants trust or writes shims.

Top-level `package available|add|refresh` now fail with migration guidance instead
of activating catalog packages implicitly. Product-owned adapter compatibility
commands retain their existing semantics for now. Directory installation and
the adapter build/pack tools remain available for local development.

## HTTPS verification

Source indexes use the system TLS roots. Redirects stay on the configured HTTPS
origin and may not introduce credentials, query strings or fragments. Both
declared-length and streamed responses are limited to 1 MiB. A failed download,
invalid catalog or changed package identity leaves the accepted snapshot intact.

The package acceptance runner exercises these rules against controlled TLS
servers and validates refreshes through the canonical C catalog interface.

## Import a custom manifest

For a local package directory, pass the manifest path relative to that directory.
Artifact paths still resolve from the package root. Ext uses `ext.json` in the
installed copy and leaves the source directory intact.

```sh
ext package install ./my-package --manifest config/peer.json
ext package inspect example/my-package
ext package trust example/my-package
```

Use the same option with `ext package upgrade` for a new revision.

## Publish an adapter release

Build each supported target with the adapter's own toolchain. For the
`ext.adapter/2.0` contract, use `ext package build` for Go or `ext package pack`
on a prepared runtime directory. Other Ext contracts use the same sealed
`ext.package/v1` manifest in `ext.json` and can ship a ZIP or tar.gz. Packages
republished with a renamed manifest need a new revision. The manifest must declare
the target OS and architecture on its platform-specific artifacts. See
[adapter authoring](adapter-authoring.md).

Run the release script with the registry directory, a release version, and the
finished archives for one adapter. `ext package index` verifies the archives
with the native package store and derives the package ID, targets and checksums.
The script publishes versioned artifacts before updating the current index.

```sh
python3 scripts/release-adapter.py registry 1.2.0 ./out/my_tool-*.ctxadapter
```

Serve the registry directory over HTTPS on one origin. For `my_tool`, use
`my_tool/index.json` to track the current release or
`my_tool/releases/1.2.0/index.json` to select a specific release. Keep versioned
directories available so cached source indexes continue to resolve. A namespaced
package ID such as `example/my_tool` is served at `example~my_tool/index.json`.

```sh
ext source add my-tool https://registry.example.org/my_tool/index.json
ext package install --source my-tool
ext package inspect my_tool
ext package trust my_tool
```

To update, run `ext source refresh my-tool` and `ext source inspect my-tool`,
then `ext package upgrade --source my-tool`. Review and approve changed package
contents before invoking the adapter.
