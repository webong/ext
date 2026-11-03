#!/usr/bin/env bash
# Install the pinned Zig toolchain into a directory after checking its SHA-256, and print the path
# of the zig binary. Linux x86_64 and macOS (arm64 and x86_64). The version is pinned because Zig's
# standard library and build APIs change between releases; it is the .minimum_zig_version of
# pkg/ext-zig/build.zig.zon.
#
# Usage: scripts/install-zig.sh <directory>
set -euo pipefail
dest="${1:?usage: scripts/install-zig.sh <directory>}"
version=0.17.0
case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)
    archive="zig-aarch64-macos-$version"
    digest=b607e9b9234790a008116ae5bdb71c6243b84b9fb42a53a9e70fde41c06c536a ;;
  Darwin-x86_64)
    archive="zig-x86_64-macos-$version"
    digest=4f9a1c5269aa17ebda5e6d3c2b89d6cbf36f7d2b22a0306e9ab98f25f95529c6 ;;
  Linux-x86_64)
    archive="zig-x86_64-linux-$version"
    digest=1cbe9df9f27e6b78d14ccbca43b6703a404ef79ef1c463de901d7f088d4e2026 ;;
  *)
    echo "no pinned Zig $version for $(uname -s)-$(uname -m)" >&2
    exit 1 ;;
esac
mkdir -p "$dest"
curl -fsSL "https://ziglang.org/download/$version/$archive.tar.xz" -o "$dest/zig.tar.xz"
actual="$(shasum -a 256 "$dest/zig.tar.xz" | cut -d ' ' -f 1)"
[[ "$actual" == "$digest" ]] || { echo "the Zig archive has the checksum $actual, not $digest" >&2; exit 1; }
tar -xf "$dest/zig.tar.xz" -C "$dest"
echo "$dest/$archive/zig"
