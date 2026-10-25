# Module versioning

Every module in this repository is published independently and versions on its
own `MAJOR.MINOR.PATCH`. Go's semantic import versioning means a module path is
only retrievable at a `vN` prefix matching its major version, so the path
`github.com/webong/ext/pkg/plugin` is served from tags named
`pkg/plugin/v1.2.3`.

## Shared major version

All modules share one `MAJOR` version. The repository is a single product with a
single compatibility promise, and adapters and other
consumers depend on several modules at once.

Bumping the major version in any module bumps it everywhere. `v1` to `v2`
means every module path changes, every `require` line changes, and every
consumer must update together. That is deliberate: a breaking change to the
contract cannot leave one CTX module at `v2` while another stays at `v1`.

A major bump is a repository-wide, coordinated release.

## Independent minor and patch versions

`MINOR` and `PATCH` move independently per module. Only the modules that
actually changed get a new version, and a consumer resolves each module at
whatever version it needs.

Bump `PATCH` for a fix that keeps the existing API. Bump `MINOR` when a module
grows in a backward-compatible way, such as a new backend or a new exported
function. Bump `MAJOR` for a breaking API change, which bumps every module.

## Tagging

Tag each module separately at its own version. Never tag a module you did not
change.

```bash
# fix in one module only
git tag res/system/v1.0.1

# additive change in one module
git tag pkg/plugin-go/v1.1.0

# coordinated breaking change across every module
git tag res/web/v2.0.0 res/system/v2.0.0 pkg/plugin-go/v2.0.0 \
        pkg/plugin-hashicorp/v2.0.0 pkg/plugin-wasm/v2.0.0
```

For a major bump, update the `/vN` path suffix in each affected `go.mod`, in
every `require` across the repository, and in documentation, then tag. Bumping
one path and not the others breaks resolution for every consumer.

## Local development

The root `go.work` lists every module, so in-repository work resolves local
edits without tagging anything. Library modules carry no `replace` directives.

That is deliberate. Go ignores `replace` in any module other than the main one,
so a `replace` in a published library does nothing for consumers: they resolve
the dependency from the proxy at the required version. Keeping them out avoids
the false impression that a published module can redirect consumers to a local
path, and it means each `go.mod` describes exactly what a consumer will get.

The CLI modules `src/ctx` (module path `github.com/webong/ext/ctx`) and `src/ctn` keep
`replace` directives, but only for the sibling modules they require, because they are the modules
developers build and run. Those are what let `go build ./...` use local module code. Adapters are
ext packages that `ctx` loads, not Go imports, so neither module lists or replaces them. The Go
examples live in the library they show (for example `pkg/ext-go/examples`) and need no module
of their own. The repository root is not a module: it
holds `go.work` only, so it has no `go.mod` and Go resolves no module there.

Binary releases are tagged per product, `ctx-vX.Y.Z` and `ctn-vX.Y.Z`, and are
not Go module versions. Pushing one runs the release workflow for that product
only: `ctx-v*` builds and publishes the ctx native bundles with
`scripts/build-release.sh`, and `ctn-v*` builds the ctn bundles with
`scripts/build-release-ctn.sh`. The names use a dash, not a slash, because a
slash would read as a Go module tag for a directory that does not exist and
makes download URLs ambiguous. GitHub's "latest" release is repository-wide, so
`install.sh` and `install.ps1` resolve the newest `ctx-v*` release instead of
`latest/download`, and ctn releases are published with `--latest=false`.
The mobile SDKs follow the same dash form: `ext-android-vX.Y.Z` releases the Android AARs
(`.github/workflows/ext-android-release.yml`, published with `--latest=false`) and `ext-ios-vX.Y.Z` releases the iOS
SDK (`.github/workflows/ext-ios-release.yml`, same form). Their `MINOR` and `PATCH` move
independently; their `MAJOR` follows the repository-wide `MAJOR`.
Library modules never use these tags, and bare `vX.Y.Z` tags are not used.

`pkg/ext-ios` and `pkg/ext-android` were `pkg/plugin-ios` and `pkg/plugin-android` until they
also carried the web code (cookie, userscript and bookmarklet rules) beside the plugin code. This
was a hard cut: the tag forms `plugin-ios-vX.Y.Z` and `plugin-android-vX.Y.Z` are retired and the
release workflow no longer watches them (the one release made under the old form,
`plugin-android-v0.1.0`, stays as it was published), and the AARs are now
`ext-android-X.Y.Z.aar` and `ext-android-wamr-X.Y.Z.aar` (were `ext-plugin-…`). The scripts
`scripts/plugin-ios-*.sh` and `scripts/plugin-android*.sh` are `scripts/ext-ios-*.sh` and
`scripts/ext-android*.sh`; the SwiftPM package is `Ext` (scheme `Ext-Package`) and its plugin
products keep their `ExtPlugin…` names.

`pkg/plugin-rust`, `pkg/plugin-zig`, `pkg/plugin-ts` and `pkg/plugin-python` became `pkg/ext-rust`,
`pkg/ext-zig`, `pkg/ext-ts` and `pkg/ext-python` (a hard cut, like the mobile SDKs). The names of the packages
inside did not change: the crates `ext-plugin` and `ext-plugin-engine`, the npm package `@ctx/plugin`, the
Python package `ext-plugin` and the Zig package `ext_plugin`. None of the four is a Go module, so no tag moved.
`pkg/plugin-go` keeps its name for now, because renaming it changes published Go module paths.

`res/web/ios` (the SwiftPM package `ExtWeb`) and `res/web/android` (the Java package
`io.github.webong.ext.web`) live inside the Go module `res/web` but are not part of its version: the Go
module zip carries their files, which Go never reads, and a change to them needs no `res/web/vX.Y.Z` tag.
They reach apps through `pkg/ext-ios` and `pkg/ext-android`, so they are released with those, under
`ext-ios-vX.Y.Z` and `ext-android-vX.Y.Z`.

The iOS SDK's engine and WAMR archives are released under that same tag; see
[`pkg/ext-ios`](../pkg/ext-ios/README.md) and `scripts/ext-ios-release.sh`. It is
not a Go module version either.

Because of this, `GOWORK=off go build ./...` inside a library module fails until
that dependency is published at the required version. That failure is the
correct signal: it means the module is not yet independently consumable.

## The v0.1.0 baseline

Every module is tagged `v0.1.0`. The original modules were tagged on the commit that introduced the split; `pkg/plugin-go`, `pkg/plugin-cshared`, `pkg/plugin-hashicorp` and `pkg/plugin-wasm` were tagged later, after they were renamed out of `pkg/go` and `pkg/plugin/*`. That
release was verified by resolving the tagged modules into a scratch module
outside this repository: a consumer requiring only `pkg/plugin-go` and
`res/browser` resolved `pkg/plugin` transitively and pulled in no HashiCorp or
WASI runtime, confirming the dependency isolation holds for real consumers.

`pkg/graph/supervisor` was a module of its own at the original split and is now
a package of the `pkg/graph` module. The unpublished `pkg/supervisor/v0.1.0` tag
was deleted and no `pkg/supervisor` tag is cut again.

Because nothing was published, the `pkg/graph`, `pkg/plugin` and `res/browser`
`v0.1.0` tags were moved from the original split commit to the tree that also
carries the `pkg/plugin-*` tags, so every library's `v0.1.0` resolves to one
coherent tree that includes the supervisor. Once any tag is pushed, tags are immutable:
never move one again, and release changes under a new version.

The repository is now `github.com/webong/ext`, and every module path moved with
it. Tags are named for the module's directory, so their names did not change,
but the unpublished `v0.1.0` tags were moved again to the commit with the new
paths so each one resolves to a `go.mod` that matches its own module path.

The `v0.1.0` tags were pushed to `origin` and are immutable.

## `res/browser` became `res/web`

The browser resources module was renamed `res/web`, because web is the broader
concept: browsers today and the web engine that runs web content through them.
A module path and its tags are a pair, so this is a new module, not a new version:

- `res/browser/v0.1.0` was already pushed. It is immutable, stays resolvable for
  anything that still requires `github.com/webong/ext/res/browser`, and is the last
  tag that path will ever have. Never move or delete it, and cut no further
  `res/browser` tags.
- `res/web/v0.1.0` is the first tag of the new path, cut on a tree whose `go.mod`
  declares `github.com/webong/ext/res/web`.
- A module that requires `res/web` cannot resolve it from the proxy until that tag is
  pushed. The published `v0.1.0` tags of the adapters still require `res/browser`
  and remain valid; their next release requires `res/web`.

## `pkg/plugin-go` became `pkg/ext-go`

The Go plugin library is `pkg/ext-go`, to match the other language packages (`pkg/ext-rust`, `ext-zig`,
`ext-ts`, `ext-python`, `ext-ios`, `ext-android`). A module in a subdirectory carries the directory in its
path, so all seven modules changed path, as `cengine` and `contract` did before them: `pkg/ext-go` (the
umbrella), `pkg/ext-go/engine`, `cgoengine`, `c2goengine` (and its `tools` module), `hashicorp` and `wasm`.
Each is a new module, not a new version:

- The tags under `pkg/plugin-go/...` are immutable and stay resolvable for anything that requires the old
  paths; they are the last versions of those paths. Nothing is tagged under the new paths until a release
  round cuts them, on a tree whose `go.mod` files declare them.
- `v0.8.0` is the last version of the old ways (`pkg/plugin-go/contract`, `cengine` and the umbrella). The
  v0.9.0 round that was prepared under the old layout is not cut; the next round is considered only after
  this rename is on `main`, and the versions of the new paths are chosen then.
- Every import path, `require`, `replace`, `go.work` entry, script, workflow and document that named
  `pkg/plugin-go` now names `pkg/ext-go`. The history in the sections below keeps the old names, because
  those are the paths the tags carry.
- The `go.sum` records of the renamed modules still hold the hashes of the old paths. A module zip's hash
  covers its path, so they are wrong for the new ones; `scripts/release-pins.sh` recomputes them for the
  round's versions file and `scripts/release-rehearse.sh` fails on any that is not right, so none can be
  tagged by mistake.

## `pkg/plugin-go/cengine` became `pkg/plugin-go/cgoengine`

The Go binding of the C engine was renamed because it is now one of three engines (the pure-Go
`contract`, now `engine` (next section), the C engine through cgo, and the C engine translated to Go by ccgo, `c2goengine`; see
[the engine selection ADR](adr-engine-selection.md)) and `cengine` did not say which. A module path
and its tags are a pair, so this is a new module, not a new version:

- The tags of `pkg/plugin-go/cengine` (`v0.2.0` to `v0.8.0`) are immutable, stay resolvable for
  anything that requires `github.com/webong/ext/pkg/plugin-go/cengine`, and `v0.8.0` is the last tag
  that path will ever have. Never move or delete them, and cut no further `cengine` tags. There is no
  forwarding module and no alias package. The v0.8.0 round was cut on the tree whose module was still
  `cengine`; the rename landed after it.
- `pkg/plugin-go/cgoengine/v0.1.0` is the first tag of the new path, cut on a tree whose `go.mod`
  declares `github.com/webong/ext/pkg/plugin-go/cgoengine`.
- A module that requires `cgoengine` cannot resolve it from the proxy until that tag is pushed. The
  `go.mod` files in the tree already require `cgoengine v0.1.0` (the umbrella and the 24 adapters, with
  no `go.sum` lines for it yet), so they build in the workspace and are skipped by the standalone check
  until the tag exists. The published releases of those modules still require `cengine` and remain
  valid; their next release requires `cgoengine`.
- The Go package was renamed with the directory, from `goengine` to `cgoengine`, so importers no longer
  need an alias. The build tags `ext_cengine` and `ext_cengine_shared`, the `EXT_CENGINE_*` variables
  and the `scripts/plugin-cengine*.sh` scripts name the C engine, not the module, and did not change.

The history below keeps the names of its time: a release row that says `cengine` means the module now
called `cgoengine`.

## `pkg/plugin-go/contract` became `pkg/plugin-go/engine`

The pure-Go module is the oracle the other two engines are held against, and the third engine beside
`cgoengine` and `c2goengine`, so it is called `engine`. The package that chooses between the three is
`core` (`pkg/plugin-go/core`, in the umbrella). Again a new module, not a new version:

- The tags of `pkg/plugin-go/contract` (`v0.2.0` to `v0.8.0`) are immutable and `v0.8.0` is the last tag
  that path will ever have. They are not deleted, and the path is not reused for anything else. Deleting
  a tag from GitHub would not remove the version: `proxy.golang.org` keeps what it has served and the
  checksum database `sum.golang.org` keeps its hash for good, so `go get` of the old path would still
  resolve it. That is why the selection package is `core` and not `contract`; it is the repository's rule
  that a published path is not reused by a different module (Go itself does not forbid it). A final
  `// Deprecated: use github.com/webong/ext/pkg/plugin-go/engine.` commit on the last tag, as for
  `pkg/plugin-cshared`, retires the path.
- `pkg/plugin-go/engine/v0.1.0` is the first tag of the new path, cut on a tree whose `go.mod` declares
  `github.com/webong/ext/pkg/plugin-go/engine`. Until it exists every module that requires it (the
  umbrella, `cgoengine`, `c2goengine`, `wasm`, `hashicorp`, the adapters and the products) is skipped by
  the standalone check, and the products resolve it in the workspace through their `replace`.
- The Go package was renamed with the directory (`contract` to `engine`). Importers named it `plugin`
  and are unchanged apart from the path. The contract itself, the data model (`ContractRef`, the
  `contract` field of the wire format, the `ext.*` contracts), keeps its name: only the module moved.
- The subpackages moved with it: `engine/host`, `engine/guest`, `engine/transport`, `engine/validate`,
  `engine/endpoint`, `engine/package` and `engine/plugintest` (with `enginetest`). Everything below this
  point in the document that says `contract` for the module means the module now called `engine`.

## Releases since the baseline

| Release | Modules | Why |
| --- | --- | --- |
| `v0.2.0` | `pkg/plugin`, `pkg/plugin-wasm` | additive: the adapter package, the reactor host, components and `Inspect` |
| `v0.1.0` (first tag) | `res/web`, `adapters/wasm`, `adapters/evm` | new modules; `res/web` was the renamed `res/browser` |
| `v0.1.1` | `res/web` | console output from a page is delivered in order, and an exit no longer overtakes it |
| `v0.1.1` | `pkg/plugin-hashicorp` | only its `go` line: it now declares `go 1.26.0`, which its dependencies already required |
| `v0.1.1` | `adapters/chromium`, `adapters/firefox`, `adapters/safari` and the other 15 browser adapters | they now require `res/web` instead of `res/browser` |

Modules that did not change keep `v0.1.0`: `pkg/graph`, `pkg/plugin-go`, `pkg/plugin-cshared`,
`res/credential`, and the `credman`, `git`, `keychain` and
`secret_service` adapters. Every tagged module requires the new versions of the modules it
uses. The first `res/web/v0.1.0` tag was local only, so it was re-cut at the release commit.

It was verified the way consumers meet it: from a scratch module outside the repository, with
no workspace and no `replace`, the new tags resolved from GitHub, the libraries imported, the
`wasm`, `evm` and `firefox` adapter programs built, and the old `res/browser` path appeared
nowhere in the resolved graph.

## Verifying a release

Before tagging, confirm each module builds and tests on its own with the
workspace active, and that `src/ctx` resolves the intended versions:

```bash
for module in src/ctx src/ctn pkg/ext-go pkg/ext-go/engine pkg/ext-go/cgoengine \
              pkg/ext-go/hashicorp pkg/ext-go/wasm res/system adapters/wasm adapters/evm \
              res/web res/credential; do
  (cd "$module" && go build ./... && go test ./...) || exit 1
done
```

## The Go version floor

Each module's `go` line is the oldest Go that builds it, so it must be at least what its own
dependencies require; `go mod tidy` raises it when it is not. The light libraries
(`pkg/ext-go/engine`, `pkg/ext-go/cgoengine`, `pkg/ext-go/wasm`, `res/*` and the adapters) declare `go 1.23.0` and their dependencies allow it. Four modules declare
`go 1.26.0` because `golang.org/x/net`, `x/sys` and `x/text` at the versions they use require it:
`pkg/ext-go/hashicorp` (gRPC), the umbrella `pkg/ext-go` (it requires `hashicorp`), `src/ctx`
and `src/ctn`. A fifth, `pkg/ext-go/c2goengine`, declares `go 1.26.0` because the runtime library the
translated code links against (`modernc.org/libc`) requires it. The workspace file
`go.work` declares the highest of them, and CI runs Go 1.26.x.

`pkg/plugin-hashicorp/v0.1.0` was published declaring `go 1.23.0` while requiring those
dependencies, so it is mislabeled: a consumer on an older Go gets an error naming 1.26. Published
tags are immutable, so the correct floor ships in `pkg/plugin-hashicorp/v0.1.1`. Resolved from GitHub
in a clean module, v0.1.0 reports `go 1.23.0` and v0.1.1 reports `go 1.26.0`. Consumers on an
older Go should require v0.1.1 or newer, and the v0.1.0 tag stays as published.

## The plugin library is a tree of modules

`pkg/ext-go/` is a directory tree in which each heavy part is its own module,
so an application imports only what it needs and stacks the rest:

| Module | Contents | Requires |
| --- | --- | --- |
| `pkg/ext-go/engine` | the pure-Go engine (the oracle): the contract, sessions, guests, packages, the package store and host, bridges and tooling (the old `pkg/plugin`); standard library only | nothing |
| `pkg/ext-go/cgoengine` | the Go binding of the C engine, a pure-Go build of the same API, engine-routed variants of `store` and `host` with the same API as the ones in `engine`, the C ABI loader (the old `pkg/plugin-cshared`) and a generated copy of the C sources | `contract` |
| `pkg/ext-go/c2goengine` | the C engine's portable core translated to Go by ccgo (`modernc.org/ccgo`), with no cgo and no C compiler: one generated file per platform, made on that platform (see its README) | `engine` |
| `pkg/ext-go/wasm` | the wazero backend (the old `pkg/plugin-wasm`) | `engine` |
| `pkg/ext-go/hashicorp` | the go-plugin (gRPC) backend (the old `pkg/plugin-hashicorp`) | `engine` |
| `pkg/ext-go` | the umbrella: requires every module above at matching versions and holds what needs several of them (`PlanGraph`, the `core` package that chooses between `engine`, `cgoengine` and `c2goengine`, the bridge command, the cross-language tests) | all of the above, `res/system` |

The module was called `contract` until v0.8.0 because it is the contract plus tooling that needs
nothing beyond the standard library, not a runtime; it became `engine` once it was the oracle
among three engines (see above), and `core` is the package that chooses between them. `wasm` and `hashicorp`
never depend on `cgoengine`, in code, tests or `go.mod`, so a wazero or gRPC user needs
no C toolchain; `src/ctx/internal/arch` enforces this.

### The translated engine

`c2goengine` is the same C as `cgoengine`, run as Go. Its translation of `lib/plugin`'s portable core is
generated, one file per platform (`internal/ccgo/generated_<goos>_<goarch>.go`), by the tools module
in its `tools` directory, which pins ccgo and runs only on the platform it writes for. A published
module contains only its own directory, and the generated files are that directory's copy of the C,
so no C sources travel with it. The `c2goengine` workflow regenerates the file for each platform and
fails when it differs from the one checked in. Tags follow the usual rule: `pkg/ext-go/c2goengine/vX.Y.Z`,
with an `engine` release that holds the `plugintest/enginetest` checks it runs below it (`engine` v0.1.0; `contract` v0.8.0 has the service checks only, and the instance, session and process checks came after).

### The C sources

A published module contains only its own directory, so `cgoengine` carries the sources
it compiles as `_csrc`, a copy of `lib/plugin` generated by `scripts/cgoengine-sync.sh`.
`TestCSourcesMatchCanonical` fails when the two differ. `lib/plugin` stays canonical
for the iOS, Android, Rust and Zig bindings. The cgo preamble includes the headers
from `_csrc` and links the engine library, which is built from `_csrc` with CMake
(the default build tag set uses no C at all).

### Migration

| Old import path (module) | New import path (module) |
| --- | --- |
| `pkg/plugin` and its subpackages, including `store` and `host` (`pkg/plugin`, up to v0.6.0) | `pkg/plugin-go/contract` and `.../contract/<subpackage>` (`pkg/plugin-go/contract`); the packages that run plugins on a machine are grouped under `contract/host` (`store` becomes `contract/host/store`, `process` becomes `contract/host/process`, `adapter` becomes `contract/host/adapter`, `interop` becomes `contract/host/interop`, `nativego` becomes `contract/host/nativego`; what a plugin implementation uses is grouped under `contract/guest`: `author` becomes `contract/guest/author`, `capability` becomes `contract/guest/capability`, `instance` becomes `contract/guest/instance`, `stream` becomes `contract/guest/stream`; the transports and validators are grouped too: `jsonline` becomes `contract/transport/jsonline`, `inprocess` becomes `contract/transport/inprocess`, `bridge` becomes `contract/transport/bridge`, `schema` becomes `contract/validate/schema`, `jsonschema` becomes `contract/validate/jsonschema`); an application that wants the engine-routed behavior swaps `contract/host` and `contract/host/store` for `cengine/host` and `cengine/host/store`, which have an identical API |
| `pkg/plugin-go` root, the engine API (`pkg/plugin-go`, up to v0.1.1) | `pkg/plugin-go/cengine` (`pkg/plugin-go/cengine`) |
| `pkg/plugin-go/cshared`, `.../cshared/abi`, `.../cshared/guest` | `pkg/plugin-go/cengine/cshared`, `.../abi`, `.../guest` (`pkg/plugin-go/cengine`) |
| `pkg/plugin-cshared` (up to v0.1.0) | `pkg/plugin-go/cengine/cshared` (`pkg/plugin-go/cengine`) |
| `pkg/plugin-wasm` (up to v0.2.1) | `pkg/plugin-go/wasm` (`pkg/plugin-go/wasm`) |
| `pkg/plugin-hashicorp` (up to v0.1.1) | `pkg/plugin-go/hashicorp` (`pkg/plugin-go/hashicorp`) |
| `pkg/plugin-hashicorp/cmd/ext-plugin-bridge` | `pkg/plugin-go/cmd/ext-plugin-bridge` (`pkg/plugin-go`) |
| `pkg/graph` (v0.1.0) | the generic store is `res/system/graph`, in module `res/system` (see below) |
| `res/graph` (v0.1.0, published for a short time) | `res/system` (module `res/system`): the discovery package that lived in `res/graph/system` (declared name `systemgraph`) is now the module root, package `system`; the store, `res/graph`'s own package, is `res/system/graph`; `res/graph/supervisor` is `res/system/supervisor` |
| `packagekit.Plan.Graph(namespace)` | `plugingo.PlanGraph(plan, namespace)` in `pkg/plugin-go`; `packagekit.IdentityKey` is now exported. The conversion lives in the umbrella so `contract` has no `res/system` dependency; the transaction it returns is `res/system/graph`'s. No caller in this repository other than the method's own test used it, and the consumer repository calls neither it nor `res/graph` |

Subpackages of the old `pkg/plugin-go` and `pkg/plugin` that move under `contract`:
`package` (the old `packagekit`: its import path is `contract/package`, and because `package` is a Go
keyword the declared package name stays `packagekit`) and `plugintest` at the top, with the transports grouped under `contract/transport`
(`transport/jsonline`, `transport/inprocess`, `transport/bridge`), the validators under
`contract/validate` (`validate/schema`, `validate/jsonschema`), the host-side ones grouped under `contract/host` (`host` itself and `host/adapter`, `host/interop`,
`host/nativego`, `host/process`, `host/store`) and the guest-side ones under `contract/guest`
(`guest/author`, `guest/capability`, `guest/instance`, `guest/stream`). `cgoengine`
holds `packages` (engine-only) and engine-routed variants of `host` and `host/store` whose exported API is
identical to `contract`'s; `src/ctx/internal/arch` enforces that, and
`TestContractStoreMatchesEngineStore` compares their behavior.

Import paths that existed in published `pkg/plugin-go` v0.1.x are `pkg/plugin-go`
(the root package) and `pkg/plugin-go/cshared/guest`. The new umbrella reuses the
root path `pkg/plugin-go` for a different package (`plugingo`), which is why every new
version starts at v0.2.0, above v0.1.1. No other path is shared between modules.

### Versions and tags

The release went out in two rounds because of one mistake, recorded here so it is not repeated, and
later rounds followed for new content.

| Round | Tags |
| --- | --- |
| First (`v0.2.0`) | `res/graph` v0.1.0, `pkg/plugin-go/contract`, `.../cengine`, `.../wasm`, `.../hashicorp` and `pkg/plugin-go` at v0.2.0, and the adapters as patch bumps (`adapters/chromium` v0.3.1, `adapters/firefox` v0.2.1, and so on) |
| Corrective (`v0.2.1`) | `res/system` v0.1.0 (the module was renamed from `res/graph` before anyone used it); `pkg/plugin-go/contract`, `.../cengine`, `.../wasm`, `.../hashicorp` and `pkg/plugin-go` at v0.2.1; and the adapters again (`adapters/chromium` v0.3.2, `adapters/firefox` v0.2.2, `adapters/safari` v0.1.4, the rest as listed by `git tag`) |
| Bounds and listing (`v0.3.0`) | `pkg/plugin-go/contract`, `pkg/plugin-go/cengine` and `pkg/plugin-go` at v0.3.0, and nothing else |
| Manifest names (`v0.4.0`) | `pkg/plugin-go/contract`, `pkg/plugin-go/cengine` and `pkg/plugin-go` at v0.4.0, and the 24 adapter modules at their next minor version: `adapters/chromium` v0.4.0; `adapters/firefox`, `adapters/chrome` and `adapters/edge` v0.3.0; and `adapters/safari`, `adapters/credman`, `adapters/keychain`, `adapters/secret_service`, `adapters/git`, `adapters/evm`, `adapters/wasm` and the Chromium- and Firefox-family adapters v0.2.0 |
| Reverse calls, progress and endpoints (`v0.5.0`) | `pkg/plugin-go/contract`, `pkg/plugin-go/cengine` and `pkg/plugin-go` at v0.5.0; `res/system` v0.1.1 and `res/web` v0.5.1 as patch releases; no adapter, `wasm` or `hashicorp` |
| Dependency security (`v0.5.1`) | `pkg/plugin-go/hashicorp` v0.3.0 and `pkg/plugin-go` v0.5.1; no other module |
| API additions (`v0.6.0`) | `pkg/plugin-go/contract`, `pkg/plugin-go/cengine` and `pkg/plugin-go` at v0.6.0; no other module |
| Engine parity (`v0.7.0`) | `pkg/plugin-go/contract`, `pkg/plugin-go/cengine` and `pkg/plugin-go` at v0.7.0; no other module |
| Engine test harness (`v0.8.0`) | `pkg/plugin-go/contract`, `pkg/plugin-go/cengine` and `pkg/plugin-go` at v0.8.0; no other module |
| Three engines (`v0.9.0`) | `pkg/plugin-go` at v0.9.0; `pkg/plugin-go/engine`, `pkg/plugin-go/cgoengine` and `pkg/plugin-go/c2goengine` at v0.1.0 (new modules); `hashicorp` v0.3.1, `wasm` v0.2.2 and the 24 adapters as patch releases |

`v0.3.0` is a minor version because `Store.ListUnverified` and `Layered.ListUnverified` are new API in
both `contract/host/store` and `cengine/host/store`. It also bounds the cost of validating and resolving
untrusted manifests in the C engine and in `contract/package`: a 4 MB manifest that took minutes (4096
artifacts) now takes a tenth of a second, and a directory digest stops at 250,000 files or 64 MiB of path
text. Only the modules whose own files changed are tagged: `contract` and `cengine` (which also carries the
engine's C sources in `_csrc`), plus the umbrella, whose `go.mod` now requires both at v0.3.0 so a consumer
of the umbrella takes the fixed engine and the new API. `wasm`, `hashicorp`, `res/system` and the adapters
have the same bytes as before and are not retagged. They still require `contract` v0.2.1, which is
fine: a build that also requires v0.3.0 (the umbrella does) resolves to it. A consumer that wants the
bounds in an adapter's binary requires `contract` v0.3.0 itself. A manifest whose tree overlaps another
artifact is rejected as before, but the error may name a different one of the overlapping artifacts.

`v0.4.0` renames the package manifest file from `package.json` to `plugin.json`
(`store.ManifestName`, in `contract/host/store` and `cengine/host/store`). It is a **behavioural break at the
same Go API**, which is why it is a new minor version of the three modules (a 0.x break) rather than a patch;
the exported names and signatures do not change. A package directory is read only through `plugin.json`:
a directory whose manifest is still named `package.json` is not read (it is reported as not found, with no
fallback and no dual support), so installed packages and package sources have to be regenerated or have their
manifest renamed. A `package.json` is now an ordinary file: a package can ship one as a declared artifact beside
`plugin.json`, a tree artifact can hold its own, and an unlisted top-level one is rejected like any unlisted file.
`plugin.json` is reserved as an artifact path in any case. A `plugin.json` is told apart from other files of that
name by its `apiVersion` (`ext.package/v1`). The reason is that npm and Node tooling own the name `package.json`,
and a package that carries a Node tree could not coexist with an ext manifest in the same directory.
`pkg/ext-python` is consumed from the repository, not published: its examples and docs now say `plugin.json`
(`serve_stdio` accepts a manifest, a bare descriptor file or a dict, by structure, so only the example strings
changed). `v0.3.0` stays published and still reads `package.json`; it has `ListUnverified` and the validation
bounds, and anything that adopted it only has to move to `v0.4.0` to rename its manifests. No adapter or other
module read the manifest by file name, so none of them changes or is retagged.

`v0.5.0` adds three features to the Go plugin contract, described in
[the decision record](adr-plugin-reverse-progress-endpoints.md): calls from a guest back into its host
(`Manifest.WithReverse`), bounded progress and log frames streamed from a call, and a standard declared
loopback data endpoint (`contract/endpoint`), plus `Session.Done`. It is a new minor version of
`contract`, `cengine` and the umbrella because the exported API grows and the descriptor gains optional
`reverse` and `progress` declarations: **a plugin that declares either needs a v0.5 host**, while a plugin
that declares neither runs on any host, and the wire version stays `ext.plugin/v1`. The C engine does not
implement them and **fails closed**: its descriptor parser rejects a package that declares them, and the
C build of `cengine` refuses a host that enables reverse calls, instead of ignoring either
([the parity note](plugin-engine-parity.md)). `res/system` v0.1.1 carries the supervisor
fix that makes `Instance` never report a state the graph has not recorded, and the library examples
moved into it; `res/web` v0.5.1 carries the examples and the `wasm/reactor.js` asset that moved into it.
Both are patch releases, since neither changes an exported name. `wasm`, `hashicorp` and the 24 adapters
have the same bytes as before and are not retagged: they still require `contract` v0.4.0, which is fine,
because a build that also requires v0.5.0 (the umbrella and the products do) resolves to it.

The `v0.8.0` round publishes what the engine tests were moved into. `contract` gains `plugintest/enginetest`, the
engine-agnostic differential checks that the C-backed `cengine` runs under the same names as the pure-Go core, and
`instance.Manager` now reports a disposal before `Close` returns, where it used to tell `Close` it was done first, so
a host could see `Close` return with a disposal unreported (a behavior change, hence a minor version). `cengine`
changes with its tests and the engine sources it carries, and the umbrella follows because it pins both. The round
exists because the `cengine` tests import `enginetest`, which `contract` v0.7.0 does not contain, so a standalone
`go mod tidy` of `cengine` failed on `main` from the commit that moved the checks until this release. `wasm`,
`hashicorp` (one test fixture), `res/*` and the adapters have the same bytes as before and are not retagged.

The `v0.9.0` round publishes the three engines under their final names. `engine` is the renamed `contract` and `cgoengine`
the renamed `cengine`, so both are new modules at v0.1.0 (`contract/v0.8.0` and `cengine/v0.8.0` stay the last tags of the
old paths, and a `// Deprecated:` commit on each retires them); `c2goengine` is new at v0.1.0. `engine` also carries the
instance, session, open, drain and process checks of `plugintest/enginetest` with the `Session`, `Instances`, `Lease` and
`Launch` types and `MaybeServeGuest` that an engine passes to run them; the v0.8.0 harness had the service checks only,
which is why the engines could not build against it. The umbrella gains the `core` package, which chooses between the three
engines, and pins all of them. `hashicorp`, `wasm` and the 24 adapters changed because their imports and requirements moved
to `engine` and `cgoengine` (behavior is the same, hence patch releases), and their go.sum records for the new modules have
to exist before the tags do. `res/*` are not retagged; `src/ctx`, `src/ctn` and `cmd/ext` are products built with the
workspace and resolve `engine` through their `replace`.

The `v0.7.0` round brings the C engine to the behavior of the pure-Go baseline: reverse calls, progress frames,
endpoint validation and extension backends now run on both (see [engine parity](plugin-engine-parity.md)). `contract`
gains the `plugintest` frames conformance suite, and a call refused while the host drains is now reported to observers
as `draining` rather than `failed`, a behavior change that takes a minor version. `cengine` carries the engine
sources it is built from (`_csrc`), so its bytes change with every engine fix. The C ABI is additive and
`EXT_HOST_ABI_VERSION` stays 2. The umbrella follows because it pins both. `hashicorp` differs from its `v0.3.0` tag
only in a test fixture, and `wasm`, `res/*` and the adapters have the same bytes as before, so none is retagged. The
Zig guest SDK gains capability contracts in the same round; it is not a Go module and has no tag here.

The `v0.6.0` round adds API found by a consumer using reverse calls, progress and endpoints in real code, and fixes
one defect. `contract` gains `endpoint.Decode`, `Endpoint.Check` and `endpoint.Key`; `Grant.AuthorizationHeader` and
`Grant.DialTCP`; `SplitProgressText` and `ProgressLimitsFrom`; and a handler that returns a `RemoteError` the peer would
reject no longer ends the peer's connection (the error becomes `operation_failed`). `contract/host` and `cengine/host`
gain `Options.Env` and `Process.Done`, with an identical exported API. All of it is additive, so the modules take a
minor version, and the umbrella follows because it pins them. `hashicorp` differs from its `v0.3.0` tag only in a test
fixture and keeps that tag. The C engine still refuses reverse calls and progress; see
[engine parity](plugin-engine-parity.md).

The `v0.5.1` round is a dependency-security patch, plus one small API move. `hashicorp` v0.3.0 and
the umbrella v0.5.1 move `google.golang.org/grpc` from v1.83.1 to v1.83.2 and `golang.org/x/net` from v0.59.0 to
v0.60.0. `govulncheck` on `main` reported GO-2026-6443 (grpc) and GO-2026-6603, GO-2026-6611, GO-2026-6612 and
GO-2026-6617 (x/net) for the umbrella; the same scan of the prepared tree reports none of them for either module.
The standard-library findings it still lists depend on the Go toolchain, not on `go.mod`. `hashicorp` pinned the
older versions as well, so the umbrella alone could not fix it, which is why both modules are tagged. A pin is only a
minimum: a consumer of `hashicorp` v0.2.1 or the umbrella v0.5.0 takes the vulnerable versions unless it requires the
patched ones itself, and `go get google.golang.org/grpc@v1.83.2 golang.org/x/net@v0.60.0` fixes that without
waiting. `v0.5.0` and `v0.2.1` stay published.

`hashicorp` also gains `Serve(guest, protocol)`, which builds the go-plugin server configuration that the bridge
command and the example each built by hand. That is a new exported function, so `hashicorp` takes a minor
version (v0.3.0, not a patch). The umbrella's own exported API is unchanged, so it stays a patch, and it no longer
imports `go-plugin` or `go-hclog`: the umbrella coordinates modules and leaves each runtime to the module that
owns it. Its `go.mod` still lists both as indirect, which is the go command's record of the module graph, not an
import.

`pkg/plugin-go/contract` v0.2.0 is correct: it needs no sibling checksums, because it has no sibling
dependencies. Its v0.2.1 differs from it only by one doc comment in `session.go`, which named
`res/graph/supervisor` before the rename; a consumer that pins `contract` alone can stay on v0.2.0. The first
round's other tags resolve and build for every consumer, but their `go.sum` files record checksums for sibling versions that are wrong, so a
checkout of one cannot build inside its own directory with the workspace off. The cause is that the module
proxy adds the repository's root `LICENSE` to the zip of a module in a subdirectory, and the zips used to
compute those checksums did not. The `v0.2.1` line carries the correct checksums; prefer it.

Every `go.sum` carries the real checksums of the sibling versions it requires, computed before tagging, in
dependency order, so one commit carries every tag. [Releasing a round](#releasing-a-round) says how.

### Adapters as packages

The adapter manifest `adapter.toml` is replaced by `plugin.json`, the ext package manifest
([the decision](adr-adapter-package.md)). It is a hard cut at the same Go API: `contract/host/adapter`
reads only `plugin.json`, so an adapter installed under the old format fails to load (the error says
that `adapter.toml` is no longer read) until it is reinstalled, and because the trust digest covers
the files of the adapter's directory, every adapter is trusted again after that reinstall. Archives
built before the change contain `adapter.toml` and a `ctx-package.json` and cannot be installed; the
new archive is the sealed package directory and has no second record. The adapter API stays `2.0`.

This ships in the same round as the package manifest rename above (`v0.4.0`), so `contract` carries
both and users face one `contract` version and one adapter trust break. The change is in the
`contract` module (the adapter package), and the manifest sits inside each of the 24 adapter Go
modules, so each of them changes bytes and takes a new minor version with recomputed pins, tagged in
the layers `scripts/release-layers.sh` prints. `ctx` and `ctn` must be released with them, because an
older host cannot read the new manifests. A host that fixtures adapters in its tests writes
`plugin.json` (`adapter.WriteKeyValueManifest` takes the old compact text) instead of `adapter.toml`.

### Releasing a round

A tag can never be moved or reused, and a `go.sum` that records the wrong hash of a sibling makes every
checkout of the dependent module unbuildable, so a round is rehearsed before the first tag and verified
against the published bytes layer by layer. The scripts below live in `scripts/`; they need only bash, git,
awk, sed and Go, and run from a clean checkout on macOS and Linux. Each starts with a comment saying what it
checks. They take a **versions file** of `module version` lines (the module is a directory relative to the
repository root, for example `pkg/plugin-go/contract v0.4.0`; `#` starts a comment).

1. **Scope.** Tag a module only if its own bytes changed, plus a thin umbrella whose `go.mod` must pin the
   modules that did. A module whose bytes did not change keeps its tag and keeps working. Choose a minor
   version for new API or a behavioural break (this is 0.x), a patch otherwise.
2. **Layers.** `scripts/release-layers.sh VERSIONS` prints the dependency layers: a module sits one layer above
   the highest listed module it requires.
3. **Pins and checksums.** `scripts/release-pins.sh VERSIONS` bumps every requirement on a listed module to its
   release version and records the true hashes in each `go.sum`, layer by layer, by resolving the lower layers from
   a throwaway repository with the real go toolchain. Bump the product modules that build against local `replace`
   directives (`src/ctx`, `src/ctn`) with `go mod edit -require`. A listed module that another workspace module
   requires at its release version has no tag yet, so the go command cannot read its `go.mod` and every
   workspace build fails with `unknown revision`, on every platform. `src/ctx` already replaces the modules it
   imports; for any other such module (in v0.5.0, `cengine`, required by the umbrella) add a version-pinned
   `replace <module> <version> => ./<dir>` to `go.work`. It must carry the version: Go rejects a workspace
   module replaced at all versions. Remove the line in the commit after the last layer is tagged. Update the
   docs and READMEs, commit, open a pull request, and merge it as a merge commit once its checks pass; check
   that the merged tree equals the prepared one.
4. **Two independent hash checks on the commit you will tag.** Both must report 0 mismatches.
   `scripts/release-rehearse.sh -o WANT VERSIONS REVISION` tags every module in a throwaway repository and lets
   `go mod download` compute each hash, which is the code path the module proxy runs.
   `scripts/release-treehash.sh VERSIONS REVISION` computes them from the git tree with
   `zip.CreateFromVCS`. The two share no code, so agreement rules out a mistake in either.
   `scripts/release-standalone.sh VERSIONS REVISION` then builds every listed module on its own from that
   commit (`go vet` on linux, darwin and windows, and `go mod tidy -diff`, with the workspace off and a read-only
   `go.mod` and `go.sum`), resolving the round's modules from the throwaway repository. It is the check
   `check-standalone-modules.sh` makes after tagging, made while a failure can still be fixed.
5. **Tag, one layer at a time.** `scripts/release-layer.sh --push VERSIONS WANT LAYER REVISION` creates the
   annotated tags on the merged commit, pushes them in one atomic push, and compares the published zip hash and
   `go.mod` hash with `WANT`, once from the remote with `GOPROXY=direct` and once from `proxy.golang.org`. Without
   `--push` it only prints what it would tag. After each layer run
   `STANDALONE_ONLY="<the layer's modules>" scripts/check-standalone-modules.sh`, which builds and tidies them from
   the published tags with the workspace off. **Stop at the first difference**; tell whoever depends on the round,
   and fix the cause with a new version, not a retag.
6. **After the last layer.** Run `scripts/check-standalone-modules.sh` for every module, and build a clean module
   that requires the released versions with the default checksum database.

The zip layout is the reason for the two checks. The module proxy, and `zip.CreateFromVCS`, add the repository's
root `LICENSE` to the zip of a module that lives in a subdirectory and has no `LICENSE` of its own. A zip built
any other way (for example `zip.CreateFromDir` over the directory) lacks it, so its hash is one no proxy will
ever serve; recording that hash in a `go.sum` is what made 28 tags of the first round unbuildable on their own.
Hashes must therefore come from the go toolchain or from `zip.CreateFromVCS`, never from a hand-built zip. Use a
fresh module cache whenever a version is rehearsed twice (Go indexes cache directories by path and assumes they
never change); the scripts do. Because tags are immutable, anything wrong in a published layer is superseded by a
new version, which is why each layer is verified before the next one is tagged.

### Deprecating the old modules

Each old module gets one final tag whose `go.mod` carries a deprecation comment above
the `module` line, then no further tags:

| Old module | Final tag content | Points to |
| --- | --- | --- |
| `pkg/plugin` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/contract.` | `pkg/plugin-go/contract` |
| `pkg/plugin-cshared` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/cengine.` | `pkg/plugin-go/cengine` |
| `pkg/plugin-wasm` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/wasm.` | `pkg/plugin-go/wasm` |
| `pkg/plugin-hashicorp` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/hashicorp.` | `pkg/plugin-go/hashicorp` |
| `pkg/graph` | `// Deprecated: use github.com/webong/ext/res/system.` (v0.1.2; v0.1.1 named `res/graph`, which was then renamed) | `res/system` |
| `res/graph` | `// Deprecated: use github.com/webong/ext/res/system.` (v0.1.1) | `res/system` |

The old `pkg/plugin-go` v0.1.x keeps resolving for existing consumers; its replacement
at the same path is the umbrella v0.2.0 and its engine API is `cengine`. Tell every
consumer before any tag is pushed, because they migrate by rewriting imports.
