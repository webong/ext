# c2goengine

The C libraries of `lib/`, translated to Go, one package each; the module has no root package.
`c2goengine/plugin` is lib/plugin's:

The C engine, translated to Go by [ccgo](https://pkg.go.dev/modernc.org/ccgo/v4) and linked into your
program: no cgo, no C compiler, the same answers as the C library.

```go
out, err := plugin.EngineCall("descriptor.validate", descriptorJSON)

session, err := plugin.Open(ctx, descriptor, plugin.Options{Verify: ..., Authorize: ..., Connect: ...})
session, err = plugin.OpenProcess(ctx, descriptor, plugin.LaunchOptions{...}, options)
leases, err := plugin.NewInstances(instance.Options[T]{...})
```

- `EngineCall` runs the engine's pure services (descriptor, request, response, frame, manifest, schema
  and endpoint rules; see [lib/plugin/services.md](../../../lib/plugin/services.md)) on bounded JSON,
  exactly as `pkg/ext-go/cgoengine.EngineCall` does through cgo. Nothing here starts a plugin.
- `Open` and `OpenProcess` are the engine's sessions: the host's state machine, admission, deadlines,
  draining, policies and observer events (host.c, run as Go), over a Go `plugin.Backend` or over a
  child process the engine itself starts. `NewInstances` is its lease manager.

## What it is for

`pkg/ext-go` has three ways to run the engine's rules:

| | needs | notes |
| --- | --- | --- |
| the pure-Go engine (`engine`) | nothing | the oracle: written separately, so a bug in the C cannot hide in it |
| `cgoengine` | cgo and the C library | the C itself, at native speed |
| `c2goengine` | nothing | the C, translated; about twice as slow as native and about twice as fast as the Go contract on the services measured |

`engine/plugin/plugintest/enginetest` holds an engine against the oracle: the same documents, requests,
schemas and endpoints, and the answers must agree. `TestEngineChecks` here runs every check of that
harness that needs only `EngineCall`, and the module's own tests add concurrency, memory and
platform-table checks.

## Platforms

The translation is generated for a platform on that platform (ccgo reads the platform's headers),
so there is one file per target. POSIX platforms translate the whole engine (the services and the
host); Windows translates the services only, because the host's filesystem and random-number
code there is Win32. The platforms are darwin/arm64, darwin/amd64, linux/arm64, linux/amd64,
windows/amd64 and windows/arm64.

The translation is not committed. This module holds the generator (`tools`) and the hand-written
binding beside the translation (`internal/ccgo`), which builds only with `-tags ext_c2go`, once a
translation is there; without it `Available` and `HostAvailable` are false and the calls return
`plugin.ErrUnsupported`.

## Getting the translation

A project fetches it from the ext-plugin release, which translates and tests every platform and
publishes each as the c2go driver's archive:

```sh
go tool ext bundle fetch -lib ext_plugin -driver c2go   # internal/extlib/plugin: the translation and its binding
```

The package it writes is the engine itself (`Call`, `Open`, `OpenProcess`, `NewInstances`), named for
the project, with each file named for its platform and no build tag needed.

## Generating it here

```sh
cd pkg/ext-go/c2goengine/tools
GOWORK=off go run .                     # internal/ccgo/generated_<goos>_<goarch>.go, not committed
cd .. && go test -tags ext_c2go ./...    # the translation with its binding, against the oracle
```

or `scripts/c2goengine-translate.sh`, which does both (and, given a version, stages the release's
archive: the generator's `-publish dir` writes the files of the package that build for the platform,
each named for it). The generator needs Go 1.26 or newer (ccgo's floor), a C compiler on the path for
the preprocessor, and `gofmt`. It copies `lib/plugin`'s `core_sources` (read from `CMakeLists.txt`)
into a temporary directory, applies the few edits ccgo needs (listed in `tools/main.go`, each checked
to match exactly once, none to the repository's files), and runs ccgo there with relative paths, so
the output is the same bytes wherever it runs. Each generated file names a digest of what it was made
from (the sources and every header without their comments, the flags, and the ccgo and libc
versions). The `c2goengine` workflow translates and tests every supported platform on each change.

`libc` is pinned in three places that must agree: `libcVersion` in `tools/main.go`, `tools/go.mod`
and this module's `go.mod`. The generator refuses to run when they differ.

## Windows

Both Windows platforms translate the services, not the host: `Available` is true and `EngineCall`
answers, while `HostAvailable` is false, so `Open`, `OpenProcess` and `NewInstances` return
`plugin.ErrUnsupported`. The host's filesystem and random-number code is Win32 (`fs_win32.c`,
`random_win32.c`) and its channel to a child process needs the Windows API; translating them is the
same work again.

The generator runs in an MSYS2 shell for the compiler's headers (MinGW-w64 gcc for amd64, clang on
arm64 where gcc has no port), which the workflow sets up. Three things stood in the way, all in the
generator's shims and none in the C:

- MinGW's `_mingw.h` declares `__int128` itself when the compiler's `__SIZEOF_INT128__` is missing,
  and ccgo cannot type-check that declaration; on Windows the macro stays defined (the engine never
  uses the type).
- ccgo's Windows libc has no `__fpclassify`, which `fpclassify` expands to, and no `clock_gettime64`,
  which winpthreads' inline `clock_gettime` calls. `tools/shim/windows.c` defines both, once: the
  wall clock from `_ftime64` (millisecond resolution), the monotonic clock from the performance
  counter.
- The shim is a source of its own and not part of the force-included header, because every
  translation unit includes the header and the definitions would be repeated.

## What the translation needed

ccgo translates C to Go over a Go reimplementation of libc. Getting this engine through it took
changes in the generator and none in the C:

- yyjson, the JSON library: its own switches for no 128-bit integers, no alignment or inline
  attributes, and no GCC inline assembly (edits to the copy of `yyjson.h` and `yyjson.c`, each
  checked to match exactly once).
- `<stdatomic.h>`: ccgo does not translate the `__c11_atomic` builtins, so a stand-in over the GCC
  ones it does translate.
- Condition variables (`tools/shim/compat.h`, `missing.c`): ccgo's libc does not implement
  condition-variable attributes, and takes timed-wait deadlines on the real-time clock where the
  engine's are on the monotonic one, so a wait that takes either replaces `pthread_cond_timedwait`.
  macOS's relative timed wait and the attribute functions are defined the same way.
- The channel to a child process (`channel.h`, eight functions): written in Go over `os/exec`
  (`internal/ccgo/channel.go`) in place of `channel_posix.c`, which needs `socketpair` and
  `posix_spawn`, absent from ccgo's libc. The C calls it through a table the Go code installs.

Every byte slice that crosses is copied into memory the translated code owns: it does pointer
arithmetic on what it is given, including the address one past the end, which a Go allocation does not
allow (the race detector's pointer checks stop the program).

The vet diagnostics ccgo's output attracts (unreachable code, `unsafe.Pointer` conversions) are not
ours to fix, so `scripts/check-standalone-modules.sh` and `scripts/release-standalone.sh` vet every
package of the module except `internal/ccgo`.

## Not here yet

- Progress events and reverse calls over `Open`: a plugin that declares them is refused, and
  `Options.Reverse` is unsupported. cgoengine carries both through the engine's frame hooks.
- Streams, the package store and the digests, which cgoengine also binds.
- The host on Windows (see above).
- `CallProgress`.

The checks of `enginetest` for all that exist in `cgoengine` only; moving them behind the harness
and binding the rest is the same work again for each.

## Ledger driver

`ledger.Bind()` uses the translated `lib/ledger` portable profile. Generate it
with `go run . -library ledger` in `tools`, then build with
`-tags ext_ledger_c2go`. `-publish <directory>` stages the translation and driver
without build tags, for a fetched package. Nothing is published by this command.

The source list is `ledger_portable_sources` in the C library's CMake file.
Memory/file storage, watches, validation, transaction fingerprints and recorder
projections execute translated C. Native collection and process supervision
return `unsupported`; Windows and non-64-bit translation return an unavailable
driver error. The current supported targets are Linux/macOS on amd64/arm64.

`bash scripts/ledger-c2go.sh` builds and tests the native portable profile,
regenerates the translation, and requires all translated ledger checks to run.
The c2go CI cells run it on their supported POSIX targets.

## Driver and backend boundaries

This module implements translated-C drivers and bindings, retaining its existing
build tags, supported-target checks and unavailable behavior. It does not own
Yaegi, WASM or HashiCorp guest execution; those are independent plugin backends
at their existing module paths. Shared plugin definitions live in `core/plugin`
and remain available through `engine/plugin` compatibility exports.
