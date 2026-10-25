#!/usr/bin/env bash
# Build and test the iOS module of res/web (res/web/ios, the SwiftPM package ExtWeb): the cookie,
# userscript and bookmarklet rules, against the language-neutral fixtures in res/web/testdata.
# It is a package of its own with no dependency, so this needs Swift only: no engine
# xcframework, and nothing of the plugin code can be imported. pkg/ext-ios ships the same
# sources (its Sources/ExtWeb is a link to this package's target).
# EXT_WEB_DIFF=<corpus.json> also compares the port with a corpus the Go implementation wrote
# (scripts/web-differential.sh makes one and runs this).
# Usage: scripts/web-ios.sh [scratch-path]
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo 'web-ios.sh needs macOS (ExtWeb uses CryptoKit)' >&2; exit 1; }
scratch="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-web-swift.XXXXXX")}"
swift test --package-path res/web/ios --filter ExtWebTests --scratch-path "$scratch"
