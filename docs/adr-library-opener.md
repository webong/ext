# The library opener: ext-go carries no C

Status: accepted on 2026-11-22. Step 1 (the `core` module, its driver switcher and the plugin selection on
it) is built; the rest is the order in [Steps](#steps).

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

**2. `pkg/ext-go/core` is the switcher, and knows no library.** For each library of `lib/` it picks how
the program runs it: go (the pure-Go engine), purego (the engine's bindings to a built copy of the C
library), cgo or ccgo. Only purego needs a built copy at run time, so finding, fetching, verifying and
opening that copy is part of the switch, and the opener lives here too; the engine never touches files.
The `core` module holds:

| Package | Role |
| --- | --- |
| `core/driver` | The driver switcher: `cgo`, `c2go`, `purego`, `go`, preferred in that order. `Choose` takes one by name, by `EXT_ENGINE` or as the best available one that fits; a name that cannot be served is an error, never another driver. |
| `core/library` | The opener: loads a shared library once per canonical path through purego and binds its exports, and grows the verbs below. |

It requires purego and no ext module, and `core/driver` and `core/library` import nothing of ext
(`src/ctx/internal/arch` enforces it). The loader for plugin guests built as C shared libraries is a
plugin backend like yaegi, not a way of reaching a library of `lib/`, so it is its own module,
`pkg/ext-go/cshared`, which opens guests through `core/library`. A
library's own Go package lists its drivers and its citation; `pkg/ext-go/plugin` does that for the plugin
engine today. The module has no root package: the umbrella published its selection package at the
import path `pkg/ext-go/core` up to v0.12.0, and a root package here would give a program that requires
both an ambiguous import.

**3. The opener's verbs.** A library is reached through four steps, named for a library's lending desk:

| Verb | What it does |
| --- | --- |
| `Cite` | Builds a `Citation`, the pin, as plain data: the library's name (`ext_ledger`), its release version, the symbol that reports its version and the versions accepted, and per OS and architecture the file name, the download URL and its SHA-256. The library's Go package declares it; `core` holds none. |
| `Browse` | Lists the copies that match a citation without loading anything: the path the program supplied, then the cache, then the directories the program names. |
| `Reserve` | Produces a verified local copy. A supplied path is checked against the citation; otherwise the pinned file is fetched into the cache and checked against its SHA-256 before anything uses it. `Fetch` replaces the downloader (ext passes its own), `Offline` refuses any fetch, and the cache defaults to `os.UserCacheDir()/ext-go/library/<name>/<version>`. |
| `Borrow` | Loads a reserved copy through purego, calls the version symbol and refuses a version outside the citation. It returns the `Library` handle that `Symbol`, `Bind` and `BindAll` already serve. |

Fetching is one HTTPS GET of one file with no archive handling; the digest is checked before the file is
moved into the cache. A copy, once loaded, is never unloaded.

**4. Build-time drivers are shelved, not linked by `core`.** cgo and ccgo are fixed when a program is
built, so `core` cannot link an unknown library. A library's Go package registers them from files built
only with that driver (`library.Shelve(name, driver, implementation)`), and `Borrow` returns a shelved
implementation before it loads anything. With `Auto` the order is what the build shelved (cgo, then
c2go), then purego, then go. A program can name one driver or forbid one, such as purego where the
operating system refuses code fetched at run time.

**5. cgo links what the program supplies.** A cgo build finds the library and its header through
`pkg-config` or `CGO_CFLAGS`/`CGO_LDFLAGS`. ext-go ships declarations only: `cgoengine/_csrc`,
`scripts/cgoengine-sync.sh`, the drift test and the prebuilt static link go.

**6. c2go stays generated and pinned.** `c2goengine/<library>` is Go translated from one release of the
library; it shelves itself, and moving to a new release regenerates it.

**7. Releases publish shared libraries.** `lib/plugin` and `lib/ledger` release one shared library per
platform with its SHA-256 as release assets, and each library's citation is written from that release.

## Consequences

- ext-go's module graph holds no C sources, and a library added to `lib/` needs a citation and a list of
  drivers in its Go package, not a copy of its code.
- Only purego is a run-time choice. cgo and c2go are fixed by the build, so at run time `Choose` picks
  among what the build holds.
- iOS loads no code fetched at run time: there a program uses cgo with a statically linked library, c2go
  or pure Go, or a path to a library the app bundles.
- The pure-Go implementations stay as the fallback on every platform; the C library leads and they
  follow it, as [engine parity](plugin-engine-parity.md) says.

## Steps

1. Done: `pkg/ext-go/runtime` becomes the `core` module (`core/library`), `core/driver` is the
   switcher, the guest loader becomes the module `pkg/ext-go/cshared`, and the plugin selection moves to `pkg/ext-go/plugin` and chooses through it.
2. `core/library`: `Citation`, `Browse`, `Reserve` (supplied path, cache, fetch, `Fetch`, `Offline`) and
   `Borrow` with the version check, and `Shelve`.
3. Releases of `lib/plugin` and `lib/ledger` publish per-platform shared libraries and digests; the
   citations are written from them.
4. `cgoengine` links the supplied library and loses `_csrc`; the plugin engine gains a purego driver;
   `pkg/ext-go/plugin` lists it.
5. The ledger: its drivers (purego first, then cgo) and a `pkg/ext-go/ledger` that lists them; ctx moves
   onto it.
