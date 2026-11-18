#!/usr/bin/env bash
# ext-android release, in the Android emulator: the shipped AARs in an app (R8 on).
set -euo pipefail
cd "$(dirname "$0")/../../.."
scripts/ext-android-consumer.sh --aar dist/ext-android-${VERSION}/ext-android-${VERSION}.aar
scripts/ext-android-consumer.sh --with-wamr --aar dist/ext-android-${VERSION}/ext-android-wamr-${VERSION}.aar
