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

cargo fmt --manifest-path pkg/ext-rust/Cargo.toml -- --check
cargo clippy --locked --manifest-path pkg/ext-rust/Cargo.toml --all-targets -- -D warnings
cargo test --locked --manifest-path pkg/ext-rust/Cargo.toml
cargo build --locked --manifest-path pkg/ext-rust/Cargo.toml --examples
# Keep DWARF stack-trace decoding out of bounded WASI cancellation checks.
cargo build --release --locked --manifest-path pkg/ext-rust/Cargo.toml --target wasm32-wasip1 --example guest
# The frames guest (progress events and reverse calls) as a WASI command: no threads.
cargo build --release --locked --manifest-path pkg/ext-rust/Cargo.toml --target wasm32-wasip1 --example frames
# The shared example is the same guest as a WebAssembly reactor: the C ABI plus
# ext_plugin_alloc/ext_plugin_free (docs/plugin-reactor-abi.md).
cargo build --release --locked --manifest-path pkg/ext-rust/Cargo.toml --target wasm32-wasip1 --example shared
"$ZIG_BIN" build test --build-file pkg/ext-zig/build.zig --prefix "$build_dir/zig"
"$ZIG_BIN" build --build-file pkg/ext-zig/build.zig --prefix "$build_dir/zig"
# Optimized: a Debug module is slow enough to compile that it exhausts the harness's 3 s session timeout.
"$ZIG_BIN" build --build-file pkg/ext-zig/build.zig -Dtarget=wasm32-wasi -Doptimize=ReleaseSafe --prefix "$build_dir/wasi"

case "$(uname -s)" in
  Darwin) extension=dylib ;;
  Linux) extension=so ;;
  *) echo 'The cross-language native runner currently supports Linux and macOS.' >&2; exit 1 ;;
esac
go build -o "$build_dir/go-guest" ./pkg/ext-go/crosslang/testdata/go-guest
# The reference guest of the frames suite (progress events and reverse calls); a
# language's guest joins it by setting EXT_<LANGUAGE>_FRAMES_GUEST.
go build -o "$build_dir/go-frames-guest" ./pkg/ext-go/crosslang/testdata/frames-guest
export EXT_GO_FRAMES_GUEST="$build_dir/go-frames-guest"
export EXT_RUST_FRAMES_GUEST="$CARGO_TARGET_DIR/debug/examples/frames"
export EXT_RUST_INLINE_FRAMES_GUEST="$CARGO_TARGET_DIR/debug/examples/frames_inline"
export EXT_RUST_FRAMES_WASM="$CARGO_TARGET_DIR/wasm32-wasip1/release/examples/frames.wasm"
export EXT_FRAMES_REQUIRED="GO RUST RUST_INLINE RUST_WASI"
# TypeScript runs on any host with Node 22 (the engine starts a guest with an empty
# environment, so the launcher names node and the script by absolute path).
if command -v node >/dev/null 2>&1; then
  cat >"$build_dir/ext-ts-frames-guest" <<LAUNCHER
#!/bin/sh
exec "$(command -v node)" "$PWD/pkg/ext-ts/examples/frames/guest.mjs"
LAUNCHER
  chmod +x "$build_dir/ext-ts-frames-guest"
  export EXT_TS_FRAMES_GUEST="$build_dir/ext-ts-frames-guest"
  EXT_FRAMES_REQUIRED="$EXT_FRAMES_REQUIRED TS"
fi
go build -buildmode=c-shared -o "$build_dir/go-shared.$extension" ./pkg/ext-go/crosslang/testdata/go-shared
export EXT_GO_GUEST="$build_dir/go-guest" EXT_GO_SHARED="$build_dir/go-shared.$extension"
export EXT_RUST_GUEST="$CARGO_TARGET_DIR/debug/examples/guest" EXT_RUST_HOST="$CARGO_TARGET_DIR/debug/examples/host"
export EXT_RUST_SHARED="$CARGO_TARGET_DIR/debug/examples/libshared.$extension"
export EXT_RUST_WASM="$CARGO_TARGET_DIR/wasm32-wasip1/release/examples/guest.wasm"
export EXT_RUST_REACTOR="$CARGO_TARGET_DIR/wasm32-wasip1/release/examples/shared.wasm" EXT_REACTOR_REQUIRED=1
export EXT_ZIG_GUEST="$build_dir/zig/bin/ext-zig-guest" EXT_ZIG_HOST="$build_dir/zig/bin/ext-zig-host"
export EXT_ZIG_SHARED="$build_dir/zig/lib/libext-zig-guest.$extension"
export EXT_ZIG_WASM="$build_dir/wasi/bin/ext-zig-guest.wasm"
# The Zig frames guest reads inline, natively and as a WASI command (no threads).
export EXT_ZIG_FRAMES_GUEST="$build_dir/zig/bin/ext-zig-frames"
export EXT_ZIG_FRAMES_WASM="$build_dir/wasi/bin/ext-zig-frames.wasm"
EXT_FRAMES_REQUIRED="$EXT_FRAMES_REQUIRED ZIG ZIG_WASI"
# The Zig capability guest (ext.health, ext.configuration, ext.stream) is called with the Go
# library's own methods and stream client, natively and as a WASI command; the Zig capability host
# calls the same contracts of a guest the Go library serves.
export EXT_ZIG_CAPABILITY_GUEST="$build_dir/zig/bin/ext-zig-capability"
export EXT_ZIG_CAPABILITY_WASM="$build_dir/wasi/bin/ext-zig-capability.wasm"
export EXT_ZIG_CAPABILITY_HOST="$build_dir/zig/bin/ext-zig-capability-host"
export EXT_CAPABILITY_REQUIRED="ZIG ZIG_WASI"
# The mobile hosts embed the page glue and the page shim; fail if a copy is stale.
scripts/plugin-mobile-assets.sh --check
# Swift builds on Apple platforms only. Its package tests run on the host, then
# the dynamic fixture is checked through the same Go cshared suite as the others.
if [[ "$(uname -s)" == Darwin ]]; then
  scripts/ext-ios-xcframework.sh
  swift test --package-path pkg/ext-ios --scratch-path "$build_dir/swift"
  swift build --package-path pkg/ext-ios --scratch-path "$build_dir/swift" --product ExtConformancePlugin
  scripts/ext-ios-objc.sh
  export EXT_SWIFT_SHARED="$build_dir/swift/debug/libExtConformancePlugin.dylib" EXT_SWIFT_REQUIRED=1
  # The frames guest (progress events and reverse calls) is a JSON-line process, so it joins the
  # frames suite beside the guests of the other languages.
  swift build --package-path pkg/ext-ios --scratch-path "$build_dir/swift" --product ExtFramesGuest
  export EXT_SWIFT_FRAMES_GUEST="$build_dir/swift/debug/ExtFramesGuest"
  EXT_FRAMES_REQUIRED="$EXT_FRAMES_REQUIRED SWIFT"
fi
# Java runs over JNI on any host with a JDK. Its guest is a JSON-line process.
if command -v javac >/dev/null 2>&1; then
  scripts/ext-android.sh "$build_dir/java"
  export EXT_JAVA_GUEST="$build_dir/java/ext-java-guest" EXT_JAVA_REQUIRED=1
  export EXT_JAVA_FRAMES_GUEST="$build_dir/java/ext-java-frames-guest"
  EXT_FRAMES_REQUIRED="$EXT_FRAMES_REQUIRED JAVA"
fi
# Python needs only python3. Its frames guest reads inline (no reverse call but one at a time).
# The launcher names the interpreter by absolute path, since the engine starts a guest with
# an empty environment.
if command -v python3 >/dev/null 2>&1; then
  cat >"$build_dir/ext-python-frames-guest" <<LAUNCHER
#!/bin/sh
exec "$(python3 -c 'import sys; print(sys.executable)')" -I "$PWD/pkg/ext-python/tests/frames_guest.py"
LAUNCHER
  chmod +x "$build_dir/ext-python-frames-guest"
  export EXT_PYTHON_FRAMES_GUEST="$build_dir/ext-python-frames-guest"
  EXT_FRAMES_REQUIRED="$EXT_FRAMES_REQUIRED PYTHON"
fi
export EXT_CROSSLANG_REQUIRED=1
go build -race -buildmode=plugin -o "$build_dir/go-native.so" ./pkg/ext-go/crosslang/testdata/go-native
export EXT_GO_NATIVE="$build_dir/go-native.so"
go test "${go_tags[@]}" -race -count=1 -timeout=180s -v ./pkg/ext-go/crosslang
