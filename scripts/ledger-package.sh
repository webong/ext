#!/usr/bin/env bash
# Builds lib/ledger's shared library and seals it as an ext package for this
# machine's OS and architecture: a directory holding plugin.json and the
# library, and the same as a .tar.gz, ready for `ext package install`. The
# library is an ext plugin (contract ext.graph v1, runtime cshared), loaded on
# demand by a host; see lib/ledger/README.md.
#
# Usage: scripts/ledger-package.sh <revision> [out-dir]
# Environment:
#   EXT_LEDGER_CMAKE_ARGS  extra CMake arguments (a toolchain file, for another target)
#   EXT_LEDGER_OS, EXT_LEDGER_ARCH  the Go names of the target, when cross-building
set -euo pipefail
cd "$(dirname "$0")/.."
revision=${1:?usage: scripts/ledger-package.sh <revision> [out-dir]}
out=${2:-dist/ledger}
case "$revision" in
  [A-Za-z0-9]*) ;;
  *) echo "revision must start with a letter or digit" >&2; exit 64 ;;
esac

os=${EXT_LEDGER_OS:-}
arch=${EXT_LEDGER_ARCH:-}
if [ -z "$os" ]; then
  case "$(uname -s)" in
    Darwin) os=darwin ;; Linux) os=linux ;; MINGW*|MSYS*|CYGWIN*) os=windows ;;
    *) echo "unknown OS $(uname -s); set EXT_LEDGER_OS" >&2; exit 64 ;;
  esac
fi
if [ -z "$arch" ]; then
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;;
    *) echo "unknown architecture $(uname -m); set EXT_LEDGER_ARCH" >&2; exit 64 ;;
  esac
fi
case "$os" in
  darwin) library=libext_ledger.dylib ;; windows) library=libext_ledger.dll ;; *) library=libext_ledger.so ;;
esac

build=$(mktemp -d "${TMPDIR:-/tmp}/ext-ledger-package.XXXXXX")
trap 'rm -rf "$build"' EXIT
generator=()
if command -v ninja >/dev/null 2>&1; then generator=(-G Ninja); fi
# shellcheck disable=SC2206 # extra CMake arguments, split on spaces
cmake -S lib/ledger -B "$build" "${generator[@]}" -DCMAKE_BUILD_TYPE=Release -DEXT_LEDGER_SHARED=ON \
  -DEXT_LEDGER_TESTS=OFF -DEXT_LEDGER_COMMAND=OFF -DEXT_LEDGER_REVISION="$revision" ${EXT_LEDGER_CMAKE_ARGS:-} >/dev/null
cmake --build "$build" --target ext_ledger_shared -j 4 >/dev/null
# The unversioned name, a link to the versioned file where there is one; cp copies what it names.
built=$(find "$build" -maxdepth 2 \( -name 'libext_ledger.so' -o -name 'libext_ledger.dylib' -o -name 'libext_ledger.dll' -o -name 'ext_ledger.dll' \) | head -n 1)
[ -n "$built" ] || { echo "the shared library was not built" >&2; exit 1; }

name="ext-ledger-$revision-$os-$arch"
dir="$out/$name"
rm -rf "$dir"
mkdir -p "$dir"
cp "$built" "$dir/$library"
cp LICENSE "$dir/LICENSE"
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
python3 - "$revision" "$library" "$(sha "$dir/$library")" "$(sha "$dir/LICENSE")" "$os" "$arch" lib/ledger/package/descriptor.json "$dir/plugin.json" <<'PY'
import json, sys
revision, library, digest, license_digest, os_, arch, template, out = sys.argv[1:]
descriptor = json.load(open(template))
descriptor["identity"]["revision"] = revision
manifest = {
    "apiVersion": "ext.package/v1",
    "descriptor": descriptor,
    "license": "MIT",
    "artifacts": [
        {"name": "library", "path": library, "sha256": digest, "os": os_, "arch": arch},
        {"name": "license", "path": "LICENSE", "sha256": license_digest},
    ],
    "entrypoints": [{"name": "library", "runtime": "cshared", "artifact": "library", "protocols": ["ext.plugin/v1"]}],
}
with open(out, "w") as f:
    json.dump(manifest, f, indent=2)
    f.write("\n")
PY
(cd "$out" && tar -czf "$name.tar.gz" "$name")
echo "$dir"
echo "$out/$name.tar.gz"
