#!/usr/bin/env bash
# plugin-engine/posix: drafted from .github/workflows/plugin-engine.yml (job posix).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# Build the engine and run its tests
(
scripts/plugin-engine-test.sh "$RUNNER_TEMP/engine"
)
ci_import_env
