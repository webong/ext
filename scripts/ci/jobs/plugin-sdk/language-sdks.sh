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

  # The Go example hosts on the canonical C engine, which has no Go fallback.
  cmake -S lib/plugin -B "$RUNNER_TEMP/language-plugin" -DCMAKE_BUILD_TYPE=Release -DEXT_BUILD_SHARED=ON
  cmake --build "$RUNNER_TEMP/language-plugin" --target ext_plugin_host_shared --config Release -j 4
  case "$(go env GOOS)" in
    linux) library_suffix=so ;;
    darwin) library_suffix=dylib ;;
    *) echo 'the TypeScript example check requires a POSIX shared-library build' >&2; exit 1 ;;
  esac
  export EXT_PLUGIN_LIBRARY="$RUNNER_TEMP/language-plugin/libext_plugin_host.$library_suffix"
  test -f "$EXT_PLUGIN_LIBRARY"

  # Exercise both directions of the Go and TypeScript JSON-line example.
  go run ./examples/plugins/go/plugin-typescript
  go build -o "$RUNNER_TEMP/ext-plugin-example" ./examples/plugins/go/plugin-typescript
  node examples/plugins/go/plugin-typescript/host.mjs "$RUNNER_TEMP/ext-plugin-example"
fi
