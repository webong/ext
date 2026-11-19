# lib/ledger

The `res/graph` graph in C: one library that Linux, Darwin (macOS and iOS) and Windows
programs embed, from any language with a C FFI, with nothing beyond the C library and the OS.
It replaces the Go `res/graph` as the implementation of the graph; the contract is still the data in
[`res/graph/testdata`](../../res/graph/testdata), and this library is held to it.

| Part | State |
| --- | --- |
| Store | Done: `include/ext_ledger.h`, memory and file backends, the whole store contract |
| SQLite backend | Optional (`EXT_LEDGER_SQLITE`), on the `tables.json` layout; not written yet |
| Reader | Next: a read-only open and a command for shells |
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

## Backends

- **memory** (`ext_ledger_open_memory`): one process's state.
- **file** (`ext_ledger_open`): a directory laid out as
  [`ledger.json`](../../res/graph/testdata/store/v1/ledger.json) says. An append-only log of
  framed, checksummed canonical-JSON lines and a checkpoint; an advisory lock (`flock`,
  `LockFileEx`) serialises writers, and readers never lock. Every handle keeps the state in memory
  and catches up by reading what other handles appended. Handles in one process and in many
  processes share a directory.
- **sqlite** (`ext_ledger_open_sqlite`): optional; `EXT_LEDGER_UNSUPPORTED` in a build without it.

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
| `src/platform_posix.c`, `src/platform_win32.c` | Threads, time, files and locks |
| `tests/fixtures.c` | Runs `cases.json` and `fingerprints.json` on every backend |
| `tests/behaviour.c` | What the cases cannot hold: watches, concurrency, rewrites, torn writes |

Like `lib/plugin`, the tree holds no Go code.
