#!/usr/bin/env bash
# Build and run the native workspace integration test. Provide AndroidX WebKit 1.9+ classes.jar
# and its runtime dependency jars (colon-separated) in EXT_ANDROIDX_CLASSPATH. Nothing is fetched.
# Requires an already running device/emulator; ANDROID_SERIAL selects it.
set -euo pipefail
cd "$(dirname "$0")/.."
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
platform="$(ls -d "$sdk"/platforms/android-*/android.jar | sort -V | tail -1)"
build_tools="$(ls -d "$sdk"/build-tools/* | sort -V | tail -1)"
adb="$sdk/platform-tools/adb"
: "${EXT_ANDROIDX_CLASSPATH:?provide AndroidX WebKit and runtime dependency jars}"
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-workspace-android.XXXXXX")"
package=io.github.webong.ext.web.workspacetest
trap '"$adb" uninstall "$package" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT
"$adb" get-state >/dev/null
mkdir -p "$work/classes" "$work/dex"
javac --release 11 -Xlint:all -Werror -classpath "$platform" -d "$work/classes" \
  res/web/android/src/main/java/io/github/webong/ext/web/*.java \
  res/web/android/src/android/java/io/github/webong/ext/web/android/*.java \
  res/web/android/src/androidTest/java/io/github/webong/ext/web/workspacetest/*.java
IFS=: read -r -a dependencies <<< "$EXT_ANDROIDX_CLASSPATH"
"$build_tools/d8" --lib "$platform" --min-api 24 --output "$work/dex" \
  $(find "$work/classes" -name '*.class') "${dependencies[@]}"
"$build_tools/aapt2" link -o "$work/base.apk" -I "$platform" \
  --manifest res/web/android/src/androidTest/AndroidManifest.xml --min-sdk-version 24 --target-sdk-version 34
(cd "$work/dex" && zip -q "$work/base.apk" classes*.dex)
"$build_tools/zipalign" -f -p 4 "$work/base.apk" "$work/aligned.apk"
keytool -genkeypair -keystore "$work/key" -storepass android -keypass android -alias debug \
  -keyalg RSA -keysize 2048 -validity 1 -dname 'CN=Ext workspace tests' >/dev/null 2>&1
"$build_tools/apksigner" sign --ks "$work/key" --ks-pass pass:android --out "$work/signed.apk" "$work/aligned.apk"
"$adb" install -r -t "$work/signed.apk" >/dev/null
"$adb" shell am start -W -n "$package/.TestActivity" >/dev/null
pid="$("$adb" shell pidof "$package" | tr -d '\r')"
for _ in $(seq 1 45); do
  log="$("$adb" logcat -d --pid="$pid" -s EXTWORKSPACE:I)"
  if grep -q 'DONE passed=' <<<"$log"; then
    printf '%s\n' "$log"
    grep -q 'DONE passed=[1-9][0-9]* failed=0' <<<"$log"
    exit $?
  fi
  sleep 1
done
printf '%s\n' "$log"
echo 'No successful workspace result' >&2
exit 1
