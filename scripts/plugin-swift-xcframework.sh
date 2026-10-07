#!/usr/bin/env bash
# Build the guest core of the shared C engine as an Apple xcframework for
# pkg/plugin-swift: macOS (arm64, x86_64), iOS (arm64) and iOS Simulator
# (arm64, x86_64). The host half (posix_spawn, pthreads) is not built, because
# iOS cannot spawn processes. Output: pkg/plugin-swift/Frameworks/CExtEngine.xcframework
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo "plugin-swift-xcframework.sh needs macOS and Xcode" >&2; exit 1; }
engine=pkg/plugin-engine
out="${EXT_SWIFT_FRAMEWORKS:-pkg/plugin-swift/Frameworks}"
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-swift-xcframework.XXXXXX")"
trap 'rm -rf "$work"' EXIT

# The engine renames its vendored parser symbols through a header CMake generates.
cmake -S "$engine" -B "$work/gen" -DEXT_BUILD_HOST=OFF >/dev/null
namespace="$work/gen/parser_namespace.h"
sources=(src/common.c src/wire.c src/services.c src/guest.c src/sha256.c src/packages.c vendor/yyjson.c)

# slice name | sdk | clang target
slices=(
  "macos-arm64|macosx|arm64-apple-macos12.0"
  "macos-x86_64|macosx|x86_64-apple-macos12.0"
  "ios-arm64|iphoneos|arm64-apple-ios13.0"
  "iossim-arm64|iphonesimulator|arm64-apple-ios13.0-simulator"
  "iossim-x86_64|iphonesimulator|x86_64-apple-ios13.0-simulator"
)
for slice in "${slices[@]}"; do
  IFS='|' read -r name sdk target <<<"$slice"
  mkdir -p "$work/$name"
  for source in "${sources[@]}"; do
    xcrun -sdk "$sdk" clang -target "$target" -std=c11 -O2 -Wall -Wextra -Werror \
      -DEXT_HOST_BUILD -DEXT_HOST_STATIC -fvisibility=hidden \
      -I"$engine/include" -I"$engine/src" -I"$engine/vendor" -include "$namespace" \
      -c "$engine/$source" -o "$work/$name/$(basename "${source%.c}").o"
  done
  xcrun -sdk "$sdk" libtool -static -o "$work/$name/libext_guest.a" "$work/$name"/*.o
done

# Universal libraries per platform.
for platform in macos iossim; do
  mkdir -p "$work/$platform"
  lipo -create "$work/$platform"-*/libext_guest.a -output "$work/$platform/libext_guest.a"
done
mkdir -p "$work/ios" && cp "$work/ios-arm64/libext_guest.a" "$work/ios/libext_guest.a"

headers="$work/Headers"
mkdir -p "$headers"
cp "$engine/include/ext_host.h" "$engine/include/ext_guest.h" pkg/plugin-cshared/ext_plugin.h "$headers/"
cat >"$headers/module.modulemap" <<'MODULE'
module CExtEngine {
  header "ext_host.h"
  header "ext_guest.h"
  header "ext_plugin.h"
  export *
}
MODULE

rm -rf "$out/CExtEngine.xcframework"
mkdir -p "$out"
xcodebuild -create-xcframework \
  -library "$work/macos/libext_guest.a" -headers "$headers" \
  -library "$work/ios/libext_guest.a" -headers "$headers" \
  -library "$work/iossim/libext_guest.a" -headers "$headers" \
  -output "$out/CExtEngine.xcframework" >/dev/null
echo "wrote $out/CExtEngine.xcframework"
