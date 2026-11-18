#!/usr/bin/env bash
# ctn release, prepare: verify (or build) what dist/ holds before it is attested.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# Test ctn and the libraries it builds on
(
for module in src/ctn res/system pkg/ext-go pkg/ext-go/engine pkg/ext-go/cgoengine res/web/host; do
  (cd "$module" && go test ./...) || exit 1
done
)
ci_import_env

# sh ./scripts/build-release-ctn.sh "${GITHUB_REF_NAME#ctn-}" dist
(
sh ./scripts/build-release-ctn.sh "${GITHUB_REF_NAME#ctn-}" dist
)
ci_import_env

# Create checksums
(
(cd dist && sha256sum ctn-* > checksums.txt)
)
ci_import_env

