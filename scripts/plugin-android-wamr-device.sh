#!/usr/bin/env bash
# Build the WAMR plugin host for Android (libextwamrjni with the NDK, the Java classes and a
# test app), run it on an emulator or device, and report. Needs the Android SDK (platform,
# build-tools, NDK), a JDK, cmake, git and cargo with wasm32-wasip1. WAMR is fetched at the
# pinned commit (scripts/plugin-wamr-source.sh).
# Usage: scripts/plugin-android-wamr-device.sh [reactor.wasm]
#        (default: built from pkg/plugin-rust, the Rust shared example)
# Uses a running device (ANDROID_SERIAL selects one). If none is running it starts the first
# AVD headless and stops it afterwards. The ABI follows the device (arm64-v8a or x86_64).
set -euo pipefail
cd "$(dirname "$0")/.."
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
jar="$(ls -d "$sdk"/platforms/android-*/android.jar 2>/dev/null | sort -V | tail -1)"
tools="$(ls -d "$sdk"/build-tools/* 2>/dev/null | sort -V | tail -1)"
ndk="${ANDROID_NDK_ROOT:-$(ls -d "$sdk"/ndk/* 2>/dev/null | sort -V | tail -1)}"
[[ -f "$jar" && -x "$tools/aapt2" && -x "$tools/d8" ]] || { echo "need an Android platform and build-tools under $sdk" >&2; exit 1; }
[[ -f "$ndk/build/cmake/android.toolchain.cmake" ]] || { echo "need the Android NDK under $sdk/ndk" >&2; exit 1; }
adb="$sdk/platform-tools/adb"
sdkpkg=pkg/plugin-android
package=io.github.webong.ext.plugin.wamrtest
wamr="$(scripts/plugin-wamr-source.sh)"

work="$(mktemp -d "${TMPDIR:-/tmp}/ext-android-wamr.XXXXXX")"
emulator_pid=""
cleanup() {
  "$adb" uninstall "$package" >/dev/null 2>&1 || true
  if [[ -n "$emulator_pid" ]]; then "$adb" emu kill >/dev/null 2>&1 || kill "$emulator_pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

reactor="${1:-}"
if [[ -z "$reactor" ]]; then
  CARGO_TARGET_DIR="$work/cargo" cargo build --release --locked \
    --manifest-path pkg/plugin-rust/Cargo.toml --target wasm32-wasip1 --example shared
  reactor="$work/cargo/wasm32-wasip1/release/examples/shared.wasm"
fi
[[ -f "$reactor" ]] || { echo "no reactor module at $reactor" >&2; exit 2; }

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
abi="$("$adb" shell getprop ro.product.cpu.abi | tr -d '\r')"

# Native library for the device's ABI. API 28 is the floor the WAMR platform layer supports.
cmake -S "$sdkpkg" -B "$work/native" -DCMAKE_BUILD_TYPE=Release -DEXT_WAMR_ROOT="$wamr" \
  -DCMAKE_TOOLCHAIN_FILE="$ndk/build/cmake/android.toolchain.cmake" -DANDROID_ABI="$abi" \
  -DANDROID_PLATFORM=android-28 >/dev/null
cmake --build "$work/native" --target extwamrjni -j 4 >/dev/null

# Build: javac, dex, link the manifest and assets, add the dex and the library, align and sign.
mkdir -p "$work/classes" "$work/dex" "$work/assets" "$work/apk/lib/$abi"
cp "$reactor" "$work/assets/reactor.wasm"
cp "$work/native/libextwamrjni.so" "$work/apk/lib/$abi/"
javac -Xlint:all --release 11 -classpath "$jar" -d "$work/classes" \
  $sdkpkg/src/main/java/io/github/webong/ext/plugin/Wamr*.java \
  $sdkpkg/src/test/java/io/github/webong/ext/plugin/WamrPluginHostTests.java \
  $sdkpkg/src/androidTest/java/io/github/webong/ext/plugin/wamrtest/WamrTestActivity.java
"$tools/d8" --lib "$jar" --min-api 28 --output "$work/dex" $(find "$work/classes" -name '*.class')
"$tools/aapt2" link -o "$work/base.apk" -I "$jar" --manifest "$sdkpkg/src/androidTest/AndroidManifestWamr.xml" \
  --min-sdk-version 28 --target-sdk-version 34 -A "$work/assets"
(cd "$work/dex" && zip -q "$work/base.apk" classes.dex)
(cd "$work/apk" && zip -q -r "$work/base.apk" lib)
"$tools/zipalign" -f -p 4 "$work/base.apk" "$work/aligned.apk"
keytool -genkeypair -keystore "$work/debug.keystore" -storepass android -keypass android -alias debug \
  -keyalg RSA -keysize 2048 -validity 10000 -dname 'CN=ext wamr test' >/dev/null 2>&1
"$tools/apksigner" sign --ks "$work/debug.keystore" --ks-pass pass:android --key-pass pass:android \
  --out "$work/signed.apk" "$work/aligned.apk"

# A copy signed with another key (an interrupted earlier run) would refuse the update.
"$adb" uninstall "$package" >/dev/null 2>&1 || true
"$adb" install -t "$work/signed.apk" >/dev/null
"$adb" logcat -c
"$adb" shell am start -W -n "$package/.WamrTestActivity" >/dev/null
for _ in $(seq 1 150); do
  if "$adb" logcat -d -s EXTTEST:I 2>/dev/null | grep -q 'DONE passed='; then break; fi
  sleep 2
done
log="$("$adb" logcat -d -s EXTTEST:I | sed -E 's/^[0-9-]+ [0-9:.]+ +[0-9]+ +[0-9]+ +[A-Z] EXTTEST *: ?//')"
printf '%s\n' "$log"
summary="$(grep 'DONE passed=' <<<"$log" | tail -1)"
if [[ -z "$summary" ]]; then
  echo 'no result from the test app; recent crash log:' >&2
  "$adb" logcat -d -t 400 AndroidRuntime:E DEBUG:F libc:F System.err:W EXTTEST:V '*:S' 2>&1 | tail -40 >&2 || true
fi
[[ "$summary" =~ passed=([0-9]+)\ failed=([0-9]+) && "${BASH_REMATCH[1]}" -gt 0 && "${BASH_REMATCH[2]}" -eq 0 ]]
