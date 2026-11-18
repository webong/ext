#!/usr/bin/env bash
# plugin-wamr/apple: drafted from .github/workflows/plugin-wamr.yml (job apple).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action dtolnay/rust-toolchain@stable)

# (action actions/cache@v5)

# macOS (Swift tests on the host)
(
scripts/ext-darwin-wamr.sh
)
ci_import_env

# iOS Simulator
(
set -euo pipefail
reactor="$RUNNER_TEMP/reactor.wasm"
CARGO_TARGET_DIR="$RUNNER_TEMP/cargo" cargo build --release --locked \
  --manifest-path pkg/ext-rust/Cargo.toml --target wasm32-wasip1 --example shared
cp "$RUNNER_TEMP/cargo/wasm32-wasip1/release/examples/shared.wasm" "$reactor"
# The frameworks were built by the step above. Pick an installed iPhone simulator.
device="$(xcrun simctl list devices available | grep -m1 -E 'iPhone' | sed -E 's/^ *(.*) \(([0-9A-F-]{36})\).*/\2/')"
[[ -n "$device" ]] || { echo 'no iPhone simulator is installed' >&2; exit 1; }
cd pkg/ext-darwin
TEST_RUNNER_EXT_RUST_REACTOR="$reactor" xcodebuild test -scheme Ext-Package \
  -destination "platform=iOS Simulator,id=$device" -only-testing:ExtPluginWamrTests
)
ci_import_env
