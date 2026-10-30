# ctn verify

`ctn verify FILE` runs content through a sandboxed [evaluator](evaluator-adapters.md) and says
whether it ran cleanly. It is the first consumer of the evaluator contract: it uses an
evaluator's reported `isolation` to decide whether the content may run at all.

```sh
ctn verify module.wasm
ctn verify --timeout 5s --json contract.hex
ctn verify --engine wasm --selection wasmtime component.wasm
```

CTN consumes evaluator capabilities from ext adapters. `ext ctn verify` and
standalone `ctn verify` use the same implementation and configured adapter home;
CTX does not need to be running. `ext` is the main installation entry point,
currently through `ext adapter install`. `ctn adapter ls` lists installed
adapters; CTN still applies its own capability and isolation checks before use.

## Rules

- **Only sandboxed evaluators run content.** `ctn` asks the evaluator for its `observe` output
  and runs the content only if the evaluator reports `isolation: sandboxed`. With
  `--selection`, that selection's context must be sandboxed. Without one, every context
  the evaluator offers must be, because the adapter chooses which runs. A `host` evaluator such
  as `jvm`, a `restricted` one, an evaluator that reports nothing, and output that cannot be
  read all mean the content is not run.
- **The evaluator must be trusted**, with `ext adapter trust NAME` (or standalone
  `ctx adapter trust NAME`), like any ext adapter.
- **The evaluator is chosen by the file's extension** (`.wasm` is `wasm`; `.evm` and `.hex`
  are `evm`), or named with `--engine`. Anything else needs `--engine`.
- **`--timeout`** (default 30 seconds) is passed to the evaluator, and `ctn` adds its own
  outer limit a few seconds longer, so an evaluator that ignores its timeout cannot hang
  the check.

## Verdicts

| Verdict | Exit | Meaning |
| --- | --- | --- |
| `PASS` | 0 | the content ran and exited 0 |
| `FAIL` | 1 | the content ran and failed; its own exit status is reported as `exitCode` |
| `UNSUPPORTED` | 3 | it could not be checked: the evaluator is untrusted or not sandboxed, or it cannot run this input (exit 126 or 127 from the evaluator) |
| `TIMEOUT` | 124 | it did not finish in time |
| (usage) | 2 | the arguments or file are wrong, or no evaluator adapter is installed |

`UNSUPPORTED` is deliberately not `FAIL`: content the evaluator cannot run, such as a web
module, says nothing about whether the content is good.

Every verdict carries the SHA-256 of the file, so a result names exactly what was
checked. With `--json` it is one object: `verdict`, `file`, `sha256`, `engine`,
`selection`, `isolation`, `exitCode`, `millis` and `reason`.

## What it does not do

It reports that content ran cleanly in a confined evaluator, not that the content is
correct or safe: a program that exits 0 can still be wrong, and a sandbox is only as
strong as the evaluator's promise (see isolation in the evaluator contract). It keeps no
records, and the web engine is not an evaluator adapter, so web bundles are not covered.
