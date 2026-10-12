#!/usr/bin/env bash
# Test the Android SDK the way an app consumes it: stage the AAR with plugin-android-release.sh,
# build examples/android-consumer against it (a flat local repository, R8 on, minified release),
# and run its instrumented tests on an emulator or device. It checks what the in-repo tests
# cannot: packaged libraries per ABI, minSdk and manifest merging, Java 17 desugaring, and that
# R8 does not strip what the AAR's native code calls. Publishes nothing.
# Needs the Android SDK (platform 34, build-tools, NDK), JDK 17, cargo with wasm32-wasip1, and
# Gradle (GRADLE=<path>, else `gradle` on PATH, else the newest 8.x in ~/.gradle/wrapper/dists).
# Downloads the Android Gradle Plugin and androidx.test from Google/Maven on first use.
# Usage: scripts/plugin-android-consumer.sh [--with-wamr]
set -euo pipefail
cd "$(dirname "$0")/.."
with_wamr=""
if [[ "${1:-}" == --with-wamr ]]; then with_wamr=1; shift; fi
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
export ANDROID_HOME="$sdk"
adb="$sdk/platform-tools/adb"
gradle="${GRADLE:-$(command -v gradle || ls -d "$HOME"/.gradle/wrapper/dists/gradle-8.*-bin/*/gradle-8.*/bin/gradle 2>/dev/null | sort -V | tail -1)}"
[[ -x "$gradle" ]] || { echo 'need Gradle: set GRADLE or put gradle on PATH' >&2; exit 1; }
version=0.0.1
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-android-consumer.XXXXXX")"
emulator_pid=""
cleanup() {
  "$adb" uninstall dev.ext.consumer >/dev/null 2>&1 || true
  "$adb" uninstall dev.ext.consumer.test >/dev/null 2>&1 || true
  if [[ -n "$emulator_pid" ]]; then "$adb" emu kill >/dev/null 2>&1 || kill "$emulator_pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# Stage the AAR under the same name the release script gives it, in a repository of its own.
scripts/plugin-android-release.sh ${with_wamr:+--with-wamr} "$version" >/dev/null
mkdir -p "$work/aar"
cp "dist/plugin-android-$version/ext-plugin-$version.aar" "$work/aar/"
rm -rf "dist/plugin-android-$version"

# The reactor module the host tests run.
CARGO_TARGET_DIR="$work/cargo" cargo build --release --locked \
  --manifest-path pkg/plugin-rust/Cargo.toml --target wasm32-wasip1 --example shared
cp -R examples/android-consumer "$work/app"
mkdir -p "$work/app/app/src/main/assets"
cp "$work/cargo/wasm32-wasip1/release/examples/shared.wasm" "$work/app/app/src/main/assets/reactor.wasm"
printf 'sdk.dir=%s\n' "$sdk" >"$work/app/local.properties"

# A device: the one running, or a headless AVD started for this run.
if [[ -z "$("$adb" devices | awk 'NR>1 && $2=="device"')" ]]; then
  avd="${EXT_ANDROID_AVD:-$("$sdk/emulator/emulator" -list-avds | head -1)}"
  [[ -n "$avd" ]] || { echo 'no running device and no AVD to start' >&2; exit 1; }
  echo "starting emulator $avd"
  "$sdk/emulator/emulator" -avd "$avd" -no-window -no-audio -no-snapshot-save >/dev/null 2>&1 &
  emulator_pid=$!
  "$adb" wait-for-device
  for _ in $(seq 1 450); do
    if [[ "$("$adb" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" == 1 ]]; then break; fi
    sleep 2
  done
  sleep 10
fi

args=(-p "$work/app" -PaarDir="$work/aar" -PaarName="ext-plugin-$version" --no-daemon --console=plain)
[[ -z "$with_wamr" ]] || args+=(-PwithWamr)
"$gradle" "${args[@]}" connectedReleaseAndroidTest
# The merged manifest must carry the minimum the AAR needs.
apk="$(find "$work/app/app/build/outputs/apk" -name '*.apk' -path '*release*' ! -name '*androidTest*' | head -1)"
if [[ -n "$apk" ]]; then
  libs="$(unzip -Z1 "$apk" | grep '^lib/' | sort | tr '\n' ' ')"
  echo "packaged: $libs"
fi
echo "consumer test passed ${with_wamr:+(with WAMR)}"
