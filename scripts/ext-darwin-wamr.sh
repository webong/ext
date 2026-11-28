#!/usr/bin/env bash
# Build the optional WAMR host for pkg/ext-darwin and run its tests on macOS against the
# Rust SDK's conformance reactor. Needs Xcode, cmake, git and cargo with wasm32-wasip1.
# WAMR is fetched at the pinned commit (scripts/plugin-wamr-source.sh).
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo 'ext-darwin-wamr.sh needs macOS and Xcode' >&2; exit 1; }
scripts/ext-darwin-xcframework.sh >/dev/null
scripts/ext-darwin-ledger-xcframework.sh >/dev/null
scripts/ext-darwin-wamr-xcframework.sh >/dev/null
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-ios-wamr.XXXXXX")"
trap 'rm -rf "$work"' EXIT
CARGO_TARGET_DIR="$work/cargo" cargo build --release --locked \
  --manifest-path pkg/ext-rust/Cargo.toml --target wasm32-wasip1 --example shared --example reactor_reverse
EXT_RUST_REACTOR="$work/cargo/wasm32-wasip1/release/examples/shared.wasm" \
EXT_RUST_REVERSE_REACTOR="$work/cargo/wasm32-wasip1/release/examples/reactor_reverse.wasm" \
  swift test --package-path pkg/ext-darwin --filter ExtPluginWamrTests
