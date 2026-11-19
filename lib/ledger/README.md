# lib/ledger

The `res/graph` graph in C: one library that Linux, Darwin (macOS and iOS) and Windows
programs embed, from any language with a C FFI, with nothing beyond the C library and the OS.
It replaces the Go `res/graph` as the implementation of the graph; the contract is still the data in
[`res/graph/testdata`](../../res/graph/testdata), and this library is held to it.

| Part | State |
| --- | --- |
| Store | Done: `include/ext_ledger.h`, memory and file backends, the whole store contract |
| SQLite backend | Done, optional (`EXT_LEDGER_SQLITE`): the `tables.json` layout, which the Go store writes too |
| Reader | Done: `ext_ledger_open_reader`, which never locks or writes, and the `ext-ledger` command |
| Recorder | Later: the `ext.system` vocabulary and per-kernel collectors |

## Building

```bash
cmake -S lib/ledger -B build/ledger
cmake --build build/ledger
ctest --test-dir build/ledger --output-on-failure
```

Options: `EXT_LEDGER_SHARED` (also a shared library), `EXT_LEDGER_SQLITE` (the SQLite backend,
linked against the system's SQLite), `EXT_LEDGER_SANITIZE` (address and undefined-behaviour
sanitizers). C11, no third-party code: the JSON reader and writer, SHA-256 and CRC-32 are the
library's own.

## The API

Values cross the API as JSON in the wire form of `entities.schema.json`, and everything the
library returns is canonical JSON (the contract's README, Canonical JSON). Every call returns one
of the contract's codes (`ext_ledger_code`, named by `ext_ledger_code_name`). A call takes an
optional `ext_ledger_context` for cancellation and a deadline.

```c
ext_ledger *store;
ext_ledger_open(NULL, "/path/to/graph", NULL, &store);
ext_ledger_register(store, NULL, schema, strlen(schema));
char *commit; size_t len;
ext_ledger_apply(store, NULL, transaction, strlen(transaction), &commit, &len);
ext_ledger_free(commit);
ext_ledger_close(store);
```

A watch (`ext_ledger_watch_open`, `_next`, `_close`) has no thread of its own: `_next` waits on
the store, and on a file store it also looks for other processes' commits every 150 ms.

## From a shell

`ext-ledger` (built by default, `EXT_LEDGER_COMMAND`) answers every read through a reader and
opens the store only to register or apply:

```bash
ext-ledger register ~/.config/ext/graph '{"namespace":"my.app","version":"1"}'
ext-ledger apply ~/.config/ext/graph '{"namespace":"my.app","vertices":[{"id":"a","kind":"my.app/node"}]}'
ext-ledger snapshot ~/.config/ext/graph my.app
ext-ledger watch ~/.config/ext/graph my.app --after 0
ext-ledger --sqlite snapshot ~/.config/ext/graph.db my.app
```

Output is canonical JSON on standard output, one value per line (`watch` prints one change per
line until it ends). A failure prints `{"error":"<code>"}` on standard error and exits with the
code's number; a usage error exits with 64. A JSON argument of `-` is read from standard input.

## Backends

- **memory** (`ext_ledger_open_memory`): one process's state.
- **file** (`ext_ledger_open`): a directory laid out as
  [`ledger.json`](../../res/graph/testdata/store/v1/ledger.json) says. An append-only log of
  framed, checksummed canonical-JSON lines and a checkpoint; an advisory lock (`flock`,
  `LockFileEx`) serialises writers, and readers never lock. Every handle keeps the state in memory
  and catches up by reading what other handles appended. Handles in one process and in many
  processes share a directory.
- **sqlite** (`ext_ledger_open_sqlite`): optional, linked against the system's SQLite (no vendored
  copy); `EXT_LEDGER_UNSUPPORTED` in a build without it. One database laid out as
  [`tables.json`](../../res/graph/testdata/store/v1/tables.json) says, which is what the Go store
  `res/graph/store/sqlite` writes: either opens the other's database, and
  `scripts/ledger-sqlite-cross.sh` checks it both ways (Go writes, C reads and writes, Go reads and
  replays C's idempotency key). The handle catches up with other connections when SQLite's
  `data_version` moves; a commit, with its compaction, is one `BEGIN IMMEDIATE` transaction.

## Layout

| Path | What it is |
| --- | --- |
| `include/ext_ledger.h` | The public API |
| `src/json.c` | JSON reading and canonical writing |
| `src/hash.c` | SHA-256 and CRC-32 |
| `src/model.c` | The entities: decoding their wire form and writing it |
| `src/rules.c` | The rules of `rules.json`, and the hash map |
| `src/state.c` | The in-memory graph and the change a transaction makes |
| `src/store.c` | The public API over the state and a backend |
| `src/file.c` | The file backend |
| `src/sqlite.c`, `src/sqlite_none.c` | The SQLite backend, and its stand-in when it is not built |
| `src/platform_posix.c`, `src/platform_win32.c` | Threads, time, files and locks |
| `tests/fixtures.c` | Runs `cases.json` and `fingerprints.json` on every backend, and checks the SQLite tables against `tables.json` |
| `tests/behaviour.c` | What the cases cannot hold: watches, concurrency, rewrites, torn writes, readers |
| `tests/command.cmake` | Drives `ext-ledger` as a shell would |
| `cmd/ext-ledger.c` | The command |

Like `lib/plugin`, the tree holds no Go code.
