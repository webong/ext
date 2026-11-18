#!/usr/bin/env bash
# plugin-engine/fuzz: drafted from .github/workflows/plugin-engine.yml (job fuzz).
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# Build the fuzz target
(
cmake -S lib/plugin -B "$RUNNER_TEMP/fuzz" -G Ninja -DCMAKE_C_COMPILER=clang -DCMAKE_BUILD_TYPE=RelWithDebInfo -DEXT_FUZZ=ON
cmake --build "$RUNNER_TEMP/fuzz" --target ext_fuzz
)
ci_import_env

# Fuzz for a minute
(
mkdir -p "$RUNNER_TEMP/corpus"
cp lib/plugin/testdata/fuzz/*.json lib/plugin/testdata/v1/*.json "$RUNNER_TEMP/corpus/"
"$RUNNER_TEMP/fuzz/ext_fuzz" -max_len=262144 -timeout=5 -max_total_time=60 "$RUNNER_TEMP/corpus"
)
ci_import_env
