#!/usr/bin/env bash
# ext-plugin release, c2go: translate the engine for this cell's platform, test it, and stage the
# c2go driver's archive (scripts/c2goengine-translate.sh).
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# Version
(
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
if [[ "$REF_TYPE" == tag ]]; then
  version="${REF_NAME#ext-plugin-v}"
else
  version="$(sed -n 's/^project(ext_plugin VERSION \([0-9.]*\) .*/\1/p' lib/plugin/CMakeLists.txt)"
fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.2.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Use the matrix compiler
if [ "${MATRIX_CC:-}" != "" ]; then
(
echo "CC=${MATRIX_CC:-}" >> "$GITHUB_ENV"
)
ci_import_env
fi

# Translate, test and stage
if [ "$RUNNER_OS" != Windows ]; then
(
EXT_C2GO_RACE="${MATRIX_RACE:-}" scripts/c2goengine-translate.sh "$VERSION"
)
ci_import_env
fi
if [ "$RUNNER_OS" = Windows ]; then
(
export VERSION
ci_msys2 <<'MSYS2'
scripts/c2goengine-translate.sh "$VERSION"
MSYS2
)
ci_import_env
fi

# Keep what this cell ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-plugin-*-c2go-*.tar.gz dist/ext-plugin-*-c2go-*.sha256; do cp "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
