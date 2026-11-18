#!/usr/bin/env bash
# ctx release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"
export MACOSX_DEPLOYMENT_TARGET="11.0"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# go test ./adapters/keychain/store
(
go test ./adapters/keychain/store
)
ci_import_env

# go test ./service/credential/...
(
cd res/system
go test ./service/credential/...
)
ci_import_env

# mkdir -p keychain && go build -trimpath -ldflags '-s -w' -o keychain/ctx-keychain ./adapters/keychain/native
(
mkdir -p keychain && go build -trimpath -ldflags '-s -w' -o keychain/ctx-keychain ./adapters/keychain/native
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out/${MATRIX_ARCH}
shopt -s nullglob
found=0
for f in keychain/ctx-keychain; do cp -R "$f" release-out/${MATRIX_ARCH}/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
