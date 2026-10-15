# SQLite test fixtures

Small synthetic databases for the built-in reader. They contain invented data,
no real cookies. `make-fixtures.sh` regenerates all of them with the `sqlite3`
shell (the committed files were made with 3.50.4):

    ./make-fixtures.sh

| File | Shape |
| --- | --- |
| `chromium.db` | Chromium cookies and meta tables, with a unique index, blobs, NULLs, large and negative integers, Unicode text |
| `firefox.db` | `moz_cookies` with `id INTEGER PRIMARY KEY` and `user_version` 16 |
| `multipage.db` | 2500 rows on 512-byte pages: a three-level b-tree |
| `overflow.db` | Payloads at and past the on-page limit, up to 30 000 bytes, as text and as blobs |
| `wal.db`, `wal.db-wal` | Write-ahead log mode; later commits exist only in the log (copied while the connection was open, checkpointing off) |
| `altered.db` | Columns added with `ALTER TABLE ... ADD COLUMN ... DEFAULT` |
| `withoutrowid.db`, `utf16.db`, `generated.db` | Features the reader reports as unsupported |

`adapters/chromium/engine/testdata/Cookies` and
`adapters/firefox/engine/testdata/cookies.sqlite` are copies of `chromium.db` and
`firefox.db` for the adapters' tests.

Fuzz with `go test -run '^$' -fuzz FuzzReader -fuzztime 120s -fuzzminimizetime 0 ./sqlite`
(about 90 000 execs/sec on an Apple M1). Without `-fuzzminimizetime 0` the exec
counter can sit at 0/sec for up to a minute at a time: the Go fuzz engine is
minimizing a large new input, which counts no execs, and the reader is not
stuck. `TestNoInputIsSlow` guards the reader's time per input.
