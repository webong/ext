#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
build="$(mktemp -d "${TMPDIR:-/tmp}/ext-network-android.XXXXXX")"
trap 'rm -rf "$build"' EXIT
javac -Xlint:all -Werror -d "$build" \
  res/network/android/src/main/java/io/github/webong/ext/network/*.java \
  res/network/android/src/test/java/io/github/webong/ext/network/*.java
java -ea -cp "$build" io.github.webong.ext.network.NetworkTests
