#!/usr/bin/env bash
# The Python guest SDK and host (pkg/ext-python). Needs python3.
#   test   the unittest suite, run isolated (-I) so nothing from the working directory is imported
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  python3 -I -m unittest discover -s pkg/ext-python/tests
}

lang_main "$0" "$@"
