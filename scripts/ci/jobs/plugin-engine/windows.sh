#!/usr/bin/env bash
# plugin-engine/windows: drafted from .github/workflows/plugin-engine.yml (job windows).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action msys2/setup-msys2@v2)

# Build the engine and run its tests
(
ci_msys2 <<'MSYS2'
scripts/plugin-engine-test.sh "$RUNNER_TEMP/engine"
MSYS2
)
ci_import_env
