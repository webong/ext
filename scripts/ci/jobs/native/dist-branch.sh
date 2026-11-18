#!/usr/bin/env bash
# native/dist-branch: drafted from .github/workflows/native.yml (job dist-branch).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# ./scripts/dist-branch-test.sh
(
./scripts/dist-branch-test.sh
)
ci_import_env
