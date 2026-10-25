#!/usr/bin/env bash
# Build and run the native workspace integration test. Provide AndroidX WebKit 1.9+ AARs (or unminified JARs)
# and its runtime dependencies (colon-separated) in EXT_ANDROIDX_CLASSPATH. Nothing is fetched.
# Requires an already running device/emulator; ANDROID_SERIAL selects it.
# --aar <path> uses the shipped SDK instead of sources; --minify verifies R8 consumer rules.
# --build-only <apk> prepares a signed test APK without touching a device.
# --without-androidx checks navigation and explicit rejection of unsupported userscripts.
set -euo pipefail
cd "$(dirname "$0")/.."
aar=""
minify=""
build_only=""
without_androidx=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --aar) aar="${2:?--aar requires a path}"; shift 2 ;;
    --minify) minify=1; shift ;;
    --without-androidx) without_androidx=1; shift ;;
    --build-only) build_only="${2:?--build-only requires an output APK}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ -z "$minify" || -n "$aar" ]] || { echo '--minify requires --aar' >&2; exit 2; }
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
platform="$(ls -d "$sdk"/platforms/android-*/android.jar | sort -V | tail -1)"
build_tools="$(ls -d "$sdk"/build-tools/* | sort -V | tail -1)"
adb="$sdk/platform-tools/adb"
if [[ -z "$without_androidx" ]]; then
  : "${EXT_ANDROIDX_CLASSPATH:?provide AndroidX WebKit and runtime dependency AARs or JARs}"
fi
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-workspace-android.XXXXXX")"
package=io.github.webong.ext.web.workspacetest
installed=""
cleanup() {
  if [[ -n "$installed" ]]; then "$adb" uninstall "$package" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT
if [[ -z "$build_only" ]]; then "$adb" get-state >/dev/null; fi
mkdir -p "$work/classes" "$work/dex"
sources=(res/web/android/src/androidTest/java/io/github/webong/ext/web/workspacetest/*.java
  res/web/android/src/androidTest/java/io/github/webong/ext/web/android/*.java)
classpath="$platform"
inputs=()
if [[ -n "$aar" ]]; then
  unzip -p "$aar" classes.jar >"$work/sdk.jar"
  unzip -p "$aar" proguard.txt >"$work/consumer.pro"
  classpath="$classpath:$work/sdk.jar"
  inputs+=("$work/sdk.jar")
else
  sources+=(res/web/android/src/main/java/io/github/webong/ext/web/*.java
    res/web/android/src/android/java/io/github/webong/ext/web/android/*.java)
fi
javac --release 11 -Xlint:all -Werror -classpath "$classpath" -d "$work/classes" "${sources[@]}"
inputs+=($(find "$work/classes" -name '*.class'))
if [[ -z "$without_androidx" ]]; then
  IFS=: read -r -a dependencies <<< "$EXT_ANDROIDX_CLASSPATH"
  for index in "${!dependencies[@]}"; do
    dependency="${dependencies[$index]}"
    if [[ "$dependency" == *.aar ]]; then
      unzip -p "$dependency" classes.jar >"$work/dependency-$index.jar"
      inputs+=("$work/dependency-$index.jar")
      if unzip -Z1 "$dependency" | grep -qx proguard.txt; then
        printf '\n' >>"$work/consumer.pro"
        unzip -p "$dependency" proguard.txt >>"$work/consumer.pro"
      fi
    else
      [[ -z "$minify" ]] || { echo 'Use dependency AARs with --minify so their consumer rules are included' >&2; exit 2; }
      inputs+=("$dependency")
    fi
  done
fi
if [[ -n "$minify" ]]; then
  cat >>"$work/consumer.pro" <<'RULES'
-keep public class io.github.webong.ext.web.workspacetest.TestActivity { public <init>(); }
-dontwarn androidx.annotation.**
RULES
  java -cp "$build_tools/lib/d8.jar" com.android.tools.r8.R8 --release \
    --lib "$platform" --min-api 24 --output "$work/dex" --pg-conf "$work/consumer.pro" "${inputs[@]}"
else
  "$build_tools/d8" --lib "$platform" --min-api 24 --output "$work/dex" "${inputs[@]}"
fi
"$build_tools/aapt2" link -o "$work/base.apk" -I "$platform" \
  --manifest res/web/android/src/androidTest/AndroidManifest.xml --min-sdk-version 24 --target-sdk-version 34
(cd "$work/dex" && zip -q "$work/base.apk" classes*.dex)
"$build_tools/zipalign" -f -p 4 "$work/base.apk" "$work/aligned.apk"
keytool -genkeypair -keystore "$work/key" -storepass android -keypass android -alias debug \
  -keyalg RSA -keysize 2048 -validity 1 -dname 'CN=Ext workspace tests' >/dev/null 2>&1
"$build_tools/apksigner" sign --ks "$work/key" --ks-pass pass:android --out "$work/signed.apk" "$work/aligned.apk"
if [[ -n "$build_only" ]]; then
  cp "$work/signed.apk" "$build_only"
  echo "test APK: $build_only"
  exit 0
fi
"$adb" install --no-incremental -r -t "$work/signed.apk" >/dev/null
installed=1
expected=true
[[ -z "$without_androidx" ]] || expected=false
"$adb" shell am start -W -n "$package/.TestActivity" --ez documentStart "$expected" >/dev/null
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
