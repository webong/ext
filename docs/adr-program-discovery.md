# ADR: Program discovery and installers

Status: proposed. A spike on the `program-discovery` branch builds `res/system/program`,
the `programs` operation in `ctx`, one installer adapter (`homebrew`) and the `php`
migration, and was run end to end: with only those two adapters trusted, `php`'s
context reports `manager = homebrew` and the `php` adapter contains no Homebrew code.
`ctx graph programs` is not built yet.

## Problem

A runtime adapter has to answer "which copies of this program are on the machine,
and which are safe to run?". Today each adapter answers alone. `adapters/php/ctx-php`
asks Homebrew (`brew list --formula --versions`), then falls back to the `php` on
`PATH`. So `php` knows about Homebrew and nothing else: not asdf, phpbrew, MacPorts,
apt or a hand-built prefix. `python`, `node` and `ruby` would each repeat that work,
with a different set of installers each, and every installer would be learned once
per language.

Two properties of the current graph shape the fix:

- `ctx graph scan` runs every adapter's `observe` independently and concurrently. An
  adapter cannot see another adapter's result during a scan.
- Resource relations are adapter-local: a relation joins two resources reported by
  the same adapter. A `php` resource cannot be related to a `homebrew` resource.

## Vocabulary

A **program** is an executable with a role: an interpreter, compiler, transpiler,
bundler or runtime. A **runtime adapter** owns one family of programs (`php`, later
`python`, `node`).

An **installer** is the tool that put a program on the machine or that owns where
it lives: Homebrew, asdf, nvm, pyenv, SDKMAN, uv, rustup, apt, winget, scoop. The
word `manager` is taken: a `manager` runtime means a workload manager such as
Docker or Podman. An installer adapter declares `supports = "installer"`.

Roles are values of `supports`. They come on two independent axes, and an adapter
declares as many as apply, followed by its kind.

**Levels** say what boundary a program virtualizes. `virtualizer` is the umbrella,
and every adapter that has a level also declares `virtualizer`, so
`--supports virtualizer` lists all of them and a level narrows the list. The levels
are defined by the boundary, not by the mechanism:

| Level | Boundary | Examples |
| --- | --- | --- |
| `evaluator` | a process or a language | V8, Zend PHP, Wasm, the JVM |
| `executor` | the operating system, with a shared kernel | Docker, LXC |
| `emulator` | a whole system | QEMU, RVVM, VMware ESXi, KVM |

KVM and ESXi are hypervisors and not strict emulators; the level names the boundary
they virtualize, which is the whole machine.

**Functions** say what a program does with code: `interpreter`, `compiler`,
`transpiler` and probably `bundler`. A function has no boundary: `javac` is a
compiler and virtualizes nothing, while PHP is both an interpreter and an evaluator.

The kind (`php`, `jvm`, `docker`) comes last, as it does today. Examples:

| Adapter | `supports` |
| --- | --- |
| `php` | `virtualizer,evaluator,interpreter,php` |
| `jvm` | `virtualizer,evaluator,compiler,jvm` |
| `docker` | `virtualizer,executor,container,docker` |
| `qemu` | `virtualizer,emulator,qemu` |
| `orbstack`, `rancher_desktop` | `virtualizer,emulator,executor,container` |

This replaces `engine` and the draft's "mirroring `engine,jvm`" wording. The
vocabulary is open: core does not enumerate it.

## Decisions

### Who owns what

| Concern | Owner |
| --- | --- |
| Candidate type, scanning `PATH` for given names, resolving links, merging records for one executable | `res/system/program`. Mechanism only: it never branches on a program or installer name. |
| Where an installer keeps programs, and what it installed | the installer adapter, through a new `programs` operation |
| What a program is: its version, its selection name (`8.3`), whether it is usable | the runtime adapter, which owns the language |

This keeps native behavior out of the shared library, as `AGENTS.md` requires, and
means a new installer teaches every runtime adapter at once while a new language
adds one small adapter.

### The `programs` operation

A new optional adapter capability, `programs`. CTX calls
`<adapter> programs NAME...` and expects one JSON object on stdout:

```json
{
  "version": 1,
  "programs": [
    {"name": "php", "path": "/opt/homebrew/opt/php@8.3/bin/php",
     "realpath": "/opt/homebrew/Cellar/php@8.3/8.3.12/bin/php",
     "version": "8.3.12", "attributes": {"formula": "php@8.3"}}
  ]
}
```

The names are the union of `computer_commands` across installed adapters, so the
installer is asked about what someone has declared interest in and never to dump
everything it owns (Homebrew alone owns hundreds of executables). The record limits
of `observe` apply: 1 MiB, unknown fields rejected, 10 seconds. `version` from an
installer is a hint (a package manager knows it for free); the runtime adapter
probes the real value.

An installer adapter still provides `validate`, `doctor` and `run`, like every
adapter. For `homebrew`, `run` runs `brew`.

### Data flow

1. CTX builds the list of names from every adapter's `computer_commands`.
2. It scans `PATH` for those names with `res/system/program`. This only stats
   files; it executes nothing.
3. It calls `programs NAME...` on each trusted installer adapter.
4. `program.Merge` joins both lists by resolved path. A program found by an
   installer carries that adapter's name in `installers`. A program found only on
   `PATH` carries no installer and `source = "path"`.
5. For each runtime adapter, CTX passes the merged candidates whose name matches one
   of its `computer_commands` in `CTX_PROGRAMS`, on every operation. The value is one
   line per candidate with tab-separated `name`, `path`, `realpath`, `version`,
   `installers` (comma-joined) and `on_path` (`1` or `0`), so a shell adapter reads
   it without a JSON tool. The variable is set whenever CTX looked, and empty when
   it found nothing; unset means the adapter was not run by CTX.
6. The runtime adapter probes each candidate (`php -r 'echo PHP_VERSION;'`), derives
   its selection names and reports contexts with `installer` as an attribute.

Run outside CTX, with the variable unset, a runtime adapter looks at `PATH` only.
Because the spike's installer is a shell script, a field with a control character
is dropped from `CTX_PROGRAMS` rather than escaped.
There is no second discovery path hidden in the adapter and no installer-specific
fallback.

Graph relations stay adapter-local, so attribution is an attribute, not an edge:
a runtime context has `manager = "homebrew"` (several installers are joined with a
comma, and `path` means none claimed it). A later `ctx graph programs [NAME]
[--installer NAME]` could show the merged view; the spike leaves it out.

### Trust and safety

A candidate is an observation, not permission to run it, the same rule as
`ResolveInventory`. CTX never executes a candidate. Only the trusted runtime
adapter does, and only for paths it was handed. `PATH` entries that are relative or
empty are skipped, so a scan never depends on the working directory. A program
reached through a link is recorded under its resolved path, so a link outside any
installer's tree stays `source = "path"` instead of borrowing an installer's name.
An installer's claim is checked by the merge: it must match a path that exists.

### Cost

The scan is `stat` calls bounded by the declared names. Installer queries are
bounded by the 10 second limit and run concurrently with the existing observation
pool. Nothing is probed by executing it until a runtime adapter does so for its own
program. Results are written to the graph with `observed_at`, so a stale view is
visible.

## Non-goals

- Installing, upgrading or removing programs, or running an installer's own commands
  for the user.
- Changing the user's global `php`, `node` or `python`, or installing shims for
  discovered programs.
- Cross-adapter graph edges. If a later feature needs them, they belong in a
  separate decision about the relation model.

## Consequences

- `ctx-php` loses its `brew` loop. Homebrew PHPs appear because `homebrew` reports
  them. A user without the `homebrew` adapter installed sees the PHP on `PATH` only,
  which is a deliberate hard cut and not a compatibility path.
- Every new interpreter or compiler adapter is a manifest, a version probe and a
  selection rule. Every new installer adapter is `programs` plus the usual five.
- The adapter API gains one capability and one environment variable, documented in
  `docs/adapter-api.md`.

## Open questions

Decided in the spike:

- The `engine` role is gone, as a hard cut with no alias. `jvm`, `wasm`, `evm` and
  `php` are evaluators; `docker` and `nerdctl` are executors; `podman`,
  `docker_desktop`, `orbstack`, `rancher_desktop` and `apple` are emulators and
  executors; `aws` and `gcloud` are emulators; `kube` declares no level and is not a
  virtualizer. `engines_test.go` now checks the vocabulary. The `--engine` flag of
  `ctn verify` is `--evaluator`, and its JSON field and output line say `evaluator`.
  `docs/engine-adapters.md` is `docs/evaluator-adapters.md`.
Still open:

- Whether an installer should also report programs it knows about but are not
  linked into any directory (`brew install php@8.1` is keg-only). The spike treats
  them as programs, with a `linked = false` attribute, because the runtime adapter
  can still run them by path.
- Whether two installers can claim one executable. The merge keeps all of them in
  `installers`, in adapter-name order.
