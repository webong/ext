#!/usr/bin/env bash
# Build the canonical C engine from this CLI release's commit, never an unpinned
# downloaded engine. The common release workflow uploads release-out/.
. "$(dirname "$0")/../../ci/jobs/lib.sh"
(
version=$(sed -n 's/^project(ext_plugin VERSION \([0-9.]*\) .*/\1/p' lib/plugin/CMakeLists.txt)
platform=${MATRIX_PLATFORM:?native platform required}
build="$RUNNER_TEMP/ext-cli-engine"
bash scripts/ext-plugin-release.sh "$version" "$platform" "$build"
stage="$build/stage/ext-plugin-$version-$platform"
case "$platform" in
  macos-universal) library="$stage/lib/libext_plugin_host.dylib"; targets="darwin/amd64 darwin/arm64" ;;
  linux-x86_64) library="$stage/lib/libext_plugin_host.so"; targets="linux/amd64" ;;
  linux-aarch64) library="$stage/lib/libext_plugin_host.so"; targets="linux/arm64" ;;
  windows-x86_64) library="$stage/bin/ext_plugin_host.dll"; targets="windows/amd64" ;;
  windows-aarch64) library="$stage/bin/ext_plugin_host.dll"; targets="windows/arm64" ;;
  *) echo "unsupported CLI engine platform $platform" >&2; exit 1 ;;
esac
ledger_version=$(sed -n 's/^project(ext_ledger VERSION \([0-9.]*\) .*/\1/p' lib/ledger/CMakeLists.txt)
bash scripts/ext-ledger-release.sh "$ledger_version" "$platform" "$build/ledger-release"
ledger_stage="$build/ledger-release/stage/ext-ledger-$ledger_version-$platform"
case "$platform" in
  macos-universal) ledger="$ledger_stage/lib/libext_ledger.0.dylib" ;;
  linux-*) ledger="$ledger_stage/lib/libext_ledger.so.0" ;;
  windows-*) ledger="$ledger_stage/bin/ext_ledger.dll" ;;
esac
python=$(command -v python3 || command -v python)
for target in $targets; do
  "$python" scripts/stage-cli-engine.py stage "$library" "$target" "release-out/native/$target" --ledger "$ledger"
done
case "$platform" in
  linux-x86_64) guest_arch=amd64 ;;
  linux-aarch64) guest_arch=arm64 ;;
  *) guest_arch= ;;
esac
if [ -n "$guest_arch" ]; then
  (cd pkg/ext-linux/net/guest && CGO_ENABLED=1 go build -o "$build/network-linux" .)
  package="release-out/system.network.linux-$guest_arch"
  "$python" scripts/release/pack-resource-guest.py \
    pkg/ext-linux/net/guest/ext.json "$build/network-linux" "$package" linux "$guest_arch"
  rm -rf "$package"
fi
)
ci_import_env
