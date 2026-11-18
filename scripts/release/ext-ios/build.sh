#!/usr/bin/env bash
# ext-ios release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# Version
(
export INPUT_VERSION="${INPUT_VERSION_DISPATCH:-}"
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
if [[ "$REF_TYPE" == tag ]]; then version="${REF_NAME#ext-ios-v}"; else version="$INPUT_VERSION"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# (action actions/setup-go@v6)

# (action actions/setup-java@v5)

# (action dtolnay/rust-toolchain@stable)

# (action actions/cache@v5)

# Web rules against the fixtures and against Go
(
scripts/web-differential.sh
)
ci_import_env

# Clipboard contracts and native binding
(
swift test --package-path res/system/capture/clipboard/ios
)
ci_import_env

# System shell contracts and native binding
(
for package in capture/surface/ios service/host/ios capture/share/ios capture/audio/ios; do
  swift test --package-path "res/system/$package"
done
)
ci_import_env

# System network observation contracts and native binding
(
swift test --package-path res/system/service/network/ios
)
ci_import_env

# Stage the release
(
export VERSION="${VERSION}"
scripts/ext-ios-release.sh --with-wamr "$VERSION"
)
ci_import_env

# Build the reactor the host tests run
(
CARGO_TARGET_DIR="$RUNNER_TEMP/cargo" cargo build --release --locked \
  --manifest-path pkg/ext-rust/Cargo.toml --target wasm32-wasip1 --example shared
echo "EXT_RUST_REACTOR=$RUNNER_TEMP/cargo/wasm32-wasip1/release/examples/shared.wasm" >>"$GITHUB_ENV"
)
ci_import_env

# Swift tests on the host
(
swift test --package-path pkg/ext-ios
)
ci_import_env

# Swift tests in an iOS Simulator
(
set -euo pipefail
device="$(xcrun simctl list devices available | grep -m1 -E 'iPhone' | sed -E 's/^ *(.*) \(([0-9A-F-]{36})\).*/\2/')"
[[ -n "$device" ]] || { echo 'no iPhone simulator is installed' >&2; exit 1; }
cd pkg/ext-ios
TEST_RUNNER_EXT_RUST_REACTOR="$EXT_RUST_REACTOR" xcodebuild test -scheme Ext-Package \
  -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

# Web rules in an iOS Simulator
(
set -euo pipefail
device="$(xcrun simctl list devices available | grep -m1 -E 'iPhone' | sed -E 's/^ *(.*) \(([0-9A-F-]{36})\).*/\2/')"
[[ -n "$device" ]] || { echo 'no iPhone simulator is installed' >&2; exit 1; }
cd res/web/ios
xcodebuild test -scheme ExtWeb-Package -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

# Clipboard binding in an iOS Simulator
(
set -euo pipefail
device="$(xcrun simctl list devices available | grep -m1 -E 'iPhone' | sed -E 's/^ *(.*) \(([0-9A-F-]{36})\).*/\2/')"
[[ -n "$device" ]] || { echo 'no iPhone simulator is installed' >&2; exit 1; }
cd res/system/capture/clipboard/ios
xcodebuild test -scheme ExtClipboard -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

# System shell library in an iOS Simulator
(
set -euo pipefail
device="$(xcrun simctl list devices available | grep -m1 -E 'iPhone' | sed -E 's/^ *(.*) \(([0-9A-F-]{36})\).*/\2/')"
[[ -n "$device" ]] || { echo 'no iPhone simulator is installed' >&2; exit 1; }
for package in capture/surface/ios:ExtSystem service/host/ios:ExtHost capture/share/ios:ExtShare capture/audio/ios:ExtAudio; do
  (cd "res/system/${package%%:*}" && xcodebuild test -scheme "${package##*:}" -destination "platform=iOS Simulator,id=$device")
done
)
ci_import_env

# System network observation in an iOS Simulator
(
set -euo pipefail
device="$(xcrun simctl list devices available | grep -m1 -E 'iPhone' | sed -E 's/^ *(.*) \(([0-9A-F-]{36})\).*/\2/')"
[[ -n "$device" ]] || { echo 'no iPhone simulator is installed' >&2; exit 1; }
cd res/system/service/network/ios
xcodebuild test -scheme ExtNetwork -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

# Checksums
(
export VERSION="${VERSION}"
set -euo pipefail
cd "dist/ext-ios-$VERSION"
tar -czf "ext-ios-$VERSION-package.tar.gz" -C package .
shasum -a 256 *.zip *.tar.gz WAMR-LICENSE | tee checksums.txt
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-ios-${VERSION}/*.zip; do cp -R "$f" release-out/; found=1; done
for f in dist/ext-ios-${VERSION}/*.tar.gz; do cp -R "$f" release-out/; found=1; done
for f in dist/ext-ios-${VERSION}/WAMR-LICENSE; do cp -R "$f" release-out/; found=1; done
for f in dist/ext-ios-${VERSION}/checksums.txt; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
