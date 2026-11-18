#!/usr/bin/env bash
# ctx release, prepare: verify (or build) what dist/ holds before it is attested.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# The two keychain helpers, as the old job downloaded them.
(
mkdir -p native-keychain
mv dist/amd64 native-keychain/amd64
mv dist/arm64 native-keychain/arm64
)

# (action actions/checkout@v5)

# (action actions/download-artifact@v7)

# (action actions/download-artifact@v7)

# (action actions/setup-go@v6)

# Test library modules
(
for module in src/ctx src/ctn res/system res/graph pkg/ext-go pkg/ext-go/engine pkg/ext-go/cgoengine pkg/ext-go/hashicorp pkg/ext-go/wasm pkg/ext-go/yaegi adapters/wasm adapters/evm res/web/host; do
  (cd "$module" && go test ./...) || exit 1
done
)
ci_import_env

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

# sh ./scripts/build-release.sh "${GITHUB_REF_NAME#ctx-}" dist
(
export CTX_KEYCHAIN_AMD64="$GITHUB_WORKSPACE/native-keychain/amd64/ctx-keychain"
export CTX_KEYCHAIN_ARM64="$GITHUB_WORKSPACE/native-keychain/arm64/ctx-keychain"
sh ./scripts/build-release.sh "${GITHUB_REF_NAME#ctx-}" dist
)
ci_import_env

# Create checksums
(
(cd dist && sha256sum ctx-* > checksums.txt)
)
ci_import_env

