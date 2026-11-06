-- The guest: hello, request validation, errors, strict framing, descriptors and standard output.
local plugin = require("ext_io.plugin")
local json = require("ext_io.json")
local t = require("support")

local tests = {}
local function test(name, f) tests[#tests + 1] = { name, f } end

local IDENT = { id = "example/p", revision = "r1" }
local DESC = {
  apiVersion = "ext.plugin/v1",
  identity = IDENT,
  contracts = {
    {
      name = "example.echo",
      version = "v1",
      operations = { { name = "echo" }, { name = "seen", surface = "observation" }, { name = "boom" }, { name = "busy" }, { name = "noisy" } },
    },
  },
}
local FUTURE = "2999-01-01T00:00:00Z"

local function with(base, fields)
  local r = t.copy(base)
  for k, v in pairs(fields or {}) do r[k] = v end
  return r
end

local function req(op, fields)
  local r = {
    apiVersion = "ext.plugin/v1",
    id = "c1",
    plugin = IDENT,
    contract = { name = "example.echo", version = "v1" },
    operation = op or "echo",
    deadline = FUTURE,
    payload = { v = 1 },
  }
  return with(r, fields)
end

local function handler(contract, op, payload)
  if op == "boom" then error(string.rep("x", 5000)) end
  if op == "busy" then error(plugin.remote_error("busy", "later", 250)) end
  if op == "noisy" then
    print("stray output")
    return "ok"
  end
  return payload
end

local function hello(fields)
  return with({
    apiVersion = "ext.plugin/v1", id = "hello", plugin = { id = "", revision = "" },
    contract = { name = "", version = "" }, operation = "plugin.hello", deadline = FUTURE,
  }, fields)
end

local function run(data, opts)
  local out = t.writer()
  local g = plugin.Guest(DESC, handler, opts)
  local ok, err = pcall(plugin.serve, g, t.reader(data), out)
  local lines = {}
  for i, line in ipairs(out:lines()) do lines[i] = json.decode(line) end
  return lines, (not ok) and err or nil
end

test("hello and echo", function()
  local out, err = run(t.frames(hello(), req()))
  t.eq(err, nil)
  t.eq(out[1].payload, DESC)
  t.eq(out[2], { apiVersion = "ext.plugin/v1", id = "c1", payload = { v = 1 } })
end)

test("the frames are written byte for byte as Python writes them", function()
  local out = t.writer()
  local g = plugin.Guest(DESC, handler)
  plugin.serve(g, t.reader(t.frames(hello(), req(), req("busy"), req("missing"))), out)
  local lines = out:lines()
  t.eq(lines[1], '{"apiVersion":"ext.plugin/v1","id":"hello","payload":{"apiVersion":"ext.plugin/v1","identity":{"id":"example/p","revision":"r1"},"contracts":[{"name":"example.echo","version":"v1","operations":[{"name":"echo"},{"name":"seen","surface":"observation"},{"name":"boom"},{"name":"busy"},{"name":"noisy"}]}]}}')
  t.eq(lines[2], '{"apiVersion":"ext.plugin/v1","id":"c1","payload":{"v":1}}')
  t.eq(lines[3], '{"apiVersion":"ext.plugin/v1","id":"c1","error":{"code":"busy","message":"later","retryAfterMilliseconds":250}}')
  t.eq(lines[4], '{"apiVersion":"ext.plugin/v1","id":"c1","error":{"code":"invalid_request","message":"request does not match selected contract"}}')
end)

test("hello must be first and strict", function()
  for _, bad in ipairs({
    req(), hello({ id = "x" }), hello({ operation = "echo" }), hello({ payload = json.null }),
    hello({ plugin = IDENT }), hello({ surface = "s" }), hello({ deadline = "2000-01-01T00:00:00Z" }),
  }) do
    local out, err = run(t.frames(bad))
    t.ok(plugin.is_protocol_error(err), t.show(bad))
    t.eq(#out, 0)
  end
end)

test("an empty result is null", function()
  local g = plugin.Guest(DESC, function() return nil end)
  local resp = g:invoke(req())
  t.ok(resp.payload == json.null)
  t.eq(json.encode(resp), '{"apiVersion":"ext.plugin/v1","id":"c1","payload":null}')
end)

test("request validation", function()
  local missing = req()
  missing.deadline = nil
  local cases = {
    req(nil, { apiVersion = "ext.plugin/v2" }),
    req(nil, { id = "-bad" }),
    req(nil, { plugin = { id = "example/p", revision = "r2" } }),
    req(nil, { contract = { name = "example.echo", version = "v2" } }),
    req("missing"),
    req("seen"), -- declared surface missing
    req(nil, { deadline = "0001-01-01T00:00:00Z" }),
    req("plugin.hello"),
    missing,
  }
  local g = plugin.Guest(DESC, handler)
  for _, r in ipairs(cases) do
    local resp = g:invoke(r)
    t.eq(resp.error and resp.error.code, "invalid_request", t.show(r))
    t.eq(resp.payload, nil)
  end
  t.eq(g:invoke(req("seen", { surface = "observation" })).payload, { v = 1 })
  t.eq(g:invoke(req("seen", { surface = "other" })).error.code, "invalid_request")
end)

test("envelopes Go would not decode end the stream", function()
  local g = plugin.Guest(DESC, handler)
  for _, r in ipairs({
    req(nil, { extra = 1 }), req(nil, { id = 7 }), req(nil, { operation = json.null }),
    req(nil, { plugin = { id = "example/p", revision = "r1", other = "x" } }),
    req(nil, { plugin = { id = 1, revision = "r1" } }), req(nil, { deadline = 5 }),
    req(nil, { parent = 1 }), req(nil, { progress = { eventsPerSecond = 1.5 } }),
    req(nil, { progress = { other = 1 } }), req(nil, { progress = 3 }),
  }) do
    t.ok(plugin.is_protocol_error(t.raises(function() g:invoke(r) end, t.show(r))))
  end
  t.eq(g:invoke(req(nil, { parent = json.null, plugin = with(IDENT, { version = "" }) })).payload, { v = 1 })
end)

test("an expired deadline", function()
  local resp = plugin.Guest(DESC, handler):invoke(req(nil, { deadline = "2000-01-01T00:00:00Z" }))
  t.eq(resp.error, { code = "provider_error", message = "deadline exceeded" })
end)

test("deadline offsets and fractions", function()
  for _, stamp in ipairs({ "2999-01-01T00:00:00.123456789Z", "2999-01-01T05:30:00+05:30", "2999-01-01T00:00:00.5-07:00", "2999-01-01t00:00:00z" }) do
    t.eq(plugin.Guest(DESC, handler):invoke(req(nil, { deadline = stamp })).payload, { v = 1 }, stamp)
  end
  for _, stamp in ipairs({ "2999-02-30T00:00:00Z", "2999-01-01T24:00:00Z", "2999-01-01T00:00:60Z", "2999-01-01T00:00:00+24:00", "2999-01-01 00:00:00Z", "2999-01-01T00:00:00", "0000-01-01T00:00:00Z" }) do
    t.eq(plugin.Guest(DESC, handler):invoke(req(nil, { deadline = stamp })).error.code, "invalid_request", stamp)
  end
end)

test("errors", function()
  local g = plugin.Guest(DESC, handler)
  t.eq(g:invoke(req("busy")).error, { code = "busy", message = "later", retryAfterMilliseconds = 250 })
  local boom = g:invoke(req("boom")).error
  t.eq(boom.code, "provider_error")
  t.eq(#boom.message, 4096)
  local redacted = plugin.Guest(DESC, handler, { redact_errors = true }):invoke(req("boom")).error
  t.eq(redacted, { code = "operation_failed", message = "plugin operation failed" })
  local multibyte = plugin.Guest(DESC, function() error(string.rep("\195\169", 4000), 0) end)
  local message = multibyte:invoke(req()).error.message
  t.ok(#message <= 4096 and json.utf8_valid(message), "cut on a character")
  t.eq(#message, 4096)
  local binary = plugin.Guest(DESC, function() error("bad \255 byte", 0) end)
  t.eq(binary:invoke(req()).error.message, "bad ? byte")
  local plain = plugin.Guest(DESC, function() error({}) end)
  t.ok(plain:invoke(req()).error.message:find("^table") ~= nil)
  t.eq(plugin.Guest(DESC, function() error(plugin.remote_error("ok", "m", 0)) end):invoke(req()).error, { code = "ok", message = "m" })
end)

test("remote_error validation", function()
  for _, args in ipairs({
    { "Bad", "m" }, { "ok", string.rep("x", 4097) }, { "ok", "m", -1 }, { "ok", "m", plugin.MAX_RETRY_AFTER_MS + 1 },
    { "ok", "m", true }, { "ok", "m", 1.5 }, { "ok", 7 }, { "ok", "\255" }, { "", "m" },
  }) do
    t.raises(function() plugin.remote_error(args[1], args[2], args[3]) end, t.show(args))
  end
  local e = plugin.remote_error("ok", "m", plugin.MAX_RETRY_AFTER_MS)
  t.ok(plugin.is_remote_error(e) and e.code == "ok" and e.message == "m" and tostring(e) == "m")
end)

test("an unserializable result is an error", function()
  t.eq(plugin.Guest(DESC, function() return print end):invoke(req()).error.code, "provider_error")
  t.eq(plugin.Guest(DESC, function() return 0 / 0 end):invoke(req()).error.code, "provider_error")
  t.eq(plugin.Guest(DESC, function() return { 1, x = 2 } end):invoke(req()).error.code, "provider_error")
  t.eq(plugin.Guest(DESC, function() return "\255" end):invoke(req()).error.code, "provider_error")
end)

test("current_call", function()
  local seen
  local function h()
    local info = plugin.current_call()
    seen = { op = info.operation, id = info.request_id, ok = info:remaining() > 0, contract = info.contract_name, version = info.contract_version }
  end
  plugin.Guest(DESC, h):invoke(req())
  t.eq(seen, { op = "echo", id = "c1", ok = true, contract = "example.echo", version = "v1" })
  t.eq(plugin.current_call(), nil)
  -- the call is bounded by max_call_seconds, on the guest's clock
  local now = 1000
  local g = plugin.Guest(DESC, function() return plugin.current_call():remaining() end, { clock = function() return now end, max_call_seconds = 5 })
  t.eq(g:invoke(req()).payload, 5)
  local late = plugin.Guest(DESC, function() now = now + 10 end, { clock = function() return now end, max_call_seconds = 5 })
  t.eq(late:invoke(req()).error.message, "deadline exceeded")
end)

test("the handler gets nil for a null or absent payload", function()
  local got = {}
  local g = plugin.Guest(DESC, function(_, _, p) got[#got + 1] = p == nil end)
  g:invoke(req(nil, { payload = json.null }))
  local r = req()
  r.payload = nil
  g:invoke(r)
  t.eq(got, { true, true })
end)

test("strict JSON ends the stream", function()
  local bad = {
    '{"apiVersion":"ext.plugin/v1","id":"hello","id":"hello"}\n',
    '{"apiVersion":"ext.plugin/v1","unknown":1}\n',
    "[1,2]\n",
    '{"a":NaN}\n',
    '{"a":"\\ud800"}\n',
    '{"a":1} {"b":2}\n',
    "\255\254\n",
    '{"a":' .. string.rep("[", 70) .. string.rep("]", 70) .. "}\n",
  }
  for _, raw in ipairs(bad) do
    local out, err = run(raw)
    t.ok(plugin.is_protocol_error(err), raw)
    t.eq(#out, 0)
  end
  for _, raw in ipairs(t.fixture("invalid.json")) do
    local out, err = run(raw .. "\n")
    t.ok(plugin.is_protocol_error(err), raw)
    t.eq(#out, 0)
  end
end)

test("nesting is bounded at 64 levels", function()
  local function nested(n) return string.rep("[", n) .. string.rep("]", n) end
  local out, err
  out, err = run(t.frames(hello()) .. json.encode(req(nil, { payload = json.decode(nested(64)) })) .. "\n")
  t.eq(err, nil)
  t.eq(#out, 2)
  out, err = run(t.frames(hello()) .. json.encode(req(nil, { payload = json.decode(nested(65)) })) .. "\n")
  t.ok(plugin.is_protocol_error(err) and #out == 1)
end)

test("payload strictness after hello", function()
  local out, err = run(t.frames(hello()) .. '{"apiVersion":"ext.plugin/v1","id":"c1","operation":"echo","deadline":"2999-01-01T00:00:00Z","payload":{"a":1,"a":2}}\n')
  t.ok(plugin.is_protocol_error(err))
  t.eq(#out, 1)
end)

test("an astral pair is allowed", function()
  local out, err = run(t.frames(hello(), req(nil, { payload = "\240\159\152\128" })))
  t.eq(err, nil)
  t.eq(out[2].payload, "\240\159\152\128")
end)

test("the frame limit", function()
  local limit = plugin.MAX_FRAME_BYTES
  local _, err = run(string.rep("x", limit + 1) .. "\n")
  t.ok(plugin.is_protocol_error(err) and err.message == "frame too large", tostring(err))
  _, err = run(string.rep("x", limit + 5)) -- no newline
  t.ok(plugin.is_protocol_error(err) and err.message == "frame too large", tostring(err))
  _, err = run(t.frames(hello()):sub(1, -2)) -- EOF inside a frame
  t.ok(plugin.is_protocol_error(err) and err.message == "unexpected EOF inside a frame", tostring(err))
end)

test("an oversize response becomes an error", function()
  local g = plugin.Guest(DESC, function() return string.rep("x", plugin.MAX_FRAME_BYTES) end)
  local out = t.writer()
  plugin.serve(g, t.reader(t.frames(hello(), req())), out)
  t.eq(json.decode(out:lines()[2]).error.code, "response_too_large")
end)

test("a clean end", function()
  local out, err = run("")
  t.eq(#out, 0)
  t.eq(err, nil)
end)

test("descriptor loading", function()
  local dir = os.tmpname()
  os.remove(dir)
  local bare, pkg = dir .. "-descriptor.json", dir .. "-plugin.json"
  t.write_file(bare, json.encode(DESC))
  t.write_file(pkg, json.encode({ apiVersion = "ext.package/v1", descriptor = DESC, artifacts = json.array() }))
  t.eq(plugin.load_descriptor(bare), DESC)
  t.eq(plugin.load_descriptor(pkg), DESC)
  t.ok(getmetatable(plugin.Guest(pkg, handler)) == plugin.Guest)
  t.write_file(pkg, json.encode({ apiVersion = "other", descriptor = DESC }))
  t.raises(function() plugin.load_descriptor(pkg) end, "other package apiVersion")
  t.write_file(pkg, '{"apiVersion":"ext.package/v1","descriptor":{},"descriptor":{}}')
  t.raises(function() plugin.load_descriptor(pkg) end, "duplicate key")
  t.raises(function() plugin.load_descriptor(dir .. "-missing.json") end, "missing file")
  os.remove(bare)
  os.remove(pkg)
end)

test("the hello answer keeps the descriptor file's key order", function()
  local path = os.tmpname()
  t.write_file(path, '{"contracts":[{"operations":[{"name":"echo"}],"version":"v1","name":"c"}],"reverse":null,"identity":{"revision":"r","id":"a"},"apiVersion":"ext.plugin/v1"}')
  local out = t.writer()
  plugin.serve(plugin.Guest(path, handler), t.reader(t.frames(hello())), out)
  t.eq(out:lines()[1], '{"apiVersion":"ext.plugin/v1","id":"hello","payload":{"contracts":[{"operations":[{"name":"echo"}],"version":"v1","name":"c"}],"reverse":null,"identity":{"revision":"r","id":"a"},"apiVersion":"ext.plugin/v1"}}')
  os.remove(path)
  -- A descriptor written in Lua: an empty reverse list is an array on the wire.
  local d = t.copy(DESC)
  d.reverse = {}
  t.eq(json.encode(plugin.Guest(d, handler):handshake().reverse), "[]")
end)

test("descriptor validation", function()
  local function mutate(f)
    local d = t.copy(DESC)
    f(d)
    return d
  end
  local bad = {
    mutate(function(d) d.apiVersion = "x" end),
    mutate(function(d) d.identity.id = "Bad" end),
    mutate(function(d) d.identity.version = json.null end),
    mutate(function(d) d.contracts = json.array() end),
    mutate(function(d) d.contracts[1].operations = json.array() end),
    mutate(function(d) table.insert(d.contracts[1].operations, { name = "plugin.hello" }) end),
    mutate(function(d) table.insert(d.contracts[1].operations, { name = "echo" }) end),
    mutate(function(d) table.insert(d.contracts, t.copy(d.contracts[1])) end),
    mutate(function(d) d.extra = 1 end),
    mutate(function(d) d.contracts[1].operations[1].progress = "yes" end),
    mutate(function(d) d.reverse = { { name = "h", version = "v1", operations = { { name = "x", progress = true } } } } end),
    mutate(function(d) d.reverse = json.object() end),
    "not a table",
  }
  for _, d in ipairs(bad) do
    t.raises(function() plugin.validate_descriptor(d) end, t.show(d))
  end
  t.raises(function() plugin.Guest(bad[1], handler) end, "Guest validates")
  t.raises(function() plugin.Guest(DESC, nil) end, "handler required")
  t.raises(function() plugin.Guest(DESC, handler, { max_call_seconds = 0 }) end, "positive")
  plugin.validate_descriptor(mutate(function(d) d.reverse = json.null end))
  plugin.validate_descriptor(mutate(function(d) d.contracts[1].operations[1].surface = json.null end))
  plugin.Guest(DESC, setmetatable({}, { __call = function() end }))
end)

test("standard output carries only frames", function()
  local lua = t.interpreter()
  local dir = os.tmpname()
  os.remove(dir)
  local script, input, output, errors = dir .. "-guest.lua", dir .. "-in", dir .. "-out", dir .. "-err"
  t.write_file(script, string.format([[
package.path = %q .. "/?.lua;" .. %q .. "/?/init.lua;" .. package.path
local plugin = require("ext_io.plugin")
plugin.serve_stdio(plugin.json.decode(%q), function()
  print("noise on stdout")
  io.write("more noise\n")
  io.stdout:write("and more\n")
  return "ok"
end)
print("after serving")
]], t.ROOT, t.ROOT, json.encode(DESC)))
  t.write_file(input, t.frames(hello(), req()))
  local status = os.execute(string.format("%q %q < %q > %q 2> %q", lua, script, input, output, errors))
  t.ok(status == true or status == 0, "the guest failed: " .. t.read_file(errors))
  local lines = t.writer()
  lines.parts = { t.read_file(output) }
  local got = lines:lines()
  t.eq(#got, 3, t.read_file(output))
  t.eq(json.decode(got[2]).payload, "ok")
  t.eq(got[3], "after serving")
  local stderr = t.read_file(errors)
  t.ok(stderr:find("noise on stdout", 1, true) and stderr:find("more noise", 1, true) and stderr:find("and more", 1, true), stderr)
  for _, p in ipairs({ script, input, output, errors }) do os.remove(p) end
end)

return tests
