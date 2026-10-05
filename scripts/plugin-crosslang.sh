#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# CI supplies an exact Zig version. Artifacts/caches stay outside the checkout.
ZIG_BIN="${ZIG_BIN:-zig}"
if [[ "$("$ZIG_BIN" version)" != "0.17.0" ]]; then
  echo 'The Zig SDK currently targets Zig 0.17.0.' >&2
  exit 1
fi
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ctx-crosslang.XXXXXX")"
trap 'rm -rf "$build_dir"' EXIT
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$build_dir/rust}"
export ZIG_GLOBAL_CACHE_DIR="${ZIG_GLOBAL_CACHE_DIR:-$build_dir/zig-global}"
export ZIG_LOCAL_CACHE_DIR="${ZIG_LOCAL_CACHE_DIR:-$build_dir/zig-cache}"

go_tags=(-tags "")
case "${CTX_CROSSLANG_ENGINE:-go}" in
  go) ;;
  c)
    : "${CTX_CENGINE_BUILD_DIR:?set CTX_CENGINE_BUILD_DIR to the native C runner directory}"
    go_tags=(-tags ctx_cengine)
    if [[ "${CTX_CENGINE_LINKAGE:-static}" == shared ]]; then go_tags=(-tags ctx_cengine,ctx_cengine_shared); fi
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$CTX_CENGINE_BUILD_DIR/release -Wl,-rpath,$CTX_CENGINE_BUILD_DIR/release"
    ;;
  *) echo 'CTX_CROSSLANG_ENGINE must be go or c' >&2; exit 1 ;;
esac

cargo fmt --manifest-path pkg/rust/Cargo.toml -- --check
cargo clippy --locked --manifest-path pkg/rust/Cargo.toml --all-targets -- -D warnings
cargo test --locked --manifest-path pkg/rust/Cargo.toml
cargo build --locked --manifest-path pkg/rust/Cargo.toml --examples
# Keep DWARF stack-trace decoding out of bounded WASI cancellation checks.
cargo build --release --locked --manifest-path pkg/rust/Cargo.toml --target wasm32-wasip1 --example guest
"$ZIG_BIN" build test --build-file pkg/zig/build.zig --prefix "$build_dir/zig"
"$ZIG_BIN" build --build-file pkg/zig/build.zig --prefix "$build_dir/zig"
"$ZIG_BIN" build --build-file pkg/zig/build.zig -Dtarget=wasm32-wasi --prefix "$build_dir/wasi"

case "$(uname -s)" in
  Darwin) extension=dylib ;;
  Linux) extension=so ;;
  *) echo 'The cross-language native runner currently supports Linux and macOS.' >&2; exit 1 ;;
esac
go build -o "$build_dir/go-guest" ./pkg/plugin/wasm/crosslang/testdata/go-guest
go build -buildmode=c-shared -o "$build_dir/go-shared.$extension" ./pkg/plugin/wasm/crosslang/testdata/go-shared
export CTX_GO_GUEST="$build_dir/go-guest" CTX_GO_SHARED="$build_dir/go-shared.$extension"
export CTX_RUST_GUEST="$CARGO_TARGET_DIR/debug/examples/guest" CTX_RUST_HOST="$CARGO_TARGET_DIR/debug/examples/host"
export CTX_RUST_SHARED="$CARGO_TARGET_DIR/debug/examples/libshared.$extension"
export CTX_RUST_WASM="$CARGO_TARGET_DIR/wasm32-wasip1/release/examples/guest.wasm"
export CTX_ZIG_GUEST="$build_dir/zig/bin/ctx-zig-guest" CTX_ZIG_HOST="$build_dir/zig/bin/ctx-zig-host"
export CTX_ZIG_SHARED="$build_dir/zig/lib/libctx-zig-guest.$extension"
export CTX_ZIG_WASM="$build_dir/wasi/bin/ctx-zig-guest.wasm"
export CTX_CROSSLANG_REQUIRED=1
go build -race -buildmode=plugin -o "$build_dir/go-native.so" ./pkg/plugin/wasm/crosslang/testdata/go-native
export CTX_GO_NATIVE="$build_dir/go-native.so"
go test "${go_tags[@]}" -race -count=1 -timeout=180s -v ./pkg/plugin/wasm/crosslang
