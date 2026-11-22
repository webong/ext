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

  # An interrupt already pending when a call begins makes Ruby skip the native call, and most are
  # not cancellations: another thread's time slice, a signal trap, a GC's finalizers, and on
  # Windows every tick of the VM clock (100 ms). The call runs once Ruby has handled it, instead of
  # ending as canceled without reaching the engine. The window is small, so this only makes the
  # skip likely: quick calls for a second, beside a thread that wants the lock.
  def test_a_pending_interrupt_does_not_cancel_the_call
    @engine.process(@descriptor, @guest) do |h|
      h.start
      busy = Thread.new { loop { Thread.pass } }
      calls = 0
      begin
        deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + 1.0
        while Process.clock_gettime(Process::CLOCK_MONOTONIC) < deadline
          assert_equal calls, h.call(@contract, "echo", { "value" => calls })["value"]
          calls += 1
        end
      ensure
        busy.kill
        busy.join
      end
      assert_operator calls, :>, 0
    end
  end

  # Ruby wakes a waiting call for every interrupt, also for one it only notes: a child process
  # ending (SIGCHLD) interrupts the main thread. The call goes on and ends at its own deadline.
  def test_a_child_exiting_does_not_cancel_the_call
    @engine.process(@descriptor, @guest) do |h|
      h.start
      children = Thread.new { 5.times { system("true"); sleep 0.05 } }
      started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      e = assert_raises(ExtIO::Host::HostError) { h.call(@contract, "wait", nil, timeout: 0.6) }
      children.join
      assert_equal "timeout", e.status
      assert_operator Process.clock_gettime(Process::CLOCK_MONOTONIC) - started, :>=, 0.5
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

# Reverse services and the guest registry, against the engine's scripted guests:
# EXT_REVERSE_TEST_GUEST (lib/plugin/tests/reverse.c) and EXT_REGISTRY_TEST_GUEST
# (lib/plugin/tests/registry.c), which scripts/host-cases.sh builds.
module ReverseSupport
  module_function

  def guests(test)
    why = HostSupport.load
    raise why if why && HostSupport::REQUIRED
    test.skip why if why
    reverse = HostSupport.need("EXT_REVERSE_TEST_GUEST")
    registry = HostSupport.need("EXT_REGISTRY_TEST_GUEST")
    test.skip "set EXT_REVERSE_TEST_GUEST and EXT_REGISTRY_TEST_GUEST, or run scripts/host-cases.sh" unless
      HostSupport.need("EXT_HOST_LIBRARY") && reverse && registry
    [reverse, registry]
  end
end

class ReverseServicesTest < Minitest::Test
  RUN = { "name" => "reverse.test", "version" => "v1" }.freeze

  def setup
    @guest, = ReverseSupport.guests(self)
    @descriptor, status = Open3.capture2(@guest, "--descriptor")
    assert status.success?
    @handled = []
  end

  def host(mode, serve: true)
    h = HostSupport.engine.process(@descriptor, @guest, arguments: ["--guest", mode])
    if serve
      h.serve(authorize: ->(call) { call.remaining.positive? && call.request["operation"] != "secret" },
              handle: lambda { |call|
                @handled << call.request
                case call.request["operation"]
                when "sign" then "signed"
                when "empty" then nil
                when "errok" then raise ExtIO::Host::RemoteError.new("policy", "refused for you", retry_after_ms: 5)
                else raise "a private failure"
                end
              }, timeout: 0.15, max_payload_bytes: 64)
    end
    h.start(5)
    h
  end

  def reply(mode, serve: true)
    h = host(mode, serve: serve)
    h.call(RUN, "run", {}, timeout: 5)["reply"]
  ensure
    h&.close
    h&.destroy
  end

  def test_hooks_answer_on_the_calling_thread
    assert_equal "signed", reply("call:sign")["payload"]
    assert_equal "signing", @handled[0]["surface"]
    assert_equal "1", @handled[0]["parent"]
    assert_nil reply("call:empty")["payload"]
    assert_equal({ "code" => "policy", "message" => "refused for you", "retryAfterMilliseconds" => 5 },
                 reply("call:errok")["error"])
    assert_equal "operation_failed", reply("call:fail")["error"]["code"]
  end

  def test_policy_and_refusals
    assert_equal "denied", reply("call:secret")["error"]["code"]
    assert_empty @handled
    assert_equal "reverse_undeclared", reply("call:nothere")["error"]["code"]
    assert_equal "reverse_unavailable", reply("call:sign", serve: false)["error"]["code"]
  end

  def test_requests_between_calls_wait_for_serve_reverse
    h = host("idle")
    assert h.serve_reverse(5)
    assert_nil @handled[0]["parent"]
    assert_equal "signed", h.call(RUN, "run", {}, timeout: 5)["reply"]["payload"]
    refute h.serve_reverse(0.05)
  ensure
    h&.close
    h&.destroy
  end
end

class RegistryTest < Minitest::Test
  include TestHelper

  def setup
    _, @guest = ReverseSupport.guests(self)
    @cases_path = File.join(TestHelper::FIXTURES, "registry-cases.json")
    @cases = fixture("registry-cases.json")
  end

  def relay(registry, name, deny_forward: false, deny_reverse: false)
    descriptor, status = Open3.capture2(@guest, "--descriptor", @cases_path, name)
    assert status.success?
    h = HostSupport.engine.process(descriptor, @guest, arguments: ["--guest", @cases_path, name],
                                                       authorize: ->(_) { !deny_forward })
    h.attach(registry, authorize: ->(_) { !deny_reverse })
    h.start(5)
    h
  end

  def contract(name)
    { "name" => @cases["guests"].fetch(name), "version" => "v1" }
  end

  def test_shared_cases
    @cases["cases"].each do |c|
      registry = ExtIO::Host::Registry.new(HostSupport.engine)
      hosts = {}
      begin
        @cases["guests"].each_key do |name|
          hosts[name] = relay(registry, name, deny_forward: (c["forwardDeny"] || []).include?(name),
                                              deny_reverse: (c["reverseDeny"] || []).include?(name))
          registry.register(contract(name), hosts[name])
        end
        @cases["edges"].each { |e| registry.route(e["from"], e["to"]) }
        c["steps"].each_with_index do |step, i|
          if step["unregister"]
            registry.unregister(contract(step["unregister"]))
            next
          end
          got = hosts.fetch(step["call"]).call(contract(step["call"]), step["operation"], step["payload"],
                                               timeout: (step["timeoutMs"] || 5000) / 1000.0)
          if step.key?("result")
            assert_equal step["result"], got, "#{c["name"]} step #{i}"
          else
            assert_includes step["resultError"], got.dig("error", "code"), "#{c["name"]} step #{i}: #{got}"
          end
        end
      ensure
        hosts.each_value do |h|
          h.close
          h.destroy
        end
      end
    end
  end

  def test_configuration
    registry = ExtIO::Host::Registry.new(HostSupport.engine)
    b = relay(registry, "b")
    status = ->(&blk) { assert_raises(ExtIO::Host::HostError, &blk).status }
    assert_equal "invalid", status.call { registry.register({ "name" => "Not A Name", "version" => "v1" }, b) }
    assert_equal "unsupported", status.call { registry.register({ "name" => "registry.other", "version" => "v1" }, b) }
    registry.register(contract("b"), b)
    assert_equal "exists", status.call { registry.register(contract("b"), b) }
    from = { "name" => "registry.ask", "version" => "v1", "operation" => "echo-b" }
    to = { "name" => "registry.b", "version" => "v1", "operation" => "echo" }
    registry.route(from, to)
    assert_equal "exists", status.call { registry.route(from, to) }
    assert_equal "invalid", status.call { registry.route(from.merge("operation" => "other"), to.merge("operation" => "Bad Name")) }
    assert_equal "not_found", status.call { registry.unregister({ "name" => "registry.x", "version" => "v1" }) }
    registry.unregister(contract("b"))
    registry.register(contract("b"), b)
    assert_raises(ArgumentError) { b.attach(Object.new, authorize: ->(_) { true }) }
    b.close
    b.destroy
    # A destroyed host has left the registry.
    assert_equal "not_found", status.call { registry.unregister(contract("b")) }
  end

  def test_unreachable_hosts_and_registries_are_collected
    3.times do
      registry = ExtIO::Host::Registry.new(HostSupport.engine)
      registry.register(contract("b"), relay(registry, "b"))
    end
    GC.start
  end
end
