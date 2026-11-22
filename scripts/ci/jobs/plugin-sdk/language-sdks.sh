#!/usr/bin/env bash
# Python and TypeScript SDK checks for the Runtime workflow.
. "$(dirname "$0")/../lib.sh"

targets=" ${MATRIX_TARGETS:-javascript python} "

if [[ "$targets" == *" python "* ]]; then
  scripts/lang/python.sh test
  (cd pkg/ext-go/engine && go test -count=1 -run '^TestPythonGuest$' ./plugin/plugintest)
fi

if [[ "$targets" == *" javascript "* ]]; then
  scripts/lang/ts.sh test
  scripts/lang/ts.sh typecheck

  # Exercise both directions of the Go and TypeScript JSON-line example.
  go run ./pkg/ext-go/examples/plugin-typescript
  go build -o "$RUNNER_TEMP/ext-plugin-example" ./pkg/ext-go/examples/plugin-typescript
  node pkg/ext-go/examples/plugin-typescript/host.mjs "$RUNNER_TEMP/ext-plugin-example"
fi
