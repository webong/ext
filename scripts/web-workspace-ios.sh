#!/usr/bin/env bash
# Real WebKit tests, independent of the plugin engine.
# Default: macOS. --simulator <UDID> uses an already booted iOS simulator.
set -euo pipefail
cd "$(dirname "$0")/.."
simulator=""
if [[ "${1:-}" == --simulator ]]; then
  simulator="${2:?--simulator needs a booted simulator UDID}"
  shift 2
fi
[[ $# -eq 0 ]] || { echo 'usage: scripts/web-workspace-ios.sh [--simulator <UDID>]' >&2; exit 2; }
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-webkit.XXXXXX")"
trap 'rm -rf "$work"' EXIT
if [[ -n "$simulator" ]]; then
  # Product schemes do not expose a test action; use the package scheme and select the native tests.
  (cd res/web/ios && xcodebuild -quiet -scheme ExtWeb-Package \
    -destination "platform=iOS Simulator,id=$simulator" -derivedDataPath "$work" \
    -only-testing:ExtWebKitTests test)
else
  swift test --package-path res/web/ios --filter ExtWebKitTests --scratch-path "$work"
fi
