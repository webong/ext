# c2goengine

The C engine, translated to Go by [ccgo](https://pkg.go.dev/modernc.org/ccgo/v4) and linked into your
program: no cgo, no C compiler, the same answers as the C library.

```go
out, err := c2goengine.EngineCall("descriptor.validate", descriptorJSON)

session, err := c2goengine.Open(ctx, descriptor, plugin.Options{Verify: ..., Authorize: ..., Connect: ...})
session, err = c2goengine.OpenProcess(ctx, descriptor, c2goengine.LaunchOptions{...}, options)
leases, err := c2goengine.NewInstances(instance.Options[T]{...})
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

`engine/plugintest/enginetest` holds an engine against the oracle: the same documents, requests,
schemas and endpoints, and the answers must agree. `TestEngineChecks` here runs every check of that
harness that needs only `EngineCall`, and the module's own tests add concurrency, memory and
platform-table checks.

## Platforms

The translation is generated for a platform on that platform (ccgo reads the platform's headers),
so there is one file per target. POSIX platforms translate the whole engine (the services and the
host); Windows translates the services only, because the host's filesystem and random-number
code there is Win32.

| platform | file | state |
| --- | --- | --- |
| darwin/arm64 | `internal/ccgo/generated_darwin_arm64.go` | checked in, tested natively |
| darwin/amd64 | `internal/ccgo/generated_darwin_amd64.go` | checked in, tested under Rosetta |
| linux/arm64 | `internal/ccgo/generated_linux_arm64.go` | checked in, tested natively |
| linux/amd64 | `internal/ccgo/generated_linux_amd64.go` | checked in, generated and tested by the workflow |
| windows/amd64 | `internal/ccgo/generated_windows_amd64.go` | checked in, generated and tested by the workflow (services only) |
| windows/arm64 | `internal/ccgo/generated_windows_arm64.go` | checked in, generated and tested by the workflow (services only) |

On any other platform `Available` and `HostAvailable` are false and the calls return
`plugin.ErrUnsupported`. `internal/ccgo/targets_test.go` keeps the file list and the build
constraints in step, and the generator rewrites the constraints when it writes a file.

## Regenerating

```sh
cd pkg/ext-go/c2goengine/tools
GOWORK=off go run .
```

writes `internal/ccgo/generated_<goos>_<goarch>.go` for the machine it runs on. It needs Go 1.26 or
newer (ccgo's floor), a C compiler on the path for the preprocessor, and `gofmt`. The tool copies
`lib/plugin`'s `core_sources` (read from `CMakeLists.txt`) into a temporary directory, applies the
few edits ccgo needs (listed in `tools/main.go`, each checked to match exactly once, none to the
repository's files), and runs ccgo there with relative paths, so the output is the same bytes
wherever it runs. It also writes `internal/ccgo/inputs.sha256`, a digest of what the translation is
made from (the sources it is given and every header, as ccgo sees them, the flags, and the ccgo and
libc versions), the same on every platform, and each generated file names the digest it was made from.
The bytes of a platform's file also follow its system headers, so they are not compared; the digest is.
The digest reads the C files without their comments, trailing whitespace and blank lines
(`tools/digest.go`), ignores files that are not C (a README, a license) and `.c` files the translator
is not given (`store.c` and `trust.c`, which are built into the C engine but not translated), so
rewording a comment or a document, or adding a source the translation never reads, does not make a
translation stale; any change to a translated source or a header does. The `c2goengine` workflow does
this on every supported platform and fails when a checked-in translation is stale; run it, on every
supported platform, after changing a translated source or a header under `lib/plugin/src`, `include`
or `vendor` (the artifacts of the failed run have the files for the platforms you cannot run).

`GOWORK=off go run . -sync` only brings the build constraints of the hand-written files in line with the
generated files that exist (the workflow's artifact for a new platform has the file; commit it, then sync).

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
