#!/usr/bin/env bash
# ext-plugin release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# Version
(
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
if [[ "$REF_TYPE" == tag ]]; then
  version="${REF_NAME#ext-plugin-v}"
else
  version="$(sed -n 's/^project(ext_host_prototype VERSION \([0-9.]*\) .*/\1/p' lib/plugin/CMakeLists.txt)"
fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.2.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# (action ilammy/msvc-dev-cmd@v1 if matrix.msvc)

# Build, test and stage
(
export ENGINE_PLATFORM="${MATRIX_PLATFORM:-}"
export VERSION="${VERSION}"
scripts/ext-plugin-release.sh "$VERSION" "$ENGINE_PLATFORM" "$RUNNER_TEMP/engine"
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-plugin-*.tar.gz; do cp -R "$f" release-out/; found=1; done
for f in dist/ext-plugin-*.sha256; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
