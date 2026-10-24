#!/usr/bin/env bash
# Compile and test the web rules of pkg/ext-android (io.github.webong.ext.web): the cookie,
# userscript and bookmarklet rules of res/web, against its language-neutral fixtures in
# res/web/testdata. It needs a JDK only: no CMake, no NDK and no engine build, because the web
# package imports nothing of the plugin code. It fails if it ever does.
# EXT_WEB_DIFF=<corpus.json> also compares the port with a corpus the Go implementation wrote
# (scripts/ext-web-differential.sh makes one and runs this).
# Usage: scripts/ext-android-web.sh [build-dir]
set -euo pipefail
cd "$(dirname "$0")/.."
command -v javac >/dev/null 2>&1 || { echo 'ext-android-web.sh needs a JDK (javac)' >&2; exit 1; }
web=pkg/ext-android/src/main/java/io/github/webong/ext/web
tests=pkg/ext-android/src/test/java/io/github/webong/ext/web
if grep -rn -E 'io\.github\.webong\.ext\.plugin|System\.loadLibrary' "$web"; then
  echo 'the web package must not use the plugin code or native code' >&2
  exit 1
fi
build_dir="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-web-java.XXXXXX")}"
mkdir -p "$build_dir"
javac -Xlint:all -Werror -d "$build_dir" "$web"/*.java "$tests"/*.java
java -ea -cp "$build_dir" io.github.webong.ext.web.WebTests
