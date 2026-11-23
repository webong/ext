#!/usr/bin/env bash
# native/swift: the iOS builds and Swift tests of res/system and res/int, and the CoreML guest through CTN.
# (Split out of native/native; the steps are unchanged.)
. "$(dirname "$0")/../lib.sh"

# Generic iOS ExtSystem build
if [ "$RUNNER_OS" = macOS ]; then
(
cd res/system/capture/surface/ios
xcodebuild -scheme ExtSystem -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
)
ci_import_env
fi

# Generic iOS ExtHost, ExtShare and ExtAudio builds
if [ "$RUNNER_OS" = macOS ]; then
(
for package in service/host/ios:ExtHost capture/share/ios:ExtShare capture/audio/ios:ExtAudio; do
  (cd "res/system/${package%%:*}" && xcodebuild -scheme "${package##*:}" -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build)
done
)
ci_import_env
fi

# Generic iOS ExtNetwork build
if [ "$RUNNER_OS" = macOS ]; then
(
cd res/system/service/network/ios
xcodebuild -scheme ExtNetwork -destination 'generic/platform=iOS' -sdk iphoneos CODE_SIGNING_ALLOWED=NO build
)
ci_import_env
fi

# Swift tests (ExtSystem)
if [ "$RUNNER_OS" = macOS ]; then
(
cd res/system/capture/surface/ios
swift test
)
ci_import_env
fi

# Swift tests (ExtHost, ExtShare, ExtAudio and ExtClipboard)
if [ "$RUNNER_OS" = macOS ]; then
(
for package in service/host/ios capture/share/ios capture/audio/ios capture/clipboard/ios; do
  swift test --package-path "res/system/$package"
done
)
ci_import_env
fi

# Swift tests (ExtNetwork)
if [ "$RUNNER_OS" = macOS ]; then
(
cd res/system/service/network/ios
swift test
)
ci_import_env
fi

# Swift tests (ExtML)
if [ "$RUNNER_OS" = macOS ]; then
(
cd res/int/model/ios
swift test
)
ci_import_env
fi

# Validate native CoreML guest through CTN
if [ "$RUNNER_OS" = macOS ]; then
(
swift build --product ext-coreml-guest --package-path res/int/model/ios
EXT_COREML_GUEST_BINARY="$PWD/res/int/model/ios/.build/debug/ext-coreml-guest" go test ./src/ctn/internal/app -run '^TestModelRunThroughCoreMLGuest$' -count=1
cmake -S pkg/ext-go/cgoengine/_csrc/plugin -B "$RUNNER_TEMP/ext-model-cengine" -DCMAKE_BUILD_TYPE=Release
cmake --build "$RUNNER_TEMP/ext-model-cengine" --target ext_plugin_host_static -j 4
EXT_COREML_GUEST_BINARY="$PWD/res/int/model/ios/.build/debug/ext-coreml-guest" CGO_LDFLAGS="-L$RUNNER_TEMP/ext-model-cengine" go test -tags ext_cengine ./src/ctn/internal/app -run '^TestModelRunThroughCoreMLGuest$' -count=1
)
ci_import_env
fi
