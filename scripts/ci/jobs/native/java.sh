#!/usr/bin/env bash
# native/java: the portable and Android Java contracts of res/system and src/ctn/contract (system, network, ML) on the JVM.
# (Split out of native/native; the steps are unchanged.)
. "$(dirname "$0")/../lib.sh"

# Validate portable Android system contracts
if [ "$RUNNER_OS" != Windows ]; then
(
scripts/system-android.sh
)
ci_import_env
fi

# Validate Android system network contracts
if [ "$RUNNER_OS" != Windows ]; then
(
scripts/network-android.sh
)
ci_import_env
fi

# Validate Android ML contracts and optional TensorFlow Lite backend
if [ "$RUNNER_OS" != Windows ]; then
(
scripts/ml-android.sh
)
ci_import_env
fi
