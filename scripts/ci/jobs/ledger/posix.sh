#!/usr/bin/env bash
# ledger/posix: build lib/ledger with the sanitizers and run its tests.
. "$(dirname "$0")/../lib.sh"

(
export EXT_LEDGER_CI_SANITIZE="1"
scripts/ledger-test.sh "$RUNNER_TEMP/ledger"
)
ci_import_env
