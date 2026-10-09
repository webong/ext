#!/usr/bin/env bash
# Build the optional WAMR host of pkg/plugin-android (libextwamrjni, the Java classes) on a
# desktop JDK and run its tests against the Rust SDK's conformance reactor. Needs a JDK,
# cmake, git and cargo with wasm32-wasip1. WAMR is fetched at the pinned commit
# (scripts/plugin-wamr-source.sh). Usage: scripts/plugin-android-wamr.sh [build-dir]
set -euo pipefail
cd "$(dirname "$0")/.."
command -v javac >/dev/null 2>&1 || { echo 'plugin-android-wamr.sh needs a JDK (javac)' >&2; exit 1; }
build_dir="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-java-wamr.XXXXXX")}"
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
sdk=pkg/plugin-android
wamr="$(scripts/plugin-wamr-source.sh)"

CARGO_TARGET_DIR="$build_dir/cargo" cargo build --release --locked \
  --manifest-path pkg/plugin-rust/Cargo.toml --target wasm32-wasip1 --example shared
cmake -S "$sdk" -B "$build_dir/native" -DCMAKE_BUILD_TYPE=Release -DEXT_WAMR_ROOT="$wamr" >/dev/null
cmake --build "$build_dir/native" --target extwamrjni -j 4 >/dev/null
mkdir -p "$build_dir/classes"
javac -Xlint:all -Werror -d "$build_dir/classes" \
  "$sdk"/src/main/java/io/github/webong/ext/plugin/*.java \
  "$sdk"/src/test/java/io/github/webong/ext/plugin/WamrPluginHostTests.java
java -ea -Djava.library.path="$build_dir/native" -cp "$build_dir/classes" \
  io.github.webong.ext.plugin.WamrPluginHostTests "$build_dir/cargo/wasm32-wasip1/release/examples/shared.wasm"
