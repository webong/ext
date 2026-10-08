#!/usr/bin/env bash
# Build the Android WebView plugin host and its test app, run it on an emulator or
# device, and report. It needs only the Android SDK (platform android.jar and
# build-tools) and a JDK: the host is plain Java, and the guest is WebAssembly, so no
# NDK, Gradle or Kotlin is involved.
# Usage: scripts/plugin-android-webview.sh [reactor.wasm]
#        (default: $EXT_RUST_REACTOR, the Rust shared example built for wasm32-wasip1)
# Uses a running device (ANDROID_SERIAL selects one). If none is running it starts
# the first AVD headless and stops it afterwards.
set -euo pipefail
cd "$(dirname "$0")/.."
reactor="${1:-${EXT_RUST_REACTOR:-}}"
[[ -f "$reactor" ]] || { echo 'give the reactor module: scripts/plugin-android-webview.sh path/to/shared.wasm' >&2; exit 2; }
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
jar="$(ls -d "$sdk"/platforms/android-*/android.jar 2>/dev/null | sort -V | tail -1)"
tools="$(ls -d "$sdk"/build-tools/* 2>/dev/null | sort -V | tail -1)"
[[ -f "$jar" && -x "$tools/aapt2" && -x "$tools/d8" ]] || { echo "need an Android platform and build-tools under $sdk" >&2; exit 1; }
adb="$sdk/platform-tools/adb"
sdkpkg=pkg/plugin-android
package=io.github.webong.ext.plugin.webviewtest

work="$(mktemp -d "${TMPDIR:-/tmp}/ext-android-webview.XXXXXX")"
emulator_pid=""
cleanup() {
  "$adb" uninstall "$package" >/dev/null 2>&1 || true
  if [[ -n "$emulator_pid" ]]; then "$adb" emu kill >/dev/null 2>&1 || kill "$emulator_pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# Build: javac, dex, link the manifest and assets, add the dex, align and sign.
./scripts/plugin-mobile-assets.sh --check
mkdir -p "$work/classes" "$work/dex" "$work/assets"
cp "$reactor" "$work/assets/reactor.wasm"
javac -Xlint:all --release 11 -classpath "$jar" -d "$work/classes" \
  $(find "$sdkpkg/src/android/java" "$sdkpkg/src/androidTest/java" -name '*.java')
"$tools/d8" --lib "$jar" --min-api 24 --output "$work/dex" $(find "$work/classes" -name '*.class')
"$tools/aapt2" link -o "$work/base.apk" -I "$jar" --manifest "$sdkpkg/src/androidTest/AndroidManifest.xml" \
  --min-sdk-version 24 --target-sdk-version 34 -A "$work/assets"
(cd "$work/dex" && zip -q "$work/base.apk" classes.dex)
"$tools/zipalign" -f -p 4 "$work/base.apk" "$work/aligned.apk"
keytool -genkeypair -keystore "$work/debug.keystore" -storepass android -keypass android -alias debug \
  -keyalg RSA -keysize 2048 -validity 10000 -dname 'CN=ext webview test' >/dev/null 2>&1
"$tools/apksigner" sign --ks "$work/debug.keystore" --ks-pass pass:android --key-pass pass:android \
  --out "$work/signed.apk" "$work/aligned.apk"

# A device: the one running, or a headless AVD started for this run.
if [[ -z "$("$adb" devices | awk 'NR>1 && $2=="device"')" ]]; then
  avd="${EXT_ANDROID_AVD:-$("$sdk/emulator/emulator" -list-avds | head -1)}"
  [[ -n "$avd" ]] || { echo 'no running device and no AVD to start' >&2; exit 1; }
  echo "starting emulator $avd"
  "$sdk/emulator/emulator" -avd "$avd" -no-window -no-audio -no-snapshot-save >/dev/null 2>&1 &
  emulator_pid=$!
  "$adb" wait-for-device
  booted=""
  for _ in $(seq 1 450); do # up to 15 minutes: a loaded machine boots slowly
    if [[ "$("$adb" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" == 1 ]]; then booted=1; break; fi
    sleep 2
  done
  [[ -n "$booted" ]] || { echo 'the emulator did not finish booting' >&2; exit 1; }
  sleep 10 # let the launcher settle before starting an activity
fi

# A copy signed with another key (an interrupted earlier run) would refuse the update.
"$adb" uninstall "$package" >/dev/null 2>&1 || true
"$adb" install -t "$work/signed.apk" >/dev/null
"$adb" logcat -c
"$adb" shell am start -W -n "$package/.TestActivity" >/dev/null
for _ in $(seq 1 150); do
  if "$adb" logcat -d -s EXTTEST:I 2>/dev/null | grep -q 'DONE passed='; then break; fi
  sleep 2
done
log="$("$adb" logcat -d -s EXTTEST:I | sed -E 's/^[0-9-]+ [0-9:.]+ +[0-9]+ +[0-9]+ +[A-Z] EXTTEST *: ?//')"
printf '%s\n' "$log"
summary="$(grep 'DONE passed=' <<<"$log" | tail -1)"
if [[ -z "$summary" ]]; then
  echo 'no result from the test app; recent crash and WebView log:' >&2
  "$adb" logcat -d -t 400 AndroidRuntime:E chromium:E cr_:E WebView:E System.err:W EXTTEST:V '*:S' 2>&1 | tail -40 >&2 || true
fi
[[ "$summary" =~ passed=([0-9]+)\ failed=([0-9]+) && "${BASH_REMATCH[1]}" -gt 0 && "${BASH_REMATCH[2]}" -eq 0 ]]
