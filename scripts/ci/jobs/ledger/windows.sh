#!/usr/bin/env bash
# ledger/windows: build lib/ledger with MinGW-w64 and run its tests.
. "$(dirname "$0")/../lib.sh"

(
ci_msys2 <<'MSYS2'
scripts/ledger-test.sh "$RUNNER_TEMP/ledger"
MSYS2
)
ci_import_env
