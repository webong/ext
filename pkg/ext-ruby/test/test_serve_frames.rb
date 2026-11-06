# frozen_string_literal: true

# The inline router behind serve: progress events, reverse calls and the rules a host that
# breaks them meets. The host is a test thread on pipes.
require_relative "helper"
require "timeout"

module ServeFrames
  P = ExtIO::Plugin
  IDENT = { "id" => "rb/frames", "revision" => "r1" }.freeze
  CONTRACT = ["test.frames", "v1"].freeze
  HOST = ["host.svc", "v1"].freeze
  FUTURE = "2999-01-01T00:00:00Z"
  DESC = {
    "apiVersion" => "ext.plugin/v1",
    "identity" => IDENT,
    "contracts" => [
      {
        "name" => "test.frames",
        "version" => "v1",
        "operations" => [
          { "name" => "events", "progress" => true },
          { "name" => "flood", "progress" => true },
          { "name" => "invalid", "progress" => true },
          { "name" => "quiet" },
          { "name" => "ask" },
          { "name" => "session" },
          { "name" => "thread" },
          { "name" => "stale" }
        ]
      }
    ],
    "reverse" => [{ "name" => "host.svc", "version" => "v1", "operations" => [{ "name" => "sign" }] }]
  }.freeze

  KEPT = []

  def self.attempt(caller)
    caller.call(HOST, "sign", nil)
    "ok"
  rescue P::FrameError, P::RemoteError => e
    e.code
  end

  HANDLER = lambda do |_contract, operation, payload|
    call = P.current_call
    case operation
    when "events"
      first = call.progress(P::Progress.log("a"))
      second = call.progress(P::Progress.fraction(1, 2))
      { "sent" => [first == P::SENT, second == P::SENT] }
    when "flood"
      dropped = 300.times.count { call.progress(P::Progress.log("x")) == P::DROPPED }
      { "dropped" => dropped }
    when "quiet"
      begin
        call.progress(P::Progress.log("x"))
        { "unsupported" => false }
      rescue P::FrameError => e
        { "unsupported" => e.code == "unsupported" }
      end
    when "invalid"
      codes = [
        P::Progress.new("mystery"),
        P::Progress.fraction(3, 2),
        P::Progress.log("x" * 2049),
        P::Progress.new("log", "both"),
        P::Progress.log("\xff".b),
        P::Progress.new("progress", "out"),
        P::Progress.fraction(-1, 0),
        P::Progress.fraction(true, 2),
        nil
      ].map do |event|
        call.progress(event)
        "sent"
      rescue P::FrameError => e
        e.code
      end
      { "codes" => codes }
    when "ask"
      begin
        { "result" => call.reverse.call(HOST, payload.fetch("op", "sign"), payload["payload"]) }
      rescue P::RemoteError => e
        { "code" => e.code }
      rescue P::FrameError => e
        { "frame" => e.code }
      end
    when "session"
      { "session" => !call.session_reverse.nil?, "reverse" => !call.reverse.nil?,
        "concurrent" => call.reverse ? call.reverse.concurrent : false }
    when "thread"
      out = []
      Thread.new { out << ServeFrames.attempt(call.reverse) }.join
      { "codes" => out }
    when "stale"
      KEPT << call.reverse
      nil
    else
      raise "unexpected #{operation}"
    end
  end

  def self.hello(**kw)
    r = { "apiVersion" => "ext.plugin/v1", "id" => "hello", "operation" => "plugin.hello", "deadline" => FUTURE }
    kw.each { |k, v| r[k.to_s] = v }
    r
  end

  def self.answer(request_id, **body)
    r = { "frame" => "reverse_response", "apiVersion" => "ext.plugin/v1", "id" => request_id }
    body.each { |k, v| r[k.to_s] = v }
    r
  end

  # Serves a guest on pipes and reads what it writes.
  class Host
    attr_reader :outcome

    def initialize(first: true)
      @guest = P::Guest.new(DESC, HANDLER, redact_errors: true)
      guest_in, @to = IO.pipe
      @from, guest_out = IO.pipe
      [guest_in, @to, @from, guest_out].each(&:binmode)
      @to.sync = true
      @frames = Queue.new
      @outcome = nil
      @next = 0
      @served = Thread.new do
        P.serve(@guest, guest_in, guest_out)
      rescue Exception => e # rubocop:disable Lint/RescueException -- the test reads what ended the session
        @outcome = e
      ensure
        guest_out.close
        guest_in.close
      end
      @reader = Thread.new do
        @from.each_line { |line| @frames << JSON.parse(line) }
      end
      send_frame(ServeFrames.hello) if first
    end

    def send_frame(frame)
      @to.write(JSON.generate(frame) + "\n")
    end

    # The next frame the guest wrote, or nil when none comes within +wait+ seconds.
    def recv(wait = 5.0)
      Timeout.timeout(wait) { @frames.pop } # Queue#pop(timeout:) is Ruby 3.2
    rescue Timeout::Error
      nil
    end

    def request(operation, payload = nil, progress: false)
      @next += 1
      request_id = @next.to_s
      r = {
        "apiVersion" => "ext.plugin/v1",
        "id" => request_id,
        "plugin" => IDENT,
        "contract" => { "name" => CONTRACT[0], "version" => CONTRACT[1] },
        "operation" => operation,
        "payload" => payload,
        "deadline" => FUTURE
      }
      r["progress"] = { "eventsPerSecond" => 5 } if progress
      send_frame(r)
      request_id
    end

    # Reads frames up to the response to +request_id+: those before it, and it.
    def until_response(request_id)
      before = []
      loop do
        frame = recv
        raise "the guest wrote nothing" if frame.nil?
        return [before, frame] if !frame.key?("frame") && frame["id"] == request_id

        before << frame
      end
    end

    # Ends the host's side and waits for the guest to end; what ended the session, or nil.
    def finish
      @to.close unless @to.closed?
      raise "the guest did not end" if @served.join(10).nil?

      @reader.join(10) # the guest closed its end, so the reader reaches the end of the stream
      @from.close
      @outcome
    end
  end

  def self.started
    host = Host.new
    raise "no hello" unless host.recv["id"] == "hello"

    host
  end
end

class ServeFramesTest < Minitest::Test
  P = ExtIO::Plugin
  F = ExtIO::Plugin::Frames
  include ServeFrames

  def started
    ServeFrames.started
  end

  def test_the_descriptor_declares_progress_and_reverse
    host = ServeFrames::Host.new
    served = host.recv["payload"]
    ops = served["contracts"][0]["operations"].to_h { |op| [op["name"], op] }
    assert_equal true, ops["events"]["progress"]
    refute ops["quiet"].key?("progress")
    assert_equal "host.svc", served["reverse"][0]["name"]
    assert_nil host.finish
  end

  def test_events_come_in_order_before_the_response
    host = started
    request_id = host.request("events", progress: true)
    events, response = host.until_response(request_id)
    assert_equal 2, events.length, events.inspect
    events.each do |event|
      parent, = F.validate_progress_frame(event)
      assert_equal request_id, parent
    end
    assert_equal %w[log a], [events[0]["kind"], events[0]["text"]]
    assert_equal ["progress", 1, 2], [events[1]["kind"], events[1]["completed"], events[1]["total"]]
    assert_equal({ "sent" => [true, true] }, response["payload"])
    assert_nil host.recv(0.1), "a frame after the response"
    assert_nil host.finish
  end

  def test_a_flood_is_limited_counted_and_closed_with_a_marker
    host = started
    request_id = host.request("flood", progress: true)
    events, response = host.until_response(request_id)
    dropped = response["payload"]["dropped"]
    delivered = events.count { |e| e.key?("text") }
    reported = events.sum { |e| e.fetch("dropped", 0) }
    assert delivered >= 10 && delivered + dropped == 300 && reported == dropped, [delivered, dropped, reported].inspect
    # The drops still unreported when the handler returned are in a marker with only a kind and a count.
    last = events[-1]
    assert !last.key?("text") && last["kind"] == "log" && last["dropped"].positive?, last.inspect
    assert_nil host.finish
  end

  def test_an_operation_that_did_not_declare_progress_cannot_report
    host = started
    [false, true].each do |progress| # a rate for an operation that declared none is not a reporter either
      request_id = host.request("quiet", progress: progress)
      events, response = host.until_response(request_id)
      assert_equal [], events
      assert_equal true, response["payload"]["unsupported"]
    end
    # A declared operation the host asked no events of cannot report.
    request_id = host.request("events", progress: false)
    events, response = host.until_response(request_id)
    assert_equal [], events
    assert_equal "operation_failed", response["error"]["code"]
    assert_nil host.finish
  end

  def test_an_event_that_breaks_the_rules_is_refused_and_never_sent
    host = started
    request_id = host.request("invalid", progress: true)
    events, response = host.until_response(request_id)
    assert_equal [], events
    assert_equal ["invalid"] * 9, response["payload"]["codes"]
    assert_nil host.finish
  end

  def test_a_reverse_call_names_the_call_and_returns_the_answer
    host = started
    request_id = host.request("ask", { "op" => "sign", "payload" => { "n" => 1 } })
    request = host.recv
    F.validate_reverse_request(request)
    assert_equal ["reverse_request", "g1", request_id, "sign"], request.values_at("frame", "id", "parent", "operation")
    assert_equal({ "name" => "host.svc", "version" => "v1" }, request["contract"])
    assert_equal({ "n" => 1 }, request["payload"])
    host.send_frame(ServeFrames.answer("g1", payload: "signed"))
    assert_equal({ "result" => "signed" }, host.until_response(request_id)[1]["payload"])
    # The host's error comes back with its code, and ids go on counting.
    request_id = host.request("ask", { "op" => "sign" })
    assert_equal "g2", host.recv["id"]
    host.send_frame(ServeFrames.answer("g2", error: { "code" => "denied", "message" => "reverse call refused" }))
    assert_equal({ "code" => "denied" }, host.until_response(request_id)[1]["payload"])
    # An operation the descriptor does not list is refused without a frame.
    request_id = host.request("ask", { "op" => "nothere" })
    before, response = host.until_response(request_id)
    assert_equal [], before
    assert_equal({ "code" => "reverse_undeclared" }, response["payload"])
    assert_nil host.finish
  end

  def test_only_one_reverse_call_at_a_time_and_no_session_caller
    host = started
    _, response = host.until_response(host.request("session"))
    assert_equal({ "session" => false, "reverse" => true, "concurrent" => false }, response["payload"])
    # A thread other than the handler's cannot read the connection.
    before, response = host.until_response(host.request("thread"))
    assert_equal [[], { "codes" => ["unsupported"] }], [before, response["payload"]]
    assert_nil host.finish
  end

  def test_a_caller_ends_with_its_call
    host = started
    host.until_response(host.request("stale"))
    # A request names the call it belongs to, so a caller kept past the call writes nothing.
    raised = assert_raises(P::FrameError) { ServeFrames::KEPT[-1].call(ServeFrames::HOST, "sign", nil) }
    assert_equal "closed", raised.code
    assert_nil host.recv(0.1)
    assert_nil host.finish
  end

  def test_what_the_host_may_not_do_ends_the_session
    violations = [
      ["a second request during a call", nil],
      ["an unknown frame", { "frame" => "mystery" }],
      ["an answer to a request never issued", ServeFrames.answer("g99", payload: 1)],
      ["a progress frame from the host", { "frame" => "progress", "apiVersion" => "ext.plugin/v1", "parent" => "1", "kind" => "log" }],
      ["a malformed answer to the request waiting", ServeFrames.answer("g1", payload: 1, extra: 1)],
      ["an answer with both payload and error", ServeFrames.answer("g1", payload: 1, error: { "code" => "x", "message" => "m" })],
      ["an answer with neither payload nor error", ServeFrames.answer("g1")],
      ["a frame that is not an object", 7]
    ]
    violations.each do |what, frame|
      host = started
      request_id = host.request("ask", { "op" => "sign" })
      assert_equal "g1", host.recv["id"]
      if frame.nil?
        host.request("events", progress: true)
      else
        host.send_frame(frame)
      end
      assert_instance_of P::ProtocolError, host.finish, what
      # The call that was in flight is never answered.
      while (left = host.recv(0.05))
        assert left.key?("frame") || left["id"] != request_id, "#{what}: answered #{left}"
      end
    end
  end

  def test_a_connection_that_ends_releases_a_waiting_handler
    host = started
    request_id = host.request("ask", { "op" => "sign" })
    assert_equal "g1", host.recv["id"]
    # The host closes its end: the handler waiting for the answer is released, and the call is
    # still answered (the host may be reading), then the session ends.
    finishing = Thread.new { host.finish }
    _, response = host.until_response(request_id)
    assert_equal({ "frame" => "closed" }, response["payload"])
    finishing.join(10)
    assert_nil host.outcome
  end

  def test_a_guest_invoked_directly_has_no_frames
    guest = P::Guest.new(ServeFrames::DESC, ServeFrames::HANDLER, redact_errors: true)
    request = lambda do |operation|
      { "apiVersion" => "ext.plugin/v1", "id" => "1", "plugin" => ServeFrames::IDENT,
        "contract" => { "name" => "test.frames", "version" => "v1" }, "operation" => operation,
        "payload" => {}, "deadline" => ServeFrames::FUTURE, "progress" => {} }
    end
    assert_equal "operation_failed", guest.invoke(request.call("events"))["error"]["code"] # needs a reporter
    assert_equal "operation_failed", guest.invoke(request.call("ask"))["error"]["code"] # needs a reverse caller
    assert_equal({ "session" => false, "reverse" => false, "concurrent" => false }, guest.invoke(request.call("session"))["payload"])
  end

  def test_hello_may_not_carry_a_rate_or_a_parent
    [{ progress: {} }, { parent: "1" }].each do |extra|
      host = ServeFrames::Host.new(first: false)
      host.send_frame(ServeFrames.hello(**extra))
      assert_instance_of P::ProtocolError, host.finish, extra.inspect
      assert_nil host.recv(0.05)
    end
  end
end

class SplitTest < Minitest::Test
  P = ExtIO::Plugin

  def test_text_is_cut_on_characters
    pieces = P.split_progress_text(("é" * 1500) + "\xff".dup.force_encoding("UTF-8")) # 3000 bytes, then a replacement
    assert_equal ("é" * 1500) + "�", pieces.join
    assert(pieces.all? { |piece| piece.bytesize <= 2048 })
    assert_equal 2, pieces.length
    assert_equal [], P.split_progress_text("")
  end
end
