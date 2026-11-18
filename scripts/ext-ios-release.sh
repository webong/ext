#!/usr/bin/env bash
# Stage a release of the Apple SDK (plugin, web, clipboard, system, and network code) for one
# platform. The sources are pkg/ext-ios's, and two packages are released from them:
#   --platform ios    ext-ios: the iOS and iOS Simulator slices, tagged ext-ios-v<version>, for dist/ext-ios
#   --platform macos  ext-macos: the macOS slice, tagged ext-macos-v<version>, for dist/ext-macos
# Both export the same module names (ExtWeb, ExtSystem, ...), so an app depends on one of them.
# SwiftPM resolves a package from a repository root and this monorepo's root is not a package, so
# a release is two pieces:
#   1. CExtEngine.xcframework.zip, the engine's guest core and package store for the platform's
#      slices, attached to the GitHub release of that tag, and
#   2. a distribution package (Package.swift plus the Swift sources) that declares only that
#      platform and whose binary target points at that archive by URL and checksum, to publish at
#      the root of the package's dist/ branch (scripts/dist-branch.sh), where SwiftPM can resolve it.
# This script builds both under dist/ext-<platform>-<version>/ and checks the distribution package
# builds against the archive. It publishes nothing. It leaves the platform's xcframeworks in
# pkg/ext-ios/Frameworks, where the release build's tests use them; run
# scripts/ext-ios-xcframework.sh without --platform to get every slice back for development.
#
# What only makes sense on macOS ships only in ext-macos: the ExtFramesGuest executable, a
# JSON-line guest a host starts as a process, which iOS cannot do.
#
# CExtEngine compiles in the engine's vendored code, the yyjson parser (MIT) and miniz's
# inflate decoder (MIT): their licenses are staged as yyjson-LICENSE and miniz-LICENSE beside
# the archives and in the package's LICENSES/ directory, and ship with any binary that links it.
#
# --with-wamr also stages the optional WAMR host: CExtWamr.xcframework.zip, a second release
# asset, and the ExtPluginWamr product in the distribution package. WAMR is Apache-2.0 with the
# LLVM exception: its license is staged as WAMR-LICENSE beside the archives and in the package's
# LICENSES/ directory, and must ship with any binary that links it. Without the flag the package
# carries neither the product nor its sources.
# Usage: scripts/ext-ios-release.sh --platform ios|macos [--with-wamr] <version, for example 0.1.0>
set -euo pipefail
cd "$(dirname "$0")/.."
usage='usage: scripts/ext-ios-release.sh --platform ios|macos [--with-wamr] <version>'
platform=""
with_wamr=""
while [[ $# -gt 1 ]]; do
  case "$1" in
    --platform) platform="${2:?$usage}"; shift 2 ;;
    --with-wamr) with_wamr=1; shift ;;
    *) echo "$usage" >&2; exit 2 ;;
  esac
done
version="${1:?$usage}"
case "$platform" in
  ios) platforms='[.iOS(.v13)]' ;;
  macos) platforms='[.macOS(.v12)]' ;;
  *) echo "$usage" >&2; exit 2 ;;
esac
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'version must look like 0.1.0' >&2; exit 2; }
[[ "$(uname -s)" == Darwin ]] || { echo 'needs macOS and Xcode' >&2; exit 1; }
name="ext-$platform"
tag="$name-v$version"
repository="${EXT_REPOSITORY:-webong/ext}"
sdk=pkg/ext-ios
out="dist/$name-$version"
rm -rf "$out"
mkdir -p "$out"

./scripts/plugin-mobile-assets.sh --check
./scripts/ext-ios-xcframework.sh --platform "$platform" >/dev/null
( cd "$sdk/Frameworks" && ditto -c -k --sequesterRsrc --keepParent CExtEngine.xcframework "../../../$out/CExtEngine.xcframework.zip" )
checksum="$(swift package compute-checksum "$out/CExtEngine.xcframework.zip")"
vendored=(yyjson-LICENSE miniz-LICENSE)
for license in "${vendored[@]}"; do cp "$sdk/Frameworks/$license" "$out/$license"; done
url="https://github.com/$repository/releases/download/$tag/CExtEngine.xcframework.zip"
wamr_checksum=""
wamr_url="https://github.com/$repository/releases/download/$tag/CExtWamr.xcframework.zip"
if [[ -n "$with_wamr" ]]; then
  ./scripts/ext-ios-wamr-xcframework.sh --platform "$platform" >/dev/null
  ( cd "$sdk/Frameworks" && ditto -c -k --sequesterRsrc --keepParent CExtWamr.xcframework "../../../$out/CExtWamr.xcframework.zip" )
  wamr_checksum="$(swift package compute-checksum "$out/CExtWamr.xcframework.zip")"
  cp "$sdk/Frameworks/WAMR-LICENSE" "$out/WAMR-LICENSE"
fi

# The distribution package: the manifest with the binary target by URL and checksum.
dist="$out/package"
mkdir -p "$dist"
cp -RL "$sdk/Sources" "$dist/Sources"
for source in BundleRunner BundleServer BundleAssets; do
  [[ -f "$dist/Sources/ExtWebKit/$source.swift" ]] || { echo "distribution is missing $source" >&2; exit 1; }
done
[[ -f "$dist/Sources/ExtWeb/BundlePolicy.swift" ]] || { echo 'distribution is missing BundlePolicy' >&2; exit 1; }
[[ -f "$dist/Sources/ExtPluginStore/PackageStore.swift" ]] || { echo 'distribution is missing ExtPluginStore' >&2; exit 1; }
[[ -f "$dist/Sources/ExtPluginStore/ArchiveDownloader.swift" ]] || { echo 'distribution is missing the archive downloader' >&2; exit 1; }
[[ -f "$dist/Sources/ExtClipboard/Clipboard.swift" ]] || { echo 'distribution is missing ExtClipboard' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/Surface.swift" ]] || { echo 'distribution is missing ExtSystem' >&2; exit 1; }
[[ -f "$dist/Sources/ExtHost/Resources.swift" ]] || { echo 'distribution is missing ExtHost resources' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/Accessibility.swift" ]] || { echo 'distribution is missing ExtSystem accessibility observation' >&2; exit 1; }
[[ -f "$dist/Sources/ExtNetwork/Network.swift" ]] || { echo 'distribution is missing ExtNetwork' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/AccessibilityControl.swift" ]] || { echo 'distribution is missing ExtSystem accessibility control' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/AppleEvent.swift" ]] || { echo 'distribution is missing ExtSystem Apple events' >&2; exit 1; }
[[ -f "$dist/Sources/ExtAudio/AudioPlayback.swift" ]] || { echo 'distribution is missing ExtAudio playback' >&2; exit 1; }
[[ -f "$dist/Sources/ExtShare/KeyboardExtension.swift" ]] || { echo 'distribution is missing ExtShare keyboard extension' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/MediaCapture.swift" ]] || { echo 'distribution is missing ExtSystem media capture' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/Permission.swift" ]] || { echo 'distribution is missing ExtSystem permission' >&2; exit 1; }
[[ -f "$dist/Sources/ExtShare/ShareExtension.swift" ]] || { echo 'distribution is missing ExtShare share extension' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/SurfaceControl.swift" ]] || { echo 'distribution is missing ExtSystem surface control' >&2; exit 1; }
rm -rf "$dist/Sources/ExtConformancePlugin"
[[ "$platform" == macos ]] || rm -rf "$dist/Sources/ExtFramesGuest"
cp "$sdk/README.md" "$dist/README.md"
cp LICENSE "$dist/LICENSE"
mkdir -p "$dist/LICENSES"
for license in "${vendored[@]}"; do cp "$out/$license" "$dist/LICENSES/$license"; done
if [[ -n "$with_wamr" ]]; then
  cp "$out/WAMR-LICENSE" "$dist/LICENSES/WAMR-LICENSE"
else
  rm -rf "$dist/Sources/ExtPluginWamr"
fi
python3 - "$sdk/Package.swift" "$dist/Package.swift" "$url" "$checksum" "$wamr_url" "$wamr_checksum" "$platform" "$platforms" <<'PY'
import re, sys
source, target, url, checksum, wamr_url, wamr_checksum, platform, platforms = sys.argv[1:9]
text = open(source).read()
# One platform per release: ext-ios declares iOS, ext-macos declares macOS.
development = 'platforms: [.macOS(.v12), .iOS(.v13)],'
if development not in text:
    raise SystemExit('the development manifest no longer has the expected platforms')
text = text.replace(development, f'platforms: {platforms},')
local = '.binaryTarget(name: "CExtEngine", path: "Frameworks/CExtEngine.xcframework")'
if local not in text:
    raise SystemExit('the development manifest no longer has the expected binary target')
text = text.replace(local, f'.binaryTarget(name: "CExtEngine", url: "{url}", checksum: "{checksum}")')
# The WAMR host is opt-in. The development manifest enables it when a local xcframework exists;
# a release either pins it by URL and checksum or leaves it out entirely.
text = re.sub(r'// The WAMR host is opt-in.*?\nlet withWamr[^\n]*\n\n', '', text, flags=re.S)
text = text.replace('import Foundation\n', '')
block = re.search(r'\nif withWamr \{.*?\n\}\n', text, flags=re.S)
if not block:
    raise SystemExit('the development manifest no longer has the expected WAMR block')
if wamr_checksum:
    pinned = block.group(0).replace('if withWamr {', 'do {')
    pinned = pinned.replace('.binaryTarget(name: "CExtWamr", path: "Frameworks/CExtWamr.xcframework")',
                            f'.binaryTarget(name: "CExtWamr", url: "{wamr_url}", checksum: "{wamr_checksum}")')
    text = text.replace(block.group(0), pinned)
else:
    text = text.replace(block.group(0), '')
# Drop what only the repository's own tree can use: the tests, which read res/web and the
# engine, and the conformance fixture, which is not part of the SDK.
text = re.sub(r'\n\s*\.testTarget\([^\n]*\),', '', text)
text = re.sub(r'\n\s*// Test fixture for the cross-language conformance suite\.\n\s*\.library\(name: "ExtConformancePlugin"[^\n]*\n', '\n', text)
text = re.sub(r'\n\s*\.target\(name: "ExtConformancePlugin"[^\n]*\),', '', text)
# The frames guest is a process a host starts: it ships only for macOS.
if platform != 'macos':
    text = re.sub(r'\n\s*// The frames conformance guest[^\n]*\n\s*\.executable\(name: "ExtFramesGuest"[^\n]*\n', '\n', text)
    text = re.sub(r'\n\s*\.executableTarget\(name: "ExtFramesGuest"[^\n]*\),', '', text)
for gone in ('ExtConformancePlugin',) + (() if platform == 'macos' else ('ExtFramesGuest',)):
    if f'"{gone}"' in text:
        raise SystemExit(f'the release manifest still names {gone}')
text = re.sub(r'// Development manifest\..*?\n(?=let package)', f'// Release manifest of ext-{platform}, generated by scripts/ext-ios-release.sh. The binary\n// target is the published engine archive, pinned by checksum.\n', text, flags=re.S)
open(target, 'w').write(text)
PY

# Prove the staged package builds. The archive is on a release that does not exist
# yet, so build a copy whose binary target is the unzipped local xcframework.
verify="$(mktemp -d "${TMPDIR:-/tmp}/$name-release.XXXXXX")"
trap 'rm -rf "$verify"' EXIT
cp -R "$dist/Sources" "$verify/Sources"
ditto -x -k "$out/CExtEngine.xcframework.zip" "$verify/Frameworks"
sed -E "s#\.binaryTarget\(name: \"CExtEngine\", url: \"[^\"]*\", checksum: \"[^\"]*\"\)#.binaryTarget(name: \"CExtEngine\", path: \"Frameworks/CExtEngine.xcframework\")#" "$dist/Package.swift" >"$verify/Package.swift"
if [[ -n "$with_wamr" ]]; then
  ditto -x -k "$out/CExtWamr.xcframework.zip" "$verify/Frameworks"
  sed -i '' -E "s#\.binaryTarget\(name: \"CExtWamr\", url: \"[^\"]*\", checksum: \"[^\"]*\"\)#.binaryTarget(name: \"CExtWamr\", path: \"Frameworks/CExtWamr.xcframework\")#" "$verify/Package.swift"
fi
if [[ "$platform" == macos ]]; then
  swift build --package-path "$verify" >/dev/null
else
  for destination in 'generic/platform=iOS' 'generic/platform=iOS Simulator'; do
    (cd "$verify" && xcodebuild build -quiet -scheme Ext-Package -destination "$destination" \
      -derivedDataPath "$verify/.derived" >/dev/null)
  done
fi

echo "package   $name"
echo "version   $version"
echo "tag       $tag"
echo "archive   $out/CExtEngine.xcframework.zip"
echo "checksum  $checksum"
echo "url       $url"
echo "licenses  $out/yyjson-LICENSE $out/miniz-LICENSE"
if [[ -n "$with_wamr" ]]; then
  echo "wamr      $out/CExtWamr.xcframework.zip"
  echo "wamr sum  $wamr_checksum"
  echo "license   $out/WAMR-LICENSE"
fi
echo "package   $dist (builds against the archive)"
