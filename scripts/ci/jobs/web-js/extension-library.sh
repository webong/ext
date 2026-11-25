#!/usr/bin/env bash
# web-js/extension-library: drafted from .github/workflows/web-js.yml (job extension-library).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-node@v5)

# Test
(
cd pkg/ext-web
npm test
)
ci_import_env

# Check TypeScript declarations
(
npx --yes --package typescript@5.8.3 tsc --noEmit --strict --module NodeNext --moduleResolution NodeNext --target ES2022 pkg/ext-web/typecheck.mts
)
ci_import_env

# Build example extensions
(
cd examples/web/extensions
npm run build:examples
)
ci_import_env

# (action actions/setup-go@v6)

# Package the examples with the Go tooling
(
export EXT_WEB_DIST="$GITHUB_WORKSPACE/examples/web/extensions/dist"
cd res/web/host
go test ./artifact/extension -run TestExtWebExamplesPackage -count=1 -v
)
ci_import_env
