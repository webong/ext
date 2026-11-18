#!/usr/bin/env bash
# ext-lua/lua: drafted from .github/workflows/ext-lua.yml (job lua).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action leafo/gh-actions-lua@v13)

# Test the SDK and run it under the Go hosts
(
if [[ "${MATRIX_LUA:-}" == luajit* ]]; then export EXT_LUA=luajit; else export EXT_LUA=lua; fi
scripts/lang/lua.sh test
)
ci_import_env
