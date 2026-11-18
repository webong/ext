#!/usr/bin/env bash
# ext-zig release, distribute: push the package to its dist/ branch.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/download-artifact@v7)

# Push the package
(
export VERSION="${VERSION}"
scripts/dist-branch.sh dist/ext-zig "ext-zig-$VERSION" "dist/ext-zig-$VERSION-package.tar.gz" \
  "ext-zig $VERSION" \
  "Staged by $GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID from $GITHUB_REF_NAME."
)
ci_import_env

# Install Zig with a verified checksum
(
echo "ZIG_BIN=$(scripts/install-zig.sh "$RUNNER_TEMP/zig")" >>"$GITHUB_ENV"
)
ci_import_env

# Fetch it the way a consumer does
(
export VERSION="${VERSION}"
set -euo pipefail
url="$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/archive/refs/tags/ext-zig-$VERSION.tar.gz"
expected="$(cat "dist/ext-zig-$VERSION-package-hash.txt")"
actual=""
for attempt in 1 2 3 4 5; do
  if actual="$(ZIG_GLOBAL_CACHE_DIR="$RUNNER_TEMP/zig-global" "$ZIG_BIN" fetch "$url")"; then break; fi
  actual=""
  sleep 10
done
[[ "$actual" == "$expected" ]] || { echo "zig fetch of $url gave '$actual', not '$expected'" >&2; exit 1; }
{
  echo "### ext-zig $VERSION"
  echo
  echo '```'
  echo "zig fetch --save $url"
  echo '```'
  echo
  echo "Package hash: \`$actual\`"
} >>"$GITHUB_STEP_SUMMARY"
)
ci_import_env
