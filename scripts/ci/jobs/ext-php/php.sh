#!/usr/bin/env bash
# ext-php/php: drafted from .github/workflows/ext-php.yml (job php).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action shivammathur/setup-php@v2)

# (action actions/setup-go@v6)

# Unit tests, the shared fixtures, and the guest under the Go process host
(
scripts/lang/php.sh test
)
ci_import_env
# (step timeout-minutes 16)
