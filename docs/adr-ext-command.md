# One `ext` command, with ctx and ctn as libraries

Status: proposed. Nothing here is implemented; this page records the intended
design so it can be reviewed before any code moves.

`ctx` and `ctn` are products under `src/`: modules that nothing may import, whose
module paths omit `src/` and so cannot be fetched with `go get`, released only as
binaries (`ctx-vX.Y.Z`, `ctn-vX.Y.Z`). That makes them the one part of ext that is
not reusable, although ext is a plugin system in which a guest can also be a host.
The proposal is to turn both into libraries, keep their standalone binaries, and add
one command, `ext`, that runs either of them or any other command.

## Decisions

**ctx and ctn become libraries.** `src/ctx` moves to `pkg/ctx` and `src/ctn` to
`pkg/ctn`. Each stays its own Go module, with its private code in `internal/` and a
small exported API, and is `go get`-resolvable. Both keep a `cmd/` directory in the
same module with the standalone `main`. Hard cut: the old `src/` directories and the
`github.com/webong/ext/ctx` and `.../ctn` module paths are removed, with no
forwarding stub, and `src/` goes away as a container.

**One entry contract.** Each library exports the entry point it already has
internally, `app.Run(args []string, stdout, stderr io.Writer) int`, as
`ctx.Run` and `ctn.Run` with the same signature. The standalone `main` is
`os.Exit(ctx.Run(os.Args[1:], os.Stdout, os.Stderr))` and nothing else. A command
must behave the same however it was started, so `ext ctx <args>` and `ctx <args>` are
the same code with the same exit code.

**`ext` is a third front door.** `cmd/ext` is its own module at `github.com/webong/ext/cmd/ext`
(`go install github.com/webong/ext/cmd/ext@latest`). The repository root stays
workspace-only: it holds `go.work` and no module. `ext <command> <args>` looks the
command up in a registry and runs it. `ext ctx ...` and `ext ctn ...` are the first
two entries. `ext` is a plugin host, so it runs any other command a package
declares through the same path.

**ctx and ctn do not import each other.** Only `ext` imports both, and
`src/ctx/internal/arch` (moved with ctx) enforces it: a product never imports
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

There are two tag families, because a library and a binary are different things:

- **Module tags** for the libraries and the command module: `pkg/ctx/vX.Y.Z`,
  `pkg/ctn/vX.Y.Z`, `cmd/ext/vX.Y.Z`, versioned and released with the rest of the
  modules by the method in [module versioning](module-versioning.md).
- **Binary tags** stay `ctx-vX.Y.Z` and `ctn-vX.Y.Z`, and add `ext-vX.Y.Z`. They are
  not Go module versions, and `install.sh` and `install.ps1` keep resolving the
  newest `ctx-v` release by tag. Each binary release states the library version it was
  built from.

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

1. Move `src/ctx` and `src/ctn` to `pkg/ctx` and `pkg/ctn`: module paths, `go.work`,
   the arch tests, `AGENTS.md`, docs, the release workflows and scripts. Behavior is
   unchanged; the standalone binaries are byte-for-byte equivalent in behavior.
2. Export `Run` from both libraries and make the standalone `main` a one-liner.
3. Add `cmd/ext` with the registry and the two builtins, and `ext commands`,
   `ext which`, `ext version`.
4. Add package-sourced commands: the manifest field that declares them, the host
   path, trust, shadowing and the pin.
5. Installers and the first `ext` binary release.

Each step is its own pull request and lands green before the next starts.

## Open questions

- Whether the standing source choice is per command or global, and its config key.
- Whether `ext` should also forward unknown names to installed adapter commands, or
  only to commands that packages declare explicitly.
- Whether `ext` replaces `ctx` as the documented entry point, or stays an addition.
