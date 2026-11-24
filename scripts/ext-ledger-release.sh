#!/usr/bin/env bash
# Builds, tests and stages one platform's prebuilt ledger (lib/ledger) for an ext-ledger-vX.Y.Z
# release: the shared and static libraries, the header, the ext-ledger command, the description of
# its library interface and the license, as dist/ext-ledger-X.Y.Z-<platform>.tar.gz with its SHA-256.
# It publishes nothing.
#
# Usage: scripts/ext-ledger-release.sh <version> <platform> [build-dir]
#   version   must be the VERSION of project() in lib/ledger/CMakeLists.txt
#   platform  linux-x86_64, linux-aarch64, macos-universal, windows-x86_64 or windows-aarch64
# On Windows, run it from a shell where MSVC (cl) is set up.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
version="${1:?usage: ext-ledger-release.sh <version> <platform> [build-dir]}"
platform="${2:?usage: ext-ledger-release.sh <version> <platform> [build-dir]}"
build="${3:-$(mktemp -d "${TMPDIR:-/tmp}/ext-ledger-release.XXXXXX")}"
mkdir -p "$build"
build="$(cd "$build" && pwd)"

declared="$(sed -n 's/^project(ext_ledger VERSION \([0-9.]*\) .*/\1/p' lib/ledger/CMakeLists.txt)"
[[ "$version" == "$declared" ]] || { echo "version $version is not lib/ledger's $declared" >&2; exit 1; }

args=(-DCMAKE_BUILD_TYPE=Release -DEXT_LEDGER_SHARED=ON)
case "$platform" in
  linux-x86_64|linux-aarch64) library=lib/libext_ledger.so.0 ;;
  macos-universal) library=lib/libext_ledger.0.dylib
    args+=(-DCMAKE_OSX_ARCHITECTURES="arm64;x86_64" -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0) ;;
  windows-x86_64|windows-aarch64) library=bin/ext_ledger.dll
    args+=(-DCMAKE_C_COMPILER=cl) ;;
  *) echo "unknown platform $platform" >&2; exit 1 ;;
esac
if command -v ninja >/dev/null 2>&1; then args+=(-G Ninja); fi
winpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }

cmake -S lib/ledger -B "$build/ledger" "${args[@]}"
cmake --build "$build/ledger" -j 4 --config Release
echo "== the ledger's tests"
ctest --test-dir "$build/ledger" --output-on-failure -C Release

name="ext-ledger-$version-$platform"
stage="$build/stage/$name"
rm -rf "$stage"
cmake --install "$build/ledger" --prefix "$stage" --config Release
cp LICENSE "$stage/LICENSE"
cp lib/ledger/interface.json "$stage/interface.json" # the description ext lib bind generates a binding from
cat >"$stage/README.md" <<README
# ext ledger $version ($platform)

The prebuilt ledger of ext (lib/ledger): \`include/ext_ledger.h\` is the header, \`interface.json\` the
description of its library interface, \`$library\` the shared library a binding loads at run time,
\`lib/\` the static library and \`bin/\` the \`ext-ledger\` command. Built from
$(git rev-parse HEAD 2>/dev/null || echo "an unknown commit").
README

# The staged library must stand alone: load it from the stage and ask its ABI version.
python="$(command -v python3 || command -v python)"
"$python" - "$(winpath "$stage/$library")" <<'PY'
import ctypes, sys
lib = ctypes.CDLL(sys.argv[1])
lib.ext_ledger_abi_version.restype = ctypes.c_uint32
assert lib.ext_ledger_abi_version() == 1, lib.ext_ledger_abi_version()
print("loaded", sys.argv[1])
PY

mkdir -p dist
archive="$root/dist/$name.tar.gz"
tar -C "$build/stage" -czf "$archive" "$name"
(cd dist && { sha256sum "$name.tar.gz" 2>/dev/null || shasum -a 256 "$name.tar.gz"; } >"$name.sha256")
echo "staged dist/$name.tar.gz"
