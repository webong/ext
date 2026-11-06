#!/usr/bin/env bash
# Runs the shared host cases (lib/plugin/testdata/v1/host-cases.json) on every host that has a
# runner: the pure-Go host, the oracle (tests/crosslang TestHostCases), and the hosts that bind the
# C engine (pkg/ext-python and pkg/ext-lua, ext_io.host). It builds the engine's shared library and the reference
# guest twice: as a JSON-line child process and as a C ABI library.
#
# Environment:
#   EXT_HOST_CASES_BUILD_DIR  where to build (default: a new temporary directory)
#   PYTHON                    the Python to run the Python host with (default: python3)
#   EXT_LUA                   the Lua to run the Lua host with (default: lua); LUA_INCDIR as for
#                             scripts/ext-lua-host.sh
set -euo pipefail
cd "$(dirname "$0")/.."
build_dir="${EXT_HOST_CASES_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-host-cases.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
python="${PYTHON:-python3}"

case "$(uname -s)" in
  Darwin) library=libext_host.dylib shared=dylib ;;
  *) library=libext_host.so shared=so ;;
esac

cmake -S lib/plugin -B "$build_dir/engine" -DCMAKE_BUILD_TYPE=Release -DEXT_BUILD_SHARED=ON >/dev/null
cmake --build "$build_dir/engine" --target ext_host_shared -j 4 >/dev/null
go build -o "$build_dir/go-guest" ./tests/crosslang/testdata/go-guest
go build -buildmode=c-shared -o "$build_dir/go-shared.$shared" ./tests/crosslang/testdata/go-shared

export EXT_GO_GUEST="$build_dir/go-guest" EXT_GO_SHARED="$build_dir/go-shared.$shared"
export EXT_HOST_LIBRARY="$build_dir/engine/$library" EXT_HOST_REQUIRED=1 EXT_CROSSLANG_REQUIRED=1

echo "== the Go host (the oracle)"
go test -count=1 -run 'TestHostCases$' ./tests/crosslang
echo "== the Python host ($("$python" --version 2>&1))"
"$python" -I -m unittest discover -s pkg/ext-python/tests -p 'test_host.py' -v
echo "== the Lua host ($("${EXT_LUA:-lua}" -v 2>&1 | head -1))"
scripts/ext-lua-host.sh "$build_dir/lua"
LUA_CPATH="$build_dir/lua/?.so" "${EXT_LUA:-lua}" pkg/ext-lua/tests/run.lua test_host
