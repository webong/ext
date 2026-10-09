#!/usr/bin/env bash
# Build the optional WAMR host for pkg/plugin-ios and run its tests on macOS against the
# Rust SDK's conformance reactor. Needs Xcode, cmake, git and cargo with wasm32-wasip1.
# WAMR is fetched at the pinned commit (scripts/plugin-wamr-source.sh).
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo 'plugin-ios-wamr.sh needs macOS and Xcode' >&2; exit 1; }
scripts/plugin-ios-xcframework.sh >/dev/null
scripts/plugin-ios-wamr-xcframework.sh >/dev/null
work="$(mktemp -d "${TMPDIR:-/tmp}/ext-ios-wamr.XXXXXX")"
trap 'rm -rf "$work"' EXIT
CARGO_TARGET_DIR="$work/cargo" cargo build --release --locked \
  --manifest-path pkg/plugin-rust/Cargo.toml --target wasm32-wasip1 --example shared
EXT_RUST_REACTOR="$work/cargo/wasm32-wasip1/release/examples/shared.wasm" \
  swift test --package-path pkg/plugin-ios --filter ExtPluginWamrTests
