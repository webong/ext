#!/usr/bin/env bash
# Build lib/ledger as the CExtLedger binary target consumed by the Apple bindings.
set -euo pipefail
cd "$(dirname "$0")/.."
platform=all
if [[ "${1:-}" == --platform ]]; then platform="${2:?--platform needs ios, macos or all}"; shift 2; fi
[[ $# -eq 0 ]] || { echo 'usage: scripts/ext-darwin-ledger-xcframework.sh [--platform ios|macos|all]' >&2; exit 2; }
case "$platform" in
  ios) slices=(ios-arm64 iossim-arm64 iossim-x86_64) ;;
  macos) slices=(macos-arm64 macos-x86_64) ;;
  all) slices=(macos-arm64 macos-x86_64 ios-arm64 iossim-arm64 iossim-x86_64) ;;
  *) echo "unknown platform: $platform" >&2; exit 2 ;;
esac
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-ledger-xcframework.XXXXXX")"
trap 'rm -rf "$work"' EXIT
for slice in "${slices[@]}"; do
  group="${slice%%-*}"
  arch="${slice#*-}"
  case "$group" in
    macos) sdk=macosx; system=Darwin; minimum=12.0 ;;
    ios) sdk=iphoneos; system=iOS; minimum=13.0 ;;
    iossim) sdk=iphonesimulator; system=iOS; minimum=13.0 ;;
  esac
  cmake -S lib/ledger -B "$work/$slice" -G 'Unix Makefiles' \
    -DCMAKE_SYSTEM_NAME="$system" -DCMAKE_OSX_SYSROOT="$(xcrun --sdk "$sdk" --show-sdk-path)" \
    -DCMAKE_OSX_ARCHITECTURES="$arch" -DCMAKE_OSX_DEPLOYMENT_TARGET="$minimum" \
    -DEXT_LEDGER_TESTS=OFF -DEXT_LEDGER_COMMAND=OFF >/dev/null
  cmake --build "$work/$slice" --target ext_ledger_static -j 4 >/dev/null
done
libraries=()
for group in macos ios iossim; do
  members=("$work/$group"-*/libext_ledger.a)
  [[ -f "${members[0]}" ]] || continue
  mkdir -p "$work/$group"
  lipo -create "${members[@]}" -output "$work/$group/libext_ledger.a"
  libraries+=(-library "$work/$group/libext_ledger.a" -headers "$work/headers")
done
mkdir -p "$work/headers"
cp lib/ledger/include/ext_ledger.h "$work/headers/"
cat >"$work/headers/module.modulemap" <<'MODULE'
module CExtLedger {
  header "ext_ledger.h"
  export *
}
MODULE
out="${EXT_SWIFT_FRAMEWORKS:-pkg/ext-darwin/Frameworks}/CExtLedger.xcframework"
rm -rf "$out"
mkdir -p "$(dirname "$out")"
xcodebuild -create-xcframework "${libraries[@]}" -output "$out" >/dev/null
echo "wrote $out"
