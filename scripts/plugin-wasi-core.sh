#!/usr/bin/env bash
# Compile and execute the portable C guest core and its Rust binding in WASI.
set -euo pipefail
cd "$(dirname "$0")/.."
build_dir="${EXT_WASI_CORE_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-wasi-core.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
zig_bin="$(command -v "${ZIG_BIN:-zig}")"
test "$("$zig_bin" version)" = 0.17.0
export ZIG_GLOBAL_CACHE_DIR="${ZIG_GLOBAL_CACHE_DIR:-$build_dir/zig-global}"
cmake -S pkg/plugin-engine -B "$build_dir/core" -DEXT_BUILD_HOST=OFF \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_TOOLCHAIN_FILE="$PWD/pkg/plugin-engine/cmake/wasi-zig.cmake" \
  -DEXT_ZIG="$zig_bin"
cmake --build "$build_dir/core" -j 4
go run ./pkg/plugin-wasm/cmd/wasirun "$build_dir/core/ext_guest_core_test"
EXT_ENGINE_LIB_DIR="$build_dir/core" CARGO_TARGET_DIR="$build_dir/cargo" \
  cargo build --locked --manifest-path pkg/plugin-rust/engine/Cargo.toml \
  --target wasm32-wasip1 --features guest-only --example guest
go run ./pkg/plugin-wasm/cmd/wasirun "$build_dir/cargo/wasm32-wasip1/debug/examples/guest.wasm"
echo "Portable guest artifacts: $build_dir"
