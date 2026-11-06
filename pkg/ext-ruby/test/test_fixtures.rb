# frozen_string_literal: true

# The shared fixtures for progress events and reverse calls (lib/plugin/testdata/v1), which the
# Go contract computes and every SDK reproduces: frames, the sender's rate limiter and the wire
# model.
require_relative "helper"

class FixtureTest < Minitest::Test
  include TestHelper
  F = ExtIO::Plugin::Frames

  # Collects the first few so a failure says what diverged.
  class Mismatches
    attr_reader :first, :count

    def initialize
      @first = []
      @count = 0
    end

    def note(section, kase, what)
      @count += 1
      @first << "#{section}: #{kase["name"]}: #{what}" if @first.length < 12
    end
  end

  def accepts
    yield
    true
  rescue P::ProtocolError, ArgumentError
    false
  end

  def check(bad)
    assert_equal 0, bad.count, "#{bad.count} mismatches, the first:\n#{bad.first.join("\n")}"
  end

  def test_frames_are_judged_as_the_go_contract_judges_them
    root = fixture("frame-cases.json")
    bad = Mismatches.new
    total = 0
    sections = {
      "progress" => ->(raw, _id) { F.validate_progress_frame(P.decode_frame(raw)) },
      "reverseRequest" => ->(raw, _id) { F.validate_reverse_request(P.decode_frame(raw)) },
      "reverseResponse" => ->(raw, id) { F.validate_reverse_response(P.decode_frame(raw), id) }
    }
    sections.each do |section, judge|
      cases = root[section]
      valid = cases.count { |c| c["valid"] }
      assert cases.length >= 20 && valid >= 5 && cases.length - valid >= 5, "#{section} proves little"
      cases.each do |kase|
        total += 1
        got = accepts { judge.call(kase["frame"].b, kase.fetch("id", "")) }
        bad.note(section, kase, "accepted #{got}: #{kase["frame"]}") if got != kase["valid"]
      end
    end
    assert_operator total, :>=, 500
    check(bad)
  end

  def test_the_rate_limiter_reproduces_every_step
    cases = fixture("progress-limiter-cases.json")["cases"]
    assert_operator cases.length, :>=, 10
    bad = Mismatches.new
    cases.each do |kase|
      limiter = F::Limiter.new(kase["eventsPerSecond"], kase["bytesPerSecond"], 0)
      kase["steps"].each_with_index do |step, i|
        got = limiter.admit(step["textBytes"], step["atMs"] * 1_000_000)
        want = step["admitted"]
        if !got.nil? != want || (want && got != step.fetch("dropped", 0))
          bad.note("limiter", kase, "step #{i}: #{got.inspect}, want #{want} #{step.fetch("dropped", 0)}")
        end
      end
      bad.note("limiter", kase, "pending count") if limiter.pending != kase["pending"]
    end
    check(bad)
  end

  def test_the_wire_model_is_judged_as_the_go_contract_judges_it
    root = fixture("model-cases.json")
    bad = Mismatches.new
    assert_operator root["descriptors"].length, :>, 100
    root["descriptors"].each do |kase|
      got = accepts { P.validate_descriptor(P.decode_frame(kase["descriptor"].b)) }
      bad.note("descriptor", kase, "accepted #{got}: #{kase["descriptor"]}") if got != kase["valid"]
    end
    selected = root["requestDescriptor"]
    P.validate_descriptor(selected)
    assert_operator root["requests"].length, :>, 100
    root["requests"].each do |kase|
      ran = []
      guest = P::Guest.new(selected) { |_c, _o, _p| ran << P.current_call; nil }
      link = F::Link.new(StringIO.new(+""), StringIO.new(+""))
      begin
        guest.invoke(P.decode_frame(kase["request"].b), link)
      rescue P::ProtocolError
        nil
      end
      if !ran.empty? != kase["valid"]
        bad.note("request", kase, "accepted #{!ran.empty?}: #{kase["request"]}")
        next
      end
      next if ran.empty?

      # The call gets a reporter when its operation declared progress and the request carries a
      # rate; the rate has its defaults filled in.
      limits = ran[0].progress_limits
      if !limits.nil? != (kase["reporter"] ? true : false)
        bad.note("request", kase, "reporter #{!limits.nil?}")
      elsif !limits.nil? && (limits.events_per_second != kase["events"] || limits.bytes_per_second != kase["bytes"])
        bad.note("request", kase, "rate #{limits.to_a.inspect}")
      end
    end
    check(bad)
  end
end
