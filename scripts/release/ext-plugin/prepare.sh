#!/usr/bin/env bash
# ext-plugin release, prepare: verify (or build) what dist/ holds before it is attested.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/download-artifact@v7)

# Verify and collect checksums
(
set -euo pipefail
cd dist
cat ./*.sha256 >checksums.txt
rm ./*.sha256
sha256sum -c checksums.txt
[ "$(ls ./*.tar.gz | grep -vc -- -c2go-)" -eq 5 ] || { echo "expected 5 platform archives" >&2; ls; exit 1; }
[ "$(ls ./*-c2go-*.tar.gz | wc -l)" -eq 6 ] || { echo "expected 6 c2go translation archives" >&2; ls; exit 1; }
)
ci_import_env

# The citation ext lib fetch reads: each archive's URL, SHA-256 and the files each driver takes.
(
set -euo pipefail
version="${GITHUB_REF_NAME#ext-plugin-v}"
[[ "$GITHUB_REF_NAME" == ext-plugin-v* ]] || version="$(sed -n 's/^project(ext_plugin VERSION \([0-9.]*\) .*/\1/p' lib/plugin/CMakeLists.txt)"
python3 -I scripts/library-citation.py scripts/release/ext-plugin/citation.json "$version" "ext-plugin-v$version" dist
cp lib/plugin/interface.json dist/ext_plugin.interface.json
)
ci_import_env

