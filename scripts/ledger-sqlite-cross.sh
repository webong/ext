#!/usr/bin/env bash
# Checks the native SQLite backend against a frozen database from the retired Go store.
# Usage: scripts/ledger-sqlite-cross.sh [build-dir] (needs SQLite headers and Python 3)
set -euo pipefail
cd "$(dirname "$0")/.."
build=${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-ledger-cross.XXXXXX")}
mkdir -p "$build"
build=$(cd "$build" && pwd)
cmake -S lib/ledger -B "$build/ledger" -DEXT_LEDGER_SQLITE=ON -DEXT_LEDGER_TESTS=OFF ${EXT_LEDGER_CMAKE_ARGS:-}
cmake --build "$build/ledger" -j 4 --target ext-ledger
ledger=$(find "$build/ledger" -name 'ext-ledger' -o -name 'ext-ledger.exe' | head -1)
db="$build/cross.db"
rm -f "$db" "$db-wal" "$db-shm"
python3 - "$db" <<'PYSQL'
import sqlite3, sys
from pathlib import Path
with sqlite3.connect(sys.argv[1]) as db:
    db.executescript(Path("lib/ledger/testdata/store/v1/legacy-go.sql").read_text())
PYSQL
ns=cross.example
expect() { # expect <pattern> <command...>
  local pattern=$1; shift
  local out; out=$("$ledger" --sqlite "$@")
  case "$out" in *"$pattern"*) ;; *) echo "ext-ledger $*: want $pattern in: $out" >&2; exit 1 ;; esac
}
expect '"cursor":2' snapshot "$db" "$ns"
expect '"labels":{"tier":"gold"}' get-vertex "$db" "$ns" a
expect '"at":"2026-01-02T03:04:05.0000006+01:00"' get-vertex "$db" "$ns" a
expect '"list":["x",true,null],"n":1.5' get-vertex "$db" "$ns" a
expect '"replayed":true' apply "$db" '{"namespace":"cross.example","idempotency_key":"go-1","vertices":[{"id":"a","kind":"cross.example/node","labels":{"tier":"gold"},"attributes":{"n":1.5,"list":["x",true,null]},"provenance":{"source":"go","operation":"write","at":"2026-01-02T03:04:05.0000006+01:00"}},{"id":"b","kind":"cross.example/node"}]}'
expect '"cursor":3' apply "$db" '{"namespace":"cross.example","idempotency_key":"c-1","vertices":[{"id":"c","kind":"cross.example/node","attributes":{"from":"c"}}]}'
expect '"cursor":3' snapshot "$db" "$ns"
expect '"from":"c"' get-vertex "$db" "$ns" c
expect '"replayed":true' apply "$db" '{"namespace":"cross.example","idempotency_key":"c-1","vertices":[{"id":"c","kind":"cross.example/node","attributes":{"from":"c"}}]}'
echo "ok: C reads, updates and replays the frozen legacy Go database"
