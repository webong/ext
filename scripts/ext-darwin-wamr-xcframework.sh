#!/usr/bin/env bash
# Build the optional WAMR host (the engine's host half, the WAMR reactor backend and
# WAMR's interpreter) as an Apple xcframework for pkg/ext-darwin: macOS, iOS and iOS
# Simulator. It carries the host half only; the guest core and the package store stay in
# CExtEngine, so the two do not define the same symbols. It ships no headers: CExtEngine
# declares the CExtWamr module and header, because two xcframeworks must not ship the same
# header or module map. WAMR is fetched at the pinned commit by plugin-wamr-source.sh. Its license goes
# beside the framework and must ship with any binary that links it.
# --platform ios|macos|all picks the slices as scripts/ext-darwin-xcframework.sh does.
# Usage: scripts/ext-darwin-wamr-xcframework.sh [--platform ios|macos|all]
# Output: pkg/ext-darwin/Frameworks/CExtWamr.xcframework
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo "ext-darwin-wamr-xcframework.sh needs macOS and Xcode" >&2; exit 1; }
platform=all
if [[ "${1:-}" == --platform ]]; then platform="${2:?--platform needs ios, macos or all}"; shift 2; fi
[[ $# -eq 0 ]] || { echo 'usage: scripts/ext-darwin-wamr-xcframework.sh [--platform ios|macos|all]' >&2; exit 2; }
case "$platform" in
  ios) groups=(ios iossim) ;;
  macos) groups=(macos) ;;
  all) groups=(macos ios iossim) ;;
  *) echo "unknown platform '$platform': ios, macos or all" >&2; exit 2 ;;
esac
wamr="$(scripts/plugin-wamr-source.sh)"
out="${EXT_SWIFT_FRAMEWORKS:-pkg/ext-darwin/Frameworks}"
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-wamr-xcframework.XXXXXX")"
trap 'rm -rf "$work"' EXIT
# Objects CExtEngine already carries (scripts/ext-darwin-xcframework.sh: the guest core and the
# package store with the file layer under it), so the two frameworks never define a symbol twice.
core=(common.c.o wire.c.o progress.c.o reverse.c.o endpoint.c.o services.c.o guest.c.o sha256.c.o packages.c.o yyjson.c.o
  fs_posix.c.o fs_write_posix.c.o random_posix.c.o integrity.c.o archive.c.o store.c.o)

# slice | sdk | architecture | deployment target | system name
slices=(
  "macos-arm64|macosx|arm64|12.0|"
  "macos-x86_64|macosx|x86_64|12.0|"
  "ios-arm64|iphoneos|arm64|13.0|iOS"
  "iossim-arm64|iphonesimulator|arm64|13.0|iOS"
  "iossim-x86_64|iphonesimulator|x86_64|13.0|iOS"
)
for slice in "${slices[@]}"; do
  IFS='|' read -r name sdk arch target system <<<"$slice"
  [[ " ${groups[*]} " == *" ${name%%-*} "* ]] || continue
  sysroot="$(xcrun --sdk "$sdk" --show-sdk-path)"
  args=(-DCMAKE_BUILD_TYPE=Release -DEXT_WITH_WAMR=ON -DEXT_WAMR_ROOT="$wamr"
    -DCMAKE_OSX_SYSROOT="$sysroot" -DCMAKE_OSX_ARCHITECTURES="$arch" -DCMAKE_OSX_DEPLOYMENT_TARGET="$target")
  [[ -z "$system" ]] || args+=(-DCMAKE_SYSTEM_NAME="$system")
  cmake -S lib/plugin -B "$work/$name" "${args[@]}" >/dev/null
  cmake --build "$work/$name" --target ext_wamr -j 4 >/dev/null
  mkdir -p "$work/$name/objs"
  for lib in "$work/$name/libext_plugin_host_static.a" "$work/$name/wamr/libext_wamr.a" "$work/$name/wamr/libext_wamr_vm.a"; do
    [[ -f "$lib" ]] || lib="$(find "$work/$name" -name "$(basename "$lib")" | head -1)"
    sub="$work/$name/objs/$(basename "$lib" .a)"
    mkdir -p "$sub" && (cd "$sub" && ar x "$lib")
  done
  for object in "${core[@]}"; do find "$work/$name/objs" -name "$object" -delete; done
  # Same-named objects from different libraries would overwrite each other in one directory.
  xcrun --sdk "$sdk" libtool -static -o "$work/$name/libext_wamr_host.a" $(find "$work/$name/objs" -name '*.o') 2>/dev/null
done
libraries=()
for group in "${groups[@]}"; do
  mkdir -p "$work/$group"
  lipo -create "$work/$group"-*/libext_wamr_host.a -output "$work/$group/libext_wamr_host.a"
  libraries+=(-library "$work/$group/libext_wamr_host.a")
done

rm -rf "$out/CExtWamr.xcframework"
mkdir -p "$out"
xcodebuild -create-xcframework "${libraries[@]}" \
  -output "$out/CExtWamr.xcframework" >/dev/null
cp "$wamr/LICENSE" "$out/WAMR-LICENSE"
echo "wrote $out/CExtWamr.xcframework"
