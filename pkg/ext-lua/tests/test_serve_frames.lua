-- The inline router behind serve: progress events, reverse calls and the rules a host that breaks
-- them meets. The host is a script in a coroutine: the guest resumes it whenever it needs input,
-- which is exactly when a real host would see the guest waiting.
local plugin = require("ext_io.plugin")
local json = require("ext_io.json")
local frames = require("ext_io.plugin.frames")
local t = require("support")

local tests = {}
local function test(name, f) tests[#tests + 1] = { name, f } end

local IDENT = { id = "lua/frames", revision = "r1" }
local HOST = { "host.svc", "v1" }
local FUTURE = "2999-01-01T00:00:00Z"
local DESC = {
  apiVersion = "ext.plugin/v1",
  identity = IDENT,
  contracts = {
    {
      name = "test.frames",
      version = "v1",
      operations = {
        { name = "events", progress = true },
        { name = "flood", progress = true },
        { name = "invalid", progress = true },
        { name = "quiet" },
        { name = "ask" },
        { name = "session" },
        { name = "coroutine" },
        { name = "stale" },
        { name = "timeout" },
      },
    },
  },
  reverse = { { name = "host.svc", version = "v1", operations = { { name = "sign" } } } },
}

local kept = {}

local function attempt(caller)
  local ok, err = pcall(caller.call, caller, HOST, "sign", nil)
  if ok then return "ok" end
  return err.code
end

local function handler(contract, operation, payload)
  local call = plugin.current_call()
  if operation == "events" then
    local first = call:progress(plugin.Progress.log("a"))
    local second = call:progress(plugin.Progress.fraction(1, 2))
    return { sent = { first == plugin.SENT, second == plugin.SENT } }
  elseif operation == "flood" then
    local dropped = 0
    for _ = 1, 300 do
      if call:progress(plugin.Progress.log("x")) == plugin.DROPPED then dropped = dropped + 1 end
    end
    return { dropped = dropped }
  elseif operation == "quiet" then
    local ok, err = pcall(call.progress, call, plugin.Progress.log("x"))
    return { unsupported = (not ok) and plugin.is_frame_error(err) and err.code == "unsupported" }
  elseif operation == "invalid" then
    local codes = {}
    for _, event in ipairs({
      plugin.Progress("mystery"),
      plugin.Progress.fraction(3, 2),
      plugin.Progress.log(string.rep("x", 2049)),
      plugin.Progress("log", "both"),
      plugin.Progress.log("\237\160\128"), -- an encoded surrogate is not UTF-8
      plugin.Progress("progress", "out"),
      plugin.Progress.fraction(-1, 0),
      plugin.Progress.fraction(true, 2),
      plugin.Progress.fraction(1.5, 2),
      plugin.Progress.log(7),
      false,
    }) do
      local ok, err = pcall(call.progress, call, event or nil)
      codes[#codes + 1] = ok and "sent" or err.code
    end
    return { codes = codes }
  elseif operation == "ask" then
    local ok, result = pcall(call.reverse.call, call.reverse, HOST, payload.op or "sign", payload.payload)
    if ok then return { result = result } end
    if plugin.is_remote_error(result) then return { code = result.code } end
    return { frame = result.code }
  elseif operation == "timeout" then
    return { result = call.reverse:call(HOST, "sign", nil, 0) }
  elseif operation == "session" then
    return { session = call.session_reverse ~= nil, reverse = call.reverse ~= nil, concurrent = (call.reverse and call.reverse.concurrent) and true or false }
  elseif operation == "coroutine" then
    -- Lua has no threads: a coroutine of the handler reads inline as the handler would.
    local co = coroutine.create(function() return attempt(call.reverse) end)
    local _, code = coroutine.resume(co)
    return { codes = { code } }
  elseif operation == "stale" then
    kept[#kept + 1] = call.reverse
    return nil
  end
  error("unknown operation " .. tostring(operation))
end

local function hello(fields)
  local r = { apiVersion = "ext.plugin/v1", id = "hello", operation = "plugin.hello", deadline = FUTURE }
  for k, v in pairs(fields or {}) do r[k] = v end
  return r
end

local function answer(request_id, body)
  local r = { frame = "reverse_response", apiVersion = "ext.plugin/v1", id = request_id }
  for k, v in pairs(body or {}) do r[k] = v end
  return r
end

-- Runs `script(host)` as the host of a guest served on in-memory streams. Returns the script's
-- results after the guest has ended.
local function hosted(script, first)
  local g = plugin.Guest(DESC, handler, { redact_errors = true })
  local state = { inbuf = "", closed = false, sent = false, done = false, outcome = nil, queue = {}, partial = "", next = 0 }
  local co
  local host = {}

  function host.send(frame)
    state.inbuf = state.inbuf .. (type(frame) == "string" and frame or json.encode(frame)) .. "\n"
    state.sent = true
  end

  --- The next frame the guest wrote, or nil when it wrote nothing more before waiting for input.
  function host.recv()
    while true do
      if #state.queue > 0 then return table.remove(state.queue, 1) end
      if state.done or not state.sent then return nil end
      coroutine.yield()
    end
  end

  function host.request(operation, payload, progress)
    state.next = state.next + 1
    local id = tostring(state.next)
    local r = {
      apiVersion = "ext.plugin/v1", id = id, plugin = IDENT,
      contract = { name = "test.frames", version = "v1" }, operation = operation,
      payload = payload == nil and json.null or payload, deadline = FUTURE,
    }
    if progress then r.progress = { eventsPerSecond = 5 } end
    host.send(r)
    return id
  end

  --- Reads frames up to the response to request_id: those before it, and it.
  function host.until_response(request_id)
    local before = {}
    while true do
      local frame = host.recv()
      assert(frame ~= nil, "the guest wrote nothing")
      if frame.frame == nil and frame.id == request_id then return before, frame end
      before[#before + 1] = frame
    end
  end

  --- Ends the host's side and waits for the guest to end; what ended the session, or nil.
  function host.finish()
    state.closed = true
    state.sent = true
    while not state.done do coroutine.yield() end
    return state.outcome
  end

  local results
  co = coroutine.create(function()
    if first ~= false then host.send(hello()) end
    results = { script(host) }
  end)

  local function resume()
    state.sent = false
    local ok, err = coroutine.resume(co)
    if not ok then
      state.script_error = state.script_error or debug.traceback(co, tostring(err))
      error("host script failed", 0)
    end
  end

  local input = {}
  function input.read(_, fmt)
    assert(fmt == "*L" or fmt == 1)
    while true do
      local e = state.inbuf:find("\n", 1, true)
      if e or (state.closed and state.inbuf ~= "") then
        if fmt == 1 then
          local c = state.inbuf:sub(1, 1)
          state.inbuf = state.inbuf:sub(2)
          return c
        end
        e = e or #state.inbuf
        local line = state.inbuf:sub(1, e)
        state.inbuf = state.inbuf:sub(e + 1)
        return line
      end
      if state.closed or coroutine.status(co) == "dead" then return nil end
      resume()
    end
  end

  local output = {}
  function output.write(self, ...)
    for i = 1, select("#", ...) do
      local piece = state.partial .. select(i, ...)
      state.partial = ""
      for line, nl in piece:gmatch("([^\n]*)(\n?)") do
        if nl == "\n" then
          state.queue[#state.queue + 1] = json.decode(line)
        else
          state.partial = state.partial .. line
        end
      end
    end
    return self
  end
  function output.flush() return true end

  local ok, err = pcall(plugin.serve, g, input, output)
  state.done = true
  state.outcome = (not ok) and err or nil
  if state.script_error then error(state.script_error, 0) end
  while coroutine.status(co) ~= "dead" do
    local resumed, why = coroutine.resume(co)
    if not resumed then error(debug.traceback(co, tostring(why)), 0) end
  end
  if state.script_error then error(state.script_error, 0) end
  return (unpack or table.unpack)(results)
end

local function started(host)
  assert(host.recv().id == "hello")
end

test("the descriptor declares progress and reverse", function()
  hosted(function(host)
    local served = host.recv().payload
    local ops = {}
    for _, op in ipairs(served.contracts[1].operations) do ops[op.name] = op end
    t.ok(ops.events.progress == true)
    t.eq(ops.quiet.progress, nil)
    t.eq(served.reverse[1].name, "host.svc")
    t.eq(host.finish(), nil)
  end)
end)

test("events come in order before the response", function()
  hosted(function(host)
    started(host)
    local request_id = host.request("events", nil, true)
    local events, response = host.until_response(request_id)
    t.eq(#events, 2, t.show(events))
    for _, event in ipairs(events) do
      local parent = frames.validate_progress_frame(t.copy(event))
      t.eq(parent, request_id)
    end
    t.eq({ events[1].kind, events[1].text }, { "log", "a" })
    t.eq({ events[2].kind, events[2].completed, events[2].total }, { "progress", 1, 2 })
    t.eq(response.payload, { sent = { true, true } })
    t.eq(host.recv(), nil, "a frame after the response")
    t.eq(host.finish(), nil)
  end)
end)

test("a flood is limited, counted and closed with a marker", function()
  hosted(function(host)
    started(host)
    local request_id = host.request("flood", nil, true)
    local events, response = host.until_response(request_id)
    local dropped = response.payload.dropped
    local delivered, reported = 0, 0
    for _, e in ipairs(events) do
      if e.text ~= nil then delivered = delivered + 1 end
      reported = reported + (e.dropped or 0)
    end
    t.ok(delivered >= 10 and delivered + dropped == 300 and reported == dropped, string.format("%d %d %d", delivered, dropped, reported))
    -- The drops still unreported when the handler returned are in a marker with only a kind and a count.
    local last = events[#events]
    t.ok(last.text == nil and last.kind == "log" and last.dropped > 0, t.show(last))
    t.eq(host.finish(), nil)
  end)
end)

test("an operation that did not declare progress cannot report", function()
  hosted(function(host)
    started(host)
    for _, progress in ipairs({ false, true }) do -- a rate for an operation that declared none is not a reporter either
      local request_id = host.request("quiet", nil, progress)
      local events, response = host.until_response(request_id)
      t.eq(#events, 0)
      t.ok(response.payload.unsupported == true)
    end
    -- A declared operation the host asked no events of cannot report.
    local request_id = host.request("events", nil, false)
    local events, response = host.until_response(request_id)
    t.eq(#events, 0)
    t.eq(response.error.code, "operation_failed")
    t.eq(host.finish(), nil)
  end)
end)

test("an event that breaks the rules is refused and never sent", function()
  hosted(function(host)
    started(host)
    local request_id = host.request("invalid", nil, true)
    local events, response = host.until_response(request_id)
    t.eq(#events, 0)
    local want = {}
    for i = 1, 11 do want[i] = "invalid" end
    t.eq(response.payload.codes, want)
    t.eq(host.finish(), nil)
  end)
end)

test("a reverse call names the call and returns the answer", function()
  hosted(function(host)
    started(host)
    local request_id = host.request("ask", { op = "sign", payload = { n = 1 } })
    local request = host.recv()
    frames.validate_reverse_request(request)
    t.eq({ request.frame, request.id, request.parent, request.operation }, { "reverse_request", "g1", request_id, "sign" })
    t.eq(request.contract, { name = "host.svc", version = "v1" })
    t.eq(request.payload, { n = 1 })
    -- The call's own limit: max_call_seconds from now, in milliseconds.
    t.ok(request.deadline:find("^%d%d%d%d%-%d%d%-%d%dT%d%d:%d%d:%d%d%.%d%d%dZ$"), request.deadline)
    local limit = plugin.current_call == nil or require("ext_io.plugin.core").parse_deadline(request.deadline)
    t.ok(limit > plugin.clock.now() + 25 and limit <= plugin.clock.now() + 31, request.deadline)
    host.send(answer("g1", { payload = "signed" }))
    local _, response = host.until_response(request_id)
    t.eq(response.payload, { result = "signed" })
    -- The host's error comes back with its code, and ids go on counting.
    request_id = host.request("ask", { op = "sign" })
    request = host.recv()
    t.eq(request.id, "g2")
    t.ok(request.payload == json.null)
    host.send(answer("g2", { error = { code = "denied", message = "reverse call refused" } }))
    _, response = host.until_response(request_id)
    t.eq(response.payload, { code = "denied" })
    -- An operation the descriptor does not list is refused without a frame.
    request_id = host.request("ask", { op = "nothere" })
    local before
    before, response = host.until_response(request_id)
    t.eq(#before, 0)
    t.eq(response.payload, { code = "reverse_undeclared" })
    -- A timeout bounds the deadline the host is given.
    request_id = host.request("timeout")
    request = host.recv()
    local bound = require("ext_io.plugin.core").parse_deadline(request.deadline)
    t.ok(bound <= plugin.clock.now() + 1, request.deadline)
    host.send(answer("g3", { payload = json.null }))
    _, response = host.until_response(request_id)
    t.ok(response.payload.result == json.null, t.show(response))
    t.eq(host.finish(), nil)
  end)
end)

test("one reverse call at a time and no session caller", function()
  hosted(function(host)
    started(host)
    local request_id = host.request("session")
    local _, response = host.until_response(request_id)
    t.eq(response.payload, { session = false, reverse = true, concurrent = false })
    -- A coroutine of the handler calls the host like the handler.
    request_id = host.request("coroutine")
    local request = host.recv()
    t.eq(request.frame, "reverse_request")
    host.send(answer(request.id, { payload = 1 }))
    _, response = host.until_response(request_id)
    t.eq(response.payload, { codes = { "ok" } })
    t.eq(host.finish(), nil)
  end)
end)

test("a caller ends with its call", function()
  hosted(function(host)
    started(host)
    host.until_response(host.request("stale"))
    -- A request names the call it belongs to, so a caller kept past the call writes nothing.
    local err = t.raises(function() kept[#kept]:call(HOST, "sign", nil) end)
    t.ok(plugin.is_frame_error(err) and err.code == "closed")
    t.eq(host.recv(), nil)
    t.eq(host.finish(), nil)
  end)
end)

test("what the host may not do ends the session", function()
  local violations = {
    { "a second request during a call", nil },
    { "an unknown frame", { frame = "mystery" } },
    { "an answer to a request never issued", answer("g99", { payload = 1 }) },
    { "a progress frame from the host", { frame = "progress", apiVersion = "ext.plugin/v1", parent = "1", kind = "log" } },
    { "a malformed answer to the request waiting", answer("g1", { payload = 1, extra = 1 }) },
    { "an answer with both payload and error", answer("g1", { payload = 1, error = { code = "x", message = "m" } }) },
    { "an answer with neither payload nor error", answer("g1") },
    { "a frame that is not an object", 7 },
    { "an answer that is not JSON", "{oops" },
  }
  for _, v in ipairs(violations) do
    local what, frame = v[1], v[2]
    hosted(function(host)
      started(host)
      local request_id = host.request("ask", { op = "sign" })
      t.eq(host.recv().id, "g1")
      if frame == nil then
        host.request("events", nil, true)
      else
        host.send(type(frame) == "number" and tostring(frame) or frame)
      end
      local failure = host.finish()
      t.ok(plugin.is_protocol_error(failure), what .. ": " .. tostring(failure))
      -- The call that was in flight is never answered.
      while true do
        local left = host.recv()
        if left == nil then break end
        t.ok(left.frame ~= nil or left.id ~= request_id, what .. ": answered " .. t.show(left))
      end
    end)
  end
end)

test("a connection that ends releases a waiting handler", function()
  hosted(function(host)
    started(host)
    local request_id = host.request("ask", { op = "sign" })
    t.eq(host.recv().id, "g1")
    -- The host closes its end: the handler waiting for the answer is released, and the call is
    -- still answered (the host may be reading), then the session ends.
    t.eq(host.finish(), nil)
    local _, response = host.until_response(request_id)
    t.eq(response.payload, { frame = "closed" })
  end)
end)

test("a guest invoked directly has no frames", function()
  local g = plugin.Guest(DESC, handler, { redact_errors = true })
  local function request(operation)
    return {
      apiVersion = "ext.plugin/v1", id = "1", plugin = IDENT,
      contract = { name = "test.frames", version = "v1" }, operation = operation,
      payload = {}, deadline = FUTURE, progress = {},
    }
  end
  t.eq(g:invoke(request("events")).error.code, "operation_failed") -- needs a reporter
  t.eq(g:invoke(request("ask")).error.code, "operation_failed") -- needs a reverse caller
  t.eq(g:invoke(request("session")).payload, { session = false, reverse = false, concurrent = false })
end)

test("hello may not carry a rate or a parent", function()
  for _, extra in ipairs({ { progress = {} }, { parent = "1" } }) do
    hosted(function(host)
      host.send(hello(extra))
      t.ok(plugin.is_protocol_error(host.finish()), t.show(extra))
      t.eq(host.recv(), nil)
    end, false)
  end
end)

test("the byte reader of Lua 5.1 frames the same way", function()
  local core = require("ext_io.plugin.core")
  local saved = core.byte_reader
  core.byte_reader = true
  local ok, err = pcall(function()
    hosted(function(host)
      started(host)
      local request_id = host.request("ask", { op = "sign" })
      t.eq(host.recv().id, "g1")
      host.send(answer("g1", { payload = "signed" }))
      local _, response = host.until_response(request_id)
      t.eq(response.payload, { result = "signed" })
      t.eq(host.finish(), nil)
    end)
    local out = t.writer()
    local e = t.raises(function() plugin.serve(plugin.Guest(DESC, handler), t.reader(string.rep("x", plugin.MAX_FRAME_BYTES + 5)), out) end)
    t.ok(plugin.is_protocol_error(e) and e.message == "frame too large", tostring(e))
    e = t.raises(function() plugin.serve(plugin.Guest(DESC, handler), t.reader('{"a":1}'), out) end)
    t.ok(plugin.is_protocol_error(e) and e.message == "unexpected EOF inside a frame", tostring(e))
  end)
  core.byte_reader = saved
  if not ok then error(err, 0) end
end)

test("text is cut on characters", function()
  local e = "\195\169"
  local pieces = plugin.split_progress_text(string.rep(e, 1500) .. "\237\160\128") -- 3000 bytes, then a replacement
  t.eq(table.concat(pieces), string.rep(e, 1500) .. "\239\191\189")
  for _, p in ipairs(pieces) do t.ok(#p <= 2048) end
  t.eq(#pieces, 2)
  t.eq(#plugin.split_progress_text(""), 0)
  t.eq(plugin.split_progress_text("a\255b"), { "a\239\191\189b" })
  local astral = string.rep("\240\159\152\128", 600) -- 2400 bytes of four-byte characters
  pieces = plugin.split_progress_text(astral)
  t.eq(#pieces[1], 2048)
  t.eq(table.concat(pieces), astral)
end)

return tests
