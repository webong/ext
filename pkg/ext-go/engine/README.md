# Go library APIs

`github.com/webong/ext/pkg/ext-go/engine` holds guest authoring, plugin contracts and compatibility
ledger record aliases, with no root package. It builds on `core` and never
imports `cengine`. The module requires Go 1.25.

`engine/plugin` retains its public contract names as aliases of `core/plugin`.
Guests, package management, transports and authoring stay here. Production
hosts use `cengine/plugin` or `cgoengine/plugin`; the Go session remains only as
the differential-test oracle.
`engine/ledger` retains compatibility record aliases only; its
shared types alias `core/ledger` and `core/ledger/observation`.

The native ledger binding moved to `cengine/ledger`. CTX uses that binding's
`Bind`, `Library.OpenStore` and `NewGraph`; no C loader remains in this module.

`cengine/plugin/host.OpenRuntime` starts a source plugin under a separately installed runtime
package. The caller supplies the reviewed runtime digest and explicit runtime
and script artifacts; both packages are reverified immediately before launch.
`cgoengine/plugin/host.OpenRuntime` exposes the same API. A runtime tree can include
an interpreter and its dependencies without creating another package store.

Two packages sit at the top level because they belong to no single side: `package` (the package
manifest and dependency model) and `plugintest` (the conformance suite). `package` is a Go
keyword, so the import path is `.../engine/package` and the declared package name stays
`packagekit` (`packagekit.Manifest`, `packagekit.Resolve`). The rest are
four groups:

- `transport`: how frames travel. `transport/jsonline` (newline-delimited JSON over pipes),
  `transport/inprocess` (no IPC) and `transport/bridge` (relaying one transport to another).
- `validate`: how values are checked. `validate/schema` (the contract's bounded schema vocabulary)
  and `validate/jsonschema` (an offline JSON Schema subset).
- `host`: everything that runs plugins on a machine. the installed-package host moved to `cengine/plugin/host`; `host/store` installs, verifies and lists packages under a root you choose;
  `host/process` starts the child process; `host/adapter` manages host-side adapters;
  `host/interop` resolves which backend serves a request; `host/nativego` loads a Go plugin built
  as a shared library.
- `guest`: what a plugin implementation uses. `guest/author` (typed operations and guest
  construction), `guest/capability` (the health and configuration contracts), `guest/instance`
  (configured resources) and `guest/stream` (the pull-stream service). The `Guest` type itself is
  in the root package, beside the host's `Session`.

`transport` and `validate` are leaves; `host` and `guest` build on them and never import each other.
The [`cgoengine`](../cgoengine) module has engine-routed variants of `host` and `host/store` with an
identical API.

Both stores own installation, replacement, removal, and verification for sealed
packages. `CopyOptions.PreserveExecutableArtifacts` is an explicit option for
hosts whose packages include executable helpers outside the declared entrypoint;
ordinary installs keep those files non-executable. CTX seals adapter templates
with adapter-specific metadata, then uses this store for the package lifecycle.

## Installing from a pinned archive

`Store.InstallArchive` installs a package, or upgrades the installed one of the same ID, from a
zip, `.tar.gz` or plain tar named by a local path or an HTTPS URL and pinned by its SHA-256:

```go
installed, err := s.InstallArchive(ctx, store.Archive{
    Source: "https://example.com/releases/reader-1.2.0.tar.gz",
    SHA256: "…", // required
    ID:     "example/reader", // optional: the package the archive must hold
}, store.ArchiveOptions{})
```

The format is read from the content, not the name, and an unrecognized one is `ErrUnsupported`.
Formats that need a dependency (zstd, xz) are not supported, since the portable plugin packages use only core contracts and the standard library. The archive is checked against the digest before
anything is extracted; extraction is bounded by the store's `Limits` (`MaxArchiveBytes`,
`MaxTreeFiles`, `MaxTreeBytes`, `MaxArtifactBytes`) and refuses links, special files, absolute paths
and paths that leave the destination. The manifest may sit at the top of the archive or in one
top-level directory. What was extracted is then installed like a directory, so it is verified
against its manifest.

A successful install writes a `Receipt` (`Store.Receipt`) under `.receipts` in the root: the archive
digest and the package revision it produced. A repeated call with the same digest returns the
installed package without reading or downloading the archive, once that package has verified again.

`Download`, `NewDownloadClient` and `ExtractArchive` are the same steps on their own. The default
client follows at most `MaxDownloadRedirects` redirects, to HTTPS URLs without user information or a
fragment only, and removes `Authorization`, `Cookie` and `Referer` from a redirected request; pass
your own through `ArchiveOptions.Client`. The engine-routed store (`cgoengine/plugin/host/store`) runs the
install in the C engine (`ext_plugin_store_install_archive`: the digest check, the extraction, the install
or upgrade and the receipt), which the other language bindings share; it downloads an HTTPS source
in Go first, since the engine leaves fetching to its caller. `ExtractArchive` and `Download` are Go
in both.

What an archive install and a download must do is fixed as fixtures every language's store runs:
`lib/plugin/testdata/v1/archive-cases.json` with the archives beside it, and `download-cases.json`.
`TestArchiveCases` and `TestDownloadCases` hold both Go stores to them;
`EXT_UPDATE_FIXTURES=1 go test -run TestWriteArchiveFixtures ./host/store` rewrites them.

## The package manifest

A package directory holds `plugin.json`, the `ext.package/v1` manifest, and exactly the artifacts it lists
(`store.ManifestName`). A `package.json` is an ordinary file there: ship one as an artifact, or carry a Node
tree with its own. Manifests named `package.json`, as in releases before v0.4.0, are not read.

## Listing installed packages

`host/store` has two ways to enumerate a root. `List` and `Load` verify every package, hashing each
artifact, and fail closed on the first bad one. `ListUnverified` (also on `store.NewLayered`) reads
only each package's manifest, so its cost does not depend on artifact size, and reports packages it
could not read as `Check` values beside the good ones instead of failing the whole listing:

```go
packages, problems, err := s.ListUnverified()
for _, p := range packages {
    fmt.Println(p.Name(), p.Version(), p.Supports("example.read", "v1"), p.IsExecutable())
}
for _, c := range problems { // every c.Err is non-nil
    log.Printf("%s (%s): %v", c.ID, c.Directory, c.Err)
}
```

The result is **unverified**: use it to show what is installed, never to launch it. Content is checked
only by `Installed.Verify`, `host.Open` and `ArtifactPath`, against the manifest read at listing time,
and `host.Open` refuses a package whose content changed. With a 20,000-file tree package installed a
listing takes about 80 microseconds where `List` takes 1.7 s.

This is the lean choice. Add [`cgoengine`](../cgoengine) for the C engine binding,
[`wasm`](../wasm) for WebAssembly or [`hashicorp`](../hashicorp) for go-plugin, or
require the umbrella module `github.com/webong/ext/pkg/ext-go` to take them all.
A backend never depends on `cgoengine`.
