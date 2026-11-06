#!/usr/bin/env ruby
# frozen_string_literal: true

# An example plugin: echoes its payload, counts with progress events, and answers ext.health.
# Run it under a host, for example:
#   go run ./pkg/ext-go/hashicorp/cmd/ext-plugin inspect --root pkg/ext-ruby/examples/echo \
#     --entry main pkg/ext-ruby/examples/echo/plugin.json
$LOAD_PATH.unshift(File.expand_path("../../lib", __dir__)) # not needed once the gem is installed
require "ext_io/plugin"

P = ExtIO::Plugin

router = P::Capability::Router.new
router.health { { status: "healthy", ready: true } }
router.on("example.echo", "v1", "echo") { |payload| payload }
router.on("example.echo", "v1", "count") do |payload|
  call = P.current_call
  total = payload.is_a?(Hash) ? payload.fetch("to", 3) : 3
  raise P::RemoteError.new("invalid_input", "to must be 1..1000") unless total.is_a?(Integer) && total.between?(1, 1000)

  total.times do |i|
    begin
      call.progress(P::Progress.fraction(i + 1, total))
    rescue P::FrameError
      break # the host asked for no events
    end
  end
  { counted: total }
end

P.serve_stdio(File.join(__dir__, "plugin.json"), router)
