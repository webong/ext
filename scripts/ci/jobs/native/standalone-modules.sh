#!/usr/bin/env bash
# native/standalone-modules: drafted from .github/workflows/native.yml (job standalone-modules).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# ./scripts/check-standalone-modules.sh
(
export STANDALONE_ALLOW_SKIPS="1"
./scripts/check-standalone-modules.sh
)
ci_import_env
