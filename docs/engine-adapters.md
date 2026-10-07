# Engine adapters

An **engine** is an adapter that executes programs: a Java virtual machine, a
WebAssembly runtime, an EVM. This page is the contract every engine adapter
follows, so a user, ctx, ctn or another tool can treat any engine the same way.
It builds on the [adapter API](adapter-api.md); the maintained engines are `jvm`,
`wasm` and `evm`.

The word is used in two senses in this repository. An **engine adapter** is on this
page: an adapter that runs a program. The **plugin engine**, `pkg/plugin-engine`,
is the shared C library that the language SDKs bind to host or author
`ext.plugin/v1` plugins; it is not an adapter. Mobile support belongs to the second:
see [`pkg/plugin-ios`](../pkg/plugin-ios/README.md) and
[`pkg/plugin-android`](../pkg/plugin-android/README.md).

## Declaration

```toml
runtime = "computer"
supports = "engine,<kind>"        # for example engine,jvm
selector_key = "<kind>"
capabilities = "list,observe,validate,run,doctor"
```

An engine declares `engine` plus one specific kind (`jvm`, `wasm`, `evm`). With
`ctx graph resolve all --supports engine` a tool finds every engine, and with
`--supports <kind>` it narrows to one. A built-in engine has no native executable
to find, so it also sets `self_contained = "true"` and names its command in
`computer_commands`.

## Selections

The selection names the one thing that varies between installations of the engine:

| Engine | A selection is | Default with no selection |
| --- | --- | --- |
| `jvm` | a major Java version (`21`) | the first installation found |
| `wasm` | an engine (`embedded`) | `embedded` |
| `evm` | a rule set (`cancun`) | the newest (`prague`) |

**An engine must work with no selection,** using a documented default, so a project
can run a program without choosing anything. A selection that is not available is
refused with exit status 1 and a message that lists what is.

## Operations

All five are required.

- **`list`** prints one selection per line.
- **`observe`** prints the graph JSON, with these attributes on every context:
  - `kind`: the engine kind, the same name used in `supports`.
  - `version`: the version of that engine or installation.
  - `isolation`: `sandboxed`, `restricted` or `host` (below).

  An engine may add more, such as `home`, `source` or `engine`.
- **`validate`** succeeds when the selection is available.
- **`doctor`** proves the engine can run something, not only that it exists: it runs
  a trivial program and reports the result.
- **`run`** executes a program. The adapter receives `run [selection] -- ARGUMENTS`.

## Isolation

`isolation` states honestly what the program can reach.

- **`sandboxed`** (`wasm`, `evm`): the program gets nothing from the host (no
  environment, filesystem or network) unless a flag grants it. A fresh, in-memory
  environment is created for each run.
- **`restricted`**: the program runs in a mainstream sandbox that the engine does
  not control (a browser's), and the engine narrows what it can reach by policy
  rather than by capability. The [web engine](web-engine.md) is the example: a
  content security policy denies the network by default, but a policy cannot stop a
  page from navigating its window away, and the page runs in whichever browser
  profile the host opened. It sits between the other two. The web engine is a
  library, not an adapter, so no maintained adapter reports this value yet; an
  engine that does must say what its policy cannot prevent.
- **`host`** (`jvm`): the program runs as a normal process with the user's full
  privileges. The engine selects and launches it and does not confine it.

A tool that runs untrusted content must require `sandboxed`. `restricted` is for
content the user chose to run and wants kept off the network, not for hostile code.

## Flags for `run`

Engines that grant host access use these spellings, so the same words mean the same
thing everywhere:

| Flag | Meaning |
| --- | --- |
| `--timeout DURATION` | stop the program after this long; `0` means no limit |
| `--env KEY=VALUE` | give the program one environment variable; repeatable |
| `--dir GUEST=HOST` | mount a host directory read-write; repeatable |
| `--ro-dir GUEST=HOST` | mount a host directory read-only; repeatable |

A resource bound has the name its engine's own unit gives it, and is documented
with the engine: `--memory-pages` (`wasm`), `--gas` (`evm`). An engine does not
invent a second spelling for something in the table. Flags come before the program
and are parsed by the adapter; everything after the program belongs to the program.

## Exit status

| Status | Meaning |
| --- | --- |
| `0` | the program succeeded |
| `1` | the program ran and failed, or the engine failed |
| `2` | usage error: the adapter's own arguments are wrong |
| `124` | `--timeout` stopped the program |
| `126` | the engine cannot run this input (a kind of program it does not support) |
| `127` | the engine or a tool it needs is not installed |

An engine whose programs have their own exit status (`wasm`) passes it through when
the program ran, so a program that itself exits 124 or 126 is indistinguishable
from the engine doing so. Diagnostics from the adapter go to standard error with
the adapter's name as a prefix (`wasm: …`).

## Output

- An engine whose program streams output (`jvm`, `wasm`) connects the program's own
  standard streams.
- An engine whose program produces a value (`evm`) prints **one JSON object** on
  standard output with a `status`, and the exit status above still applies, so a
  caller can use either. The object never mixes in diagnostics.

## Preflight (optional)

An engine that can tell, without running anything, whether it can run an input
offers `--inspect`. It prints a JSON object with at least a `runnable` boolean and
exits 0 whether or not the input is runnable. `wasm` does this: it reports what a
module imports and whether the built-in engine can supply them.

## Determinism

A sandboxed engine documents which sources of variation it gives the program, and
gives none by accident. `evm` gives none: its block context (timestamp, number,
origin, coinbase) is fixed unless a flag changes it, so two runs of the same input
give the same result. `wasm` gives the WASI clocks and random source, because WASI
programs expect them, and nothing else from the host.

## Discovery by host engines

An engine that finds installations on the machine (`jvm`) prefers the user's own
over the system's, and an explicit environment variable such as `JAVA_HOME` over
both, so a developer's choice wins and the result does not change between runs.
Product-specific conventions such as those variables belong in the adapter, not in
ctx.

## Adding an engine

1. Declare it as above and implement the five operations.
2. Decide `isolation` honestly and apply the defaults for it.
3. Use the flag names and exit statuses in this page.
4. Document the selection, its default and the engine-specific flags in
   [the adapter list](../adapters/README.md).
5. Add a test that runs a real program through `run`, covering a success, a
   failure, the timeout and an invalid input.

The repository's architecture tests check the parts that can be checked from the
manifest: the declaration and the required operations.
