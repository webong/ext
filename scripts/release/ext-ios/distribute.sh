#!/usr/bin/env bash
# ext-ios release, distribute: push the package to its dist/ branch.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/download-artifact@v7)

# Push the package
(
export VERSION="${VERSION}"
scripts/dist-branch.sh dist/ext-ios "$VERSION" "dist/ext-ios-$VERSION-package.tar.gz" \
  "ext-ios $VERSION" \
  "Staged by $GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID from $GITHUB_REF_NAME."
)
ci_import_env
