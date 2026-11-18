#!/usr/bin/env bash
# plugin-sdk/foreign-sdks: drafted from .github/workflows/plugin-sdk.yml (job foreign-sdks).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-java@v5)

# (action actions/setup-go@v6)

# (action dtolnay/rust-toolchain@stable)

# Install Zig 0.17.0 with verified checksum
(
if [[ "$(uname -s)-$(uname -m)" == Darwin-arm64 ]]; then
  archive=zig-aarch64-macos-0.17.0
  digest=b607e9b9234790a008116ae5bdb71c6243b84b9fb42a53a9e70fde41c06c536a
elif [[ "$(uname -s)" == Darwin ]]; then
  archive=zig-x86_64-macos-0.17.0
  digest=4f9a1c5269aa17ebda5e6d3c2b89d6cbf36f7d2b22a0306e9ab98f25f95529c6
else
  archive=zig-x86_64-linux-0.17.0
  digest=1cbe9df9f27e6b78d14ccbca43b6703a404ef79ef1c463de901d7f088d4e2026
fi
curl -fsSL "https://ziglang.org/download/0.17.0/$archive.tar.xz" -o "$RUNNER_TEMP/zig.tar.xz"
actual=$(shasum -a 256 "$RUNNER_TEMP/zig.tar.xz" | cut -d ' ' -f 1)
test "$actual" = "$digest"
tar -xf "$RUNNER_TEMP/zig.tar.xz" -C "$RUNNER_TEMP"
echo "ZIG_BIN=$RUNNER_TEMP/$archive/zig" >> "$GITHUB_ENV"
)
ci_import_env

# (action actions/setup-node@v5)

# Rust and Zig host/guest conformance
(
scripts/plugin-crosslang.sh
)
ci_import_env

# Shared C engine conformance
(
export CC="clang"
export EXT_CENGINE_BUILD_DIR="$RUNNER_TEMP/ext-cengine-proof"
export EXT_CENGINE_LINKAGE="${MATRIX_LINKAGE:-}"
scripts/plugin-cengine.sh
)
ci_import_env

# Portable guest core in WASI
(
export EXT_WASI_CORE_BUILD_DIR="$RUNNER_TEMP/ext-wasi-core"
scripts/plugin-wasi-core.sh
)
ci_import_env

# Foreign runtime conformance through the C session
(
export EXT_CENGINE_BUILD_DIR="$RUNNER_TEMP/ext-cengine-proof"
export EXT_CENGINE_LINKAGE="${MATRIX_LINKAGE:-}"
export EXT_CROSSLANG_ENGINE="c"
scripts/plugin-crosslang.sh
)
ci_import_env

# Preserve prototype measurements: ci.yml uploads ci-artifacts/ (the cell's artifact) at the end.
(
mkdir -p ci-artifacts
for f in benchmarks.jsonl benchmark-report.md; do
  [ -f "$RUNNER_TEMP/ext-cengine-proof/$f" ] && cp "$RUNNER_TEMP/ext-cengine-proof/$f" ci-artifacts/
done
true
)

# Cross-runtime interoperability
(
export EXT_CENGINE_BUILD_DIR="$RUNNER_TEMP/ext-cengine-proof"
export EXT_INTEROP_BUILD_DIR="$RUNNER_TEMP/ext-interop-proof"
export EXT_INTEROP_REUSE_CENGINE="1"
scripts/plugin-interop.sh
)
ci_import_env

# Bidirectional bridge with C-owned sessions
(
export EXT_CENGINE_BUILD_DIR="$RUNNER_TEMP/ext-cengine-proof"
export EXT_CENGINE_LINKAGE="${MATRIX_LINKAGE:-}"
export EXT_INTEROP_BUILD_DIR="$RUNNER_TEMP/ext-interop-c"
export EXT_INTEROP_ENGINE="c"
export EXT_INTEROP_REUSE_CENGINE="1"
scripts/plugin-interop.sh
)
ci_import_env
