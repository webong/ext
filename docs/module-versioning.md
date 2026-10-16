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
git tag res/graph/v1.0.1

# additive change in one module
git tag pkg/plugin-go/v1.1.0

# coordinated breaking change across every module
git tag res/web/v2.0.0 res/graph/v2.0.0 pkg/plugin-go/v2.0.0 \
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

The CLI module `src/ctx` (module path `github.com/webong/ext/ctx`) and the
`examples` module keep `replace` directives
because they are the modules developers build and run. Those are what let
`go build ./...` use local module code. The repository root is not a module: it
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
The mobile SDKs follow the same dash form: `plugin-android-vX.Y.Z` releases the Android AARs
(`.github/workflows/plugin-release.yml`, published with `--latest=false`) and `plugin-ios-vX.Y.Z` is
reserved for the iOS SDK, which has no release job yet. Their `MINOR` and `PATCH` move
independently; their `MAJOR` follows the repository-wide `MAJOR`.
Library modules never use these tags, and bare `vX.Y.Z` tags are not used.

The iOS SDK's engine archive is released under its own tag, `plugin-ios-vX.Y.Z`; see
[`pkg/plugin-ios`](../pkg/plugin-ios/README.md) and `scripts/plugin-ios-release.sh`. It is
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
for module in src/ctx src/ctn examples pkg/plugin-go res/graph \
              pkg/plugin-hashicorp pkg/plugin-wasm \
              res/web res/credential; do
  (cd "$module" && go build ./... && go test ./...) || exit 1
done
```

## The Go version floor

Each module's `go` line is the oldest Go that builds it, so it must be at least what its own
dependencies require; `go mod tidy` raises it when it is not. The light libraries
(`pkg/plugin-go`, `pkg/plugin-wasm`, `res/*` and the adapters) declare `go 1.23.0` and their dependencies allow it. Four modules declare
`go 1.26.0` because `golang.org/x/net`, `x/sys` and `x/text` at the versions they use require it:
`pkg/plugin-hashicorp` (gRPC), `src/ctx`, `src/ctn` and `examples`. The workspace file
`go.work` declares the highest of them, and CI runs Go 1.26.x.

`pkg/plugin-hashicorp/v0.1.0` was published declaring `go 1.23.0` while requiring those
dependencies, so it is mislabeled: a consumer on an older Go gets an error naming 1.26. Published
tags are immutable, so the correct floor ships in `pkg/plugin-hashicorp/v0.1.1`. Resolved from GitHub
in a clean module, v0.1.0 reports `go 1.23.0` and v0.1.1 reports `go 1.26.0`. Consumers on an
older Go should require v0.1.1 or newer, and the v0.1.0 tag stays as published.

## The plugin library is a tree of modules

`pkg/plugin-go/` is a directory tree in which each heavy part is its own module,
so an application imports only what it needs and stacks the rest:

| Module | Contents | Requires |
| --- | --- | --- |
| `pkg/plugin-go/contract` | the contract, sessions, guests, packages, the package store and host, bridges and tooling (the old `pkg/plugin`); standard library only | nothing |
| `pkg/plugin-go/cengine` | the Go binding of the C engine, a pure-Go build of the same API, engine-routed variants of `store` and `host` with the same API as the ones in `contract`, the C ABI loader (the old `pkg/plugin-cshared`) and a generated copy of the C sources | `contract` |
| `pkg/plugin-go/wasm` | the wazero backend (the old `pkg/plugin-wasm`) | `contract` |
| `pkg/plugin-go/hashicorp` | the go-plugin (gRPC) backend (the old `pkg/plugin-hashicorp`) | `contract` |
| `pkg/plugin-go` | the umbrella: requires every module above at matching versions and holds what needs several of them (`PlanGraph`, the bridge command, the cross-language tests) | all of the above, `res/graph` |

The name `contract` was chosen over `core` because it is already the name of the Go
package and of every alias in use, and the module is that contract plus tooling that
needs nothing beyond the standard library, not a runtime. `wasm` and `hashicorp`
never depend on `cengine`, in code, tests or `go.mod`, so a wazero or gRPC user needs
no C toolchain; `src/ctx/internal/arch` enforces this.

### The C sources

A published module contains only its own directory, so `cengine` carries the sources
it compiles as `_csrc`, a copy of `lib/plugin` generated by `scripts/cengine-sync.sh`.
`TestCSourcesMatchCanonical` fails when the two differ. `lib/plugin` stays canonical
for the iOS, Android, Rust and Zig bindings. The cgo preamble includes the headers
from `_csrc` and links the engine library, which is built from `_csrc` with CMake
(the default build tag set uses no C at all).

### Migration

| Old import path (module) | New import path (module) |
| --- | --- |
| `pkg/plugin` and its subpackages, including `store` and `host` (`pkg/plugin`, up to v0.6.0) | `pkg/plugin-go/contract` and `.../contract/<subpackage>` (`pkg/plugin-go/contract`); the packages that run plugins on a machine are grouped under `contract/host` (`store` becomes `contract/host/store`, `process` becomes `contract/host/process`, `adapter` becomes `contract/host/adapter`, `interop` becomes `contract/host/interop`, `nativego` becomes `contract/host/nativego`; what a plugin implementation uses is grouped under `contract/guest`: `author` becomes `contract/guest/author`, `capability` becomes `contract/guest/capability`, `instance` becomes `contract/guest/instance`, `stream` becomes `contract/guest/stream`); an application that wants the engine-routed behavior swaps `contract/host` and `contract/host/store` for `cengine/host` and `cengine/host/store`, which have an identical API |
| `pkg/plugin-go` root, the engine API (`pkg/plugin-go`, up to v0.1.1) | `pkg/plugin-go/cengine` (`pkg/plugin-go/cengine`) |
| `pkg/plugin-go/cshared`, `.../cshared/abi`, `.../cshared/guest` | `pkg/plugin-go/cengine/cshared`, `.../abi`, `.../guest` (`pkg/plugin-go/cengine`) |
| `pkg/plugin-cshared` (up to v0.1.0) | `pkg/plugin-go/cengine/cshared` (`pkg/plugin-go/cengine`) |
| `pkg/plugin-wasm` (up to v0.2.1) | `pkg/plugin-go/wasm` (`pkg/plugin-go/wasm`) |
| `pkg/plugin-hashicorp` (up to v0.1.1) | `pkg/plugin-go/hashicorp` (`pkg/plugin-go/hashicorp`) |
| `pkg/plugin-hashicorp/cmd/ext-plugin-bridge` | `pkg/plugin-go/cmd/ext-plugin-bridge` (`pkg/plugin-go`) |
| `pkg/graph` (v0.1.0) | `res/graph` (`res/graph`) |
| `packagekit.Plan.Graph(namespace)` | `plugingo.PlanGraph(plan, namespace)` in `pkg/plugin-go`; `packagekit.IdentityKey` is now exported. The conversion lives in the umbrella so `contract` has no `res/graph` dependency. No caller in this repository other than the method's own test used it, and the consumer repository calls neither it nor `res/graph` |

Subpackages of the old `pkg/plugin-go` and `pkg/plugin` that move under `contract`:
`bridge`, `inprocess`, `jsonline`, `jsonschema`, `packagekit`, `plugintest` and `schema`, with the
host-side ones grouped under `contract/host` (`host` itself and `host/adapter`, `host/interop`,
`host/nativego`, `host/process`, `host/store`) and the guest-side ones under `contract/guest`
(`guest/author`, `guest/capability`, `guest/instance`, `guest/stream`). `cengine`
holds `packages` (engine-only) and engine-routed variants of `host` and `host/store` whose exported API is
identical to `contract`'s; `src/ctx/internal/arch` enforces that, and
`TestContractStoreMatchesEngineStore` compares their behavior.

Import paths that existed in published `pkg/plugin-go` v0.1.x are `pkg/plugin-go`
(the root package) and `pkg/plugin-go/cshared/guest`. The new umbrella reuses the
root path `pkg/plugin-go` for a different package (`plugingo`), which is why every new
version starts at v0.2.0, above v0.1.1. No other path is shared between modules.

### Versions and tags

| Version | Modules |
| --- | --- |
| `v0.1.0` (first tag) | `res/graph` |
| `v0.2.0` | `pkg/plugin-go/contract`, `.../cengine`, `.../wasm`, `.../hashicorp`, `pkg/plugin-go` |
| patch bumps | the adapters (`adapters/chromium` v0.3.1, `adapters/firefox` v0.2.1, and so on); no exported API changed |

Every `go.sum` carries the real checksums of the sibling versions it requires, computed
before tagging, in dependency order, from a throwaway local module proxy holding the
new versions, so one commit carries every tag. Verify a tree this way before tagging:
build each module's zip with `golang.org/x/mod/zip`, serve them with `GOPROXY=file://`,
use a module cache path that has never held other contents for the same versions
(Go indexes cache directories by path and assumes they never change), and run
`GOWORK=off go vet ./...` on linux, darwin and windows plus `go mod tidy -diff`
in each module. `scripts/check-standalone-modules.sh` repeats this against published
tags and skips a module whose sibling tags are not yet published.

### Deprecating the old modules

Each old module gets one final tag whose `go.mod` carries a deprecation comment above
the `module` line, then no further tags:

| Old module | Final tag content | Points to |
| --- | --- | --- |
| `pkg/plugin` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/contract.` | `pkg/plugin-go/contract` |
| `pkg/plugin-cshared` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/cengine.` | `pkg/plugin-go/cengine` |
| `pkg/plugin-wasm` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/wasm.` | `pkg/plugin-go/wasm` |
| `pkg/plugin-hashicorp` | `// Deprecated: use github.com/webong/ext/pkg/plugin-go/hashicorp.` | `pkg/plugin-go/hashicorp` |
| `pkg/graph` | `// Deprecated: use github.com/webong/ext/res/graph.` | `res/graph` |

The old `pkg/plugin-go` v0.1.x keeps resolving for existing consumers; its replacement
at the same path is the umbrella v0.2.0 and its engine API is `cengine`. Tell every
consumer before any tag is pushed, because they migrate by rewriting imports.
