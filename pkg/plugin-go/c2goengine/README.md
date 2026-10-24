# c2goengine

The C engine's portable core, translated to Go by [ccgo](https://pkg.go.dev/modernc.org/ccgo/v4)
and linked into your program: no cgo, no C compiler, the same answers as the C library.

```go
out, err := c2goengine.EngineCall("descriptor.validate", descriptorJSON)
```

`EngineCall` runs the engine's pure services (descriptor, request, response, frame, manifest,
schema and endpoint rules; see [lib/plugin/services.md](../../../lib/plugin/services.md)) on bounded
JSON, exactly as `pkg/plugin-go/cengine.EngineCall` does through cgo. Nothing here starts a plugin.

## What it is for

`pkg/plugin-go` has three ways to run the engine's rules:

| | needs | notes |
| --- | --- | --- |
| the pure-Go contract (`contract`) | nothing | the oracle: written separately, so a bug in the C cannot hide in it |
| `cengine` | cgo and the C library | the C itself, at native speed |
| `c2goengine` | nothing | the C, translated; about twice as slow as native and about twice as fast as the Go contract on the services measured |

`contract/plugintest/enginetest` holds an engine against the oracle: the same documents, requests,
schemas and endpoints, and the answers must agree. `TestEngineChecks` here runs every check of that
harness that needs only `EngineCall`, and the module's own tests add concurrency, memory and
platform-table checks.

## Platforms

The translation is generated for a platform on that platform (ccgo reads the platform's headers),
so there is one file per target:

| platform | file | state |
| --- | --- | --- |
| darwin/arm64 | `internal/ccgo/generated_darwin_arm64.go` | checked in, tested |
| linux/arm64 | `internal/ccgo/generated_linux_arm64.go` | checked in, tested |
| darwin/amd64 | `internal/ccgo/generated_darwin_amd64.go` | checked in, tested under Rosetta |
| linux/amd64, windows/amd64, windows/arm64 | | not yet generated: the workflow builds and tests each and uploads the file |

On any other platform `Available` is false and `EngineCall` returns `plugin.ErrUnsupported`.
`internal/ccgo/targets_test.go` keeps the file list and the build constraints in step.

## Regenerating

```sh
cd pkg/plugin-go/c2goengine/tools
GOWORK=off go run .
```

writes `internal/ccgo/generated_<goos>_<goarch>.go` for the machine it runs on. It needs Go 1.26 or
newer (ccgo's floor), a C compiler on the path for the preprocessor, and `gofmt`. The tool copies
`lib/plugin`'s `core_sources` (read from `CMakeLists.txt`) into a temporary directory, applies the
few edits ccgo needs (listed in `tools/main.go`, each checked to match exactly once, none to the
repository's files), and runs ccgo there with relative paths, so the output is the same bytes
wherever it runs. It also writes `internal/ccgo/inputs.sha256`, a digest of what the translation is
made from (the sources as ccgo sees them, the flags, and the ccgo and libc versions), the same on
every platform, and each generated file names the digest it was made from. The bytes of a platform's
file also follow its system headers, so they are not compared; the digest is. The `c2goengine`
workflow does this on every supported platform and fails when a
checked-in translation is stale; run it, on every supported platform, after changing anything under
`lib/plugin/src`, `include` or `vendor`.

`libc` is pinned in three places that must agree: `libcVersion` in `tools/main.go`, `tools/go.mod`
and this module's `go.mod`. The generator refuses to run when they differ.

## What the translation needed

ccgo translates C to Go over a Go reimplementation of libc. For this core that took four changes,
all in the generator and none in the C: yyjson's own switches for no 128-bit integers, no alignment
or inline attributes, and no GCC inline assembly (a one-line edit to the copy of `yyjson.h`), and a
`<stdatomic.h>` stand-in over the GCC atomic builtins ccgo does translate.

The vet diagnostics ccgo's output attracts (unreachable code, `unsafe.Pointer` conversions) are not
ours to fix, so `scripts/check-standalone-modules.sh` and `scripts/release-standalone.sh` vet every
package of the module except `internal/ccgo`.

## Not here yet

The host: sessions, process ownership, instances and streams, which `cengine` reaches through the C
library's host API. A spike translated the whole host and ran the engine's own C tests over it
(threads, reverse calls, progress frames, and child processes through a channel layer written in Go
in place of `channel_posix.c`); wiring that behind the same Go API is the next step.
