# Three engines, and who chooses between them

Status: accepted and built on the engine stack (`engine/c2goengine`, the `cengine` →
`cgoengine` rename, the selection package, and the `contract` → `engine` rename with the
selection in `core`). The branches are pull requests; tags are the coordinator's.

The C engine in `lib/plugin` can be reached three ways, and `pkg/plugin-go` picks between
them. The three are named alike, as engines:

| engine | module | needs | role |
| --- | --- | --- | --- |
| the pure-Go engine | `pkg/plugin-go/engine` (formerly `contract`) | nothing | the **oracle**: written separately, so a bug in the C cannot hide in it; also the portable default |
| the C engine through cgo | `pkg/plugin-go/cgoengine` (formerly `cengine`) | cgo and `libext_host_static` | the C itself at native speed |
| the C engine translated to Go | `pkg/plugin-go/c2goengine` | nothing (Go 1.26 for the module) | the C run as Go by ccgo: about twice as slow as native, about twice as fast as the oracle on the services measured |

The package that chooses between them is `pkg/plugin-go/core`, in the umbrella module.

`engine/plugintest/enginetest` holds an engine against the oracle with 28 checks (services,
sessions over a Go backend, engine-owned child processes, instances). The cgo engine and the
translated engine both pass all 28 on darwin and linux (both architectures). On Windows the
translated engine covers the services only, so the checks that need a session, a child
process or an instance manager are skipped there.

## Decisions

**The oracle stays the oracle.** The pure-Go engine is never replaced by another engine. A
change to the plugin contract lands in `engine` and `lib/plugin` together, and `enginetest`
compares them. An engine that disagrees with the oracle is the one that is wrong until
proven otherwise.

**`cengine` becomes `cgoengine`.** The name says what distinguishes it (cgo) and sits beside
`c2goengine` (C translated to Go) without implying that one is the engine and the other a
variant. The Go package is renamed with it, from `goengine` to `cgoengine`, so the import
needs no alias.

**`contract` becomes `engine`.** The pure-Go module is the third engine, so it is named like
the other two. Its Go package is renamed with it; importers named it `plugin` and are
unchanged apart from the path. The plugin contract as a concept (`ContractRef`, the
`contract` field of the wire format) keeps its name, and so does everything inside the module:
only the module moved.

**One selection point, in the umbrella.** `pkg/plugin-go/core` is a package of the umbrella
module, so the three engine modules stay as they are and only the umbrella requires all
three. Its `Engine` interface is the surface `enginetest.Engine` already uses, with the types
the engines share:

- `Call(operation, input)`: the engine's pure services by name.
- `Open` and `OpenProcess` return a `core.Session` (`Call`, `CallProgress`, `State`, `Done`,
  `Close`, `Abort`, `Stderr`, and `Engine()`, the name of the engine that runs it).
- `core.NewInstances(e, options)` is a function and not a method, because methods cannot be
  generic. It returns an `Instances[T]` whose `Lease[T]` carries `Value` and `Revision`.

The engines are `core.CGo` (`"cgo"`), `core.C2Go` (`"c2go"`) and `core.Go` (`"go"`, the pure-Go
engine). `core.Choose(name, need)` picks one: with no name (or `auto`) the first of cgo, c2go,
go that is available and carries every feature in `need`; with a name, that engine or an error
wrapping `plugin.ErrUnsupported`. `core.FromEnvironment(need)` reads the name from
`EXT_ENGINE`. The oracle is the answer of last resort, and also the choice when a caller wants
the reference behavior. The choice is made once, when a session or an instance manager is
opened. No engine silently falls back to another, and none is replaced in the middle of a
session.

What the proposal did not say and the implementation needed:

- **Features.** The engines are not equal: the translated one has no progress events or reverse
  calls yet and no host on Windows, and the pure-Go engine has no services by name (its rules
  are Go functions). `Feature` (`Services`, `Sessions`, `Progress`, `Reverse`) lets the caller
  say what it needs, so the default order never picks an engine that cannot do it. An engine
  that is not available has no features.
- **The cgo engine is available only where the C engine is linked.** Built without cgo, the
  `ext_cengine` tag or the C library, `cgoengine` is its pure-Go build, which is the go engine
  again; `cgoengine.CEngine` says which build it is (it was documented but defined only in the
  pure-Go build; it is now `true` in the C build).
- **The oracle is held to the harness as far as the harness holds it.** `enginetest` asserts
  the C engine's deliberate differences from Go in its process check, so the go engine is run
  through the instance and session checks that `enginetest` already runs it through, and cgo
  and c2go through all of them.

**Adapters and libraries do not choose.** An adapter imports the umbrella and asks for "the
engine". Which one runs is the host application's policy, supplied through configuration, in
line with the adapter ownership rule in `AGENTS.md`: shared code must not pick native behavior
by name.

## The renames are new modules

A module path and its tags are a pair (see `docs/module-versioning.md`, the sections on
`cengine` and `contract`), so each rename is a new module, not a new version:

- The tags of `pkg/plugin-go/cengine` (v0.2.0 to v0.8.0) and of `pkg/plugin-go/contract`
  (v0.2.0 to v0.8.0) are immutable. They stay resolvable for anything that requires the old
  paths, and no further tags are cut on them. Hard cut, as in development: no forwarding
  module, no alias package.
- **Why the old paths are not deleted or reused.** Deleting a tag from GitHub does not remove
  the version: `proxy.golang.org` keeps what it has served and the checksum database keeps its
  hash for good. Go itself does not forbid reusing a path, but the repository's rule is that a
  published path is not reused by a different module (the ambiguity and `@latest` surprises
  are real), which is why the selection package is `core` and not `contract`. A
  `// Deprecated:` commit on the last tag of each old path retires it, as for
  `pkg/plugin-cshared`.
- The first tags of the new paths are `engine/v0.1.0`, `cgoengine/v0.1.0` and
  `c2goengine/v0.1.0`, cut on a tree whose `go.mod` files declare the new paths. Until they
  exist, the modules that require them are skipped by the standalone check and resolve in the
  workspace through version-pinned `replace` lines in `go.work` (the products through their
  own `replace`).
- Moved with the modules: their directories and `go.work` entries, the subpackages, every
  import, `scripts/cengine-sync.sh` (now `scripts/cgoengine-sync.sh`), the workflows, the docs
  and the arch tests.
- Kept: the build tags `ext_cengine` and `ext_cengine_shared`, the `EXT_CENGINE_*` variables,
  `scripts/plugin-cengine*.sh` and the `examples/cengine` directories of the other language
  bindings. They name the C engine, not the Go module, and are public build lines of every
  consumer.

## Order of work

1. The translated engine (`c2goengine`), with the instance, session and process checks moved
   into `enginetest`.
2. The renames, each as one change: `cengine` → `cgoengine`, then `contract` → `engine`,
   with the selection package as `core`.
3. A release round that publishes `engine` v0.1.0, `cgoengine` v0.1.0 and `c2goengine` v0.1.0,
   the umbrella, and every module whose imports moved (`wasm`, `hashicorp`, the 24 adapters),
   in dependency layers, then the deprecation commits on the old paths. The tags are the
   coordinator's, on the user's go-ahead.

## Not decided here

- Whether the oracle should be selectable by name in production builds or only in tests.
  Leaning toward both: it is the only engine that exists on every platform.
- Whether `c2goengine` should ever be preferred over cgo when both are available. The
  measurements say no (native is about twice as fast), but cgo costs a C toolchain at build
  time and a cgo call at run time, so a program that crosses the boundary often and calls the
  engine rarely may prefer the translation. That is a policy knob for the host application,
  not a default.
- Windows for the translated engine; see `pkg/plugin-go/c2goengine/README.md`.
