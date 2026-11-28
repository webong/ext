#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
command -v javac >/dev/null 2>&1 || { echo 'clipboard-android.sh needs a JDK' >&2; exit 1; }
build="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-clipboard-java.XXXXXX")}"; mkdir -p "$build"
javac -Xlint:all -Werror -d "$build" pkg/ext-android/fixtures/java/io/github/webong/ext/system/fixtures/*.java pkg/ext-android/act/clipboard/src/main/java/io/github/webong/ext/clipboard/*.java \
  pkg/ext-android/act/clipboard/src/test/java/io/github/webong/ext/clipboard/*.java
java -ea -cp "$build" io.github.webong.ext.clipboard.ClipboardTests
java -ea -cp "$build" io.github.webong.ext.clipboard.ClipboardCancellationTests
