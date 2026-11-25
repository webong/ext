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
  go run ./examples/plugins/go/plugin-typescript
  go build -o "$RUNNER_TEMP/ext-plugin-example" ./examples/plugins/go/plugin-typescript
  node examples/plugins/go/plugin-typescript/host.mjs "$RUNNER_TEMP/ext-plugin-example"
fi
