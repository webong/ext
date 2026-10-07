#!/usr/bin/env bash
# Build and exercise the experimental C engine (static by default). No registry publication.
set -euo pipefail
cd "$(dirname "$0")/.."
build_dir="${CTX_CENGINE_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ctx-cengine.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$build_dir/cargo}"
export ZIG_GLOBAL_CACHE_DIR="${ZIG_GLOBAL_CACHE_DIR:-$build_dir/zig-global}"
export ZIG_LOCAL_CACHE_DIR="${ZIG_LOCAL_CACHE_DIR:-$build_dir/zig-local}"
zig_bin="${ZIG_BIN:-zig}"
test "$("$zig_bin" version)" = 0.17.0
node_include="${NODE_INCLUDE_DIR:-$(node -p 'require("path").resolve(process.execPath,"../../include/node")')}"
test -f "$node_include/node_api.h"
linkage="${CTX_CENGINE_LINKAGE:-static}"
go_tags=ctx_cengine
rust_features=(--no-default-features)
engine_link=(-lctx_host_static -lpthread -lm)
engine_cflags=(-DCTX_HOST_STATIC)
case "$linkage" in
  static) ;;
  shared)
    go_tags=ctx_cengine,ctx_cengine_shared
    rust_features=(--features shared)
    engine_link=(-lctx_host)
    engine_cflags=(-UCTX_HOST_STATIC)
    ;;
  *) echo "CTX_CENGINE_LINKAGE must be static or shared" >&2; exit 1 ;;
esac
cmake -S pkg/plugin-engine -B "$build_dir/release" -DCMAKE_BUILD_TYPE=Release -DCTX_BUILD_SHARED=ON
cmake --build "$build_dir/release" -j 4
export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$build_dir/release -Wl,-rpath,$build_dir/release"
export CTX_CENGINE_GUEST="$build_dir/go-guest" CTX_CENGINE_FAULT_GUEST="$build_dir/fault-guest"
go build -o "$CTX_CENGINE_GUEST" ./pkg/plugin-wasm/crosslang/testdata/go-guest
go build -o "$CTX_CENGINE_FAULT_GUEST" ./pkg/plugin-engine/tests/faultguest
go test -tags "$go_tags" -race -count=1 -timeout=90s ./pkg/plugin-go
python3 - "$build_dir" <<'PY'
import json, pathlib, sys, datetime
p=pathlib.Path(sys.argv[1]);d=json.loads(pathlib.Path('pkg/plugin/testdata/v1/descriptor.json').read_text())
(p/'descriptor.json').write_text(json.dumps(d))
for op in ['echo','public-error','wait']:
 r=dict(apiVersion='ext.plugin/v1',id='1',plugin=d['identity'],contract=dict(name='ext.conformance',version='v1'),operation=op,deadline='2099-01-01T00:00:00Z',payload=dict(value=7))
 (p/(op+'.json')).write_text(json.dumps(r))
PY
cmake -S pkg/plugin-engine -B "$build_dir/sanitize" -DCMAKE_BUILD_TYPE=Debug -DCTX_SANITIZE=ON
cmake --build "$build_dir/sanitize" -j 4
"$build_dir/sanitize/ctx_guest_core_test"
"$build_dir/sanitize/ctx_resources_test"
"$build_dir/sanitize/ctx_host_native_test" "$CTX_CENGINE_GUEST" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/wait.json"
cmake -S pkg/plugin-engine -B "$build_dir/thread-sanitize" -DCMAKE_BUILD_TYPE=Debug -DCTX_THREAD_SANITIZE=ON
cmake --build "$build_dir/thread-sanitize" -j 4
"$build_dir/thread-sanitize/ctx_guest_core_test"
"$build_dir/thread-sanitize/ctx_resources_test"
"$build_dir/thread-sanitize/ctx_host_native_test" "$CTX_CENGINE_GUEST" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/wait.json"
for foreign in "${CTX_CENGINE_RUST_GUEST:-}" "${CTX_CENGINE_ZIG_GUEST:-}"; do
  if [[ -n "$foreign" ]]; then
    CTX_CENGINE_GUEST="$foreign" go test -tags "$go_tags" -race -count=1 -run '^TestConformance$' ./pkg/plugin-go
  fi
done
cargo fmt --manifest-path pkg/plugin-rust/engine/Cargo.toml -- --check
CTX_ENGINE_LIB_DIR="$build_dir/release" RUSTFLAGS="-C link-arg=-Wl,-rpath,$build_dir/release" cargo test "${rust_features[@]}" --locked --manifest-path pkg/plugin-rust/engine/Cargo.toml
"$zig_bin" fmt --check pkg/plugin-zig/engine
"$zig_bin" build --build-file pkg/plugin-zig/engine/build.zig -Dengine-include="$PWD/pkg/plugin-engine/include" -Dengine-lib="$build_dir/release" -Dshared="$([[ "$linkage" == shared ]] && echo true || echo false)" test
cargo fmt --manifest-path pkg/plugin-rust/examples/cengine/Cargo.toml -- --check
RUSTFLAGS="-L native=$build_dir/release -C link-arg=-Wl,-rpath,$build_dir/release" cargo build "${rust_features[@]}" --locked --manifest-path pkg/plugin-rust/examples/cengine/Cargo.toml
"$zig_bin" fmt --check pkg/plugin-zig/examples/cengine/main.zig
"$zig_bin" build-exe pkg/plugin-zig/examples/cengine/main.zig -O ReleaseSafe -L "$build_dir/release" "${engine_link[@]}" -lc -rpath "$build_dir/release" -femit-bin="$build_dir/zig-host"
node_link=()
if [[ "$(uname -s)" == Darwin ]]; then node_link=(-undefined dynamic_lookup); fi
"${CC:-cc}" -std=c11 -Wall -Wextra -Werror -O2 -shared -fPIC "${node_link[@]}" -I "$node_include" -I pkg/plugin-engine/include pkg/plugin-ts/examples/cengine/addon.c -L "$build_dir/release" "${engine_link[@]}" "${engine_cflags[@]}" -Wl,-rpath,"$build_dir/release" -o "$build_dir/ctx_host.node"
"${CC:-cc}" -std=c11 -Wall -Wextra -Werror -O2 -shared -fPIC "${node_link[@]}" -I "$node_include" -I pkg/plugin-engine/include pkg/plugin-ts/engine/addon.c -L "$build_dir/release" "${engine_link[@]}" "${engine_cflags[@]}" -Wl,-rpath,"$build_dir/release" -o "$build_dir/ctx_engine.node"
CTX_ENGINE_ADDON="$build_dir/ctx_engine.node" node --test pkg/plugin-ts/engine/test.mjs
go build -tags "$go_tags" -o "$build_dir/go-host" ./pkg/plugin-go/examples/host
"$build_dir/go-host" "$CTX_CENGINE_GUEST" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/public-error.json" > "$build_dir/go.jsonl"
"$CARGO_TARGET_DIR/debug/ctx-cengine-example" "$CTX_CENGINE_GUEST" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/public-error.json" > "$build_dir/rust.jsonl"
"$build_dir/zig-host" "$CTX_CENGINE_GUEST" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/public-error.json" > "$build_dir/zig.jsonl"
node pkg/plugin-ts/examples/cengine/host.cjs "$build_dir/ctx_host.node" "$CTX_CENGINE_GUEST" "$build_dir/descriptor.json" "$build_dir/echo.json" "$build_dir/public-error.json" > "$build_dir/node.jsonl"
python3 - "$build_dir" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1])
for language in ['go','rust','zig','node']:
 rows=[json.loads(line) for line in (p/(language+'.jsonl')).read_text().splitlines()]
 assert len(rows)==2 and rows[0]['payload']=={'value':7},(language,rows)
 assert rows[1]['error']['code']=='busy' and rows[1]['error']['retryAfterMilliseconds']==10,(language,rows)
print('Go, Rust, Zig and Node.js used the same C engine successfully.')
PY
CTX_CENGINE_BUILD_DIR="$build_dir" scripts/plugin-cengine-install.sh
if [[ "${CTX_CENGINE_SKIP_BENCH:-0}" == 1 ]]; then
  echo "Conformance artifacts: $build_dir"
  exit 0
fi
go build -tags "$go_tags" -o "$build_dir/bench" ./pkg/plugin-wasm/cengine/bench
: > "$build_dir/benchmarks.jsonl"
# Each sample is a fresh process. Alternate order to reduce warm-cache bias.
for repetition in 1 2 3; do
  for shape in '16 1000 1' '1024 1000 1' '1048576 50 1' '1024 1000 8'; do
    read -r size iterations concurrency <<< "$shape"
    engines=(go c); if [[ "$repetition" == 2 ]]; then engines=(c go); fi
    for engine in "${engines[@]}"; do
      "$build_dir/bench" "$engine" "$CTX_CENGINE_GUEST" "$size" "$iterations" "$concurrency" >> "$build_dir/benchmarks.jsonl"
    done
  done
done
python3 pkg/plugin-engine/bench/report.py "$build_dir/benchmarks.jsonl" > "$build_dir/benchmark-report.md"
cat "$build_dir/benchmark-report.md"
echo "Artifacts and raw measurements: $build_dir"
