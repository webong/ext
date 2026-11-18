#!/usr/bin/env bash
# ext-macos release, build: stage the macOS slice of the Apple SDK, run the Swift tests on the
# macOS host, and keep what the release ships. The iOS slices and the Simulator tests are
# ext-ios's (scripts/release/ext-ios/build.sh).
. "$(dirname "$0")/../apple.sh"

apple_version ext-macos

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

apple_stage macos
apple_reactor

# Swift tests on the host
(
swift test --package-path pkg/ext-ios
)
ci_import_env

apple_ship ext-macos
