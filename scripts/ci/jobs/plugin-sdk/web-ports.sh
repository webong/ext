#!/usr/bin/env bash
# plugin-sdk/web-ports: drafted from .github/workflows/plugin-sdk.yml (job web-ports).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action actions/setup-java@v5)

# go test ./...
(
cd res/web/host
go test ./...
)
ci_import_env

# Swift and Java ports against Go
(
scripts/web-differential.sh
)
ci_import_env

# Native WebKit workspace
if [ "$RUNNER_OS" = macOS ]; then
(
scripts/web-workspace-ios.sh
)
ci_import_env
fi
