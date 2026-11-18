#!/usr/bin/env bash
# host-cases/hosts: drafted from .github/workflows/host-cases.yml (job hosts).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action actions/setup-python@v6)

# (action ruby/setup-ruby@v1)

# (action leafo/gh-actions-lua@v13)

# (action shivammathur/setup-php@v2)

# (action actions/setup-dotnet@v5)

# Host cases
(
export EXT_LUA="$(case "${MATRIX_LUA:-}" in luajit*) echo luajit ;; *) echo lua ;; esac)"
export PYTHON="python"
scripts/host-cases.sh
)
ci_import_env
