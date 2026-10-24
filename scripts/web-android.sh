#!/usr/bin/env bash
# Compile and test the Android module of res/web (res/web/android, package
# io.github.webong.ext.web): the cookie, userscript and bookmarklet rules, against the
# language-neutral fixtures in res/web/testdata. It needs a JDK only: no CMake, no NDK and no
# engine build, and it compiles nothing but its own sources, so it cannot use the plugin code.
# pkg/ext-android ships the same classes in its AAR.
# EXT_WEB_DIFF=<corpus.json> also compares the port with a corpus the Go implementation wrote
# (scripts/web-differential.sh makes one and runs this).
# Usage: scripts/web-android.sh [build-dir]
set -euo pipefail
cd "$(dirname "$0")/.."
command -v javac >/dev/null 2>&1 || { echo 'web-android.sh needs a JDK (javac)' >&2; exit 1; }
module=res/web/android/src
build_dir="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-web-java.XXXXXX")}"
mkdir -p "$build_dir"
javac -Xlint:all -Werror -d "$build_dir" "$module"/main/java/io/github/webong/ext/web/*.java \
  "$module"/test/java/io/github/webong/ext/web/*.java
java -ea -cp "$build_dir" io.github.webong.ext.web.WebTests
