#!/usr/bin/env bash
# ext-ledger release, prepare: verify (or build) what dist/ holds before it is attested.
# Built like ext-plugin, whose release package it follows.
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
[ "$(ls ./*.tar.gz | wc -l)" -eq 5 ] || { echo "expected 5 platform archives" >&2; ls; exit 1; }
)
ci_import_env

# The citation ext lib fetch reads: each archive's URL, SHA-256 and the files each driver takes.
(
set -euo pipefail
version="${GITHUB_REF_NAME#ext-ledger-v}"
[[ "$GITHUB_REF_NAME" == ext-ledger-v* ]] || version="$(sed -n 's/^project(ext_ledger VERSION \([0-9.]*\) .*/\1/p' lib/ledger/CMakeLists.txt)"
python3 -I scripts/library-citation.py scripts/release/ext-ledger/citation.json "$version" "ext-ledger-v$version" dist
cp lib/ledger/interface.json dist/ext_ledger.interface.json
)
ci_import_env

