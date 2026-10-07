#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# CI supplies an exact Zig version. Artifacts/caches stay outside the checkout.
ZIG_BIN="${ZIG_BIN:-zig}"
if [[ "$("$ZIG_BIN" version)" != "0.17.0" ]]; then
  echo 'The Zig SDK currently targets Zig 0.17.0.' >&2
  exit 1
fi
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-crosslang.XXXXXX")"
trap 'rm -rf "$build_dir"' EXIT
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$build_dir/rust}"
export ZIG_GLOBAL_CACHE_DIR="${ZIG_GLOBAL_CACHE_DIR:-$build_dir/zig-global}"
export ZIG_LOCAL_CACHE_DIR="${ZIG_LOCAL_CACHE_DIR:-$build_dir/zig-cache}"

go_tags=(-tags "")
case "${EXT_CROSSLANG_ENGINE:-go}" in
  go) ;;
  c)
    : "${EXT_CENGINE_BUILD_DIR:?set EXT_CENGINE_BUILD_DIR to the native C runner directory}"
    go_tags=(-tags ext_cengine)
    if [[ "${EXT_CENGINE_LINKAGE:-static}" == shared ]]; then go_tags=(-tags ext_cengine,ext_cengine_shared); fi
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$EXT_CENGINE_BUILD_DIR/release -Wl,-rpath,$EXT_CENGINE_BUILD_DIR/release"
    ;;
  *) echo 'EXT_CROSSLANG_ENGINE must be go or c' >&2; exit 1 ;;
esac

cargo fmt --manifest-path pkg/plugin-rust/Cargo.toml -- --check
cargo clippy --locked --manifest-path pkg/plugin-rust/Cargo.toml --all-targets -- -D warnings
cargo test --locked --manifest-path pkg/plugin-rust/Cargo.toml
cargo build --locked --manifest-path pkg/plugin-rust/Cargo.toml --examples
# Keep DWARF stack-trace decoding out of bounded WASI cancellation checks.
cargo build --release --locked --manifest-path pkg/plugin-rust/Cargo.toml --target wasm32-wasip1 --example guest
"$ZIG_BIN" build test --build-file pkg/plugin-zig/build.zig --prefix "$build_dir/zig"
"$ZIG_BIN" build --build-file pkg/plugin-zig/build.zig --prefix "$build_dir/zig"
"$ZIG_BIN" build --build-file pkg/plugin-zig/build.zig -Dtarget=wasm32-wasi --prefix "$build_dir/wasi"

case "$(uname -s)" in
  Darwin) extension=dylib ;;
  Linux) extension=so ;;
  *) echo 'The cross-language native runner currently supports Linux and macOS.' >&2; exit 1 ;;
esac
go build -o "$build_dir/go-guest" ./pkg/plugin-wasm/crosslang/testdata/go-guest
go build -buildmode=c-shared -o "$build_dir/go-shared.$extension" ./pkg/plugin-wasm/crosslang/testdata/go-shared
export EXT_GO_GUEST="$build_dir/go-guest" EXT_GO_SHARED="$build_dir/go-shared.$extension"
export EXT_RUST_GUEST="$CARGO_TARGET_DIR/debug/examples/guest" EXT_RUST_HOST="$CARGO_TARGET_DIR/debug/examples/host"
export EXT_RUST_SHARED="$CARGO_TARGET_DIR/debug/examples/libshared.$extension"
export EXT_RUST_WASM="$CARGO_TARGET_DIR/wasm32-wasip1/release/examples/guest.wasm"
export EXT_ZIG_GUEST="$build_dir/zig/bin/ext-zig-guest" EXT_ZIG_HOST="$build_dir/zig/bin/ext-zig-host"
export EXT_ZIG_SHARED="$build_dir/zig/lib/libext-zig-guest.$extension"
export EXT_ZIG_WASM="$build_dir/wasi/bin/ext-zig-guest.wasm"
export EXT_CROSSLANG_REQUIRED=1
go build -race -buildmode=plugin -o "$build_dir/go-native.so" ./pkg/plugin-wasm/crosslang/testdata/go-native
export EXT_GO_NATIVE="$build_dir/go-native.so"
go test "${go_tags[@]}" -race -count=1 -timeout=180s -v ./pkg/plugin-wasm/crosslang
