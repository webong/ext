# lib/ledger

The ledger in C: one library that Linux, Darwin (macOS and iOS) and Windows
programs embed, from any language with a C FFI, with nothing beyond the C library and the OS.
It is the main implementation of the ledger, which the pure-Go [`pkg/ext-go/engine/ledger`](../../pkg/ext-go/engine/ledger/README.md) follows; the contract is the data in
[`lib/ledger/testdata`](../../lib/ledger/testdata), and this library is held to it.

| Part | State |
| --- | --- |
| Store | Done: `include/ext_ledger.h`, memory and file backends, the whole store contract |
| SQLite backend | Done, optional (`EXT_LEDGER_SQLITE`): the `tables.json` layout, which the Go store writes too |
| Reader | Done: `ext_ledger_store_open_reader`, which never locks or writes, and the `ext-ledger` command |
| Recorder | Done for what it is given: `ext_ledger_recorder_open` and `ext_ledger_recorder_record`, shells, web origins, host inventory, processes and adapter inventory, held to `lib/ledger/testdata/recorder/v1` |
| Collectors | Done: `ext_ledger_collect` and `ext_ledger_recorder_scan`, through each kernel's own interface, so the recorder reads the machine itself |
| Supervisor | Done: `ext_ledger_supervisor_*`, held to the Go supervisor's tests |
| Library interface | Done: `ext_ledger_open`, `ext_ledger_call`, `ext_ledger_close` and `ext_ledger_abi_version`, every object behind four functions |

## Platforms

| OS | Tested |
| --- | --- |
| macOS (arm64) | locally, with the address, undefined-behaviour and thread sanitizers, with and without SQLite |
| Linux | CI (`ledger/posix`, `ledger/sqlite`): GCC with the sanitizers, and the Go and C SQLite cross-check |
| Windows | CI (`ledger/windows`, `ledger/windows-msvc`): MinGW-w64 and MSVC |
| iOS | built for devices and the simulator (`-DCMAKE_SYSTEM_NAME=iOS`); every test passes in the iOS 18 simulator (`xcrun simctl spawn`) |
| Android | built with the NDK (arm64-v8a, API 24); every test and `ext-ledger` pass on an Android 11 emulator |

The iOS and Android builds have no SQLite backend: Android's NDK has no public SQLite, and on iOS the
file backend is the one to use. iOS and its relatives build no `ext-ledger`, since there is no shell to
run it from. iOS and Android are tested by hand for now; they have no CI cell.

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
ext_ledger_store_open(NULL, "/path/to/graph", NULL, &store);
ext_ledger_store_register(store, NULL, schema, strlen(schema));
char *commit; size_t len;
ext_ledger_store_apply(store, NULL, transaction, strlen(transaction), &commit, &len);
ext_ledger_free(commit);
ext_ledger_store_close(store);
```

A watch (`ext_ledger_store_watch_open`, `_next`, `_close`) has no thread of its own: `_next` waits on
the store, and on a file store it also looks for other processes' commits every 150 ms.

## The library interface

Every object of the ledger is also reachable through four functions, so a binding in any language
binds these and nothing else (ext-go's purego, cgo and ccgo drivers do): `ext_ledger_open` opens a
store, a watch, a recorder or a supervisor from a JSON config and returns a handle, `ext_ledger_call`
runs an operation by name with a JSON object in and a JSON value out, and `ext_ledger_close` frees it.
Handle 0 is the library itself (collect, the vocabulary, canonical transactions, code names).

```c
uint64_t store, recorder;
ext_ledger_open("{\"object\":\"store\",\"backend\":\"file\",\"path\":\"/var/lib/app/graph\"}", ..., &store);
ext_ledger_call(store, "register", "{\"schema\":{...}}", ..., &out, &out_len);
ext_ledger_open("{\"object\":\"recorder\",\"store\":1,\"machine\":true}", ..., &recorder);
ext_ledger_call(recorder, "scan", "{\"what\":\"processes\",\"timeout_ms\":5000}", ..., &out, &out_len);
```

`include/ext_ledger.h` lists the configs and operations; an input's `timeout_ms` bounds the call.
Handles are never reused, an object is freed once no call holds it, and a store with a watch, recorder
or supervisor open on it refuses to close (`EXT_LEDGER_BUSY`). The typed functions stay for C callers:
`ext_ledger_store_*`, `ext_ledger_recorder_*`, `ext_ledger_supervisor_*` and `ext_ledger_collect*`.

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

## The supervisor

`ext_ledger_supervisor_open` takes a store and runs local component processes, projecting what
it sees of each into the `ext.runtime` namespace: the artifact, the process instance, the runtime
that supervises it, and its endpoints, connections and dependencies. It decides nothing about
trust or authority. A spec is JSON (see the header).

The supervisor never calls back into its caller, so every language drives it the same way,
including one that cannot take a call from a C thread:

- **Readiness is reported.** With `await_readiness`, each launch stays `running` until the caller,
  having checked its endpoints and protocol handshake, calls `ext_ledger_supervisor_ready` with the
  launch's pid; no report within `readiness_timeout_ms` crashes it.
- **Health is reported.** `ext_ledger_supervisor_health` records the caller's check, healthy only
  while the instance is ready and its process alive.
- **Waiting needs no polling.** `ext_ledger_supervisor_await` blocks until an instance reaches one
  of the states named, or the context ends.

- **Process trees.** A child leads its own process group (POSIX) or starts suspended inside a job
  object that ends every process in it (Windows), so stopping it, or its exit, ends what it started.
  On Windows the child joins its job before its first instruction runs, which the Go supervisor left
  to a policy hook.
  Stopping sends `SIGTERM`, then `SIGKILL` after 5 s.
- **Graph first.** A state change is applied to the store before the instance's lock is released,
  so a state seen through `ext_ledger_supervisor_instance` is already in the graph.
- **Secrets.** The caller resolves secrets and passes them to `ext_ledger_supervisor_start`; they
  go only into each launch's environment, are never projected and are wiped when the instance is
  freed, and the output of a process given any is discarded.
- **Leases and orphans.** An active instance's fact carries `lease_until`, renewed every third of
  the lease. A supervisor opened while another of the same runtime holds a live lease answers
  `EXT_LEDGER_BUSY`; processes whose lease expired are orphans, which block new starts until
  `ext_ledger_supervisor_resolve_orphan` ends them (never a reused PID: the process identity is a
  digest of its PID and start time) or, with the `terminate` policy, are ended at once.
- **Bounds.** Each instance keeps the tail of its output (`log_limit`), and stopped or crashed
  instances beyond `retain_terminal` leave both the supervisor and the graph.

iOS lets no application start processes: a start there crashes as a failed start. Go's
`ProcessPolicyFactory` (a hook for platform resource or sandbox controls) is not carried over;
nothing used it, and sandboxing has its own contracts ([the decision](../../docs/adr-sandboxing.md)).

## Backends

- **memory** (`ext_ledger_store_open_memory`): one process's state.
- **file** (`ext_ledger_store_open`): a directory laid out as
  [`ledger.json`](../../lib/ledger/testdata/store/v1/ledger.json) says. An append-only log of
  framed, checksummed canonical-JSON lines and a checkpoint; an advisory lock (`flock`,
  `LockFileEx`) serialises writers, and readers never lock. Every handle keeps the state in memory
  and catches up by reading what other handles appended. Handles in one process and in many
  processes share a directory.
- **sqlite** (`ext_ledger_store_open_sqlite`): optional, linked against the system's SQLite (no vendored
  copy); `EXT_LEDGER_UNSUPPORTED` in a build without it. One database laid out as
  [`tables.json`](../../lib/ledger/testdata/store/v1/tables.json) says, which is what the Go store
  `pkg/ext-go/engine/ledger/recorder/sqlite` writes: either opens the other's database, and
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
| `src/recorder.c`, `src/record_*.c` | The `ext.system` recorder: its environment and helpers, and one file per kind of observation |
| `src/vocabulary.h` | The `ext.system` vocabulary, from `vocabulary.json` |
| `tests/recorder.c` | Runs `recorder/v1` against the recorder |
| `src/collect*.c` | The collectors, one file per kind and kernel |
| `src/supervisor.c` | The supervisor: specs, the lifecycle, projection, leases and recovery |
| `src/process_posix.c`, `src/process_win32.c` | Starting, waiting for and ending process trees; threads and random IDs |
| `tests/supervisor.c` | `pkg/ext-go/engine/ledger/supervisor`'s tests, with the test binary as its own child |

Like `lib/plugin`, the tree holds no Go code.
