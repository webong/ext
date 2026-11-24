#!/usr/bin/env bash
# Build lib/ledger and run its fixture runner and behaviour tests. Works on Linux, macOS and
# Windows (MinGW-w64 from an MSYS2 shell, or MSVC with CC=cl).
# Usage: scripts/ledger-test.sh [build-dir]
# EXT_LEDGER_CI_SANITIZE=1 builds with the address and undefined-behaviour sanitizers;
# EXT_LEDGER_CMAKE_ARGS adds CMake arguments (-DEXT_LEDGER_SQLITE=ON for the SQLite backend).
set -euo pipefail
cd "$(dirname "$0")/.."
build=${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-ledger.XXXXXX")}
mkdir -p "$build"
generator=()
if command -v ninja >/dev/null 2>&1; then generator=(-G Ninja); fi
options=(-DCMAKE_BUILD_TYPE=Debug -DEXT_LEDGER_SHARED=ON) # the shared library too
if [ "${EXT_LEDGER_CI_SANITIZE:-}" = 1 ]; then options+=(-DEXT_LEDGER_SANITIZE=ON); fi
# shellcheck disable=SC2206 # extra CMake arguments, split on spaces
options+=(${EXT_LEDGER_CMAKE_ARGS:-})
cmake -S lib/ledger -B "$build" "${generator[@]}" "${options[@]}"
cmake --build "$build" -j 4
ctest --test-dir "$build" --output-on-failure -C Debug

# The description and the generated binding, through purego against the shared library: skipped
# without Go, under the sanitizers (a sanitized library cannot load into a Go program) and with MSVC.
if command -v go >/dev/null 2>&1 && [ "${EXT_LEDGER_CI_SANITIZE:-}" != 1 ] && [ "${CC:-}" != cl ]; then
  library=$(find "$build" -maxdepth 2 \( -name 'libext_ledger.so' -o -name 'libext_ledger.dylib' -o -name '*ext_ledger*.dll' \) | head -n 1)
  if [ -n "$library" ]; then
    if command -v cygpath >/dev/null 2>&1; then library=$(cygpath -m "$library"); fi
    echo "== lib/ledger's description and its generated binding (pkg/ext-go/cengine)"
    (cd pkg/ext-go/cengine && EXT_LEDGER_LIBRARY="$library" go test -count=1 -timeout=10m -run 'Ledger' ./library ./bind)
  fi
fi
