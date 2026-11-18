#!/usr/bin/env bash
# native/android-package: drafted from .github/workflows/native.yml (job android-package).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-java@v5)

# (action android-actions/setup-android@v3)

# Install Android packaging toolchain
(
sdkmanager 'platforms;android-35' 'build-tools;35.0.0' 'ndk;27.2.12479018' 'cmake;3.22.1'
echo "ANDROID_NDK_ROOT=$ANDROID_HOME/ndk/27.2.12479018" >> "$GITHUB_ENV"
echo "$ANDROID_HOME/cmake/3.22.1/bin" >> "$GITHUB_PATH"
)
ci_import_env

# Stage Android SDK release (non-publishing)
(
scripts/ext-android-release.sh 0.0.0
)
ci_import_env
