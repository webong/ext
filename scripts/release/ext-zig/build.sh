#!/usr/bin/env bash
# ext-zig release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# Version
(
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
if [[ "$REF_TYPE" == tag ]]; then
  version="${REF_NAME#ext-zig-v}"
else
  version="$(sed -n 's/^[[:space:]]*\.version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' pkg/ext-zig/build.zig.zon)"
fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Install Zig with a verified checksum
(
echo "ZIG_BIN=$(scripts/install-zig.sh "$RUNNER_TEMP/zig")" >>"$GITHUB_ENV"
)
ci_import_env

# Stage, build and test the package
(
export VERSION="${VERSION}"
scripts/ext-zig-release.sh "$VERSION"
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-zig-${VERSION}/*.tar.gz; do cp -R "$f" release-out/; found=1; done
for f in dist/ext-zig-${VERSION}/*.txt; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
