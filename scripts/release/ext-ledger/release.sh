#!/usr/bin/env bash
# ext-ledger release, release: publish the GitHub release.
# Built like ext-plugin, whose release package it follows.
. "$(dirname "$0")/../../ci/jobs/lib.sh"


# Publish GitHub release
(
export GH_TOKEN="${GH_TOKEN}"
gh release create "$GITHUB_REF_NAME" dist/* --verify-tag --generate-notes --latest=false --title "$GITHUB_REF_NAME"
)
ci_import_env
