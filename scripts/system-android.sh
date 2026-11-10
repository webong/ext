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
  res/system/internal/fixtures/java/io/github/webong/ext/system/fixtures/*.java \
  res/system/capture/surface/android/src/main/java/io/github/webong/ext/system/*.java \
  res/system/service/host/android/src/main/java/io/github/webong/ext/system/*.java \
  res/system/capture/share/android/src/main/java/io/github/webong/ext/system/share/*.java \
  res/system/capture/share/android/src/main/java/io/github/webong/ext/system/keyboard/*.java \
  res/system/capture/surface/android/src/test/java/io/github/webong/ext/system/*.java \
  res/system/service/host/android/src/test/java/io/github/webong/ext/system/*.java \
  res/system/capture/share/android/src/test/java/io/github/webong/ext/system/share/*.java \
  res/system/capture/share/android/src/test/java/io/github/webong/ext/system/keyboard/*.java \
  res/system/capture/clipboard/android/src/main/java/io/github/webong/ext/clipboard/*.java \
  res/system/capture/clipboard/android/src/test/java/io/github/webong/ext/clipboard/*.java \
  res/system/service/network/android/src/main/java/io/github/webong/ext/network/*.java \
  res/system/service/network/android/src/test/java/io/github/webong/ext/network/*.java
java -ea -cp "$build/portable" io.github.webong.ext.system.SurfaceTests
java -ea -cp "$build/portable" io.github.webong.ext.system.ResourceTests
java -ea -cp "$build/portable" io.github.webong.ext.system.share.ShareTests
java -ea -cp "$build/portable" io.github.webong.ext.system.share.ShareFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.system.keyboard.KeyboardTests
java -ea -cp "$build/portable" io.github.webong.ext.system.keyboard.KeyboardFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.clipboard.ClipboardTests
java -ea -cp "$build/portable" io.github.webong.ext.network.NetworkTests
javac -Xlint:all -Werror -classpath "$platform:$build/portable" -d "$build/android" \
  res/system/capture/surface/android/src/android/java/io/github/webong/ext/system/android/*.java \
  res/system/service/host/android/src/android/java/io/github/webong/ext/system/android/*.java \
  res/system/capture/share/android/src/android/java/io/github/webong/ext/system/android/*.java \
  res/system/capture/audio/android/src/android/java/io/github/webong/ext/system/android/*.java \
  res/system/capture/clipboard/android/src/android/java/io/github/webong/ext/clipboard/android/*.java \
  res/system/service/network/android/src/android/java/io/github/webong/ext/network/android/*.java
