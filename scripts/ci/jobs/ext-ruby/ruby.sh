#!/usr/bin/env bash
# ext-ruby/ruby: drafted from .github/workflows/ext-ruby.yml (job ruby).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action ruby/setup-ruby@v1)

# (action actions/setup-go@v6)

# Minitest, both gems, and the guest under the Go process host
(
scripts/lang/ruby.sh test
)
ci_import_env
