-- The capability contracts (ext.health, ext.configuration, ext.stream) and their Router.
local plugin = require("ext_io.plugin")
local json = require("ext_io.json")
local cap = require("ext_io.plugin.capability")
local t = require("support")

local tests = {}
local function test(name, f) tests[#tests + 1] = { name, f } end

local IDENT = { id = "example/p", revision = "r1" }
local DESC = {
  apiVersion = "ext.plugin/v1",
  identity = IDENT,
  contracts = {
    { name = "ext.health", version = "v1", operations = { { name = "check", surface = "observation" } } },
    { name = "ext.configuration", version = "v1", operations = { { name = "update", surface = "configuration" } } },
    { name = "ext.stream", version = "v1", operations = { { name = "open" }, { name = "read" }, { name = "close" } } },
  },
}

local function request(contract, operation, payload, surface)
  return {
    apiVersion = "ext.plugin/v1", id = "c1", plugin = IDENT,
    contract = { name = contract, version = "v1" }, operation = operation,
    deadline = "2999-01-01T00:00:00Z", payload = payload, surface = surface,
  }
end

local function setup()
  local s = { updates = {}, closed = {} }
  s.router = cap.Router()
  s.router:health(function() return { status = "degraded", ready = false } end)
  s.router:configuration(function(i, r, v) s.updates[#s.updates + 1] = { i, r, v } end)
  s.router:stream(
    function() return "s1" end,
    function(_, seq, limit)
      local items = {}
      for i = 1, math.min(limit, 2) do items[i] = { v = seq } end
      return { items = items, done = seq >= 2 }
    end,
    function(id) s.closed[#s.closed + 1] = id end
  )
  s.guest = plugin.Guest(DESC, s.router)
  function s.call(contract, operation, payload, surface)
    -- through the wire form, as a host sends it
    return s.guest:invoke(json.decode(json.encode(request(contract, operation, payload, surface))))
  end
  return s
end

local function with(base, fields)
  local r = t.copy(base)
  for k, v in pairs(fields) do r[k] = v end
  return r
end

test("health", function()
  local s = setup()
  t.eq(s.call("ext.health", "check", {}, "observation").payload, { status = "degraded", ready = false })
  t.eq(s.call("ext.health", "check", { x = 1 }, "observation").error.code, "invalid_input")
end)

test("the health result is validated", function()
  local bad = cap.Router():health(function() return { status = "great", ready = true } end)
  local r = plugin.Guest(DESC, bad):invoke(request("ext.health", "check", {}, "observation"))
  t.ok(r.error ~= nil and r.error.code ~= "invalid_request", t.show(r))
end)

test("configuration", function()
  local s = setup()
  local ok = { instance = "one", revision = "r1", values = { name = "a" } }
  t.eq(s.call("ext.configuration", "update", ok, "configuration").payload, { instance = "one", revision = "r1" })
  t.eq(s.updates, { { "one", "r1", { name = "a" } } })
  for _, bad in ipairs({
    with(ok, { instance = "" }), with(ok, { revision = "" }),
    with(ok, { instance = string.rep("x", 257) }), with(ok, { revision = string.rep("r", 257) }),
    { instance = "one", revision = "r1" }, with(ok, { extra = 1 }), with(ok, { instance = 1 }),
  }) do
    t.eq(s.call("ext.configuration", "update", bad, "configuration").error.code, "invalid_input", t.show(bad))
  end
  t.eq(#s.updates, 1)
  local long_ok = with(ok, { instance = string.rep("x", 256), revision = string.rep("r", 256) })
  t.eq(s.call("ext.configuration", "update", long_ok, "configuration").payload.instance, string.rep("x", 256))
  -- characters, not bytes
  t.eq(s.call("ext.configuration", "update", with(ok, { instance = string.rep("\195\169", 256) }), "configuration").payload.revision, "r1")
end)

test("stream", function()
  local s = setup()
  t.eq(s.call("ext.stream", "open", { parameters = {} }).payload, { id = "s1" })
  t.eq(s.call("ext.stream", "read", { id = "s1", sequence = 1, limit = 5 }).payload, { items = { { v = 1 }, { v = 1 } }, done = false })
  t.eq(s.call("ext.stream", "read", { id = "s1", sequence = 2, limit = 1 }).payload.done, true)
  for _, bad in ipairs({
    { id = "", sequence = 1, limit = 1 },
    { id = string.rep("i", 65), sequence = 1, limit = 1 },
    { id = "s1", sequence = 0, limit = 1 },
    { id = "s1", sequence = 1, limit = 0 },
    { id = "s1", sequence = 1, limit = -1 },
    { id = "s1", sequence = 1, limit = cap.MAX_STREAM_ITEMS + 1 },
    { id = "s1", sequence = true, limit = 1 },
    { id = "s1", sequence = 1.5, limit = 1 },
    { id = "s1", sequence = 1, limit = 1, extra = 1 },
  }) do
    t.eq(s.call("ext.stream", "read", bad).error.code, "invalid_input", t.show(bad))
  end
  -- 1.0 is not an integer on the wire, on every Lua
  local raw = '{"apiVersion":"ext.plugin/v1","id":"c1","plugin":{"id":"example/p","revision":"r1"},"contract":{"name":"ext.stream","version":"v1"},"operation":"read","deadline":"2999-01-01T00:00:00Z","payload":{"id":"s1","sequence":1.0,"limit":1}}'
  t.eq(s.guest:invoke(json.decode(raw)).error.code, "invalid_input")
  t.eq(json.encode(s.call("ext.stream", "close", { id = "s1" }).payload), "{}")
  t.eq(s.closed, { "s1" })
  t.eq(s.call("ext.stream", "close", { id = "" }).error.code, "invalid_input")
end)

test("an empty batch is an array on the wire", function()
  local router = cap.Router():stream(function() return "s" end, function() return { items = {}, done = true } end, function() end)
  local r = plugin.Guest(DESC, router):invoke(request("ext.stream", "read", { id = "s", sequence = 1, limit = 1 }))
  t.eq(json.encode(r.payload), '{"done":true,"items":[]}')
end)

test("batch limits", function()
  local items = {}
  for i = 1, cap.MAX_STREAM_ITEMS do items[i] = { v = i } end
  cap.stream_batch({ items = items, done = false })
  items[#items + 1] = { v = 0 }
  t.raises(function() cap.stream_batch({ items = items, done = false }) end, "too many items")
  local big = string.rep("x", 600 * 1024)
  t.raises(function() cap.stream_batch({ items = { big, big }, done = false }) end, "too large")
  t.raises(function() cap.stream_batch({ items = {}, done = 1 }) end, "done is not a boolean")
  t.raises(function() cap.stream_batch({ items = { x = 1 }, done = true }) end, "items is not an array")
end)

test("an unregistered operation and duplicates", function()
  local router = cap.Router()
  local err = t.raises(function() router("ext.health", "check", {}) end)
  t.ok(plugin.is_remote_error(err) and err.code == "unsupported")
  router:health(function() return { status = "healthy", ready = true } end)
  t.raises(function() router:health(function() end) end, "duplicate")
  t.eq(router({ "ext.health", "v1" }, "check", {}), { status = "healthy", ready = true })
  t.ok(plugin.capability == cap)
end)

return tests
