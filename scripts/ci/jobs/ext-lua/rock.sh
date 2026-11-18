#!/usr/bin/env bash
# ext-lua/rock: drafted from .github/workflows/ext-lua.yml (job rock).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action leafo/gh-actions-lua@v13)

# (action leafo/gh-actions-luarocks@v6)

# Lint and install the rockspec, then load the installed modules
(
scripts/lang/lua.sh rock
)
ci_import_env
