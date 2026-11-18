#!/usr/bin/env bash
# native/native-integration: drafted from .github/workflows/native.yml (job native-integration).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# ./tests/native_test.sh
(
./tests/native_test.sh
)
ci_import_env

# ./tests/native_installer_test.sh
(
./tests/native_installer_test.sh
)
ci_import_env
