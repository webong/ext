# frozen_string_literal: true

# The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
# docs/plugin-guest-frames.md): reports progress and calls its host. It reads inline, so it
# answers the operations that need a reader of its own (idle, collect, burst) with the public
# error "unsupported".
$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))
require "ext_io/plugin"

module FramesGuest
  P = ExtIO::Plugin
  DESCRIPTOR = File.expand_path("../../../lib/plugin/testdata/v1/frames-descriptor.json", __dir__)
  HOST = ["host.svc", "v1"].freeze

  module_function

  # How a reverse call came out, in the shape the suite reads.
  def outcome
    { "result" => yield }
  rescue P::RemoteError => e
    error = { "code" => e.code, "message" => e.message }
    error["retry"] = e.retry_after_ms if e.retry_after_ms && e.retry_after_ms != 0
    { "error" => error }
  rescue P::FrameError => e
    { "error" => { "other" => %w[closed deadline canceled].include?(e.code) ? e.code : "failed" } }
  end

  def report(call, event)
    call.progress(event)
  rescue P::FrameError
    nil
  end

  def handle(_contract, operation, payload)
    call = P.current_call
    payload = {} unless payload.is_a?(Hash)
    case operation
    when "stream"
      report(call, P::Progress.fraction(1, 3))
      report(call, P::Progress.out("hello"))
      report(call, P::Progress.err("oops"))
      { "done" => true }
    when "flood"
      { "dropped" => payload.fetch("count", 300).times.count { report(call, P::Progress.log("x")) == P::DROPPED } }
    when "limits"
      limits = call.progress_limits
      { "declared" => !limits.nil?, "events" => limits ? limits.events_per_second : 0, "bytes" => limits ? limits.bytes_per_second : 0 }
    when "split"
      pieces = P.split_progress_text(payload["text"])
      pieces.each { |piece| report(call, P::Progress.out(piece)) }
      { "pieces" => pieces.length }
    when "plain"
      unsupported = begin
        call.progress(P::Progress.log("never sent"))
        false
      rescue P::FrameError => e
        e.code == "unsupported"
      end
      { "unsupported" => unsupported, "limits" => !call.progress_limits.nil? }
    when "call"
      return { "error" => { "other" => "unsupported" } } if call.reverse.nil?

      outcome { call.reverse.call(HOST, payload["op"], payload["payload"]) }
    when "twice"
      begin
        first = call.reverse.call(HOST, "sign", 1)
        second = call.reverse.call(HOST, "sign", 2)
      rescue P::RemoteError, P::FrameError => e
        raise "a reverse call failed: #{e.message}"
      end
      { "first" => first, "second" => second }
    when "idle", "collect", "burst"
      raise P::RemoteError.new("unsupported", "not supported here")
    else
      raise "unknown operation"
    end
  end
end

ExtIO::Plugin.serve_stdio(FramesGuest::DESCRIPTOR, FramesGuest.method(:handle), redact_errors: true) if $PROGRAM_NAME == __FILE__
