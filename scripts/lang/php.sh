#!/usr/bin/env bash
# The PHP guest SDK and host (pkg/ext-php). Needs php with the json extension, composer and go.
#   test   validate composer.json, run the unit tests and the shared fixtures, then the guest under
#          the Go process host: the conformance cases and the frames suite
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  (cd pkg/ext-php && composer validate --strict)
  (cd pkg/ext-php && EXT_PHP_TRACE=1 php tests/run.php)
  (cd pkg/ext-go/engine && go test -count=1 -v -run 'TestPHPGuest' ./plugin/plugintest)
  (cd tests/crosslang && EXT_FRAMES_REQUIRED=PHP go test -count=1 -v -run 'TestPHPFramesGuest' .)
}

lang_main "$0" "$@"
