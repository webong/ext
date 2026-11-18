#!/usr/bin/env bash
# plugin-sdk/sdk: drafted from .github/workflows/plugin-sdk.yml (job sdk).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action actions/setup-node@v5)

# Test plugin library modules
(
for module in pkg/ext-go pkg/ext-go/engine pkg/ext-go/cgoengine pkg/ext-go/hashicorp pkg/ext-go/wasm pkg/ext-go/yaegi res/web/host; do
  (cd "$module" && go test -race ./...) || exit 1
done
)
ci_import_env

# go test -race ./pkg/ext-go/hashicorp/cmd/ext-plugin
(
go test -race ./pkg/ext-go/hashicorp/cmd/ext-plugin
)
ci_import_env

# scripts/lang/ts.sh test
(
scripts/lang/ts.sh test
)
ci_import_env

# scripts/lang/python.sh test
(
scripts/lang/python.sh test
)
ci_import_env

# Check TypeScript declarations
(
scripts/lang/ts.sh typecheck
)
ci_import_env

# go run ./pkg/ext-go/examples/plugin-sdk --backend jsonline
(
go run ./pkg/ext-go/examples/plugin-sdk --backend jsonline
)
ci_import_env

# go run ./pkg/ext-go/examples/plugin-sdk --backend inprocess
(
go run ./pkg/ext-go/examples/plugin-sdk --backend inprocess
)
ci_import_env

# go run ./pkg/ext-go/examples/plugin-typescript
(
go run ./pkg/ext-go/examples/plugin-typescript
)
ci_import_env

# go build -o /tmp/ext-plugin-example ./pkg/ext-go/examples/plugin-typescript
(
go build -o /tmp/ext-plugin-example ./pkg/ext-go/examples/plugin-typescript
)
ci_import_env

# node pkg/ext-go/examples/plugin-typescript/host.mjs /tmp/ext-plugin-example
(
node pkg/ext-go/examples/plugin-typescript/host.mjs /tmp/ext-plugin-example
)
ci_import_env

# go run ./pkg/ext-go/hashicorp/cmd/ext-plugin inspect --root pkg/ext-go/examples/plugin-package --entry main pkg/ext-go/examples/plugin-package/plugin.json
(
go run ./pkg/ext-go/hashicorp/cmd/ext-plugin inspect --root pkg/ext-go/examples/plugin-package --entry main pkg/ext-go/examples/plugin-package/plugin.json
)
ci_import_env
