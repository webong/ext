#!/usr/bin/env bash
# Build the C plugin engine and run its native tests, then the Go suites that link
# against it. Works on Linux, macOS and Windows (MinGW-w64, from an MSYS2 shell).
# Usage: scripts/plugin-engine-test.sh [build-dir]
# EXT_ENGINE_C_ONLY=1 stops after the native tests, for toolchains cgo cannot use (MSVC).
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
build=${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-engine.XXXXXX")}
mkdir -p "$build"
build=$(cd "$build" && pwd)
exe=""
race=(-race)
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) exe=.exe; race=() ;; # no thread sanitizer on this toolchain
esac
winpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
python=$(command -v python3 || command -v python)
generator=()
if command -v ninja >/dev/null 2>&1; then generator=(-G Ninja); fi

cmake -S lib/plugin -B "$build/engine" "${generator[@]}" -DCMAKE_BUILD_TYPE=Release
cmake --build "$build/engine" -j 4

guest="$build/go-guest$exe"
fault="$build/fault-guest$exe"
go build -o "$guest" ./pkg/ext-go/crosslang/testdata/go-guest
go build -o "$fault" ./pkg/ext-go/cgoengine/testdata/faultguest

"$python" - "$build" <<'PY'
import json, pathlib, sys
p = pathlib.Path(sys.argv[1])
d = json.loads(pathlib.Path('lib/plugin/testdata/v1/descriptor.json').read_text())
(p / 'descriptor.json').write_text(json.dumps(d))
for op in ['echo', 'public-error', 'wait']:
    r = dict(apiVersion='ext.plugin/v1', id='1', plugin=d['identity'],
             contract=dict(name='ext.conformance', version='v1'), operation=op,
             deadline='2099-01-01T00:00:00Z', payload=dict(value=7))
    (p / (op + '.json')).write_text(json.dumps(r))
PY

echo "== native tests"
"$build/engine/ext_guest_core_test$exe"
"$build/engine/ext_resources_test$exe"
"$build/engine/ext_limits_test$exe"
"$build/engine/ext_tree_limits_test$exe"
"$build/engine/ext_frames_test$exe"
"$build/engine/ext_reverse_test$exe"
"$build/engine/ext_backend_test$exe"
"$build/engine/ext_endpoint_test$exe" "$(winpath "$root/lib/plugin/testdata/v1/endpoint-cases.json")"
"$build/engine/ext_fixtures_test$exe" "$(winpath "$root/lib/plugin/testdata/v1")"
"$build/engine/ext_host_native_test$exe" "$(winpath "$guest")" "$(winpath "$build/descriptor.json")" "$(winpath "$build/echo.json")" "$(winpath "$build/wait.json")"
"$build/engine/ext_store_test$exe" "$(winpath "$build/store-package")"
"$build/engine/ext_fs_write_test$exe" "$(winpath "$build/fs-write")"
"$build/engine/ext_store_lifecycle_test$exe" "$(winpath "$build/store-lifecycle")"
"$build/engine/ext_store_roots_test$exe" "$(winpath "$build/store-roots")"
"$build/engine/ext_trust_test$exe" "$(winpath "$build/trust.json")"

if [ -n "${EXT_ENGINE_C_ONLY:-}" ]; then
  echo "native engine tests passed (EXT_ENGINE_C_ONLY: Go suites skipped)"
  exit 0
fi

echo "== Go suites linked against the engine (pkg/ext-go/cgoengine)"
(cd pkg/ext-go/cgoengine
 CGO_LDFLAGS="-L$(winpath "$build/engine")" \
 EXT_CENGINE_GUEST="$(winpath "$guest")" \
 EXT_CENGINE_FAULT_GUEST="$(winpath "$fault")" \
   go test -tags ext_cengine "${race[@]}" -count=1 -timeout=10m ./...)
echo "== the engine module, standard library only (pkg/ext-go/engine)"
(cd pkg/ext-go/engine && go test -count=1 -timeout=10m ./...)
echo "== the same API without cgo, served by the pure-Go implementation"
(cd pkg/ext-go/cgoengine && CGO_ENABLED=0 go test -count=1 -timeout=10m ./...)
echo "== the umbrella module (pkg/ext-go)"
(cd pkg/ext-go && go test -count=1 -timeout=10m .)
echo "plugin engine tests passed"
