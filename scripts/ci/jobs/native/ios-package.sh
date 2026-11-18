#!/usr/bin/env bash
# native/ios-package: drafted from .github/workflows/native.yml (job ios-package).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# Check Apple packaging toolchain
(
xcodebuild -version
xcrun --sdk iphoneos --show-sdk-path
cmake --version
)
ci_import_env

# Stage the ext-ios and ext-macos releases (non-publishing)
(
scripts/ext-ios-release.sh --platform ios 0.0.0
scripts/ext-ios-release.sh --platform macos 0.0.0
)
ci_import_env
