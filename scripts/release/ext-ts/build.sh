#!/usr/bin/env bash
# ext-ts release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-node@v5)

# Version
(
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
declared=$(node -p "require('./pkg/ext-ts/package.json').version")
if [[ "$REF_TYPE" == tag ]]; then version="${REF_NAME#ext-ts-v}"; else version="$declared"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
[[ "$declared" == "$version" ]] || { echo "pkg/ext-ts/package.json says $declared but the release is $version" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Test
(
cd pkg/ext-ts
npm test
)
ci_import_env

# Check TypeScript declarations
(
npx --yes --package typescript@5.8.3 tsc --noEmit --strict --module NodeNext --moduleResolution NodeNext --target ES2022 pkg/ext-ts/typecheck.mts
)
ci_import_env

# Pack
(
export VERSION="${VERSION}"
set -euo pipefail
out="dist/ext-ts-$VERSION"
mkdir -p "$out"
(cd pkg/ext-ts && npm pack --pack-destination "../../$out")
[[ -f "$out/ext-io-$VERSION.tgz" ]] || { echo "expected ext-io-$VERSION.tgz" >&2; ls "$out" >&2; exit 1; }
# The tarball must hold exactly the files package.json lists, not the examples or tests.
tar -tzf "$out/ext-io-$VERSION.tgz" | grep -E '(test|examples|typecheck|node_modules)' && { echo 'unexpected files in the tarball' >&2; exit 1; } || true
(cd pkg/ext-ts && npm publish --dry-run)
)
ci_import_env

# Check that the tarball installs and imports
(
export VERSION="${VERSION}"
set -euo pipefail
work="$(mktemp -d)"
cd "$work"
npm init -y >/dev/null
npm install "$GITHUB_WORKSPACE/dist/ext-ts-$VERSION/ext-io-$VERSION.tgz" >/dev/null
# ext-io/plugin/engine loads a consumer-built addon on first use, so only its module is imported.
for entry in ext-io/plugin ext-io/plugin/node ext-io/plugin/engine; do
  node --input-type=module -e "await import('$entry'); console.log('imports', '$entry')"
done
)
ci_import_env

# Checksums
(
export VERSION="${VERSION}"
(cd "dist/ext-ts-$VERSION" && sha256sum ./*.tgz | tee checksums.txt)
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-ts-${VERSION}/*; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
