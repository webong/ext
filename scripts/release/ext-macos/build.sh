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
swift test --package-path pkg/ext-darwin/act/clipboard
)
ci_import_env

# System shell contracts and native binding
(
for package in act/capture/surface act/share act/capture/audio; do
  swift test --package-path "pkg/ext-darwin/$package"
done
)
ci_import_env

# System network observation contracts and native binding
(
swift test --package-path pkg/ext-darwin/net
)
ci_import_env

apple_stage macos
apple_reactor

# Swift tests on the host, including the ExtHost ledger binding.
(
swift test --package-path pkg/ext-darwin
)
ci_import_env

# The native surface mechanism also ships as a private Ext resource guest.
(
set -euo pipefail
guest="res/system/capture/surface/ios/Guest"
swift build --disable-sandbox --package-path "$guest" -c release
binary="$(swift build --disable-sandbox --package-path "$guest" -c release --show-bin-path)/ExtSurfaceGuest"
out="dist/ext-macos-$VERSION/system.surface.darwin"
case "$(uname -m)" in
  arm64) guest_arch=arm64 ;;
  x86_64) guest_arch=amd64 ;;
  *) echo 'unsupported macOS guest architecture' >&2; exit 1 ;;
esac
python3 scripts/release/pack-resource-guest.py "$guest/ext.json" "$binary" "$out" darwin "$guest_arch"
rm -rf "$out"
)
ci_import_env

apple_ship ext-macos
