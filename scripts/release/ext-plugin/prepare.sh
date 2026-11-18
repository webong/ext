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
[ "$(ls ./*.tar.gz | wc -l)" -eq 5 ] || { echo "expected 5 platform archives" >&2; ls; exit 1; }
)
ci_import_env

