# Sourced by scripts/release/ext-ios/*.sh and scripts/release/ext-macos/*.sh: the steps the two
# Apple releases share. Both are built from the same sources (pkg/ext-darwin and the source links it
# holds) by scripts/ext-darwin-release.sh, which stages one platform's slices and manifest; ext-ios
# ships iOS and the iOS Simulator, ext-macos ships macOS. Each step runs in a subshell and is
# followed by ci_import_env, as the steps of a job script are (scripts/ci/jobs/lib.sh).
. "$(dirname "${BASH_SOURCE[0]}")/../ci/jobs/lib.sh"

# The version: the tag's, after the package's prefix, or the manual run's input. Sets VERSION.
apple_version() {
  local package="$1"
  (
    if [[ "${GITHUB_REF_TYPE:-}" == tag ]]; then version="${GITHUB_REF_NAME#"$package"-v}"; else version="${INPUT_VERSION_DISPATCH:-}"; fi
    [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
    echo "VERSION=$version" >>"$GITHUB_ENV"
  )
  ci_import_env
}

# Stage the release of one platform (ios or macos) under dist/ext-<platform>-<version>, with the
# WAMR host. It leaves that platform's xcframeworks in pkg/ext-darwin/Frameworks for the tests.
apple_stage() {
  local platform="$1"
  ( scripts/ext-darwin-release.sh --platform "$platform" --with-wamr "$VERSION" )
  ci_import_env
}

# Build the reactor the host tests run. Sets EXT_RUST_REACTOR.
apple_reactor() {
  (
    CARGO_TARGET_DIR="$RUNNER_TEMP/cargo" cargo build --release --locked \
      --manifest-path pkg/ext-rust/Cargo.toml --target wasm32-wasip1 --example shared
    echo "EXT_RUST_REACTOR=$RUNNER_TEMP/cargo/wasm32-wasip1/release/examples/shared.wasm" >>"$GITHUB_ENV"
  )
  ci_import_env
}

# The first available iPhone simulator's id.
apple_simulator() {
  local device
  device="$(xcrun simctl list devices available | grep -m1 -E 'iPhone' | sed -E 's/^ *(.*) \(([0-9A-F-]{36})\).*/\2/')"
  [[ -n "$device" ]] || { echo 'no iPhone simulator is installed' >&2; return 1; }
  echo "$device"
}

# Checksum the staged release and keep what it ships: the workflow uploads release-out/ for the
# publish job.
apple_ship() {
  local package="$1"
  (
    set -euo pipefail
    cd "dist/$package-$VERSION"
    tar -czf "$package-$VERSION-package.tar.gz" -C package .
    shasum -a 256 *.zip *.tar.gz WAMR-LICENSE | tee checksums.txt
  )
  ci_import_env
  (
    mkdir -p release-out
    shopt -s nullglob
    found=0
    for f in "dist/$package-$VERSION"/*.zip "dist/$package-$VERSION"/*.tar.gz \
      "dist/$package-$VERSION"/WAMR-LICENSE "dist/$package-$VERSION"/checksums.txt; do
      cp -R "$f" release-out/; found=1
    done
    [ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
  )
}

# Push the staged package to its dist/ branch, tagged as SwiftPM or a pin will name it.
apple_distribute() {
  local package="$1" tag="$2"
  (
    scripts/dist-branch.sh "dist/$package" "$tag" "dist/$package-$VERSION-package.tar.gz" \
      "$package $VERSION" \
      "Staged by $GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID from $GITHUB_REF_NAME."
  )
  ci_import_env
}
