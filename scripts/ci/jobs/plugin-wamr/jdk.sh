#!/usr/bin/env bash
# plugin-wamr/jdk: drafted from .github/workflows/plugin-wamr.yml (job jdk).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-java@v5)

# (action dtolnay/rust-toolchain@stable)

# (action actions/cache@v5)

# scripts/ext-android-wamr.sh
(
scripts/ext-android-wamr.sh
)
ci_import_env
