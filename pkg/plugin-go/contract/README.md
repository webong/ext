# Go plugin contract

`github.com/webong/ext/pkg/plugin-go/contract`: the ext plugin contract and the tooling
built on it, using only the standard library (Go 1.23). It holds the contract
data model, host sessions and guests, `ext.package/v1` manifests and the installed
package store's building blocks, JSON-line and bridge transports, process launching,
schemas and typed authoring, instances and streams, and the conformance suite.

Two packages sit at the top level because they belong to no single side: `package` (the package
manifest and dependency model) and `plugintest` (the conformance suite). `package` is a Go
keyword, so the import path is `.../contract/package` and the declared package name stays
`packagekit` (`packagekit.Manifest`, `packagekit.Resolve`). The rest are
four groups:

- `transport`: how frames travel. `transport/jsonline` (newline-delimited JSON over pipes),
  `transport/inprocess` (no IPC) and `transport/bridge` (relaying one transport to another).
- `validate`: how values are checked. `validate/schema` (the contract's bounded schema vocabulary)
  and `validate/jsonschema` (an offline JSON Schema subset).
- `host`: everything that runs plugins on a machine. `host` opens an installed package as a running
  plugin; `host/store` installs, verifies and lists packages under a root you choose;
  `host/process` starts the child process; `host/adapter` manages host-side adapters;
  `host/interop` resolves which backend serves a request; `host/nativego` loads a Go plugin built
  as a shared library.
- `guest`: what a plugin implementation uses. `guest/author` (typed operations and guest
  construction), `guest/capability` (the health and configuration contracts), `guest/instance`
  (configured resources) and `guest/stream` (the pull-stream service). The `Guest` type itself is
  in the root package, beside the host's `Session`.

`transport` and `validate` are leaves; `host` and `guest` build on them and never import each other.
The [`cengine`](../cengine) module has engine-routed variants of `host` and `host/store` with an
identical API.

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

This is the lean choice. Add [`cengine`](../cengine) for the C engine binding,
[`wasm`](../wasm) for WebAssembly or [`hashicorp`](../hashicorp) for go-plugin, or
require the umbrella module `github.com/webong/ext/pkg/plugin-go` to take them all.
A backend never depends on `cengine`.
