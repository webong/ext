#!/usr/bin/env bash
# c2goengine/translate: drafted from .github/workflows/c2goengine.yml (job translate).
. "$(dirname "$0")/../lib.sh"

# (Windows keeps line endings as they are: the workflow sets core.autocrlf false before checkout, for
# cells with autocrlf_off.)

# (action actions/checkout@v5)

# Use the matrix compiler
if [ "${MATRIX_CC:-}" != "" ]; then
(
echo "CC=${MATRIX_CC:-}" >> "$GITHUB_ENV"
)
ci_import_env
fi

# (action actions/setup-go@v6)

# (action msys2/setup-msys2@v2 if runner.os == 'Windows')

# Generate the translation
if [ "$RUNNER_OS" != Windows ]; then
(
cd pkg/ext-go/c2goengine/tools
GOWORK=off go run .
)
ci_import_env
fi

# Generate the translation (MSYS2)
if [ "$RUNNER_OS" = Windows ]; then
(
cd pkg/ext-go/c2goengine/tools
ci_msys2 <<'MSYS2'
GOWORK=off go run .
MSYS2
)
ci_import_env
fi

# Compare with what is checked in
(
# What the translation is made from is the same on every platform, and so is the digest the
# generator writes for it; the bytes of a platform's file also follow its system headers, so
# they are not compared. A platform's own checked-in file is stale when the digest it names
# is not the one just computed.
dir=pkg/ext-go/c2goengine/internal/ccgo
own=$dir/generated_${MATRIX_GOOS:-}_${MATRIX_GOARCH:-}.go
changed=$(git status --porcelain -- "$dir/inputs.sha256" "$dir/call.go" "$dir/call_unsupported.go")
if git cat-file -e "HEAD:$own" 2>/dev/null; then
  before=$(git show "HEAD:$own" | grep -m1 'c2goengine inputs sha256:' || true)
  after=$(grep -m1 'c2goengine inputs sha256:' "$own" || true)
  if [ "$before" != "$after" ]; then
    changed="$changed
$own names inputs $before, the generator's are $after"
  fi
fi
if [ -z "$changed" ]; then
  echo "The checked-in translation is current for ${MATRIX_GOOS:-}/${MATRIX_GOARCH:-}."
  exit 0
fi
printf '%s\n' "$changed"
if [ "${MATRIX_REQUIRED:-}" = true ]; then
  echo "::error::The translation is stale: lib/plugin or the generator changed. Run 'GOWORK=off go run .' in pkg/ext-go/c2goengine/tools on every supported platform and commit the results (the translation-<goos>-<goarch> artifacts have them)."
  git diff --stat -- "$dir" | tail -5
  exit 1
fi
echo "::notice::No translation is checked in for ${MATRIX_GOOS:-}/${MATRIX_GOARCH:-} yet. The translation-${MATRIX_GOOS:-}-${MATRIX_GOARCH:-} artifact has the file and the updated build constraints."
)
ci_import_env

# Check that every checked-in translation names the same inputs
(
cd pkg/ext-go/c2goengine
go test -count=1 -run 'TestTranslationsMatchInputs|TestTargetsAgree' ./internal/ccgo
)
ci_import_env

# Test
if [ "$RUNNER_OS" != Windows ]; then
(
cd pkg/ext-go/c2goengine
go test -count=1 ${MATRIX_RACE:-} ./...
)
ci_import_env
fi

# Test (MSYS2)
if [ "$RUNNER_OS" = Windows ]; then
(
cd pkg/ext-go/c2goengine
ci_msys2 <<'MSYS2'
go test -count=1 ./...
MSYS2
)
ci_import_env
fi

# (action actions/upload-artifact@v6 if always())
