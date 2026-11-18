#!/usr/bin/env bash
# ctx release, release: publish the GitHub release.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"


# Publish GitHub release
(
export GH_TOKEN="${GH_TOKEN}"
gh release create "$GITHUB_REF_NAME" dist/* --verify-tag --generate-notes --title "$GITHUB_REF_NAME"
)
ci_import_env
