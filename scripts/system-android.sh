#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
build="$(mktemp -d "${TMPDIR:-/tmp}/ext-system-android.XXXXXX")"
trap 'rm -rf "$build"' EXIT
javac -Xlint:all -Werror -d "$build" \
  res/system/android/src/main/java/io/github/webong/ext/system/*.java \
  res/system/android/src/test/java/io/github/webong/ext/system/*.java
java -ea -cp "$build" io.github.webong.ext.system.SurfaceTests
