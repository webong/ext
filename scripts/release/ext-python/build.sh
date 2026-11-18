#!/usr/bin/env bash
# ext-python release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-python@v6)

# Version
(
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
declared=$(python3 -c 'import tomllib; print(tomllib.load(open("pkg/ext-python/pyproject.toml", "rb"))["project"]["version"])')
if [[ "$REF_TYPE" == tag ]]; then version="${REF_NAME#ext-python-v}"; else version="$declared"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
[[ "$declared" == "$version" ]] || { echo "pkg/ext-python/pyproject.toml says $declared but the release is $version" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Test
(
python3 -I -m unittest discover -s pkg/ext-python/tests
)
ci_import_env

# Build and check
(
export VERSION="${VERSION}"
set -euo pipefail
out="$GITHUB_WORKSPACE/dist/ext-python-$VERSION"
python3 -m pip install --quiet build==1.3.0 twine==6.2.0
python3 -m build --outdir "$out" pkg/ext-python
python3 -m twine check --strict "$out"/*
ls "$out/ext_io-$VERSION-py3-none-any.whl" "$out/ext_io-$VERSION.tar.gz"
)
ci_import_env

# Check that the wheel installs and imports
(
export VERSION="${VERSION}"
set -euo pipefail
venv="$(mktemp -d)"
python3 -m venv "$venv"
"$venv/bin/pip" install --quiet "dist/ext-python-$VERSION/ext_io-$VERSION-py3-none-any.whl"
(cd "$venv" && "$venv/bin/python" -I -c 'import ext_io.plugin, ext_io.host; print("imports ext_io.plugin and ext_io.host")')
)
ci_import_env

# Checksums
(
export VERSION="${VERSION}"
(cd "dist/ext-python-$VERSION" && sha256sum ./*.whl ./*.tar.gz | tee checksums.txt)
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-python-${VERSION}/*; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
