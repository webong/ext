# Build a CTX adapter

An adapter is a separate process that implements CTX's invocation contract. It
can be written in Go, a shell language, or another language. A ready-to-run
adapter needs a manifest, an executable for the target OS, and any runtime
assets or dependencies that executable uses.

Have `ctx` installed before using the build, pack, or install commands below.

## Required pieces

| Piece | What an author supplies |
| --- | --- |
| `adapter.toml` | API version, unique name, runtime, surfaces, executable name, and implemented capabilities. |
| Executable | Handlers for `validate`, `doctor`, and at least one of `run`, `open`, or `share`. Add `list` to discover selections. |
| Native integration | Product-specific discovery, selection validation, command routing, protocols, paths, and diagnostics. |
| Runtime dependencies | A native tool, script interpreter, or runtime assets where required. Document these requirements and diagnose missing dependencies; installation does not provision them. |
| Distribution package | A ready directory or one `.ctxadapter` archive per OS/architecture. A platform index is optional. |

For Go source builds, also supply a Go `main` package and a module context:
either your own `go.mod`, or an enclosing module such as this repository. Go
1.23 or newer is needed when using the current public CTX SDK. Users installing
prebuilt packages do not need Go.

Product-specific behavior belongs in the adapter. CTX core handles generic
selection, validation, trust, and dispatch. Related adapters can reuse an engine
owned by their family adapter, with each product supplying its own configuration.
See [architecture and ownership](adapter-api.md#architecture-and-ownership).

## 1. Choose the runtime and operations

| Runtime | Typical integration | Surface |
| --- | --- | --- |
| `computer` | Host CLI, cloud profile, database client, interpreter, or application | `shell` for commands; `web` for opening URLs |
| `manager` | Container/workload engine or desktop manager | Usually `shell` |
| `browser` | Browser profiles, resources, and page workflows | `web` for URL opening; add `shell` if implementing `run` |

Declare only operations the executable implements. `run` requires `shell`;
`open` requires `web`. Both surfaces can be declared. Capabilities describe
operations; `supports` describes resource kinds and does not implement an
operation by itself.

## 2. Write the manifest

A small selectable Go CLI adapter can start with:

```toml
api_version = "2.0"
name = "my_tool"
runtime = "computer"
surfaces = "shell"
executable = "ctx-my-tool"
executable_windows = "ctx-my-tool.exe"
description = "Route my tool through a project-selected context"
capabilities = "list,validate,run,doctor"
selector_key = "my_tool"
commands = "my_tool"
go_entry = "."
```

Use a unique lowercase name containing letters, numbers, or underscores; CTX
reserves its runtime, surface, and command names. Executable fields are file
names within the package. On Unix, the executable needs executable permissions.
On Windows, `executable_windows` overrides `executable`; Go builds require an
`.exe` name. Script packages can use supported `.ps1`, `.cmd`, or `.bat` entry
points on Windows.

The current manifest parser uses quoted string values and comma-separated
strings for lists, as shown above. Use `surfaces = "shell,web"`, not a TOML
array. Use standalone comment lines rather than trailing comments on values.

Optional fields include `extra_keys`, `override_env`, and `package_files`.
Set `selectable = "false"` for a share-only adapter with no context selection.
Use `dependencies_darwin`, `dependencies_linux`, or `dependencies_windows` to
declare separately installed share-space adapters, for example
`dependencies_darwin = "credential:keychain@2.0"`. Catalog selection installs
missing dependencies; manual package installation leaves them to the user.
`go_entry` defaults to `.` and can point to a relative main package such as
`./cmd/ctx-my-tool`. `package_files = "templates,policy.json"` includes assets
in a Go build. Keep credentials outside package files and project selectors.
The [manifest reference](adapter-api.md#manifest) describes specialized fields.

## 3. Implement the process contract

For a selectable adapter, CTX invokes the executable as follows:

| Invocation | Required behavior |
| --- | --- |
| `ctx-my-tool list` | Print one available selection per line on stdout. This capability is optional. |
| `ctx-my-tool validate SELECTION` | Check that the selection is usable; return nonzero for an invalid selection. |
| `ctx-my-tool doctor SELECTION` | Diagnose dependencies and selection health. The selection can be empty; diagnostics should not change configuration. |
| `ctx-my-tool run SELECTION -- ARGUMENTS...` | Execute the native command in the selected context and preserve its arguments and exit status. |
| `ctx-my-tool open SELECTION -- ARGUMENTS...` | Open the requested URLs or resources using the selected native context. |
| `ctx-my-tool share SELECTION -- SPACE ARGUMENTS...` | Implement the declared sharing space and its resource operations. |

Implement `validate`, `doctor`, and at least one of the last three operations.
Computer endpoints without a selector use `run -- ARGUMENTS...` when there is
no selection. Other operations and structured protocols are covered by the
[process reference](adapter-api.md#process-protocol).

Browser `list` output uses `name:profile` values; its native handlers receive the
profile portion as the selection. Set `selector_key = "browser"` for this route.
Browser sharing and management use their documented JSON protocols in addition
to the process arguments.

CTX provides `CTX_ADAPTER_API`, `CTX_ADAPTER_NAME`, `CTX_ADAPTER_COMMAND`,
`CTX_ADAPTER_REAL_COMMAND`, `CTX_PROJECT_DIR`, `CTX_PROFILE`, and declared values
through `CTX_ADAPTER_VALUE_<UPPERCASE_KEY>`. Use the provided real command path
when routing a native CLI behind a CTX shim, and honor native CLI/environment
overrides. Forward arguments as individual arguments, without constructing a
shell command from user input.

Keep result data on stdout and diagnostics on stderr. Exit codes are `0` for
success, `1` for validation/runtime failure, `2` for usage errors, and `127` for
a missing dependency. Adapters run as child processes; environment changes in
the adapter do not change CTX's parent shell.

Go adapters can parse invocations with:

```go
request, err := plugin.ParseAdapterInvocation(os.Args[1:])
// Handle the error, then dispatch request.Operation.
// request.Selection and request.Arguments preserve the invocation fields.
```

Import `github.com/webong/ext/pkg/plugin`. The complete
[Go echo example](../examples/adapters/go_echo/main.go) implements the basic
handlers; the [shell echo example](../examples/adapters/echo/ctx-echo) shows the
same contract without the Go SDK. These examples echo arguments; replace that
behavior with your native integration.

## 4. Build and package

### Go source

```text
my-adapter/
  adapter.toml
  go.mod
  main.go
  templates/       # optional runtime assets
```

For a separate module developing against a local CTX checkout, run these commands
inside your adapter source directory. Replace the absolute checkout path:

```sh
go mod init example.com/my-adapter
go mod edit -require=github.com/webong/ext@v0.0.0
go mod edit -replace=github.com/webong/ext=/absolute/path/to/ctx
go mod tidy
```

The local replacement supplies the SDK during development. For published source,
use a published CTX module version or commit available to your builders. The
compiled package does not need the source checkout or this replacement.

Build a package for your current machine, or select an explicit target:

```sh
ctx adapter build ./my-adapter
ctx adapter build ./my-adapter --os darwin --arch arm64
ctx adapter build ./my-adapter --os windows --arch amd64
```

The default output is `my-adapter/dist/my_tool-<os>-<arch>.ctxadapter`.
`--output` selects another destination. The build ships the manifest, compiled
executable, and `package_files`; source files and `go.mod` are excluded unless
explicitly included. Cross builds default to `CGO_ENABLED=0` unless set by the
author; native libraries may require a suitable target toolchain.

To build the repository's complete example, run this from the CTX checkout:

```sh
ctx adapter build ./examples/adapters/go_echo
```

### Other languages or an already-built executable

Prepare a directory with only the manifest, target executable, and runtime files:

```text
ready-package/
  adapter.toml
  ctx-my-tool
  templates/
```

```sh
chmod +x ./ready-package/ctx-my-tool
ctx adapter pack ./ready-package --os darwin --arch arm64 \
  --output ./my_tool-darwin-arm64.ctxadapter
```

`pack` archives every regular file in that directory and adds platform metadata;
it does not compile or convert executables. A Windows package must contain its
Windows entry point. Interpreted adapters still need their interpreter installed.

## 5. Install, review, and trust

On a matching platform:

```sh
ctx adapter install ./my-adapter/dist/my_tool-darwin-arm64.ctxadapter
ctx adapter inspect my_tool
# Review the installed manifest, executable, and runtime files.
ctx adapter trust my_tool
ctx ls my_tool
ctx set my_tool local
ctx adapter doctor my_tool
```

Use a selection your implementation actually returns; `local` is an example.
Installation validates the manifest, platform, and archive paths, copies files
to the active adapter store, and leaves a direct installation untrusted. It
does not compile source or execute adapter code. Trust records all package file
checksums and installs declared command shims where applicable. Editing package
files requires reviewing and trusting the changed content again. Trust allows
the adapter to execute with the user's permissions; it is not an OS sandbox.

For a ready directory containing the host executable,
`ctx adapter test ./ready-package` validates the package and runs its `doctor`
operation. It executes adapter code and does not check every declared operation.
Authors should also exercise discovery, invalid selections, argument forwarding,
missing dependencies, and the native operations they declare.

## 6. Add specialized capabilities or publish

- **Browser resources:** declare `share` and `browser_share`; implement the
  [browser resource protocol](adapter-api.md#process-protocol).
- **Browser installation and sessions:** declare `share` and the implemented
  `browser_management` operations; use the
  [browser management contracts](browser-management.md). Preserve native
  consent and distinguish registration, session activation, and verified
  persistent installation.
- **Engine builds and transfers:** implement the manager operations documented
  in the [process protocol](adapter-api.md#process-protocol).
- **Graph discovery:** optionally implement
  [machine observation](adapter-api.md#machine-graph-observation).
- **Distribution:** publish platform archives or generate a `.ctxadapter.json`
  index. Remote installation requires HTTPS and a supplied SHA-256 digest; the
  index selects the OS/architecture and pins each archive's digest. See
  [binary packaging and indexes](adapter-api.md#binary-packages-and-go-builds).

Third-party adapters use the same contract as maintained adapters. Direct
installation does not require adding the adapter to CTX's optional catalog.
