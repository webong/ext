#!/usr/bin/env bash
# The TypeScript guest SDK (pkg/ext-ts). Needs node 22 and npx.
#   test       the node test suites
#   typecheck  the public declarations under tsc --strict (TypeScript 5.8.3)
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  node --test pkg/ext-ts/test.mjs pkg/ext-ts/frames.test.mjs pkg/ext-ts/router.test.mjs
}

lang_cmd_typecheck() {
  npx --yes --package typescript@5.8.3 tsc --noEmit --strict --module NodeNext \
    --moduleResolution NodeNext --target ES2022 pkg/ext-ts/typecheck.mts
}

lang_main "$0" "$@"
