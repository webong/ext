#!/usr/bin/env bash
# host-cases/windows: drafted from .github/workflows/host-cases.yml (job windows).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action actions/setup-python@v6)

# (action shivammathur/setup-php@v2)

# (action actions/setup-dotnet@v5)

# (action msys2/setup-msys2@v2 if matrix.toolchain == 'mingw')

# (action ilammy/msvc-dev-cmd@v1 if matrix.toolchain == 'msvc')

# (action leafo/gh-actions-lua@v13 if matrix.toolchain == 'msvc')

# (action ruby/setup-ruby@v1 if matrix.toolchain == 'msvc')

# Host cases (MinGW-w64)
if [ "${MATRIX_TOOLCHAIN:-}" = "mingw" ]; then
(
export PYTHON="python"
ci_msys2 <<'MSYS2'
scripts/host-cases.sh
MSYS2
)
ci_import_env
fi

# Host cases (MSVC)
if [ "${MATRIX_TOOLCHAIN:-}" = "msvc" ]; then
(
export EXT_ENGINE_CC="cl"
export EXT_LUA_CC="cl"
export PYTHON="python"
scripts/host-cases.sh
)
ci_import_env
fi
