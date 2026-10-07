# Module versioning

Every module in this repository is published independently and versions on its
own `MAJOR.MINOR.PATCH`. Go's semantic import versioning means a module path is
only retrievable at a `vN` prefix matching its major version, so the path
`github.com/webong/ext/pkg/plugin` is served from tags named
`pkg/plugin/v1.2.3`.

## Shared major version

All modules share one `MAJOR` version. The repository is a single product with a
single compatibility promise, and adapters, Xallet, Cymonkey, and other
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
git tag pkg/graph/v1.0.1

# additive change in one module
git tag pkg/plugin/v1.1.0

# coordinated breaking change across every module
git tag res/browser/v2.0.0 pkg/plugin-go/v2.0.0 \
        pkg/graph/v2.0.0 pkg/plugin/v2.0.0 pkg/plugin-hashicorp/v2.0.0 \
        pkg/plugin-wasm/v2.0.0 v2.0.0
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
Library modules never use these tags, and bare `vX.Y.Z` tags are not used.

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

Local tags are not yet reachable through the public module proxy. Pushing them
is a separate, deliberate publication step, and `GOWORK=off` builds inside a
library module keep failing until it happens. That is the expected signal, not
a defect.

## Verifying a release

Before tagging, confirm each module builds and tests on its own with the
workspace active, and that `src/ctx` resolves the intended versions:

```bash
for module in src/ctx src/ctn examples pkg/plugin-go pkg/graph pkg/plugin \
              pkg/plugin-hashicorp pkg/plugin-wasm \
              res/browser res/credential; do
  (cd "$module" && go build ./... && go test ./...) || exit 1
done
```
