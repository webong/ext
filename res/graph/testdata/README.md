# res/graph fixtures

The language-neutral contract of `res/graph`, written as versioned data. Every store
implementation runs it; where an implementation and a fixture disagree, the fixture is right
and the implementation changes (`docs/adr-architecture.md`, rule 1). The layout follows
`res/system/testdata`.

```
testdata/
  store/v1/   the graph store: entities, rules, SQLite layout, operation cases, fingerprints
```

A change that alters an existing case makes a new `v<N+1>` directory, so an implementation that
has not caught up keeps running the old one. A new case that existing implementations already
satisfy may join the current version.

## Running them

| Language | Entry point | How |
| --- | --- | --- |
| Go | `storetest.Run(t, storetest.Backend{...})` and `storetest.RunFingerprints(t)` in `res/graph/store/storetest`; the memory store runs them in `store/store_test.go`, the SQLite store in `store/sqlite/sqlite_test.go` | `go test ./store/...` from `res/graph`; the fixtures are found by walking up |
| C (`lib/ledger`, when it is built) | a test that reads `cases.json` and `fingerprints.json` and drives the store's C API | not yet written: `lib/ledger` is built only on demand |
| Other languages | through the protocol (`docs/adr-architecture.md`, rule 5): a service that serves a Go store over `ext.plugin/v1` is held to these cases by the Go runner, and an SDK wrapper needs no runner of its own. A program that only reads a graph reads the documented tables with its own SQLite and checks them against `tables.json` | |

`EXT_GRAPH_TESTDATA` points a runner at a copy elsewhere (it holds `store/v1`).

## Codes

Every step states the error code it expects, or none when it must succeed. The codes are the
shared vocabulary in `res/system/testdata/codes.json`; the store uses a subset and adds none
(`store/v1/rules.json` says what each one means here). Each implementation keeps its native error
type and maps it to the code: `store.Code(err)` in Go. Error text is never part of a case.

## Areas

| Area | Versions | Run by |
| --- | --- | --- |
| `store` | v1 | Go: the memory store (`store.NewMemory`) and the SQLite store (`store/sqlite`) |
