# frozen_string_literal: true

# The host SDK (ExtIO::Host) on the C engine.
#
# It needs the native extension on the load path (scripts/ext-ruby-host.sh builds it), the
# engine's shared library (EXT_HOST_LIBRARY), and the reference guest twice: EXT_GO_GUEST, the
# JSON-line Go guest, and EXT_GO_SHARED, the same guest as a C ABI library. It runs every case of
# lib/plugin/testdata/v1/host-cases.json on both. Without them these tests are skipped, or fail
# when EXT_HOST_REQUIRED=1. scripts/host-cases.sh builds everything and runs them.
require_relative "helper"
require "open3"
require "rbconfig"

module HostSupport
  REQUIRED = ENV["EXT_HOST_REQUIRED"] == "1"

  module_function

  def need(name)
    value = ENV.fetch(name, "")
    return File.expand_path(value) unless value.empty?
    raise "#{name} is required" if REQUIRED

    nil
  end

  # The host, or a reason it is not available.
  def load
    require "ext_io/host"
    nil
  rescue LoadError => e
    "the native extension did not load (scripts/ext-ruby-host.sh builds it): #{e.message}"
  end

  def engine
    @engine ||= ExtIO::Host::Engine.new
  end
end

class GuestImportTest < Minitest::Test
  def test_guest_never_loads_the_host
    lib = File.expand_path("../lib", __dir__)
    code = "require 'ext_io/plugin'; print $LOADED_FEATURES.any? { |f| f.include?('ext_io/host') }"
    out, status = Open3.capture2(RbConfig.ruby, "-I", lib, "-e", code)
    assert status.success?
    assert_equal "false", out
  end
end

class HostCasesTest < Minitest::Test
  include TestHelper

  def setup
    why = HostSupport.load
    raise why if why && HostSupport::REQUIRED
    skip why if why
    @guest = HostSupport.need("EXT_GO_GUEST")
    @shared = HostSupport.need("EXT_GO_SHARED")
    skip "set EXT_HOST_LIBRARY, EXT_GO_GUEST and EXT_GO_SHARED, or run scripts/host-cases.sh" unless
      HostSupport.need("EXT_HOST_LIBRARY") && @guest && @shared
    @engine = HostSupport.engine
  end

  # Every case of host-cases.json, on both reference guests.
  def test_host_cases
    spec = fixture("host-cases.json")
    descriptor = fixture(spec["descriptor"])
    spec["guests"].each do |guest|
      path = guest == "jsonline" ? @guest : @shared
      spec["cases"].each do |c|
        run_case(guest, path, descriptor, spec["contract"], c)
      rescue Minitest::Assertion => e
        raise Minitest::Assertion, "#{guest}/#{c["name"]}: #{e.message}"
      end
    end
  end

  private

  def run_case(guest, path, descriptor, contract, c)
    d = JSON.parse(JSON.generate(descriptor))
    d["identity"]["revision"] = c["revision"] if c["revision"]
    opts = {}
    opts[:verify] = ->(_) { false } if c["verify"] == "deny"
    opts[:authorize] = ->(_) { false } if c["authorize"] == "deny"
    open = guest == "jsonline" ? -> { @engine.process(d, path, **opts) } : -> { @engine.shared_library(d, path, **opts) }
    h = open.call
    begin
      got = begin
        h.start(5.0)
        "ok"
      rescue ExtIO::Host::HostError => e
        e.status
      end
      assert_equal c.fetch("start", "ok"), got, "start"
      return unless got == "ok"

      c.fetch("steps", []).each_with_index do |step, i|
        if step["close"]
          h.close
        elsif step.key?("state")
          assert_equal step["state"], h.state, "step #{i}"
        else
          run_call(h, contract, i, step)
        end
      end
    ensure
      h.destroy
    end
  end

  def run_call(h, contract, i, step)
    where = "step #{i} (#{step["call"]})"
    outcome, value = "result", nil
    begin
      value = h.call(contract, step["call"], step["payload"], timeout: step.fetch("timeoutMs", 3000) / 1000.0)
    rescue ExtIO::Host::RemoteError => e
      outcome, value = "error", e
    rescue ExtIO::Host::HostError => e
      outcome, value = "status", e.status
    end
    if step["fails"]
      refute_equal "result", outcome, "#{where} succeeded with #{value.inspect}"
    elsif step.key?("result")
      assert_equal ["result", step["result"]], [outcome, value], where
    elsif step.key?("error")
      assert_equal "error", outcome, "#{where}: #{value.inspect}"
      want = step["error"]
      assert_equal [want["code"], want["message"], want.fetch("retryAfterMilliseconds", 0)],
                   [value.code, value.message, value.retry_after_ms || 0], where
    else
      allowed = Array(step["status"])
      assert_equal "status", outcome, "#{where}: #{value.inspect}"
      assert_includes allowed, value, where
    end
  end
end

# What the cases leave to each binding: threads, interrupts, policies, envelopes and ownership.
class HostTest < Minitest::Test
  include TestHelper

  def setup
    why = HostSupport.load
    raise why if why && HostSupport::REQUIRED
    skip why if why
    @guest = HostSupport.need("EXT_GO_GUEST")
    skip "set EXT_HOST_LIBRARY and EXT_GO_GUEST, or run scripts/host-cases.sh" unless
      HostSupport.need("EXT_HOST_LIBRARY") && @guest
    @engine = HostSupport.engine
    @descriptor = fixture("descriptor.json")
    @contract = @descriptor["contracts"][0]
  end

  def test_missing_library_is_reported
    assert_raises(LoadError) { ExtIO::Host::Engine.new("/does/not/exist/libext_host.so") }
  end

  def test_policies_see_json
    seen = {}
    verify = ->(d) { seen[:verify] = d["identity"]["id"] }
    authorize = ->(r) { seen[:authorize] = [r["operation"], r["payload"]] }
    @engine.process(@descriptor, @guest, verify: verify, authorize: authorize) do |h|
      h.start
      assert_equal({ "value" => 1 }, h.call(@contract, "echo", { "value" => 1 }))
    end
    assert_equal({ verify: "ctx/conformance", authorize: ["echo", { "value" => 1 }] }, seen)
  end

  def test_a_raising_policy_denies_and_keeps_the_session
    @engine.process(@descriptor, @guest, authorize: ->(_) { raise "policy bug" }) do |h|
      h.start
      e = assert_raises(ExtIO::Host::HostError) { h.call(@contract, "echo", { "value" => 1 }) }
      assert_equal "denied", e.status
      assert_equal "ready", h.state
    end
  end

  def test_a_policy_cannot_reenter_its_host
    reentered = nil
    h = nil
    authorize = lambda do |_|
      begin
        h.state
        reentered = "allowed"
      rescue ExtIO::Host::HostError => e
        reentered = e.status
      end
      true
    end
    h = @engine.process(@descriptor, @guest, authorize: authorize)
    h.start
    assert_equal({ "value" => 2 }, h.call(@contract, "echo", { "value" => 2 }))
    assert_equal "invalid", reentered
  ensure
    h&.destroy
  end

  def test_calls_from_threads_run_without_the_lock
    @engine.process(@descriptor, @guest) do |h|
      h.start
      results = Array.new(8) { |n| Thread.new { h.call(@contract, "echo", { "value" => n }) } }.map(&:value)
      assert_equal (0...8).to_a, results.map { |r| r["value"] }.sort
      # A call that waits lets another thread run: it does not hold the lock.
      ticks = 0
      ticker = Thread.new { loop { ticks += 1; sleep 0.001 } }
      begin
        assert_raises(ExtIO::Host::HostError, ExtIO::Host::RemoteError) { h.call(@contract, "wait", nil, timeout: 0.3) }
        during = ticks
      ensure
        ticker.kill
      end
      assert_operator during, :>, 20
    end
  end

  def test_an_interrupt_cancels_the_call
    @engine.process(@descriptor, @guest) do |h|
      h.start
      caught = nil
      t = Thread.new do
        h.call(@contract, "wait", nil, timeout: 10.0)
      rescue StandardError, Interrupt => e
        caught = e
      end
      sleep 0.2
      started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      t.raise(Interrupt)
      t.join(5)
      refute t.alive?, "the call was not interrupted"
      assert_operator Process.clock_gettime(Process::CLOCK_MONOTONIC) - started, :<, 3
      refute_nil caught
    end
  end

  def test_invoke_takes_envelopes
    @engine.process(@descriptor, @guest) do |h|
      h.start
      request = {
        "apiVersion" => "ext.plugin/v1", "id" => "r1", "plugin" => @descriptor["identity"],
        "contract" => { "name" => @contract["name"], "version" => @contract["version"] },
        "operation" => "echo", "deadline" => "2099-01-01T00:00:00Z", "payload" => { "value" => 3 }
      }
      response = JSON.parse(h.invoke(JSON.generate(request)))
      assert_equal ["r1", { "value" => 3 }], [response["id"], response["payload"]]
    end
  end

  def test_relative_paths_are_refused
    e = assert_raises(ExtIO::Host::HostError) { @engine.shared_library(@descriptor, "relative/libguest.so") }
    assert_equal "invalid", e.status
    e = assert_raises(ExtIO::Host::HostError) { @engine.process(@descriptor, "relative/guest") }
    assert_equal "invalid", e.status
  end

  def test_a_destroyed_host_refuses_use
    h = @engine.process(@descriptor, @guest)
    h.destroy
    h.destroy # once is enough; again is harmless
    h.close
    assert h.destroyed?
    e = assert_raises(ExtIO::Host::HostError) { h.start }
    assert_equal "closed", e.status
  end

  def test_stderr_is_kept
    @engine.process(@descriptor, @guest) do |h|
      h.start
      assert_kind_of String, h.stderr
    end
  end

  def test_drain_waits_for_the_calls_and_closes
    @engine.process(@descriptor, @guest) do |h|
      h.start
      h.call(@contract, "echo", { "value" => 4 })
      h.drain(1.0)
      assert_equal "closed", h.state
    end
  end

  def test_collected_hosts_are_freed
    3.times do
      h = @engine.process(@descriptor, @guest)
      h.start
      h.call(@contract, "echo", { "value" => 5 })
    end
    GC.start
  end
end
