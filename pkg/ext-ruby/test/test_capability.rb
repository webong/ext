# frozen_string_literal: true

require_relative "helper"

class CapabilityTest < Minitest::Test
  include TestHelper
  Cap = ExtIO::Plugin::Capability

  IDENT = { "id" => "example/p", "revision" => "r1" }.freeze
  DESC = {
    "apiVersion" => "ext.plugin/v1",
    "identity" => IDENT,
    "contracts" => [
      { "name" => "ext.health", "version" => "v1", "operations" => [{ "name" => "check", "surface" => "observation" }] },
      { "name" => "ext.configuration", "version" => "v1", "operations" => [{ "name" => "update", "surface" => "configuration" }] },
      { "name" => "ext.stream", "version" => "v1", "operations" => [{ "name" => "open" }, { "name" => "read" }, { "name" => "close" }] }
    ]
  }.freeze

  def request(contract, operation, payload, surface = "")
    r = {
      "apiVersion" => "ext.plugin/v1",
      "id" => "c1",
      "plugin" => IDENT,
      "contract" => { "name" => contract, "version" => "v1" },
      "operation" => operation,
      "deadline" => "2999-01-01T00:00:00Z",
      "payload" => payload
    }
    r["surface"] = surface unless surface.empty?
    r
  end

  def setup
    @updates = []
    @closed = []
    @router = Cap::Router.new
    # Symbol keys are read as the host reads them.
    @router.health { { status: "degraded", ready: false } }
    @router.configuration { |i, r, v| @updates << [i, r, v] }
    @router.stream(
      open: ->(_parameters) { "s1" },
      read: ->(_id, seq, limit) { { "items" => [{ "v" => seq }] * [limit, 2].min, "done" => seq >= 2 } },
      close: ->(id) { @closed << id; nil }
    )
    @guest = P::Guest.new(DESC, @router)
  end

  def call(contract, operation, payload, surface = "")
    @guest.invoke(request(contract, operation, payload, surface))
  end

  def test_health
    assert_equal({ "status" => "degraded", "ready" => false }, call("ext.health", "check", {}, "observation")["payload"])
    assert_equal "invalid_input", call("ext.health", "check", { "x" => 1 }, "observation")["error"]["code"]
  end

  def test_health_result_is_validated
    bad = Cap::Router.new.health { { "status" => "great", "ready" => true } }
    r = P::Guest.new(DESC, bad).invoke(request("ext.health", "check", {}, "observation"))
    assert r.key?("error")
    refute_equal "invalid_request", r["error"]["code"]
  end

  def test_configuration
    ok = { "instance" => "one", "revision" => "r1", "values" => { "name" => "a" } }
    assert_equal({ "instance" => "one", "revision" => "r1" }, call("ext.configuration", "update", ok, "configuration")["payload"])
    assert_equal [["one", "r1", { "name" => "a" }]], @updates
    [
      ok.merge("instance" => ""),
      ok.merge("revision" => ""),
      ok.merge("instance" => "x" * 257),
      ok.merge("revision" => "r" * 257),
      { "instance" => "one", "revision" => "r1" },
      ok.merge("extra" => 1),
      ok.merge("instance" => 1)
    ].each do |bad|
      assert_equal "invalid_input", call("ext.configuration", "update", bad, "configuration")["error"]["code"], bad.inspect
    end
    assert_equal 1, @updates.length
    long_ok = ok.merge("instance" => "x" * 256, "revision" => "r" * 256)
    assert_equal "x" * 256, call("ext.configuration", "update", long_ok, "configuration")["payload"]["instance"]
  end

  def test_stream
    assert_equal({ "id" => "s1" }, call("ext.stream", "open", { "parameters" => {} })["payload"])
    r = call("ext.stream", "read", { "id" => "s1", "sequence" => 1, "limit" => 5 })
    assert_equal({ "items" => [{ "v" => 1 }, { "v" => 1 }], "done" => false }, r["payload"])
    assert_equal true, call("ext.stream", "read", { "id" => "s1", "sequence" => 2, "limit" => 1 })["payload"]["done"]
    [
      { "id" => "", "sequence" => 1, "limit" => 1 },
      { "id" => "i" * 65, "sequence" => 1, "limit" => 1 },
      { "id" => "s1", "sequence" => 0, "limit" => 1 },
      { "id" => "s1", "sequence" => 1, "limit" => 0 },
      { "id" => "s1", "sequence" => 1, "limit" => -1 },
      { "id" => "s1", "sequence" => 1, "limit" => Cap::MAX_STREAM_ITEMS + 1 },
      { "id" => "s1", "sequence" => true, "limit" => 1 },
      { "id" => "s1", "sequence" => 1.5, "limit" => 1 }
    ].each do |bad|
      assert_equal "invalid_input", call("ext.stream", "read", bad)["error"]["code"], bad.inspect
    end
    assert_equal({}, call("ext.stream", "close", { "id" => "s1" })["payload"])
    assert_equal ["s1"], @closed
    assert_equal "invalid_input", call("ext.stream", "close", { "id" => "" })["error"]["code"]
  end

  def test_batch_limits
    Cap.stream_batch({ "items" => Array.new(Cap::MAX_STREAM_ITEMS) { |i| { "v" => i } }, "done" => false })
    assert_raises(ArgumentError) { Cap.stream_batch({ "items" => Array.new(Cap::MAX_STREAM_ITEMS + 1) { |i| { "v" => i } }, "done" => false }) }
    big = "x" * (600 << 10)
    assert_raises(ArgumentError) { Cap.stream_batch({ "items" => [big, big], "done" => false }) }
    assert_raises(ArgumentError) { Cap.stream_batch({ "items" => [], "done" => 1 }) }
    # The size is of UTF-8, not of escapes: 400 KiB of two-byte characters, twice, is too large.
    wide = "é" * (300 << 10)
    assert_raises(ArgumentError) { Cap.stream_batch({ "items" => [wide, wide], "done" => false }) }
  end

  def test_unregistered_operation_and_duplicates
    router = Cap::Router.new
    assert_raises(P::RemoteError) { router.call("ext.health", "check", {}) }
    router.health { { "status" => "healthy", "ready" => true } }
    assert_raises(ArgumentError) { router.health { { "status" => "healthy", "ready" => true } } }
  end
end
