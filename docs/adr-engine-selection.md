# Three engines, and who chooses between them

Status: the rename is accepted and done on branch `engine/cgoengine-rename` (off
`engine/c2goengine`, no pull request yet); the selection is proposed and not implemented.
`pkg/plugin-go/c2goengine` exists on `engine/c2goengine`.

The C engine in `lib/plugin` can be reached three ways, and `pkg/plugin-go` is meant to
be the place that picks between them:

| engine | module | needs | role |
| --- | --- | --- | --- |
| the pure-Go contract | `pkg/plugin-go/contract` | nothing | the **oracle**: written separately, so a bug in the C cannot hide in it; also the portable default |
| the C engine through cgo | `pkg/plugin-go/cgoengine` (formerly `cengine`) | cgo and `libext_host_static` | the C itself at native speed |
| the C engine translated to Go | `pkg/plugin-go/c2goengine` | nothing (Go 1.26 for the module) | the C run as Go by ccgo: about twice as slow as native, about twice as fast as the oracle on the services measured |

`contract/plugintest/enginetest` holds an engine against the oracle with 28 checks
(services, sessions over a Go backend, engine-owned child processes, instances). The
cgo engine and the translated engine both pass all 28 on darwin and linux (both
architectures). On Windows the translated engine covers the services only, so the checks
that need a session, a child process or an instance manager are skipped there.

## Decisions proposed

**The oracle stays the oracle.** `contract` is never replaced by an engine. A change to
the plugin contract lands in `contract` and `lib/plugin` together, and `enginetest`
compares them. An engine that disagrees with the oracle is the one that is wrong until
proven otherwise.

**`cengine` becomes `cgoengine`** (done). The name says what distinguishes it (cgo) and sits
beside `c2goengine` (C translated to Go) without implying that one is the engine and the
other a variant. The Go package is renamed with it, from `goengine` to `cgoengine`, so the
import needs no alias; nothing else about the module changes.

**One selection point, in the umbrella.** `pkg/plugin-go` offers a small `Engine`
interface, the surface the harness already uses: `Call`, `Open`, `OpenProcess` and
`NewInstances`. Each engine module provides a constructor returning it (or nothing,
when it is not available on this build). The umbrella chooses:

1. an explicit choice by the caller (option or environment variable), by engine name;
2. otherwise the best available engine in the order cgo, translated, oracle.

The oracle is the answer of last resort and also the choice when a caller wants the
reference behavior (tests, debugging a disagreement). Selection is made once, at open
time, and is visible: a session reports which engine runs it. No engine silently falls
back to another in the middle of a session.

**Adapters and libraries do not choose.** An adapter imports the umbrella and asks for
"the engine". Which one runs is the host application's policy, supplied through
configuration, in line with the adapter ownership rule in `AGENTS.md`: shared code must
not pick native behavior by name.

## The rename is a new module

A module path and its tags are a pair (see `docs/module-versioning.md`, "`pkg/plugin-go/cengine`
became `pkg/plugin-go/cgoengine`"), so this is a new module, not a new version:

- `pkg/plugin-go/cengine/v0.7.0` is published and immutable. It stays resolvable for
  anything that requires `github.com/webong/ext/pkg/plugin-go/cengine`, and no further tags
  are cut on that path. Hard cut, as in development: no forwarding module, no alias package.
- `pkg/plugin-go/cgoengine/v0.1.0` is the first tag of the new path, to be cut on a tree whose
  `go.mod` declares the new path. Until then the umbrella and the 24 adapters require
  `cgoengine v0.1.0` in their `go.mod` files, which resolves in the workspace and not from
  the proxy.
- Moved with the module: its directory and `go.work` entry, the subpackages (`host`,
  `host/store`, `packages`, `cshared`), every import, `scripts/cengine-sync.sh` (now
  `scripts/cgoengine-sync.sh`), the workflows, the docs and the arch tests.
- Kept: the build tags `ext_cengine` and `ext_cengine_shared`, the `EXT_CENGINE_*` variables,
  `scripts/plugin-cengine*.sh` and the `examples/cengine` directories of the other language
  bindings. They name the C engine, not the Go module, and are public build lines of every
  consumer.

## Order of work

1. This branch (`engine/c2goengine`): the translated engine, unchanged by the above.
2. The coordinator's round that publishes `contract` v0.8.0 (it carries `enginetest` and
   the dispose-ordering fix `c2goengine` needs), then `c2goengine/v0.1.0`.
3. The rename (done on `engine/cgoengine-rename`), then its first tag `cgoengine/v0.1.0` in the
   coordinator's round, after `contract` v0.8.0.
4. After the rename: the `Engine` interface and the selection in `pkg/plugin-go`, with
   a test that runs `enginetest` against whatever the selection returns.

## Not decided here

- Whether the oracle should be selectable by name in production builds or only in
  tests. Leaning toward both: it is the only engine that exists on every platform.
- Whether `c2goengine` should ever be preferred over cgo when both are available. The
  measurements say no (native is about twice as fast), but cgo costs a C toolchain at
  build time and a cgo call at run time, so a program that crosses the boundary often
  and calls the engine rarely may prefer the translation. That is a policy knob for
  the host application, not a default.
- Windows for the translated engine; see `pkg/plugin-go/c2goengine/README.md`.
