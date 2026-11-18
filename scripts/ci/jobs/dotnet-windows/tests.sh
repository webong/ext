#!/usr/bin/env bash
# dotnet-windows/tests: drafted from .github/workflows/dotnet-windows.yml (job tests).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-dotnet@v5)

# Tests
(
scripts/lang/dotnet.sh test
)
ci_import_env

# Pack
(
scripts/lang/dotnet.sh pack
)
ci_import_env
