#!/usr/bin/env bash
# plugin-engine/windows-msvc: drafted from .github/workflows/plugin-engine.yml (job windows-msvc).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action ilammy/msvc-dev-cmd@v1)

# Build the engine with MSVC and run the native tests
(
export CC="cl"
export EXT_ENGINE_C_ONLY="1"
scripts/plugin-engine-test.sh "$RUNNER_TEMP/engine"
)
ci_import_env
