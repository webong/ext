#!/usr/bin/env bash
# native/swift: the iOS builds and Swift tests of native libraries, and the CoreML adapter (from xallet/tab, when EXT_ADAPTERS is given) through CTN.
# (Split out of native/native; the steps are unchanged.)
. "$(dirname "$0")/../lib.sh"

# Generic iOS ExtSystem build
if [ "$RUNNER_OS" = macOS ]; then
(
cd pkg/ext-darwin/act/capture/surface
xcodebuild -scheme ExtSystem -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
)
ci_import_env
fi

# Generic iOS ExtHost, ExtShare and ExtAudio builds
if [ "$RUNNER_OS" = macOS ]; then
(
scripts/ext-darwin-xcframework.sh --platform all
scripts/ext-darwin-ledger-xcframework.sh --platform all
for package in act/share:ExtShare act/capture/audio:ExtAudio; do
  (cd "pkg/ext-darwin/${package%%:*}" && xcodebuild -scheme "${package##*:}" -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build)
done
(cd pkg/ext-darwin && xcodebuild -scheme Ext-Package -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build)
)
ci_import_env
fi

# Generic iOS ExtNetwork build
if [ "$RUNNER_OS" = macOS ]; then
(
cd pkg/ext-darwin/net
xcodebuild -scheme ExtNetwork -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
)
ci_import_env
fi

# Swift tests (ExtSystem)
if [ "$RUNNER_OS" = macOS ]; then
(
cd pkg/ext-darwin/act/capture/surface
swift test
)
ci_import_env
fi

# Swift tests (ExtHost, ExtShare and ExtAudio)
if [ "$RUNNER_OS" = macOS ]; then
(
for package in act/share act/capture/audio; do
  swift test --package-path "pkg/ext-darwin/$package"
done
swift test --package-path pkg/ext-darwin --filter ExtHostTests
swift test --package-path pkg/ext-darwin/act/clipboard
)
ci_import_env
fi

# Swift tests (ExtNetwork)
if [ "$RUNNER_OS" = macOS ]; then
(
cd pkg/ext-darwin/net
swift test
)
ci_import_env
fi

# Swift tests (ExtML)
if [ "$RUNNER_OS" = macOS ]; then
(
cd pkg/ext-darwin/int/model
swift test
)
ci_import_env
fi

# Validate native CoreML guest through CTN (when an adapter repository's workflow supplies EXT_ADAPTERS)
if [ "$RUNNER_OS" = macOS ] && [ -n "${EXT_ADAPTERS:-}" ]; then
(
guest="$(mktemp -d "$RUNNER_TEMP/coreml-adapter.XXXXXX")"
cp "$EXT_ADAPTERS/coreml/Package.swift" "$EXT_ADAPTERS/coreml/Package.resolved" "$guest/"
cp -RL "$EXT_ADAPTERS/coreml/Sources" "$guest/"
swift build --product ext-coreml-guest --package-path "$guest"
EXT_COREML_GUEST_BINARY="$guest/.build/debug/ext-coreml-guest" go test ./src/ctn/internal/app -run '^TestModelRunThroughCoreMLGuest$' -count=1
cmake -S lib/plugin -B "$RUNNER_TEMP/ext-model-cengine" -DCMAKE_BUILD_TYPE=Release
cmake --build "$RUNNER_TEMP/ext-model-cengine" --target ext_plugin_host_static -j 4
EXT_COREML_GUEST_BINARY="$guest/.build/debug/ext-coreml-guest" CGO_LDFLAGS="-L$RUNNER_TEMP/ext-model-cengine" CGO_CFLAGS="-I$PWD/lib/plugin/include" go test -tags ext_cengine ./src/ctn/internal/app -run '^TestModelRunThroughCoreMLGuest$' -count=1
)
ci_import_env
fi
