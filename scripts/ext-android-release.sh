#!/usr/bin/env bash
# Stage a release of the Android SDK (plugin, web, clipboard, system, and network code) as an AAR under dist/ext-android-<version>/:
# ext-android-<version>.aar, or ext-android-wamr-<version>.aar with --with-wamr. The two builds
# share the directory, so a release stages both in turn.
# The AAR holds the guest SDK, WebView host, web rules, native clipboard binding,
# and local system shell support (classes.jar), plus libextjni for arm64-v8a and
# x86_64. It publishes nothing. Needs the Android SDK (platform, build-tools, NDK), a JDK, cmake.
#
# --with-wamr also builds the optional WAMR host: libextwamrjni for the same ABIs and the
# WamrPluginHost classes. WAMR is Apache-2.0 with the LLVM exception; its license is staged as
# WAMR-LICENSE beside the AAR and inside it (META-INF/), and must ship with any binary that links
# it. Without the flag the AAR carries neither the library nor the classes.
# Usage: scripts/ext-android-release.sh [--with-wamr] <version, for example 0.1.0>
set -euo pipefail
cd "$(dirname "$0")/.."
with_wamr=""
if [[ "${1:-}" == --with-wamr ]]; then with_wamr=1; shift; fi
version="${1:?usage: scripts/ext-android-release.sh [--with-wamr] <version>}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'version must look like 0.1.0' >&2; exit 2; }
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
jar="$(ls -d "$sdk"/platforms/android-*/android.jar 2>/dev/null | sort -V | tail -1)"
ndk="${ANDROID_NDK_ROOT:-$(ls -d "$sdk"/ndk/* 2>/dev/null | sort -V | tail -1)}"
[[ -f "$jar" ]] || { echo "need an Android platform under $sdk" >&2; exit 1; }
[[ -f "$ndk/build/cmake/android.toolchain.cmake" ]] || { echo "need the Android NDK under $sdk/ndk" >&2; exit 1; }
pkg=pkg/ext-android
out="dist/ext-android-$version"
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-android-release.XXXXXX")"
trap 'rm -rf "$work"' EXIT
aar_name="ext-android-${with_wamr:+wamr-}$version.aar"
mkdir -p "$out" "$work/aar/jni"
rm -f "$out/$aar_name"
./scripts/plugin-mobile-assets.sh --check

wamr=""
if [[ -n "$with_wamr" ]]; then wamr="$(scripts/plugin-wamr-source.sh)"; fi
for abi in arm64-v8a x86_64; do
  args=(-DCMAKE_BUILD_TYPE=Release -DCMAKE_TOOLCHAIN_FILE="$ndk/build/cmake/android.toolchain.cmake"
    -DANDROID_ABI="$abi" -DANDROID_PLATFORM=android-28)
  [[ -z "$wamr" ]] || args+=(-DEXT_WAMR_ROOT="$wamr")
  cmake -S "$pkg" -B "$work/native-$abi" "${args[@]}" >/dev/null
  targets=(extjni)
  [[ -z "$wamr" ]] || targets+=(extwamrjni)
  for target in "${targets[@]}"; do cmake --build "$work/native-$abi" --target "$target" -j 4 >/dev/null; done
  mkdir -p "$work/aar/jni/$abi"
  for target in "${targets[@]}"; do cp "$work/native-$abi/lib$target.so" "$work/aar/jni/$abi/"; done
done

# classes.jar (Java 17, which the guest SDK uses; the app's D8 desugars it): the guest SDK, WebView host, web rules and clipboard binding, plus the WAMR host classes when requested.
mkdir -p "$work/classes"
# The plugin code and the WebView host are this package's; the web rules are res/web/android's.
sources=($(find "$pkg/src/main/java" "$pkg/src/android/java" res/web/android/src/main/java res/web/android/src/android/java \
  res/system/clipboard/android/src/main/java res/system/clipboard/android/src/android/java \
  res/system/android/src/main/java res/system/android/src/android/java \
  res/system/network/android/src/main/java res/system/network/android/src/android/java \
  res/ml/android/src/main/java res/ml/android/src/android/java -name '*.java' | grep -v '/Wamr[A-Za-z]*\.java$'))
[[ -z "$with_wamr" ]] || sources+=($(find "$pkg/src/main/java" -name 'Wamr*.java'))
javac -Xlint:all --release 17 -classpath "$jar" -d "$work/classes" "${sources[@]}"
( cd "$work/classes" && jar cf "$work/aar/classes.jar" . )

cat >"$work/aar/AndroidManifest.xml" <<MANIFEST
<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="io.github.webong.ext.plugin">
    <uses-sdk android:minSdkVersion="$([[ -n "$with_wamr" ]] && echo 28 || echo 24)" />
    <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
</manifest>
MANIFEST
: >"$work/aar/R.txt"
# Rules the native code needs from R8, applied to every app that depends on the AAR.
cp "$pkg/consumer-rules.pro" "$work/aar/proguard.txt"
if [[ -n "$with_wamr" ]]; then
  mkdir -p "$work/aar/META-INF"
  cp "$wamr/LICENSE" "$work/aar/META-INF/WAMR-LICENSE"
  cp "$wamr/LICENSE" "$out/WAMR-LICENSE"
fi
( cd "$work/aar" && zip -q -r "$OLDPWD/$out/$aar_name" . )

# Prove it: the AAR must hold what it claims, and nothing of WAMR unless asked.
listing="$(unzip -Z1 "$out/$aar_name")"
for entry in AndroidManifest.xml classes.jar proguard.txt jni/arm64-v8a/libextjni.so jni/x86_64/libextjni.so; do
  grep -qx "$entry" <<<"$listing" || { echo "AAR is missing $entry" >&2; exit 1; }
done
wamr_entries="$(grep -c -i wamr <<<"$listing" || true)"
if [[ -n "$with_wamr" ]]; then
  grep -qx jni/arm64-v8a/libextwamrjni.so <<<"$listing" || { echo 'AAR is missing libextwamrjni' >&2; exit 1; }
elif [[ "$wamr_entries" != 0 ]]; then
  echo 'a release without --with-wamr must not contain WAMR' >&2; exit 1
fi
unzip -p "$out/$aar_name" classes.jar >"$work/check.jar"
classes="$(unzip -Z1 "$work/check.jar")"
grep -qx 'io/github/webong/ext/web/android/BrowserWorkspace.class' <<<"$classes" || { echo 'classes.jar is missing the browser workspace'; exit 1; }
for entry in BundleRunner BundleServer BundleAssets; do
  grep -qx "io/github/webong/ext/web/android/$entry.class" <<<"$classes" || { echo "classes.jar is missing $entry" >&2; exit 1; }
done
grep -qx 'io/github/webong/ext/web/BundlePolicy.class' <<<"$classes" || { echo 'classes.jar is missing BundlePolicy' >&2; exit 1; }
grep -qx 'io/github/webong/ext/web/Cookie.class' <<<"$classes" || { echo 'classes.jar is missing the web rules' >&2; exit 1; }
grep -qx 'io/github/webong/ext/clipboard/ClipboardPolicy.class' <<<"$classes" || { echo 'classes.jar is missing clipboard contracts' >&2; exit 1; }
grep -qx 'io/github/webong/ext/clipboard/android/SystemClipboard.class' <<<"$classes" || { echo 'classes.jar is missing native clipboard support' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/SurfaceSnapshot.class' <<<"$classes" || { echo 'classes.jar is missing system observation contracts' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/android/SystemSurfaceObserver.class' <<<"$classes" || { echo 'classes.jar is missing native system observation' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/android/SystemAccessibilityObserver.class' <<<"$classes" || { echo 'classes.jar is missing native accessibility observation' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/android/SystemMediaProjection.class' <<<"$classes" || { echo 'classes.jar is missing Android media capture' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/android/SystemAudioPlayback.class' <<<"$classes" || { echo 'classes.jar is missing Android audio playback' >&2; exit 1; }
grep -qx 'io/github/webong/ext/ml/MlStore.class' <<<"$classes" || { echo 'classes.jar is missing ML store' >&2; exit 1; }
grep -qx 'io/github/webong/ext/ml/MlProvider.class' <<<"$classes" || { echo 'classes.jar is missing ML provider contract' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/android/SystemShare.class' <<<"$classes" || { echo 'classes.jar is missing Android share normalization' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/android/SystemKeyboard.class' <<<"$classes" || { echo 'classes.jar is missing Android keyboard helpers' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/share/SharePayload.class' <<<"$classes" || { echo 'classes.jar is missing share contracts' >&2; exit 1; }
grep -qx 'io/github/webong/ext/system/keyboard/KeyboardEditor.class' <<<"$classes" || { echo 'classes.jar is missing keyboard contracts' >&2; exit 1; }
grep -qx 'io/github/webong/ext/network/NetworkSnapshot.class' <<<"$classes" || { echo 'classes.jar is missing network observation contracts' >&2; exit 1; }
grep -qx 'io/github/webong/ext/network/android/SystemNetworkObserver.class' <<<"$classes" || { echo 'classes.jar is missing native network observation' >&2; exit 1; }
if [[ -z "$with_wamr" ]] && grep -q Wamr <<<"$classes"; then
  echo 'classes.jar contains WAMR classes' >&2; exit 1
fi
echo "version   $version"
echo "aar       $out/$aar_name"
echo "entries   $(wc -l <<<"$listing" | tr -d ' ')"
[[ -z "$with_wamr" ]] || echo "license   $out/WAMR-LICENSE"
