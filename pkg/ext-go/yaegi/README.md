# pkg/ext-go/yaegi

An `engine.Backend` that runs a guest shipped as Go source, interpreted in the host's process by
[yaegi](https://github.com/traefik/yaegi). The guest is ordinary Go written against the engine's
guest API, the same source a compiled guest uses, so a host can load reviewed automation code without
a compiler, cgo or a child process.

The module depends on `pkg/ext-go/engine` and yaegi only (never on `cgoengine`; `src/ctx/internal/arch`
enforces it), and the umbrella `pkg/ext-go` does not require it.

## The guest

The guest is one package of `.go` files at the root of a directory. It exports the factory native Go
guests export (`nativego.Symbol`):

```go
package main

import (
	"context"
	"encoding/json"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/inprocess"
)

func handle(ctx context.Context, r plugin.Request) (json.RawMessage, error) { return r.Payload, nil }

func CTXPlugin(ctx context.Context) (plugin.Backend, error) {
	g, err := plugin.NewGuest(descriptor(), plugin.GuestOptions{Handler: handle})
	if err != nil {
		return nil, err
	}
	return inprocess.New(g) // a *plugin.Guest is an endpoint; inprocess makes it a Backend
}
```

`testdata/conformance/guest.go` is the reference conformance guest written this way.

What yaegi needs from the source:

- Name every parameter of a function the host calls, with `_` when unused: an unnamed parameter
  (`func(context.Context, plugin.Request)`) corrupts the call's frame in yaegi v0.16.
- The compiled generic APIs (`author.Method`, `author.Register`, `instance.New`) are not exported to
  interpreted code; build the handler with `plugin.NewGuest`. Generics in the guest's own code work.
- Subdirectories are not evaluated, so a guest has no local subpackages; other files in the tree (data,
  tests) are allowed and covered by the digest.
- `main`, if present, is not run.

## The package

The source ships as a tree artifact, named by an entrypoint whose runtime is `go-source`
(`yaegi.Runtime`). An entrypoint may name a tree under any runtime but `process` (a manifest rule of
both engines):

```json
"artifacts": [{"name": "guest", "path": "guest", "kind": "tree", "sha256": "<TreeDigest>"}],
"entrypoints": [{"name": "main", "runtime": "go-source", "artifact": "guest", "protocols": ["ext.plugin/v1"]}]
```

## The host

```go
options, err := yaegi.PackageOptions(installed, "", yaegi.Options{
	Packages: append([]string{"net/url"}, yaegi.DefaultPackages...),
	Timeout:  50 * time.Millisecond,
}, plugin.Options{Verify: trust, Authorize: authorize})
if err != nil { return err }
// host is a caller-opened cengine/plugin.Library.
session, err := host.Open(ctx, installed.Manifest.Descriptor, options)
```

`PackageOptions` re-verifies the installed package before anything is evaluated, then runs the caller's
`Verify`, then reads the tree once into memory, holds what it read to the artifact's digest and
evaluates that. `Open(ctx, dir, digest, options)` is the same below the session, for use inside a
`plugin.Options.Connect` of the host's own.

`Options`:

| Field | Meaning |
| --- | --- |
| `Packages` | the standard library the guest may import; nil is `DefaultPackages`, an empty list allows none |
| `Symbols` | packages the host exports to the guest (`interp.Exports`, keyed `<import path>/<name>`), each importable |
| `Timeout` | bounds each call without an earlier deadline; zero leaves only the caller's context |
| `Stdout`, `Stderr` | where `fmt` and `log` output goes; nil discards it |
| `MaxSourceBytes`, `MaxSourceFiles` | bounds on the source tree (16 MiB, 4096 files) |

## Limits

- **Imports.** Every file of the package is parsed before evaluation and an import outside the
  allow-list is refused with `plugin.ErrDenied`. `DefaultPackages` holds computation, encoding, text,
  time and synchronization. It leaves out everything that reaches the filesystem, network, processes,
  environment, runtime or reflection: `os`, `os/exec`, `unsafe`, `syscall`, `reflect`, `plugin`,
  `runtime`, `net`, `net/http`, `log`, `path/filepath` and the rest. A host that lists one of them, or
  hands in yaegi's `syscall` or `unsafe` exports through `Symbols`, grants it explicitly. The guest
  always may import `GuestPackages`: the engine's root package, its guest packages, `inprocess` and
  `schema`.
- **Package variables** are copied into each interpreter, so a guest that assigns `io.EOF` or
  `plugin.ErrDenied` changes its own copy. A variable that holds a pointer still shares what it points
  to.
- **Cancellation stops the interpreter.** A call whose context ends before it returns (the caller's
  deadline, `Timeout` or `Close`) and a panic in a call make the backend stop the interpreter through
  `EvalWithContext`: every interpreted goroutine stops at its next step, and every later call fails
  with `yaegi.ErrStopped`. The session fails, as it does on any canceled call.
- **What cannot be stopped.** An interpreted goroutine blocked inside compiled code (a long
  `time.Sleep`, a read on a channel the host never closes) cannot be forcibly stopped, as no Go
  goroutine can: the caller returns at once, and the goroutine finishes when that compiled call
  returns, with its result discarded. A panic in a goroutine the guest starts itself is not recovered
  and ends the host process, as it would in compiled code.
- **Not a sandbox.** The guest shares the host's memory and scheduler and can allocate without bound.
  Run only reviewed guests, as for native Go.

## Tests

`TestConformance` runs `engine/plugin/plugintest` against the reference guest interpreted from source, and
`TestHostCases` runs the shared host cases (`lib/plugin/testdata/v1/host-cases.json`, skipped without
the repository). The rest cover refused imports, host-chosen packages and symbols, digests, timeouts,
panics, output and package-variable isolation, and `PackageOptions` over an installed package.

`internal/symbols` holds yaegi's exports of the ext guest packages, generated by `yaegi extract`:
run `go generate ./internal/symbols` after the engine's exported API changes
(`TestSymbolsAreCurrent` fails until then).
