#!/usr/bin/env bash
# The Ruby guest SDK and host (pkg/ext-ruby). Needs ruby, gem and go.
#   test   the Minitest suite, both gems built, then the guest under the Go process host: the
#          conformance cases and the frames suite
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  local ruby out
  ruby="$(command -v ruby)"
  lang_temp
  out="$LANG_TEMP"
  (cd pkg/ext-ruby && ruby -w test/all.rb)
  (cd pkg/ext-ruby && gem build ext-io.gemspec --output "$out/ext-io.gem" &&
    gem build ext-io-host.gemspec --output "$out/ext-io-host.gem")
  (cd pkg/ext-go/engine && EXT_RUBY="$ruby" go test -count=1 -run 'TestRuby' -v ./plugin/plugintest)
  (cd tests/crosslang && EXT_RUBY="$ruby" EXT_FRAMES_REQUIRED=Ruby go test -count=1 -v -run 'TestRubyFramesGuest' .)
}

lang_main "$0" "$@"
