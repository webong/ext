#!/usr/bin/env bash
# ext release, prepare: verify (or build) what dist/ holds before it is attested.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# Test ext and the libraries it builds on
(
for module in cmd/ext src/ctx src/ctn src/cto src/cto/sandbox res/system pkg/ext-go/engine pkg/ext-go/cgoengine res/web/host; do
  (cd "$module" && go test ./...) || exit 1
done
)
ci_import_env

# Assemble only after every required native engine artifact is present.
(
unset EXT_RELEASE_TARGETS
EXT_RELEASE_NATIVE_DIR="$PWD/dist/native" sh ./scripts/build-release-ext.sh "${GITHUB_REF_NAME#ext-}" cli-dist
cp cli-dist/ext-* dist/
rm -rf cli-dist dist/native
)
ci_import_env

# Create checksums
(
(cd dist && sha256sum ext-* > checksums.txt)
)
ci_import_env

