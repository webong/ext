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
