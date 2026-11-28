# Recorder contract, version 1

What the `ext.system` recorder writes for what it observes. The recorder projects observations
of a machine (its shell sessions, web origins, host inventory, processes and adapter inventory)
onto the `ext.system` vocabulary in a [store](../../store/v1/README.md), and reads them back. These
fixtures hold that projection as data, so `lib/ledger` and the Go recorder are held to the same
projection.

| File | What it fixes |
| --- | --- |
| `vocabulary.json` | The `ext.system` schema the recorder registers: every vertex kind, and every relationship with the kinds of its endpoints |
| `cases.json` | The cases: an environment and a sequence of observations and reads |

## The environment

A recorder reads the machine besides the observations it is given. In a case that reading is an
input, so the projection is a function of the case:

| Field | What the recorder takes from it |
| --- | --- |
| `now` | The time of every observation: provenance `at`, `observed_at` attributes |
| `hostname`, `os`, `architecture` | The `machine/local` vertex. `os` and `architecture` are spelled as Go spells them (`linux`, `darwin`, `windows`; `amd64`, `arm64`) |
| `parent_pid` | The shell session: `shell/<parent_pid>` is the shell that started the recorder |
| `working_directory` | The directory a shell observation without one, or with a relative one, is in |
| `variables` | Environment variables: `SHELL` (or `ComSpec`), `TERM_PROGRAM`, `PATH`, `LD_LIBRARY_PATH` |
| `paths` | The paths that exist. A project is the nearest directory, from the working directory up, that holds `.git`, `.ctx`, `go.mod` or `package.json`; without one it is the directory itself |
| `host_environment` | The opaque digest that `src/ctx/system` computes for the host (`host.Environment`), which marks inventory collected in another environment as stale |

Every v1 case uses POSIX paths.

## Steps

Each case runs its steps in order on a new, empty store with the vocabulary registered.

| `op` | `input` | Returns, for `expect` |
| --- | --- | --- |
| `observe_shell` | `cwd`, `profile`, `selections` (an object of strings) | nothing |
| `observe_web` | `provider`, `profile`, `scheme`, `host` | nothing |
| `observe_host` | the host inventory: `shells`, `filesystems`, `webviews`, `permissions` and the other kinds of `src/ctx/system`, in their JSON form. An absent kind keeps what was observed of it; an empty list removes it | nothing |
| `observe_processes` | a C ledger process snapshot in the shared observation JSON form | nothing |
| `observe_inventory` | `adapters`, `aliases`, `installers` (below) | nothing |
| `snapshot` | none | the store snapshot of `ext.system` |
| `resolve_inventory` | `runtime` (empty for every runtime) | the inventory: `observed_at` and `contexts` |
| `read_host` | none | the host snapshot |

An adapter is `name`, `runtime`, `selector`, `discovery_status`, `surfaces`, `capabilities`,
`supports`, `engine`, `role`, `browser_share`, `trusted`, `list_supported`, `observe_supported`,
`listed`, `contexts` (`selection`, `capabilities`, `supports`, `browser_share`, `attributes`),
`programs` (as the C ledger scan and CTX report them), `resources` (`id`, `kind`,
`context`, `attributes`) and `relations` (`from`, `to`, `kind`). An alias is `space`, `name`,
`adapter`, `selection` and `metadata`; an installer is `name` and `source`.

`error` is the code the step must fail with: `invalid` for an observation the recorder refuses.
`expect` is matched as the [store cases](../../store/v1/README.md) match theirs: an object matches
the fields it names, so the expectations leave out what the store sets (records' `revision`,
`namespace`, `created_at` and `updated_at`), and an array must match element by element.

## Changing the cases

The expectations describe the canonical C recorder. Update cases with the C implementation
and run `lib/ledger/tests/recorder.c` through `scripts/ledger-test.sh`. Go's
`core/ledger/observation.TestRecorderWireRecords` verifies that binding records preserve
the fixture fields. No Go recorder regenerates these expectations.

## Not covered yet

Desktop, surface and accessibility inventory, resource capacity, and `ResolveHost` and
`PrepareHost`, which inspect executables on disk. They come with the C collectors.
