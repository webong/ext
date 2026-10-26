#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
build="$(mktemp -d "${TMPDIR:-/tmp}/ext-system-android.XXXXXX")"
trap 'rm -rf "$build"' EXIT
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
platform="$(ls -d "$sdk"/platforms/android-*/android.jar 2>/dev/null | sort -V | tail -1)"
[[ -f "$platform" ]] || { echo "need an Android platform under $sdk" >&2; exit 1; }

mkdir -p "$build/portable" "$build/android"
javac -Xlint:all -Werror -d "$build/portable" \
  res/system/android/src/main/java/io/github/webong/ext/system/*.java \
  res/system/android/src/test/java/io/github/webong/ext/system/*.java \
  res/system/clipboard/android/src/main/java/io/github/webong/ext/clipboard/*.java \
  res/system/clipboard/android/src/test/java/io/github/webong/ext/clipboard/*.java \
  res/system/network/android/src/main/java/io/github/webong/ext/network/*.java \
  res/system/network/android/src/test/java/io/github/webong/ext/network/*.java
java -ea -cp "$build/portable" io.github.webong.ext.system.SurfaceTests
java -ea -cp "$build/portable" io.github.webong.ext.clipboard.ClipboardTests
java -ea -cp "$build/portable" io.github.webong.ext.network.NetworkTests
javac -Xlint:all -Werror -classpath "$platform:$build/portable" -d "$build/android" \
  res/system/android/src/android/java/io/github/webong/ext/system/android/*.java \
  res/system/clipboard/android/src/android/java/io/github/webong/ext/clipboard/android/*.java \
  res/system/network/android/src/android/java/io/github/webong/ext/network/android/*.java
