# One `ext` command, with ctx and ctn as libraries

Status: command composition is implemented. `ext` is the main installation and
combined command entry point; CTX and CTN remain standalone products. See
[adapter management](adr-adapter-management.md) for current routes and the
remaining direct `ext adapter` work. Release and installer rollout steps below
record the original plan.

`ctx` and `ctn` are products under `src/`: modules that nothing may import, whose
module paths omit `src/` and so cannot be fetched with `go get`, released only as
binaries (`ctx-vX.Y.Z`, `ctn-vX.Y.Z`). That makes them the one part of ext that is
not reusable, although ext is a plugin system in which a guest can also be a host.
The proposal is to turn both into libraries, keep their standalone binaries, and add
one command, `ext`, that runs either of them or any other command.

## Decisions

**ctx and ctn become libraries, and stay exactly where they are.** `src/ctx` and
`src/ctn` do not move and their module paths do not change (`github.com/webong/ext/ctx`
and `.../ctn`). Each stays its own Go module, with its private code in `internal/` and
a small exported API, and both keep a `cmd/` directory in the same module with the
standalone `main`. Because the paths still omit `src/`, the modules are not
`go get`-resolvable; they are built from source with the workspace and `replace`
directives, as they are today, and are released as binaries. Nothing about their layout
or names is part of this change.

**One entry contract.** Each library exports the entry point it already has
internally, `app.Run(args []string, stdout, stderr io.Writer) int`, as
`ctx.Run` and `ctn.Run` with the same signature. The standalone `main` is
`os.Exit(ctx.Run(os.Args[1:], os.Stdout, os.Stderr))` and nothing else. A command
must behave the same however it was started, so `ext ctx <args>` and `ctx <args>` are
the same code with the same exit code.

**`ext` is a third front door.** `cmd/ext` is its own module at `github.com/webong/ext/cmd/ext`,
built from source like the products it composes (it requires them through `replace`
directives, so it is not installed with `go install`). The repository root stays
workspace-only: it holds `go.work` and no module. `ext <command> <args>` looks the
command up in a registry and runs it. `ext ctx ...` and `ext ctn ...` are the first
two entries. `ext` is a plugin host, so it runs any other command a package
declares through the same path.

**ctx and ctn do not import each other.** Only `ext` imports both, and
`src/ctx/internal/arch` enforces it: a product never imports
another product, and shared code goes in `pkg/` or `res/`.

## The command registry

A command can be loaded two ways, and `ext` records which:

| Source | What it is | Transport | Trust |
| --- | --- | --- | --- |
| `builtin` | compiled into `ext` and called through `Run` | in process | as trusted as the `ext` binary |
| `package` | an installed ext package that declares the command and is run as a guest | the package's declared transport | the package trust digest, as for any package |

Each registry entry holds the command `name`, its `source`, its `version` (the
library version for a builtin, the package version for a package), and, for a
package, the digest and trust state. `ext` registers `ctx` and `ctn` as builtins at
startup and registers every installed package that declares commands.

**Introspection.** `ext commands` lists every entry with its source and version, and
`ext which <command>` says which entry would run and why. How a command was loaded is
never inferred from the environment.

**No silent shadowing.** When a package declares a name a builtin already has, the
builtin wins and the package is listed as shadowed. The package runs only when the
user chooses it, per invocation (`ext --source=package ctx ...`) or as a standing
choice in the ext config (`EXT_HOME`), and `ext which` reports the active choice and
where it came from.

**Guests can be hosts.** A command run as a package guest can host its own
adapters over session-scoped reverse calls, so `ctx` loads its adapters the same way
in either mode. This is the peer model of the plugin contract, and ctx and ctn are
its first first-party users.

## Releases and tags

Binary tags only: `ctx-vX.Y.Z` and `ctn-vX.Y.Z` stay, and `ext-vX.Y.Z` is added. They
are not Go module versions, and `install.sh` and `install.ps1` keep resolving the newest
`ctx-v` release by tag. `ctx`, `ctn` and `ext` take no module tags, because their
module paths do not match their directories and nothing outside the workspace imports
them. Each `ext` release states the `ctx` and `ctn` source revisions it was built from.

`ext ctx` runs the ctx version `ext` was built with, which can differ from a
standalone `ctx` on the same machine. `ext version` prints the embedded versions so
the difference is visible.

## Install

`install.sh` and `install.ps1` keep installing `ctx` as today. They gain an `ext`
binary next to it (`--ext` first, then by default once `ext` ships), installed to the
same `CTX_BIN_DIR`. Configuration already has one home for every ext product
(`EXT_HOME`), and `ext` uses it, so the registry choices live beside the adapter
trust state. Nothing about an existing `ctx` installation changes.

## Order of work

1. Export `Run` from both libraries and make the standalone `main` a one-liner.
2. Add `cmd/ext` with the registry and the two builtins, and `ext commands`,
   `ext which`, `ext version`.
3. Add package-sourced commands: the manifest field that declares them, the host
   path, trust, shadowing and the pin.
4. Installers and the first `ext` binary release.

Each step is its own pull request and lands green before the next starts.

## Decided questions

These were open in the first draft and are settled the following way:

- **The source choice is per command.** A package overrides a builtin only for the
  command it is chosen for, either per invocation (`--source=package`) or as a
  standing entry for that command in the ext config. There is no global switch, so
  choosing a package `ctx` never changes how `ctn` runs.
- **`ext` forwards only declared commands.** A name runs only if a builtin or a
  package declares it. Unknown names are an error with the closest match, and are
  never passed to an adapter, so a typo cannot run something.
- **`ext` is the main entry point.** It provides installation and runs CTX, CTN,
  and installed commands. CTX and CTN keep shipping standalone and consume the
  ext adapter capabilities available to them. Product selection and dispatch
  remain product-owned; generic adapter contracts and trust remain library-owned.
- **Release order.** The pending `contract` release (v0.8.0: the `instance.Manager`
  disposal ordering and `plugintest/enginetest`) is independent of this work, because
  nothing here moves or renames a module. It can ship before, during or after any step.
