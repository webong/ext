# frozen_string_literal: true

# Guest used by the Go conformance suite (pkg/ext-go/engine/plugintest, TestRubyGuest).
$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))
require "ext_io/plugin"

ExtIO::Plugin.serve_stdio(File.join(__dir__, "descriptor.json"), redact_errors: true) do |_contract, operation, payload|
  case operation
  when "wait" then sleep 60
  when "private-error" then raise "secret diagnostic"
  when "public-error" then raise ExtIO::Plugin::RemoteError.new("busy", "try later", retry_after_ms: 10)
  end
  payload
end
