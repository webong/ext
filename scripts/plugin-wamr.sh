#!/usr/bin/env bash
# Build the optional WAMR reactor backend of pkg/plugin-engine and run its test
# against the Rust SDK's conformance reactor. Needs cmake, a C compiler, git and
# cargo with the wasm32-wasip1 target. WAMR is not vendored: it is fetched at the
# pinned commit below (override the checkout with EXT_WAMR_ROOT).
set -euo pipefail
cd "$(dirname "$0")/.."
# Apache-2.0 with the LLVM exception. The limits only work with the build options
# in pkg/plugin-engine/wamr/CMakeLists.txt, which were measured at this commit.
WAMR_COMMIT=f5f57c09aee623436f5fb87a90798fdd2cdf39fd
build_dir="${EXT_WAMR_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-wamr.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
wamr_root="${EXT_WAMR_ROOT:-$build_dir/wamr}"
if [[ ! -d "$wamr_root/.git" && -z "${EXT_WAMR_ROOT:-}" ]]; then
  git clone --quiet https://github.com/bytecodealliance/wasm-micro-runtime "$wamr_root"
fi
if [[ -z "${EXT_WAMR_ROOT:-}" ]]; then git -C "$wamr_root" checkout --quiet "$WAMR_COMMIT"; fi
CARGO_TARGET_DIR="$build_dir/cargo" cargo build --release --locked \
  --manifest-path pkg/plugin-rust/Cargo.toml --target wasm32-wasip1 --example shared
reactor="$build_dir/cargo/wasm32-wasip1/release/examples/shared.wasm"
cmake -S pkg/plugin-engine -B "$build_dir/engine" -DCMAKE_BUILD_TYPE=Release \
  -DEXT_WITH_WAMR=ON -DEXT_WAMR_ROOT="$wamr_root" -DEXT_WAMR_TEST_REACTOR="$reactor"
cmake --build "$build_dir/engine" -j 4
"$build_dir/engine/wamr/ext_wamr_test" "$reactor"
echo "WAMR backend artifacts: $build_dir"
