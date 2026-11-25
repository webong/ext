#!/usr/bin/env bash
# c2goengine/translate: translate lib/plugin with ccgo for this cell's platform and test the
# translation with the binding beside it (scripts/c2goengine-translate.sh). The translation is not
# committed: the ext-plugin release publishes it, made the same way.
. "$(dirname "$0")/../lib.sh"

# (Windows keeps line endings as they are: the workflow sets core.autocrlf false before checkout, for
# cells with autocrlf_off.)

# (action actions/checkout@v5)

# Use the matrix compiler
if [ "${MATRIX_CC:-}" != "" ]; then
(
echo "CC=${MATRIX_CC:-}" >> "$GITHUB_ENV"
)
ci_import_env
fi

# (action actions/setup-go@v6)

# (action msys2/setup-msys2@v2 if runner.os == 'Windows')

# Translate and test
if [ "$RUNNER_OS" != Windows ]; then
(
EXT_C2GO_RACE="${MATRIX_RACE:-}" scripts/c2goengine-translate.sh
bash scripts/ledger-c2go.sh
)
ci_import_env
fi

# Translate and test (MSYS2)
if [ "$RUNNER_OS" = Windows ]; then
(
ci_msys2 <<'MSYS2'
scripts/c2goengine-translate.sh
MSYS2
)
ci_import_env
fi

# (action actions/upload-artifact@v6 if always())
