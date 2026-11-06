#!/usr/bin/env bash
# Builds, tests and stages one platform's prebuilt C engine (lib/plugin) for an ext-engine-vX.Y.Z
# release: the shared and static libraries, the headers, the CMake package and the licenses, as
# dist/ext-engine-X.Y.Z-<platform>.tar.gz with its SHA-256. Hosts that load the engine at run time
# (Python ctypes, PHP FFI, Ruby, Lua, .NET) and C consumers use it instead of building with CMake.
# It publishes nothing.
#
# Usage: scripts/ext-engine-release.sh <version> <platform> [build-dir]
#   version   must be the VERSION of project() in lib/plugin/CMakeLists.txt
#   platform  linux-x86_64, linux-aarch64, macos-universal, windows-x86_64 or windows-aarch64
# On Windows, run it from a shell where MSVC (cl) is set up.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
version="${1:?usage: ext-engine-release.sh <version> <platform> [build-dir]}"
platform="${2:?usage: ext-engine-release.sh <version> <platform> [build-dir]}"
build="${3:-$(mktemp -d "${TMPDIR:-/tmp}/ext-engine-release.XXXXXX")}"
mkdir -p "$build"
build="$(cd "$build" && pwd)"

declared="$(sed -n 's/^project(ext_host_prototype VERSION \([0-9.]*\) .*/\1/p' lib/plugin/CMakeLists.txt)"
[[ "$version" == "$declared" ]] || { echo "version $version is not lib/plugin's $declared" >&2; exit 1; }

exe="" args=(-DCMAKE_BUILD_TYPE=Release -DEXT_BUILD_SHARED=ON)
case "$platform" in
  linux-x86_64|linux-aarch64) library=lib/libext_host.so ;;
  macos-universal) library=lib/libext_host.dylib
    args+=(-DCMAKE_OSX_ARCHITECTURES="arm64;x86_64" -DCMAKE_OSX_DEPLOYMENT_TARGET=11.0) ;;
  windows-x86_64|windows-aarch64) library=bin/ext_host.dll exe=.exe
    args+=(-DCMAKE_C_COMPILER=cl) ;;
  *) echo "unknown platform $platform" >&2; exit 1 ;;
esac
if command -v ninja >/dev/null 2>&1; then args+=(-G Ninja); fi
winpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }

cmake -S lib/plugin -B "$build/engine" "${args[@]}"
cmake --build "$build/engine" -j 4

echo "== native tests"
"$build/engine/ext_guest_core_test$exe"
"$build/engine/ext_backend_test$exe"
"$build/engine/ext_frames_test$exe"
"$build/engine/ext_reverse_test$exe"
"$build/engine/ext_shared_library_test$exe"
"$build/engine/ext_endpoint_test$exe" "$(winpath "$root/lib/plugin/testdata/v1/endpoint-cases.json")"
"$build/engine/ext_fixtures_test$exe" "$(winpath "$root/lib/plugin/testdata/v1")"

name="ext-engine-$version-$platform"
stage="$build/stage/$name"
rm -rf "$stage"
cmake --install "$build/engine" --prefix "$stage"
cp LICENSE "$stage/LICENSE"
cat >"$stage/README.md" <<README
# ext engine $version ($platform)

The prebuilt C plugin engine of ext (lib/plugin): \`include/\` has the headers, \`$library\` the shared
library a host loads at run time (Python \`ext_io.host\` reads \`EXT_HOST_LIBRARY\`), and \`lib/\` the
static library and the CMake package (\`find_package(ext_host CONFIG)\`, targets \`EXT::host\`,
\`EXT::host_shared\`). Built from $(git rev-parse HEAD 2>/dev/null || echo "an unknown commit").
README

# The staged library must stand alone: load it from the stage and ask its ABI version.
python="$(command -v python3 || command -v python)"
"$python" - "$(winpath "$stage/$library")" <<'PY'
import ctypes, sys
lib = ctypes.CDLL(sys.argv[1])
lib.ext_host_abi_version.restype = ctypes.c_uint32
assert lib.ext_host_abi_version() == 2, lib.ext_host_abi_version()
assert hasattr(lib, "ext_shared_library_backend_create")
print("loaded", sys.argv[1])
PY

mkdir -p dist
archive="$root/dist/$name.tar.gz"
tar -C "$build/stage" -czf "$archive" "$name"
(cd dist && { sha256sum "$name.tar.gz" 2>/dev/null || shasum -a 256 "$name.tar.gz"; } >"$name.sha256")
echo "staged dist/$name.tar.gz"
