#!/usr/bin/env bash
# Stage a release of the Apple SDK (plugin, web, clipboard, system, and network code). SwiftPM resolves a package from a repository
# root and this monorepo's root is not a package, so a release is two pieces:
#   1. CExtEngine.xcframework.zip, the engine's guest core for macOS, iOS and the
#      iOS Simulator, attached to a GitHub release of this repository, and
#   2. a distribution package (Package.swift plus the Swift sources) whose binary
#      target points at that archive by URL and checksum, to publish at a repository
#      root that SwiftPM can resolve.
# This script builds both under dist/ext-ios-<version>/ and checks the distribution
# package compiles against the archive. It publishes nothing.
#
# --with-wamr also stages the optional WAMR host: CExtWamr.xcframework.zip, a second release
# asset, and the ExtPluginWamr product in the distribution package. WAMR is Apache-2.0 with the
# LLVM exception: its license is staged as WAMR-LICENSE beside the archives and in the package's
# LICENSES/ directory, and must ship with any binary that links it. Without the flag the package
# carries neither the product nor its sources.
# Usage: scripts/ext-ios-release.sh [--with-wamr] <version, for example 0.1.0>
set -euo pipefail
cd "$(dirname "$0")/.."
with_wamr=""
if [[ "${1:-}" == --with-wamr ]]; then with_wamr=1; shift; fi
version="${1:?usage: scripts/ext-ios-release.sh [--with-wamr] <version>}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'version must look like 0.1.0' >&2; exit 2; }
[[ "$(uname -s)" == Darwin ]] || { echo 'needs macOS and Xcode' >&2; exit 1; }
tag="ext-ios-v$version"
repository="${EXT_REPOSITORY:-webong/ext}"
sdk=pkg/ext-ios
out="dist/ext-ios-$version"
rm -rf "$out"
mkdir -p "$out"

./scripts/plugin-mobile-assets.sh --check
./scripts/ext-ios-xcframework.sh >/dev/null
( cd "$sdk/Frameworks" && ditto -c -k --sequesterRsrc --keepParent CExtEngine.xcframework "../../../$out/CExtEngine.xcframework.zip" )
checksum="$(swift package compute-checksum "$out/CExtEngine.xcframework.zip")"
url="https://github.com/$repository/releases/download/$tag/CExtEngine.xcframework.zip"
wamr_checksum=""
wamr_url="https://github.com/$repository/releases/download/$tag/CExtWamr.xcframework.zip"
if [[ -n "$with_wamr" ]]; then
  ./scripts/ext-ios-wamr-xcframework.sh >/dev/null
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
[[ -f "$dist/Sources/ExtClipboard/Clipboard.swift" ]] || { echo 'distribution is missing ExtClipboard' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/Surface.swift" ]] || { echo 'distribution is missing ExtSystem' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/Resources.swift" ]] || { echo 'distribution is missing ExtSystem resources' >&2; exit 1; }
[[ -f "$dist/Sources/ExtSystem/Accessibility.swift" ]] || { echo 'distribution is missing ExtSystem accessibility observation' >&2; exit 1; }
[[ -f "$dist/Sources/ExtNetwork/Network.swift" ]] || { echo 'distribution is missing ExtNetwork' >&2; exit 1; }
rm -rf "$dist/Sources/ExtConformancePlugin" "$dist/Sources/ExtFramesGuest"
cp "$sdk/README.md" "$dist/README.md"
cp LICENSE "$dist/LICENSE"
if [[ -n "$with_wamr" ]]; then
  mkdir -p "$dist/LICENSES" && cp "$out/WAMR-LICENSE" "$dist/LICENSES/WAMR-LICENSE"
else
  rm -rf "$dist/Sources/ExtPluginWamr"
fi
python3 - "$sdk/Package.swift" "$dist/Package.swift" "$url" "$checksum" "$wamr_url" "$wamr_checksum" <<'PY'
import re, sys
source, target, url, checksum, wamr_url, wamr_checksum = sys.argv[1:7]
text = open(source).read()
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
text = re.sub(r'\n\s*// The frames conformance guest[^\n]*\n\s*\.executable\(name: "ExtFramesGuest"[^\n]*\n', '\n', text)
text = re.sub(r'\n\s*\.executableTarget\(name: "ExtFramesGuest"[^\n]*\),', '', text)
text = re.sub(r'// Development manifest\..*?\n(?=let package)', '// Release manifest, generated by scripts/ext-ios-release.sh. The binary target is the\n// published engine archive, pinned by checksum.\n', text, flags=re.S)
open(target, 'w').write(text)
PY

# Prove the staged package builds. The archive is on a release that does not exist
# yet, so build a copy whose binary target is the unzipped local xcframework.
verify="$(mktemp -d "${TMPDIR:-/tmp}/ext-ios-release.XXXXXX")"
trap 'rm -rf "$verify"' EXIT
cp -R "$dist/Sources" "$verify/Sources"
ditto -x -k "$out/CExtEngine.xcframework.zip" "$verify/Frameworks"
sed -E "s#\.binaryTarget\(name: \"CExtEngine\", url: \"[^\"]*\", checksum: \"[^\"]*\"\)#.binaryTarget(name: \"CExtEngine\", path: \"Frameworks/CExtEngine.xcframework\")#" "$dist/Package.swift" >"$verify/Package.swift"
if [[ -n "$with_wamr" ]]; then
  ditto -x -k "$out/CExtWamr.xcframework.zip" "$verify/Frameworks"
  sed -i '' -E "s#\.binaryTarget\(name: \"CExtWamr\", url: \"[^\"]*\", checksum: \"[^\"]*\"\)#.binaryTarget(name: \"CExtWamr\", path: \"Frameworks/CExtWamr.xcframework\")#" "$verify/Package.swift"
fi
swift build --package-path "$verify" >/dev/null

echo "version   $version"
echo "tag       $tag"
echo "archive   $out/CExtEngine.xcframework.zip"
echo "checksum  $checksum"
echo "url       $url"
if [[ -n "$with_wamr" ]]; then
  echo "wamr      $out/CExtWamr.xcframework.zip"
  echo "wamr sum  $wamr_checksum"
  echo "license   $out/WAMR-LICENSE"
fi
echo "package   $dist (builds against the archive)"
