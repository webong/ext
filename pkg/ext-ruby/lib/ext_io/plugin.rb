# frozen_string_literal: true

# Guest SDK for ext.plugin/v1 over bounded JSON lines (standard library only).
#
#   require "ext_io/plugin"
#
#   ExtIO::Plugin.serve_stdio("plugin.json") do |contract, operation, payload|
#     raise ExtIO::Plugin::RemoteError.new("busy", "try later", retry_after_ms: 250) if operation == "busy"
#
#     payload
#   end
#
# The module answers +plugin.hello+ with the descriptor, validates every request the way the Go
# +plugin.ValidateRequest+ does, and calls the handler sequentially. It never writes to standard
# output except protocol frames.

require_relative "plugin/version"
require_relative "plugin/core"
require_relative "plugin/frames"
require_relative "plugin/guest"
require_relative "plugin/capability"
