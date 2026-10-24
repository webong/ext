#!/usr/bin/env bash
# Build and test ExtWeb of pkg/ext-ios: the cookie, userscript and bookmarklet rules of res/web,
# against its language-neutral fixtures in res/web/testdata. EXT_WEB_ONLY=1 makes the package
# manifest declare just ExtWeb and its tests, so this needs Swift only: no engine xcframework.
# It fails if ExtWeb ever imports the plugin code or the engine.
# EXT_WEB_DIFF=<corpus.json> also compares the port with a corpus the Go implementation wrote
# (scripts/ext-web-differential.sh makes one and runs this).
# Usage: scripts/ext-ios-web.sh [scratch-path]
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo 'ext-ios-web.sh needs macOS (ExtWeb uses CryptoKit)' >&2; exit 1; }
if grep -rn -E 'import +(ExtPlugin|CExt|ExtConformance)' pkg/ext-ios/Sources/ExtWeb; then
  echo 'ExtWeb must not import the plugin code or the engine' >&2
  exit 1
fi
scratch="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-web-swift.XXXXXX")}"
EXT_WEB_ONLY=1 swift test --package-path pkg/ext-ios --scratch-path "$scratch"
