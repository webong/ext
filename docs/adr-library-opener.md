# The library opener: ext-go carries no C

Status: accepted on 2026-11-22, amended on 2026-11-23 (decisions 5 to 7: a project fetches the C
before it builds; decision 8: Go binds the C libraries, like every other language). Steps 1 and 2 are built; the rest is the order in [Steps](#steps).

## Context

ext-go is the Go SDK for the libraries of `lib/`: the plugin engine (`lib/plugin`), the ledger
(`lib/ledger`) and whatever comes next. Each library has a pure-Go implementation in `engine/<library>`,
and the C library can reach a Go program four ways: linked through cgo, translated to Go by ccgo,
loaded as a shared library through purego, or not at all (pure Go).

Until now ext-go carried the C itself: `cgoengine/_csrc` is a copy of `lib/plugin`, kept in step by
`scripts/cgoengine-sync.sh` and a drift test, and a cgo build links a static library built from it.
Each new library would add another copy, and ext-go would grow the package, archive and install logic
that belongs to ext. ext-go is not ext: ext, ctx and other programs depend on it, and it takes what it
needs from them.

## Decisions

**1. The program supplies the library; a pinned release covers the rest.** A program hands ext-go the
C library it wants, as a path to a shared library or, for cgo, as a library it links. When it supplies
nothing, ext-go can fetch the release it pins. Both are supported: a program such as ext that has its
own downloader passes it in or passes the path it installed, and a program without one lets ext-go
fetch. A program can forbid fetching altogether.

**2. Two modules: the switcher and the C side.** The generic `cengine` packages know no library and import nothing of the plugin
engine. Its `cguest/backend` and `cguest/guest` packages import `core/plugin`;
`core` imports nothing of ext (`src/ctx/internal/arch` enforces these boundaries).

Running a plugin guest built as a C shared library is not loading a library of `lib/`: the guest is a
plugin, so what crosses to it is the plugin protocol. That is a plugin backend, `pkg/ext-go/cengine/cguest/backend`, beside
`yaegi`, `hashicorp` and `wasm`; it speaks `ext.io.pc/v1` over the raw loader `cengine/cguest` and requires
`core/plugin` and the local loader. The plugin contract plays no part in how the drivers reach `lib/plugin` or
`lib/ledger`, which go through each library's own C API.

| Module | Role | Requires |
| --- | --- | --- |
| `pkg/ext-go/core` | The switcher. `core/driver` picks how a program runs each library of `lib/`: `go` (its pure-Go implementation in `engine`) or one of the C drivers, `cgo`, `c2go` and `purego`. `Choose` takes one by name, by `EXT_ENGINE` or as the best available one that fits, in the order cgo, c2go, purego, go; a name that cannot be served is an error, never another driver. | nothing |
| `pkg/ext-go/cengine` | The C side. It serves the C drivers (purego, cgo, ccgo): `cgoengine` and `c2goengine` register with it. `cengine/library` is the opener purego needs: it finds, fetches, verifies and opens a built copy of the C library once per canonical path and binds its exports; the engine never touches files. `cengine/cguest` loads C ABI guests and exchanges frames as bytes, knowing nothing of the plugin protocol; `cguest/backend` and `cguest/guest` supply plugin integration. | purego, `core` for shared contracts |

A library's Go binding takes its interface from whichever driver reaches it, and is driven by or
generated from the library's description; ext-go holds none. `core` has no root package: the umbrella published its selection package at the
import path `pkg/ext-go/core` up to v0.12.0, and a root package here would give a program that requires
both an ambiguous import.

**3. The opener's verbs.** A library is reached through four steps, named for a library's lending desk:

| Verb | What it does |
| --- | --- |
| `Cite` | Builds a `Citation`, the pin, as plain data: the library's name (`ext_ledger`), its release version, the symbol that reports its version and the versions accepted, and per OS and architecture the file name, the download URL and its SHA-256. The library's Go package declares it; `cengine` holds none. |
| `Browse` | Lists the copies that match a citation without loading anything: the path the program supplied, then the cache, then the directories the program names. |
| `Reserve` | Produces a verified local copy. A supplied path is checked against the citation; otherwise the pinned file is fetched into the cache and checked against its SHA-256 before anything uses it. `Fetch` replaces the downloader (ext passes its own), `Offline` refuses any fetch, and the cache defaults to `os.UserCacheDir()/ext-go/library/<name>/<version>`. |
| `Borrow` | Loads a reserved copy through purego, calls the version symbol and refuses a version outside the citation. It returns the `Library` handle that `Symbol`, `Bind` and `BindAll` already serve. |

Fetching is one HTTPS GET of one file with no archive handling; the digest is checked before the file is
moved into the cache. A copy, once loaded, is never unloaded.

**4. cgo and ccgo register with `cengine`; they are not linked by it.** cgo and ccgo are fixed when a
program is built, so `cengine` cannot link an unknown library. `cgoengine` and `c2goengine` stay their
own modules, so a program downloads the ccgo translation only when it imports it, and they register
from files built only with that driver (`library.Shelve(name, driver, implementation)`), and `Borrow` returns a shelved
implementation before it loads anything. With `Auto` the order is what the build shelved (cgo, then
c2go), then purego, then go. A program can name one driver or forbid one, such as purego where the
operating system refuses code fetched at run time.

**5. ext-go carries no C: a project fetches it before it builds.** ext-go holds no C source, no
translation and no built library. A Go project that uses ext-go brings the C library in itself, with
`ext bundle fetch`, a builtin of the `ext` command the project adds as a Go tool and runs from `go generate`
(Go runs no code of a dependency before or during a build, and the linker fetches nothing, so this is
a step the project runs first). ext-go is a program SDK and has no command of its own: `ext bundle` is
served by the shared package `cengine/libcmd` (amended 2026-11-24; it was `cengine/cmd/ext-go`):

```
go get -tool github.com/webong/ext/cmd/ext
//go:generate go tool ext bundle fetch -lib plugin -driver cgo
go generate ./... && go build ./...
```

fetch reads the library's citation (`library.Citation`, the format `ext.library.citation/v1`: for each
`goos/goarch`, the archive's HTTPS URL, its SHA-256, and which of its files each driver takes),
downloads the archive for every target, checks the digest before it uses a byte of it, and writes a Go
package the project blank-imports:

| Driver | What fetch writes | How the build uses it |
| --- | --- | --- |
| cgo | `include/`, `lib/<goos>_<goarch>/<static library>` and a cgo file whose `#cgo CFLAGS` and `#cgo LDFLAGS` name them through `${SRCDIR}` | the link flags of an imported package reach the final link, so no `CGO_*` variable and no system install is needed |
| c2go | the translated Go source of each target and its binding, as published | it compiles with the project and registers its driver |
| purego | `lib/<goos>_<goarch>/<shared library>`, embedded with `//go:embed` and registered with `library.Provide` | the binary carries the library; `library.OpenProvided` writes it once to the user cache, in a directory named for its digest, and opens it |

Where it writes comes from `-dir`, then `EXT_GO_DIR`, then `ext-go.json` in the module root, then the
default `internal/extlib/<library>`; the directory must be inside the module, because cgo and c2go
output compiles with it. The targets default to the one `go env` reports (`-target all` takes every
target the citation publishes), so a cross-build fetches for its target; the driver defaults to cgo when
cgo is enabled and purego when it is not. Verified archives are cached in the user cache and reused, and
`-offline` uses only the cache. A file fetch wrote is listed in the directory's `.fetched`, so the next
run removes exactly those and nothing of the project's own.

A project that runs no fetch still has purego through the run-time `Reserve` and pure Go.

**6. The citation is the project's, published by the library.** A citation is data: each `lib/`
library's release publishes it beside its archives, and the project commits the file it uses, so the
pin changes only when the project changes it. `cengine` knows no library by name; `ext-go.json` maps
the project's short names (`plugin`, `ledger`) to its citation files.

**7. Releases publish what the drivers take.** The `ext-plugin-vX.Y.Z` release already publishes one
archive per platform with the headers, the shared library and the static library, and their SHA-256
(`scripts/ext-plugin-release.sh`): what cgo and purego take. It gains the citation file, and the ccgo
translation of each platform with its binding (made by `c2goengine/tools` on that platform's runner), so
the generated translations leave this repository. `lib/ledger` publishes the same way.

**8. Go binds the C libraries, like every other language.** Go hosts and the Go ledger reach
`lib/plugin` and `lib/ledger` only through their library interfaces, with the cgo, c2go and purego
drivers; no hand-written Go copy of the host engine or the ledger is kept beside the C, and the `go`
driver goes. ccgo is the build that needs no C toolchain and no shared library (Go source translated
from the C, cross-compiled like any Go), and purego is the cgo-free build over a shared library
(`fetch` embeds it in the binary). What stays pure Go is what every language writes in itself: the
guest SDK (a plugin is JSON lines over stdio, like the Python, Ruby, PHP, Lua and .NET guests) and the
shared data model (descriptors, requests, responses, manifests). The fixtures stay the contract; the C
libraries are held to them, so no Go oracle or parity suite is needed. `engine/plugin/host`, the
pure-Go build of `cgoengine` and `engine/ledger` are retired as their users (ctx, ctn, `cmd/ext`,
the adapters) move to the drivers; the ccgo gaps (no Apple frameworks, no Win32, no `fork`: on
Windows c2go has the services only, and the macOS screen collectors are not translated) are covered
there by purego or cgo. `js/wasm` and the architectures purego does not reach get no host or ledger.

## Consequences

- ext-go's module graph holds no C sources, and a library added to `lib/` needs a citation and a list of
  drivers in its Go package, not a copy of its code.
- Only purego is a run-time choice. cgo and c2go are fixed by the build, so at run time `Choose` picks
  among what the build holds.
- iOS loads no code fetched at run time: there a program uses cgo with a statically linked library, c2go
  or pure Go, or a path to a library the app bundles.
- The pure-Go plugin host remains only while its consumers migrate. The ledger has no Go implementation fallback; unsupported native drivers fail explicitly.

## Steps

1. Done: `core/driver` is the switcher; `pkg/ext-go/runtime` becomes `pkg/ext-go/cengine`, the C
   side: the opener `cengine/library` and the raw C guest loader `cengine/cguest`, with the plugin backend
   over it in `pkg/ext-go/cengine/cguest/backend` in the same `cengine` module; the plugin selection moves to `pkg/ext-go/plugin` and
   chooses through `core/driver`.
2. Done: `library.Citation`, `library.Provide` and `OpenProvided`, `cengine/fetch` and the
   fetch command (now `ext bundle fetch`), held to a scratch module that builds a C library, publishes it over HTTPS and
   runs it with the cgo and the purego drivers.
3. The library interface, in C first: done for `lib/ledger` (`ext_ledger_open`, `_call`, `_close`,
   `_abi_version`; its typed store and collector functions took the `ext_ledger_store_` and
   `ext_ledger_collect` prefixes to free those names). Next its generic Go binding in `cengine` and the
   same interface over `engine/ledger`, held to each other; then `lib/plugin`'s: done for the
   library calls, the package store and the layered view (`ext_plugin_open`, `_call`, `_close`; the
   pure services moved to `ext_plugin_services_call` and are handle 0); and its host sessions, whose
   callbacks (policies, progress, the observer, reverse calls) are events the binding polls and answers.
   Done for the plugin engine's Go side too, first as a hand-written binding (`engine/plugin/host/clib`,
   since replaced by the generated one); `pkg/ext-go/plugin`, the chooser, is gone. The event loop is
   `cengine/library.ServeEvents`, which knows no library, and each event
   carries a sequence number, so a call returns once its events are delivered. The rules stay in C (a
   host's requirements are part of its config) and the code names are identifiers, as the ledger's
   are, so the binding goes by name and only converts Go values.
   Next, the description: each library describes its interface (lib/plugin's `interface.json`, served
   by `describe` on handle 0 and compiled in by CMake), and `cengine/library.Describe` drives any
   library from it, so no library needs hand-written Go: an object is opened by kind, an operation
   called by name, and its events answered by handlers named for their kinds, in the way the
   description says (`allow`, `result`, `none`, or the call's own when tagged). Done for lib/plugin;
   the ledger's description and typed wrappers generated from a description are `cengine/bind`, run as
   `ext bundle bind` (done for lib/plugin and lib/ledger, whose generated bindings are compiled and run
   against the built libraries in its tests). The hand-written `engine/plugin/host/clib` is gone:
   `tests/crosslang` holds lib/plugin's generated binding, held to the differential checks through
   purego by a session adapter that is test code.
4. The `ext-plugin` release publishes its citation and the ccgo translations; the first release with
   the `ext_plugin_` names (after #211) is the first a citation can name. Ready for `ext-plugin-v0.4.0`
   and the first `ext-ledger-v0.1.0`: each platform archive carries `interface.json`, and the publish
   job writes `<library>.citation.json` (`scripts/library-citation.py` over the package's
   `scripts/release/<package>/citation.json`: cgo and purego on Linux and macOS, purego on Windows,
   whose MSVC static library cgo cannot link) and attaches it with `<library>.interface.json`. The
   ccgo translations are published from `ext-plugin-v0.4.2` (v0.4.1 failed to package them on Windows): six translate cells make and test each
   platform's, and publish it with its binding as `ext-plugin-X.Y.Z-c2go-<goos>-<goarch>.tar.gz`, an
   asset of its own (a citation picks an asset by target and driver).
5. Done for cgoengine: it stops carrying `_csrc`; its binding compiles against the headers and links the
   library a project fetched, given to cgo as `CGO_CFLAGS` and `CGO_LDFLAGS` (cgo's CFLAGS do not cross
   packages, so a fetched package cannot give them itself), and `scripts/cgoengine-sync.sh` and the drift
   test are gone. Done for c2goengine too: it keeps its generator and the binding beside the translation
   (built only with `-tags ext_c2go`), the committed translations are gone, and the translate jobs
   translate and test instead of comparing; `fetch` names a fetched translation for the project's
   package.
6. `cengine/library`: `Browse`, `Reserve` and `Borrow` for purego at run time, and `Shelve` for the
   build-time drivers.
7. The native ledger binding lives in `pkg/ext-go/cengine/ledger`; CTX uses
   `Bind`, `Library.OpenStore` and `NewGraph` there. Shared records
   live in `core/ledger`. Engine holds compatibility record aliases and
   never depends back on cengine. Cengine may consume core or engine.

### CTX ledger adoption

CTX now reaches `lib/ledger` through `pkg/ext-go/cengine/ledger.Bind`,
`Library.OpenStore` and `NewGraph`. The binding uses the existing generic
`cengine/library` interface with purego; it opens no Go recorder or Go store.
`Library.Objects` exposes the remaining described C operations. Additional driver
implementations remain a separate migration step.

Ledger-owned wire records live in `core/ledger/observation`; CTX owns host-selection policy. The Go recorder and SQLite store implementations were removed. The former recorder wrapper module has also been removed; consumers use `cengine/ledger.NewGraph` with an explicitly opened native store. The Go `NewMemory` implementation and Go supervisor have also been removed; use the native memory backend and `cengine/ledger/supervisor.OpenNative`.
CTX's `graphapi` owns location and upgrade policy, while collection, recording,
resolution of stored inventory, storage and reconciliation execute in C.

CTX uses its current `ledger/` C file store directly. The old `graph.db` importer
has been removed; existing SQLite files are left untouched and are not read.
Frozen SQL fixtures remain tests of the native SQLite backend.

The CLI bundle carries both C libraries. Ext gives private product guests the
ledger's library path, and standalone Go consumers explicitly load or provide
the library before calling `Bind`. Installation verifies a CTX graph operation
using the bundled ledger outside the source workspace.

### Authenticated nested shell calls

A C `command_scope` owns the reviewed dependency closure and its private package
copies. Its description exposes it as an object, including a result-bearing
`dispatch` event. The Go broker supplies a loopback transport; C authenticates
the per-consumer token before the owning product applies command policy.

CTX permits reads from a directly declared credential dependency and `real`
lookups for the consumer's declared native commands. Explicit credential selector
settings travel with the request, restricted to the selected provider's own
`EXT_<PROVIDER>_` namespace; routing tokens and process-loader settings cannot be
replaced. Provider-specific meanings remain in the provider adapter.

Closing the scope cancels and joins dispatch before removing snapshots. Admission
checks the entire closure under the package writer. Legacy adapter homes may
retain their configured regular trust file beside packages; all other unexpected
entries are rejected. The package acceptance gate requires authentication,
cancellation, snapshot cleanup, cross-product nested calls and the canonical C
ledger fixture suite. Go ledger canonicalization now uses C's
`canonical_transaction`; Go transaction preparation, traversal and fingerprint
implementations have been removed with its memory store and supervisor.

### Go host and translated ledger adoption

Go's default host now uses `cengine/plugin`: process backends and external Go
transports share the C session. The described `events` backend carries
connect/invoke/close, progress and reverse frames; Go retains transport work and
application policy. A described registry owns routing and cycle detection in C.
`engine/plugin/host` is removed and its consumers use `cengine/plugin/host`.
The old Go session remains as a differential-test oracle, not a production
fallback. Backend packages prepare transports; the caller selects the host.

The translated ledger uses the C portable profile on 64-bit Linux and macOS.
It implements memory/file stores, watches, recorder projections and canonical
transaction fingerprints. Collection and supervision fail as unsupported;
unsupported translation targets return `driver.ErrUnavailable`. Native and
translated portable profiles run the same fixtures through
`scripts/ledger-c2go.sh`; no optional check substitutes a Go implementation.
