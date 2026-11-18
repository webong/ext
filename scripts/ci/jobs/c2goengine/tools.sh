#!/usr/bin/env bash
# c2goengine/tools: drafted from .github/workflows/c2goengine.yml (job tools).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# Test the generator
(
cd pkg/ext-go/c2goengine/tools
GOWORK=off go test -count=1 ./... && GOWORK=off go vet ./...
)
ci_import_env
