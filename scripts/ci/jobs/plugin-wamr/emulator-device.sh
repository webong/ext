#!/usr/bin/env bash
# plugin-wamr/emulator, inside the Android emulator: what reactivecircus/android-emulator-runner runs
# once the emulator is up (the step after scripts/ci/jobs/plugin-wamr/emulator.sh).
set -euo pipefail
cd "$(dirname "$0")/../../../.."
scripts/ext-android-wamr-device.sh
scripts/ext-android-consumer.sh
scripts/ext-android-consumer.sh --with-wamr
