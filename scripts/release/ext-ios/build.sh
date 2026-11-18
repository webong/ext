#!/usr/bin/env bash
# ext-ios release, build: stage the iOS and iOS Simulator slices of the Apple SDK, test them in an
# iOS Simulator, and keep what the release ships. The macOS slice and the host tests are
# ext-macos's (scripts/release/ext-macos/build.sh).
. "$(dirname "$0")/../apple.sh"

apple_version ext-ios
apple_stage ios
apple_reactor

# Swift tests in an iOS Simulator
(
set -euo pipefail
device="$(apple_simulator)"
cd pkg/ext-ios
TEST_RUNNER_EXT_RUST_REACTOR="$EXT_RUST_REACTOR" xcodebuild test -scheme Ext-Package \
  -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

# Web rules in an iOS Simulator
(
set -euo pipefail
device="$(apple_simulator)"
cd res/web/ios
xcodebuild test -scheme ExtWeb-Package -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

# Clipboard binding in an iOS Simulator
(
set -euo pipefail
device="$(apple_simulator)"
cd res/system/capture/clipboard/ios
xcodebuild test -scheme ExtClipboard -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

# System shell library in an iOS Simulator
(
set -euo pipefail
device="$(apple_simulator)"
for package in capture/surface/ios:ExtSystem service/host/ios:ExtHost capture/share/ios:ExtShare capture/audio/ios:ExtAudio; do
  (cd "res/system/${package%%:*}" && xcodebuild test -scheme "${package##*:}" -destination "platform=iOS Simulator,id=$device")
done
)
ci_import_env

# System network observation in an iOS Simulator
(
set -euo pipefail
device="$(apple_simulator)"
cd res/system/service/network/ios
xcodebuild test -scheme ExtNetwork -destination "platform=iOS Simulator,id=$device"
)
ci_import_env

apple_ship ext-ios
