# ledger fixtures

The language-neutral contract of the ledger, written as versioned data. Every store
implementation runs it; where an implementation and a fixture disagree, the fixture is right
and the implementation changes (`docs/adr-architecture.md`, rule 1). The layout follows
`res/system/testdata`.

```
testdata/
  store/v1/     the graph store: entities, rules, file and SQLite layouts, operation cases, fingerprints
  recorder/v1/  the ext.system recorder: its vocabulary, and observations in with the graph out
```

A change that alters an existing case makes a new `v<N+1>` directory, so an implementation that
has not caught up keeps running the old one. A new case that existing implementations already
satisfy may join the current version.

## Running them

| Language | Entry point | How |
| --- | --- | --- |
| Go | `core/ledger/ledgertest` runs store and canonicalization fixtures against `cengine/ledger` bindings | `EXT_LEDGER_LIBRARY=/path/to/library go test ./ledger/...` from `pkg/ext-go/cengine`; the fixtures are found by walking up |
| C (`lib/ledger`) | `lib/ledger/tests/fixtures.c` runs `store/v1` on the memory, file and SQLite backends | `scripts/ledger-test.sh`, or `ctest` in a `lib/ledger` build |
| Other languages | through the protocol (`docs/adr-architecture.md`, rule 5): a service that serves a Go store over `ext.io.pc/v1` is held to these cases by the Go runner, and an SDK wrapper needs no runner of its own. A program that only reads a graph uses `lib/ledger`'s reader, or reads the documented files or tables (`ledger.json`, `tables.json`) itself | |

`EXT_GRAPH_TESTDATA` points a runner at a copy elsewhere (it holds `store/v1`).

## Codes

Every step states the error code it expects, or none when it must succeed. The codes are the
shared vocabulary in `res/system/testdata/codes.json`; the store uses a subset and adds none
(`store/v1/rules.json` says what each one means here). Each implementation keeps its native error
type and maps it to the code: `store.Code(err)` in Go. Error text is never part of a case.

## Areas

| Area | Versions | Run by |
| --- | --- | --- |
| `store` | v1 | Go: the native memory/file bindings; C: `lib/ledger` on every backend |
| `recorder` | v1 | Go wire decoding: `TestRecorderWireRecords` in `core/ledger/observation`; canonical C behavior: `lib/ledger/tests/recorder.c` |
