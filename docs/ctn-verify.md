# ctn verify

`ctn verify FILE` runs content through a sandboxed [engine](engine-adapters.md) and says
whether it ran cleanly. It is the first consumer of the engine contract: it uses an
engine's reported `isolation` to decide whether the content may run at all.

```sh
ctn verify module.wasm
ctn verify --timeout 5s --json contract.hex
ctn verify --engine wasm --selection wasmtime component.wasm
```

## Rules

- **Only sandboxed engines run content.** `ctn` asks the engine for its `observe` output
  and runs the content only if the engine reports `isolation: sandboxed`. With
  `--selection`, that selection's context must be sandboxed. Without one, every context
  the engine offers must be, because the adapter chooses which runs. A `host` engine such
  as `jvm`, a `restricted` one, an engine that reports nothing, and output that cannot be
  read all mean the content is not run.
- **The engine must be trusted**, with `ctx adapter trust NAME`, like any adapter.
- **The engine is chosen by the file's extension** (`.wasm` is `wasm`; `.evm` and `.hex`
  are `evm`), or named with `--engine`. Anything else needs `--engine`.
- **`--timeout`** (default 30 seconds) is passed to the engine, and `ctn` adds its own
  outer limit a few seconds longer, so an engine that ignores its timeout cannot hang
  the check.

## Verdicts

| Verdict | Exit | Meaning |
| --- | --- | --- |
| `PASS` | 0 | the content ran and exited 0 |
| `FAIL` | 1 | the content ran and failed; its own exit status is reported as `exitCode` |
| `UNSUPPORTED` | 3 | it could not be checked: the engine is untrusted or not sandboxed, or it cannot run this input (exit 126 or 127 from the engine) |
| `TIMEOUT` | 124 | it did not finish in time |
| (usage) | 2 | the arguments or file are wrong, or no engine adapter is installed |

`UNSUPPORTED` is deliberately not `FAIL`: content the engine cannot run, such as a web
module, says nothing about whether the content is good.

Every verdict carries the SHA-256 of the file, so a result names exactly what was
checked. With `--json` it is one object: `verdict`, `file`, `sha256`, `engine`,
`selection`, `isolation`, `exitCode`, `millis` and `reason`.

## What it does not do

It reports that content ran cleanly in a confined engine, not that the content is
correct or safe: a program that exits 0 can still be wrong, and a sandbox is only as
strong as the engine's promise (see isolation in the engine contract). It keeps no
records, and the web engine is not an engine adapter, so web bundles are not covered.
