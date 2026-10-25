#!/usr/bin/env bash
# Real WebKit tests on macOS, independent of the plugin engine.
set -euo pipefail
cd "$(dirname "$0")/.."
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-webkit.XXXXXX")"
trap 'rm -rf "$work"' EXIT
swift test --package-path res/web/ios --filter ExtWebKitTests --scratch-path "$work"
