#!/usr/bin/env bash
# Check the Swift and Java modules of res/web against its Go module beyond the fixtures:
# res/web/internal/portcorpus records what Go answers for a few thousand URLs, cookie domains,
# userscript names and bookmarklet strings, and each module must answer the same. Run it when
# Go, a module or the corpus changes. GODEBUG=urlstrictcolons=1 is what the ports follow for
# hosts with several colons.
# Usage: scripts/web-differential.sh
set -euo pipefail
cd "$(dirname "$0")/.."
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-web-diff.XXXXXX")"
trap 'rm -rf "$work"' EXIT
( cd res/web && GODEBUG=urlstrictcolons=1 go run ./internal/portcorpus "$work/corpus.json" )
export EXT_WEB_DIFF="$work/corpus.json"
scripts/web-android.sh "$work/java"
if [[ "$(uname -s)" == Darwin ]]; then
  scripts/web-ios.sh "$work/swift"
else
  echo 'skipping the Swift module: it needs macOS'
fi
