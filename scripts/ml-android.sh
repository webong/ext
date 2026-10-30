#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
build="$(mktemp -d "${TMPDIR:-/tmp}/ext-ml-android.XXXXXX")"
trap 'rm -rf "$build"' EXIT
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
platform="$(ls -d "$sdk"/platforms/android-*/android.jar 2>/dev/null | sort -V | tail -1)"
[[ -f "$platform" ]] || { echo "need an Android platform under $sdk" >&2; exit 1; }

javac -Xlint:all -Werror -d "$build" \
  res/int/model/android/src/main/java/io/github/webong/ext/ml/*.java \
  res/int/model/android/src/android/java/io/github/webong/ext/ml/android/*.java \
  res/int/model/android/src/test/java/org/tensorflow/lite/*.java \
  res/int/model/android/src/test/java/io/github/webong/ext/ml/*.java
java -ea -cp "$build" io.github.webong.ext.ml.MlTypesTest
java -ea -cp "$build" io.github.webong.ext.ml.TfliteProviderTest
