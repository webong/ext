#!/usr/bin/env bash
# ext-rust release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# (action dtolnay/rust-toolchain@stable)

# Version
(
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
declared=$(cargo metadata --no-deps --format-version 1 --manifest-path pkg/ext-rust/Cargo.toml |
  python3 -c 'import json, sys; print(next(p["version"] for p in json.load(sys.stdin)["packages"] if p["name"] == "ext-io"))')
if [[ "$REF_TYPE" == tag ]]; then version="${REF_NAME#ext-rust-v}"; else version="$declared"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
[[ "$declared" == "$version" ]] || { echo "pkg/ext-rust/Cargo.toml says $declared but the release is $version" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Test
(
cargo fmt --manifest-path pkg/ext-rust/Cargo.toml -- --check
cargo clippy --locked --manifest-path pkg/ext-rust/Cargo.toml --all-targets -- -D warnings
cargo test --locked --manifest-path pkg/ext-rust/Cargo.toml
)
ci_import_env

# Package and dry-run the publish
(
set -euo pipefail
cargo package --locked --manifest-path pkg/ext-rust/Cargo.toml --list
cargo package --locked --manifest-path pkg/ext-rust/Cargo.toml
cargo publish --dry-run --locked --manifest-path pkg/ext-rust/Cargo.toml
)
ci_import_env

# Checksums
(
export VERSION="${VERSION}"
set -euo pipefail
out="dist/ext-rust-$VERSION"
mkdir -p "$out"
target=$(cargo metadata --no-deps --format-version 1 --manifest-path pkg/ext-rust/Cargo.toml |
  python3 -c 'import json, sys; print(json.load(sys.stdin)["target_directory"])')
crate=$(find "$target/package" -maxdepth 1 -name "ext-io-$VERSION.crate" -print -quit)
[[ -n "$crate" ]] || { echo "no ext-io-$VERSION.crate under $target/package" >&2; ls -la "$target/package" >&2 || true; exit 1; }
cp "$crate" "$out/"
(cd "$out" && sha256sum ./*.crate | tee checksums.txt)
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-rust-${VERSION}/*; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
