#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
build="$(mktemp -d "${TMPDIR:-/tmp}/ext-network-android.XXXXXX")"
trap 'rm -rf "$build"' EXIT
javac -Xlint:all -Werror -d "$build" \
  pkg/ext-android/fixtures/java/io/github/webong/ext/system/fixtures/*.java \
  pkg/ext-android/net/src/main/java/io/github/webong/ext/network/*.java \
  pkg/ext-android/net/src/test/java/io/github/webong/ext/network/*.java
java -ea -cp "$build" io.github.webong.ext.network.NetworkTests
java -ea -cp "$build" io.github.webong.ext.network.NetworkFixtureTests
java -ea -cp "$build" io.github.webong.ext.network.NetworkCancellationTests
