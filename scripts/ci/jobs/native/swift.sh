#!/usr/bin/env bash
# native/swift: the iOS builds and Swift tests of res/system and res/int, and the CoreML guest through CTN.
# (Split out of native/native; the steps are unchanged.)
. "$(dirname "$0")/../lib.sh"

# Generic iOS ExtSystem build
if [ "$RUNNER_OS" = macOS ]; then
(
cd lib/system/act/capture/surface/ios
xcodebuild -scheme ExtSystem -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
)
ci_import_env
fi

# Generic iOS ExtHost, ExtShare and ExtAudio builds
if [ "$RUNNER_OS" = macOS ]; then
(
scripts/ext-darwin-xcframework.sh --platform all
scripts/ext-darwin-ledger-xcframework.sh --platform all
for package in capture/share/ios:ExtShare capture/audio/ios:ExtAudio; do
  (cd "lib/system/${package%%:*}" && xcodebuild -scheme "${package##*:}" -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build)
done
(cd pkg/ext-darwin && xcodebuild -scheme Ext-Package -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build)
)
ci_import_env
fi

# Generic iOS ExtNetwork build
if [ "$RUNNER_OS" = macOS ]; then
(
cd lib/system/net/ios
xcodebuild -scheme ExtNetwork -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
)
ci_import_env
fi

# Swift tests (ExtSystem)
if [ "$RUNNER_OS" = macOS ]; then
(
cd lib/system/act/capture/surface/ios
swift test
)
ci_import_env
fi

# Swift tests (ExtHost, ExtShare and ExtAudio)
if [ "$RUNNER_OS" = macOS ]; then
(
for package in capture/share/ios capture/audio/ios; do
  swift test --package-path "lib/system/$package"
done
swift test --package-path pkg/ext-darwin --filter ExtHostTests
swift test --package-path lib/system/act/clipboard/ios
)
ci_import_env
fi

# Swift tests (ExtNetwork)
if [ "$RUNNER_OS" = macOS ]; then
(
cd lib/system/net/ios
swift test
)
ci_import_env
fi

# Swift tests (ExtML)
if [ "$RUNNER_OS" = macOS ]; then
(
cd lib/system/int/model/ios
swift test
)
ci_import_env
fi

# Validate native CoreML guest through CTN
if [ "$RUNNER_OS" = macOS ]; then
(
swift build --product ext-coreml-guest --package-path res/int/model/coreml-guest
EXT_COREML_GUEST_BINARY="$PWD/res/int/model/coreml-guest/.build/debug/ext-coreml-guest" go test ./src/ctn/internal/app -run '^TestModelRunThroughCoreMLGuest$' -count=1
cmake -S lib/plugin -B "$RUNNER_TEMP/ext-model-cengine" -DCMAKE_BUILD_TYPE=Release
cmake --build "$RUNNER_TEMP/ext-model-cengine" --target ext_plugin_host_static -j 4
EXT_COREML_GUEST_BINARY="$PWD/res/int/model/coreml-guest/.build/debug/ext-coreml-guest" CGO_LDFLAGS="-L$RUNNER_TEMP/ext-model-cengine" CGO_CFLAGS="-I$PWD/lib/plugin/include" go test -tags ext_cengine ./src/ctn/internal/app -run '^TestModelRunThroughCoreMLGuest$' -count=1
)
ci_import_env
fi
