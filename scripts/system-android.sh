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
  pkg/ext-android/act/capture/surface/src/main/java/io/github/webong/ext/system/*.java \
  pkg/ext-android/src/main/java/io/github/webong/ext/system/*.java \
  pkg/ext-android/act/share/src/main/java/io/github/webong/ext/system/share/*.java \
  pkg/ext-android/act/share/src/main/java/io/github/webong/ext/system/keyboard/*.java \
  pkg/ext-android/act/capture/surface/src/test/java/io/github/webong/ext/system/*.java \
  pkg/ext-android/src/test/java/io/github/webong/ext/system/*.java \
  pkg/ext-android/act/share/src/test/java/io/github/webong/ext/system/share/*.java \
  pkg/ext-android/act/share/src/test/java/io/github/webong/ext/system/keyboard/*.java \
  pkg/ext-android/act/capture/audio/src/main/java/io/github/webong/ext/system/audio/*.java \
  pkg/ext-android/act/capture/audio/src/test/java/io/github/webong/ext/system/audio/*.java \
  pkg/ext-android/act/clipboard/src/main/java/io/github/webong/ext/clipboard/*.java \
  pkg/ext-android/act/clipboard/src/test/java/io/github/webong/ext/clipboard/*.java \
  pkg/ext-android/net/src/main/java/io/github/webong/ext/network/*.java \
  pkg/ext-android/net/src/test/java/io/github/webong/ext/network/*.java
java -ea -cp "$build/portable" io.github.webong.ext.system.SurfaceTests
java -ea -cp "$build/portable" io.github.webong.ext.system.SurfaceFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.system.SurfaceCancellationTests
java -ea -cp "$build/portable" io.github.webong.ext.system.ResourceTests
java -ea -cp "$build/portable" io.github.webong.ext.system.ResourceFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.system.ResourceCancellationTests
java -ea -cp "$build/portable" io.github.webong.ext.system.share.ShareTests
java -ea -cp "$build/portable" io.github.webong.ext.system.share.ShareFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.system.share.ShareCancellationTests
java -ea -cp "$build/portable" io.github.webong.ext.system.keyboard.KeyboardTests
java -ea -cp "$build/portable" io.github.webong.ext.system.keyboard.KeyboardFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.system.audio.AudioFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.system.audio.PlaybackSinkTests
java -ea -cp "$build/portable" io.github.webong.ext.clipboard.ClipboardTests
java -ea -cp "$build/portable" io.github.webong.ext.clipboard.ClipboardCancellationTests
java -ea -cp "$build/portable" io.github.webong.ext.network.NetworkTests
java -ea -cp "$build/portable" io.github.webong.ext.network.NetworkFixtureTests
java -ea -cp "$build/portable" io.github.webong.ext.network.NetworkCancellationTests
javac -Xlint:all -Werror -classpath "$platform:$build/portable" -d "$build/android" \
  pkg/ext-android/act/capture/surface/src/android/java/io/github/webong/ext/system/android/*.java \
  pkg/ext-android/src/android/java/io/github/webong/ext/system/android/*.java \
  pkg/ext-android/act/share/src/android/java/io/github/webong/ext/system/android/*.java \
  pkg/ext-android/act/capture/audio/src/android/java/io/github/webong/ext/system/android/*.java \
  pkg/ext-android/act/clipboard/src/android/java/io/github/webong/ext/clipboard/android/*.java \
  pkg/ext-android/net/src/android/java/io/github/webong/ext/network/android/*.java
