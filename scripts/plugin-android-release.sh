#!/usr/bin/env bash
# Stage a release of the Android plugin SDK as an AAR under dist/plugin-android-<version>/.
# The AAR holds the guest SDK and the WebView host (classes.jar) and libextjni for arm64-v8a and
# x86_64. It publishes nothing. Needs the Android SDK (platform, build-tools, NDK), a JDK, cmake.
#
# --with-wamr also builds the optional WAMR host: libextwamrjni for the same ABIs and the
# WamrPluginHost classes. WAMR is Apache-2.0 with the LLVM exception; its license is staged as
# WAMR-LICENSE beside the AAR and inside it (META-INF/), and must ship with any binary that links
# it. Without the flag the AAR carries neither the library nor the classes.
# Usage: scripts/plugin-android-release.sh [--with-wamr] <version, for example 0.1.0>
set -euo pipefail
cd "$(dirname "$0")/.."
with_wamr=""
if [[ "${1:-}" == --with-wamr ]]; then with_wamr=1; shift; fi
version="${1:?usage: scripts/plugin-android-release.sh [--with-wamr] <version>}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'version must look like 0.1.0' >&2; exit 2; }
sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
jar="$(ls -d "$sdk"/platforms/android-*/android.jar 2>/dev/null | sort -V | tail -1)"
ndk="${ANDROID_NDK_ROOT:-$(ls -d "$sdk"/ndk/* 2>/dev/null | sort -V | tail -1)}"
[[ -f "$jar" ]] || { echo "need an Android platform under $sdk" >&2; exit 1; }
[[ -f "$ndk/build/cmake/android.toolchain.cmake" ]] || { echo "need the Android NDK under $sdk/ndk" >&2; exit 1; }
pkg=pkg/plugin-android
out="dist/plugin-android-$version"
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-android-release.XXXXXX")"
trap 'rm -rf "$work"' EXIT
rm -rf "$out"
mkdir -p "$out" "$work/aar/jni"
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

# classes.jar (Java 17, which the guest SDK uses; the app's D8 desugars it): the guest SDK and the WebView host, plus the WAMR host classes when requested.
mkdir -p "$work/classes"
sources=($(find "$pkg/src/main/java" "$pkg/src/android/java" -name '*.java' | grep -v '/Wamr[A-Za-z]*\.java$'))
[[ -z "$with_wamr" ]] || sources+=($(find "$pkg/src/main/java" -name 'Wamr*.java'))
javac -Xlint:all --release 17 -classpath "$jar" -d "$work/classes" "${sources[@]}"
( cd "$work/classes" && jar cf "$work/aar/classes.jar" . )

cat >"$work/aar/AndroidManifest.xml" <<MANIFEST
<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="io.github.webong.ext.plugin">
    <uses-sdk android:minSdkVersion="$([[ -n "$with_wamr" ]] && echo 28 || echo 24)" />
</manifest>
MANIFEST
: >"$work/aar/R.txt"
if [[ -n "$with_wamr" ]]; then
  mkdir -p "$work/aar/META-INF"
  cp "$wamr/LICENSE" "$work/aar/META-INF/WAMR-LICENSE"
  cp "$wamr/LICENSE" "$out/WAMR-LICENSE"
fi
( cd "$work/aar" && zip -q -r "$OLDPWD/$out/ext-plugin-$version.aar" . )

# Prove it: the AAR must hold what it claims, and nothing of WAMR unless asked.
listing="$(unzip -Z1 "$out/ext-plugin-$version.aar")"
for entry in AndroidManifest.xml classes.jar jni/arm64-v8a/libextjni.so jni/x86_64/libextjni.so; do
  grep -qx "$entry" <<<"$listing" || { echo "AAR is missing $entry" >&2; exit 1; }
done
wamr_entries="$(grep -c -i wamr <<<"$listing" || true)"
if [[ -n "$with_wamr" ]]; then
  grep -qx jni/arm64-v8a/libextwamrjni.so <<<"$listing" || { echo 'AAR is missing libextwamrjni' >&2; exit 1; }
elif [[ "$wamr_entries" != 0 ]]; then
  echo 'a release without --with-wamr must not contain WAMR' >&2; exit 1
fi
unzip -p "$out/ext-plugin-$version.aar" classes.jar >"$work/check.jar"
if [[ -z "$with_wamr" ]] && unzip -Z1 "$work/check.jar" | grep -q Wamr; then
  echo 'classes.jar contains WAMR classes' >&2; exit 1
fi
echo "version   $version"
echo "aar       $out/ext-plugin-$version.aar"
echo "entries   $(wc -l <<<"$listing" | tr -d ' ')"
[[ -z "$with_wamr" ]] || echo "license   $out/WAMR-LICENSE"
