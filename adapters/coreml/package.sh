#!/usr/bin/env bash
# Build a standalone Core ML provider guest and seal a registry-ready Ext package.
set -euo pipefail

root="$(cd "$(dirname "$0")" && pwd)"
out="${1:-$root/out}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
ext="${EXT_BIN:-ext}"

[[ "$(uname -s)" == Darwin ]] || { echo 'Core ML packaging requires macOS' >&2; exit 1; }
case "$(uname -m)" in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo 'unsupported macOS architecture' >&2; exit 1 ;;
esac

package="$out/coreml.darwin-$arch"
archive="$package.zip"
index="$out/index.json"
[[ ! -e "$package" && ! -e "$archive" && ! -e "$index" ]] || {
  echo "output already exists in $out" >&2
  exit 1
}

swift build --configuration release --product ext-coreml-guest --package-path "$root"
binary_dir="$(swift build --configuration release --package-path "$root" --show-bin-path)"
"$ext" ctn model provider package --os darwin --arch "$arch" \
  coreml 1.0.0 "$binary_dir/ext-coreml-guest" "$package"
mv "$package/plugin.json" "$package/ext.json"
(cd "$package" && zip -q "$archive" ext.json provider)
"$ext" package index "$index" "$archive"
echo "$archive"
echo "$index"
