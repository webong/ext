#!/usr/bin/env bash
# Stage a release of the Zig package. Zig has no registry: a consumer runs `zig fetch` on an archive
# whose root holds build.zig.zon, and this monorepo's root is not a package. So a release is the
# package alone, as ext-zig-<version>-package.tar.gz: the files build.zig.zon lists in .paths, at
# the archive root, plus the repository LICENSE. It goes on a GitHub release and at the root of the
# dist/ext-zig branch (scripts/dist-branch.sh).
#
# This script stages under dist/ext-zig-<version>/ and checks that the version is the manifest's.
# With ZIG_BIN set (scripts/install-zig.sh) it also builds and tests the staged package on its own,
# with fresh caches, and records the hash `zig fetch` gives the archive: that hash is what a consumer's
# build.zig.zon will hold, and the release workflow checks the published branch against it. It
# publishes nothing.
#
# Usage: [ZIG_BIN=<path to zig>] scripts/ext-zig-release.sh <version, for example 0.1.0>
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:?usage: scripts/ext-zig-release.sh <version>}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'version must look like 0.1.0' >&2; exit 2; }
sdk=pkg/ext-zig
manifest="$sdk/build.zig.zon"
declared="$(sed -n 's/^[[:space:]]*\.version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$manifest")"
[[ "$declared" == "$version" ]] || { echo "build.zig.zon is version '$declared', not $version" >&2; exit 1; }

# What the package consists of is what its manifest says.
paths="$(awk '/\.paths[[:space:]]*=/ { found = 1 } found { print } found && /\}/ { exit }' "$manifest" | grep -o '"[^"]*"' | tr -d '"')"
[[ -n "$paths" ]] || { echo "$manifest lists no .paths" >&2; exit 1; }

out="dist/ext-zig-$version"
if [[ -e "$out" ]]; then rm -r "$out"; fi
package="$out/package"
mkdir -p "$package"
while IFS= read -r path; do
  case "$path" in
    /* | *..*) echo "$manifest lists the path $path, which leaves the package" >&2; exit 1 ;;
  esac
  [[ -e "$sdk/$path" ]] || { echo "$manifest lists $path, which does not exist" >&2; exit 1; }
  mkdir -p "$package/$(dirname "$path")"
  cp -R "$sdk/$path" "$package/$path"
done <<<"$paths"
find "$package" \( -name .zig-cache -o -name zig-out \) -prune -exec rm -r {} +
cp LICENSE "$package/LICENSE"

archive="ext-zig-$version-package.tar.gz"
tar -czf "$out/$archive" -C "$package" .
files=("$archive")

if [[ -n "${ZIG_BIN:-}" ]]; then
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT
  mkdir "$work/unpacked"
  tar -xzf "$out/$archive" -C "$work/unpacked"
  # Fresh caches through the environment: `zig build` rejects --global-cache-dir (Zig 0.17.0).
  export ZIG_LOCAL_CACHE_DIR="$work/cache" ZIG_GLOBAL_CACHE_DIR="$work/global"
  # The package does not carry the shared conformance fixtures, so its tests read them from the repository.
  fixtures="$PWD/lib/plugin/testdata/v1"
  [[ -d "$fixtures" ]] || { echo "$fixtures does not exist" >&2; exit 1; }
  (
    cd "$work/unpacked"
    EXT_FIXTURES="$fixtures" "$ZIG_BIN" build test
    "$ZIG_BIN" build --prefix "$work/prefix"
  )
  hash="$("$ZIG_BIN" fetch "$PWD/$out/$archive")"
  [[ -n "$hash" ]] || { echo 'zig fetch printed no hash' >&2; exit 1; }
  echo "$hash" >"$out/ext-zig-$version-package-hash.txt"
  files+=("ext-zig-$version-package-hash.txt")
  echo "package hash: $hash"
else
  echo 'ZIG_BIN is not set: the package is staged but was not built, tested or hashed' >&2
fi
(cd "$out" && shasum -a 256 "${files[@]}" | tee checksums.txt)
