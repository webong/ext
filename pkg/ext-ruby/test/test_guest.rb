# frozen_string_literal: true

require_relative "helper"
require "open3"
require "tmpdir"
require "rbconfig"

class GuestTest < Minitest::Test
  include TestHelper

  IDENT = { "id" => "example/p", "revision" => "r1" }.freeze
  DESC = {
    "apiVersion" => "ext.plugin/v1",
    "identity" => IDENT,
    "contracts" => [
      {
        "name" => "example.echo",
        "version" => "v1",
        "operations" => [{ "name" => "echo" }, { "name" => "seen", "surface" => "observation" }, { "name" => "boom" },
                         { "name" => "busy" }, { "name" => "noisy" }]
      }
    ]
  }.freeze
  FUTURE = "2999-01-01T00:00:00Z"

  HANDLER = lambda do |_contract, op, payload|
    case op
    when "boom" then raise "x" * 5000
    when "busy" then raise P::RemoteError.new("busy", "later", retry_after_ms: 250)
    when "noisy"
      puts "stray output"
      "ok"
    else payload
    end
  end

  def req(op = "echo", **kw)
    r = {
      "apiVersion" => "ext.plugin/v1",
      "id" => "c1",
      "plugin" => IDENT,
      "contract" => { "name" => "example.echo", "version" => "v1" },
      "operation" => op,
      "deadline" => FUTURE,
      "payload" => { "v" => 1 }
    }
    kw.each { |k, v| r[k.to_s] = v }
    r
  end

  def hello(**kw)
    r = { "apiVersion" => "ext.plugin/v1", "id" => "hello", "plugin" => { "id" => "", "revision" => "" },
          "contract" => { "name" => "", "version" => "" }, "operation" => "plugin.hello", "deadline" => FUTURE }
    kw.each { |k, v| r[k.to_s] = v }
    r
  end

  def run_guest(bytes, handler = HANDLER, **kw)
    out = StringIO.new(+"".b)
    guest = P::Guest.new(DESC, handler, **kw)
    err = nil
    begin
      P.serve(guest, StringIO.new(bytes.b), out)
    rescue P::ProtocolError => e
      err = e
    end
    [out.string.lines.map { |l| JSON.parse(l) }, err]
  end

  def guest(handler = HANDLER, **kw)
    P::Guest.new(DESC, handler, **kw)
  end

  def test_hello_and_echo
    out, err = run_guest(frames(hello, req))
    assert_nil err
    assert_equal DESC, out[0]["payload"]
    assert_equal({ "apiVersion" => "ext.plugin/v1", "id" => "c1", "payload" => { "v" => 1 } }, out[1])
  end

  def test_hello_must_be_first_and_strict
    [req, hello(id: "x"), hello(operation: "echo"), hello(payload: nil), hello(plugin: IDENT), hello(surface: "s")].each do |bad|
      out, err = run_guest(frames(bad))
      refute_nil err, bad.inspect
      assert_equal [], out
    end
  end

  def test_empty_result_is_null
    g = P::Guest.new(DESC) { nil }
    response = g.invoke(req)
    assert response.key?("payload")
    assert_nil response["payload"]
  end

  def test_request_validation
    cases = [
      req(apiVersion: "ext.plugin/v2"),
      req(id: "-bad"),
      req(plugin: { "id" => "example/p", "revision" => "r2" }),
      req(contract: { "name" => "example.echo", "version" => "v2" }),
      req("missing"),
      req("seen"), # declared surface missing
      req(deadline: "0001-01-01T00:00:00Z"),
      req("plugin.hello")
    ]
    g = guest
    cases.each do |r|
      response = g.invoke(r)
      assert_equal "invalid_request", response["error"]["code"], r.inspect
      refute response.key?("payload")
    end
    assert_equal({ "v" => 1 }, g.invoke(req("seen", surface: "observation"))["payload"])
    assert_equal "invalid_request", g.invoke(req("seen", surface: "other"))["error"]["code"]
  end

  def test_missing_deadline_is_invalid_request
    r = req
    r.delete("deadline")
    assert_equal "invalid_request", guest.invoke(r)["error"]["code"]
  end

  def test_expired_deadline
    assert_equal "provider_error", guest.invoke(req(deadline: "2000-01-01T00:00:00Z"))["error"]["code"]
  end

  def test_deadline_offsets_and_fractions
    ["2999-01-01T00:00:00.123456789Z", "2999-01-01T05:30:00+05:30", "2999-01-01T00:00:00.5-07:00"].each do |stamp|
      assert_equal({ "v" => 1 }, guest.invoke(req(deadline: stamp))["payload"], stamp)
    end
  end

  def test_deadline_parsing
    assert_equal Time.utc(2999, 1, 1, 0, 0, 0, 123_456), P.parse_deadline("2999-01-01T00:00:00.123456789Z")
    assert_equal Time.utc(2999, 1, 1), P.parse_deadline("2999-01-01T05:30:00+05:30")
    assert_equal Time.utc(2024, 2, 29), P.parse_deadline("2024-02-29T00:00:00Z")
    assert_equal Time.utc(1, 1, 1, 0, 0, 1), P.parse_deadline("0001-01-01T00:00:01Z")
    ["2023-02-29T00:00:00Z", "2999-13-01T00:00:00Z", "2999-01-01T24:00:00Z", "2999-01-01T00:00:60Z",
     "2999-01-01T00:00:00+24:00", "0000-01-01T00:00:00Z", "0001-01-01T00:00:00Z", "0001-01-01T01:00:00+01:00",
     "2999-01-01 00:00:00Z", "2999-01-01T00:00:00", nil, 5].each do |bad|
      assert_nil P.parse_deadline(bad), bad.inspect
    end
  end

  def test_errors
    g = guest
    assert_equal({ "code" => "busy", "message" => "later", "retryAfterMilliseconds" => 250 }, g.invoke(req("busy"))["error"])
    boom = g.invoke(req("boom"))["error"]
    assert_equal "provider_error", boom["code"]
    assert_equal 4096, boom["message"].length
    redacted = guest(redact_errors: true).invoke(req("boom"))["error"]
    assert_equal({ "code" => "operation_failed", "message" => "plugin operation failed" }, redacted)
    multibyte = P::Guest.new(DESC) { raise "é" * 4000 }
    message = multibyte.invoke(req)["error"]["message"]
    assert_operator message.bytesize, :<=, 4096
    assert message.valid_encoding?
    # What a program raises beyond StandardError is a failure too.
    assert_equal "provider_error", P::Guest.new(DESC) { raise NotImplementedError, "later" }.invoke(req)["error"]["code"]
  end

  def test_remote_error_validation
    [["Bad", "m"], ["ok", "x" * 4097], ["ok", "m", -1], ["ok", "m", P::MAX_RETRY_AFTER_MS + 1], ["ok", "m", true],
     ["ok", "\xff".b]].each do |code, message, retry_after|
      assert_raises(ArgumentError) { P::RemoteError.new(code, message, retry_after_ms: retry_after) }
    end
    P::RemoteError.new("ok", "m", retry_after_ms: P::MAX_RETRY_AFTER_MS)
    assert_equal({ "code" => "ok", "message" => "m" }, P::RemoteError.new("ok", "m", retry_after_ms: 0).to_wire)
  end

  def test_unserializable_result_is_an_error
    [Object.new, Float::NAN, Float::INFINITY, { 1 => 2 }, "\xff".b].each do |result|
      assert_equal "provider_error", P::Guest.new(DESC) { result }.invoke(req)["error"]["code"], result.inspect
    end
  end

  def test_symbols_are_written_as_strings
    response = P::Guest.new(DESC) { { ok: :yes, list: [:a] } }.invoke(req)
    assert_equal '{"ok":"yes","list":["a"]}', P.encode_frame(response["payload"])
  end

  def test_current_call
    seen = {}
    P::Guest.new(DESC) do |_c, _o, _p|
      info = P.current_call
      seen.update(op: info.operation, id: info.request_id, ok: info.remaining.positive?, deadline: info.deadline.is_a?(Time))
      nil
    end.invoke(req)
    assert_equal({ op: "echo", id: "c1", ok: true, deadline: true }, seen)
    assert_nil P.current_call
  end

  def test_max_call_seconds_bounds_the_deadline
    remaining = nil
    P::Guest.new(DESC, max_call_seconds: 2) { remaining = P.current_call.remaining }.invoke(req)
    assert_operator remaining, :<=, 2
    assert_raises(ArgumentError) { guest(max_call_seconds: 0) }
    assert_raises(TypeError) { P::Guest.new(DESC) }
  end

  def test_strict_json_ends_stream
    bad = [
      %({"apiVersion":"ext.plugin/v1","id":"hello","id":"hello"}\n),
      %({"apiVersion":"ext.plugin/v1","unknown":1}\n),
      "[1,2]\n",
      %({"a":NaN}\n),
      %({"a":"\\ud800"}\n),
      %({"a":1} {"b":2}\n),
      "\xff\xfe\n".b,
      %({"a":#{"[" * 70}#{"]" * 70}}\n)
    ]
    bad.each do |raw|
      out, err = run_guest(raw)
      refute_nil err, raw.inspect
      assert_equal [], out
    end
  end

  def test_strict_json_decoding
    [%("\\ud800\\u0041"), %("\\udc00"), %([1,]), %({"a":1,}), %(01), %(1.), %(-), %("\x01"), %("\\x"), %(\uFEFF1),
     %(Infinity), %(tru), %({1:2}), %([)].each do |raw|
      assert_raises(P::ProtocolError, raw.inspect) { P.decode_frame(raw.b) }
    end
    assert_equal [1, -0.0, 100.0, "\u{1F600}", "\n/\"", nil, true, false, {}], P.decode_frame(%( [1,-0.0,1E2,"\\ud83d\\ude00","\\n\\/\\"",null,true,false,{}] ).b)
    assert_kind_of Integer, P.decode_frame("12345678901234567890123")
    # Every value sits at a depth of at most 64, the outermost at 0, as the Python SDK counts it.
    P.decode_frame(("[" * 65) + ("]" * 65))
    P.decode_frame(("[" * 64) + "1" + ("]" * 64))
    assert_raises(P::ProtocolError) { P.decode_frame(("[" * 66) + ("]" * 66)) }
    assert_raises(P::ProtocolError) { P.decode_frame(("[" * 65) + "1" + ("]" * 65)) }
  end

  def test_payload_strictness_after_hello
    raw = frames(hello) + %({"apiVersion":"ext.plugin/v1","id":"c1","operation":"echo","deadline":"2999-01-01T00:00:00Z","payload":{"a":1,"a":2}}\n)
    out, err = run_guest(raw)
    refute_nil err
    assert_equal 1, out.length
  end

  def test_astral_pair_allowed
    out, err = run_guest(frames(hello, req(payload: "\u{1F600}")))
    assert_nil err
    assert_equal "\u{1F600}", out[1]["payload"]
  end

  def test_frame_limit
    _, err = run_guest(("x" * (P::MAX_FRAME_BYTES + 1)) + "\n")
    refute_nil err
    _, err = run_guest("x" * (P::MAX_FRAME_BYTES + 5)) # no newline, unbounded
    refute_nil err
    _, err = run_guest(frames(hello).chomp) # EOF inside a frame
    refute_nil err
  end

  def test_oversize_response_becomes_error
    g = P::Guest.new(DESC) { "x" * P::MAX_FRAME_BYTES }
    out = StringIO.new(+"".b)
    P.serve(g, StringIO.new(frames(hello, req)), out)
    assert_equal "response_too_large", JSON.parse(out.string.lines[1])["error"]["code"]
  end

  def test_clean_eof
    assert_equal [[], nil], run_guest("")
  end

  def test_descriptor_loading
    Dir.mktmpdir do |d|
      bare = File.join(d, "descriptor.json")
      pkg = File.join(d, "plugin.json")
      File.write(bare, JSON.generate(DESC))
      File.write(pkg, JSON.generate({ "apiVersion" => "ext.package/v1", "descriptor" => DESC, "artifacts" => [] }))
      assert_equal DESC, P.load_descriptor(bare)
      assert_equal DESC, P.load_descriptor(pkg)
      assert_instance_of P::Guest, P::Guest.new(pkg, HANDLER)
      File.write(pkg, JSON.generate({ "apiVersion" => "other", "descriptor" => DESC }))
      assert_raises(ArgumentError) { P.load_descriptor(pkg) }
    end
  end

  def test_a_descriptor_with_symbol_keys_is_read_as_json
    symbols = { apiVersion: "ext.plugin/v1", identity: { id: "example/p", revision: "r1" },
                contracts: [{ name: "example.echo", version: "v1", operations: [{ name: "echo" }] }] }
    g = P::Guest.new(symbols) { |_c, _o, p| p }
    assert_equal "ext.plugin/v1", g.handshake["apiVersion"]
    assert_equal({ "v" => 1 }, g.invoke(req)["payload"])
  end

  def test_descriptor_validation
    mutate = lambda do |&f|
      d = JSON.parse(JSON.generate(DESC))
      f.call(d)
      d
    end
    bad = [
      mutate.call { |d| d["apiVersion"] = "x" },
      mutate.call { |d| d["identity"]["id"] = "Bad" },
      mutate.call { |d| d["contracts"] = [] },
      mutate.call { |d| d["contracts"][0]["operations"] = [] },
      mutate.call { |d| d["contracts"][0]["operations"] << { "name" => "plugin.hello" } },
      mutate.call { |d| d["contracts"][0]["operations"] << { "name" => "echo" } },
      mutate.call { |d| d["contracts"] << d["contracts"][0].dup },
      mutate.call { |d| d["extra"] = 1 }
    ]
    bad.each { |d| assert_raises(ArgumentError, d.inspect) { P.validate_descriptor(d) } }
    assert_raises(ArgumentError) { P::Guest.new(bad[0], HANDLER) }
  end
end

class StdioTest < Minitest::Test
  include TestHelper

  def test_stdout_carries_only_frames
    lib = File.expand_path("../lib", __dir__)
    script = <<~RUBY
      require "ext_io/plugin"
      ExtIO::Plugin.serve_stdio(#{P.encode_frame(GuestTest::DESC).inspect}.then { |d| ExtIO::Plugin.decode_frame(d) }) do
        puts "noise on stdout"
        STDOUT.syswrite("raw noise\\n")
        system("echo child noise")
        "ok"
      end
    RUBY
    input = frames(GuestTest.new("x").hello, GuestTest.new("x").req)
    out, err, status = Open3.capture3(RbConfig.ruby, "--disable-gems", "-I", lib, "-e", script, stdin_data: input, binmode: true)
    assert status.success?, err
    lines = out.lines
    assert_equal 2, lines.length, out
    assert_equal "ok", JSON.parse(lines[1])["payload"]
    assert_includes err, "noise on stdout"
    assert_includes err, "raw noise"
    assert_includes err, "child noise"
  end
end
