#!/usr/bin/env bash
# host-cases/go-purego: drafted from .github/workflows/host-cases.yml (job go-purego).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action msys2/setup-msys2@v2 if matrix.os == 'windows-latest')

# (action msys2/setup-msys2@v2 if matrix.os == 'windows-11-arm')

# cshared and the host cases without cgo
if [ "$RUNNER_OS" != Windows ]; then
(
scripts/cshared-purego.sh
)
ci_import_env
fi

# cshared and the host cases without cgo (Windows)
if [ "$RUNNER_OS" = Windows ]; then
(
export CC="${MATRIX_OS == 'WINDOWS_11_ARM' && 'CLANG' || 'GCC':-}"
ci_msys2 <<'MSYS2'
scripts/cshared-purego.sh
MSYS2
)
ci_import_env
fi
