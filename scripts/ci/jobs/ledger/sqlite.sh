#!/usr/bin/env bash
# ledger/sqlite: build lib/ledger with its SQLite backend under the sanitizers, run its tests, and
# check that it and the Go store open each other's databases.
. "$(dirname "$0")/../lib.sh"

(
if ! [ -f /usr/include/sqlite3.h ]; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq libsqlite3-dev
fi
export EXT_LEDGER_CI_SANITIZE="1"
export EXT_LEDGER_CMAKE_ARGS="-DEXT_LEDGER_SQLITE=ON"
scripts/ledger-test.sh "$RUNNER_TEMP/ledger"
EXT_LEDGER_CMAKE_ARGS="" scripts/ledger-sqlite-cross.sh "$RUNNER_TEMP/ledger-cross"
)
ci_import_env
