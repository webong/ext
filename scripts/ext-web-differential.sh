#!/usr/bin/env bash
# Check the Swift and Java ports of res/web against the Go implementation beyond the fixtures:
# res/web/internal/portcorpus records what Go answers for a few thousand URLs, cookie domains,
# userscript names and bookmarklet strings, and each port must answer the same. Run it when Go,
# a port or the corpus changes. GODEBUG=urlstrictcolons=1 is what the ports follow for hosts
# with several colons.
# Usage: scripts/ext-web-differential.sh
set -euo pipefail
cd "$(dirname "$0")/.."
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-web-diff.XXXXXX")"
trap 'rm -rf "$work"' EXIT
( cd res/web && GODEBUG=urlstrictcolons=1 go run ./internal/portcorpus "$work/corpus.json" )
export EXT_WEB_DIFF="$work/corpus.json"
scripts/ext-android-web.sh "$work/java"
if [[ "$(uname -s)" == Darwin ]]; then
  scripts/ext-ios-web.sh "$work/swift"
else
  echo 'skipping the Swift port: it needs macOS'
fi
