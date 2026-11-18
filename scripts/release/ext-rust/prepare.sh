#!/usr/bin/env bash
# ext-rust release, prepare: verify (or build) what dist/ holds before it is attested.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action actions/download-artifact@v7)

# Verify checksums
(
(cd dist && sha256sum -c checksums.txt)
)
ci_import_env

