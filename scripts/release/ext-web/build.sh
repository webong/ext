#!/usr/bin/env bash
# ext-web release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-node@v5)

# Version
(
export INPUT_VERSION="${INPUT_VERSION_DISPATCH:-}"
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
if [[ "$REF_TYPE" == tag ]]; then version="${REF_NAME#ext-web-v}"; else version="$INPUT_VERSION"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
declared=$(node -p "require('./pkg/ext-web/package.json').version")
[[ "$declared" == "$version" ]] || { echo "pkg/ext-web/package.json says $declared but the release is $version" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Test
(
cd pkg/ext-web
npm test
)
ci_import_env

# Check TypeScript declarations
(
npx --yes --package typescript@5.8.3 tsc --noEmit --strict --module NodeNext --moduleResolution NodeNext --target ES2022 pkg/ext-web/typecheck.mts
)
ci_import_env

# Pack
(
export VERSION="${VERSION}"
set -euo pipefail
out="dist/ext-web-$VERSION"
mkdir -p "$out"
(cd pkg/ext-web && npm pack --pack-destination "../../$out")
[[ -f "$out/ext-web-$VERSION.tgz" ]] || { echo "expected ext-web-$VERSION.tgz" >&2; ls "$out" >&2; exit 1; }
# The tarball must hold exactly the files package.json lists, not the examples, tests or dist.
tar -tzf "$out/ext-web-$VERSION.tgz" | grep -E '(test|examples|dist|node_modules)' && { echo 'unexpected files in the tarball' >&2; exit 1; } || true
)
ci_import_env

# Check that the tarball installs and imports
(
export VERSION="${VERSION}"
set -euo pipefail
work="$(mktemp -d)"
cd "$work"
npm init -y >/dev/null
npm install "$GITHUB_WORKSPACE/dist/ext-web-$VERSION/ext-web-$VERSION.tgz" >/dev/null
for entry in ext-web ext-web/native ext-web/themes ext-web/workspace ext-web/userscripts ext-web/bridge; do
  node --input-type=module -e "await import('$entry'); console.log('imports', '$entry')"
done
)
ci_import_env

# Checksums
(
export VERSION="${VERSION}"
set -euo pipefail
cd "dist/ext-web-$VERSION"
sha256sum ./*.tgz | tee checksums.txt
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-web-${VERSION}/*; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
