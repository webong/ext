#!/usr/bin/env bash
# ledger/windows-msvc: build lib/ledger with MSVC and run its tests.
. "$(dirname "$0")/../lib.sh"

(
export CC="cl"
scripts/ledger-test.sh "$RUNNER_TEMP/ledger"
)
ci_import_env
