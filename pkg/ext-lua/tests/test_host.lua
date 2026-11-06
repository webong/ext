--- The host SDK (ext_io.host) on the C engine.
---
--- It needs ext_io.host.core on package.cpath (scripts/ext-lua-host.sh builds it), the engine's
--- shared library (EXT_HOST_LIBRARY), and the reference guest twice: EXT_GO_GUEST, the JSON-line
--- Go guest, and EXT_GO_SHARED, the same guest as a C ABI library. It runs every case of
--- lib/plugin/testdata/v1/host-cases.json on both. Without them these tests are skipped, or fail
--- when EXT_HOST_REQUIRED=1. scripts/host-cases.sh builds everything and runs them.
local t = require("support")
local json = require("ext_io.json")

local REQUIRED = os.getenv("EXT_HOST_REQUIRED") == "1"

local cases = {}
local function case(name, fn) cases[#cases + 1] = { name, fn } end

case("requiring the guest never loads the host", function()
  local lua = t.interpreter()
  local root = t.ROOT
  local code = string.format(
    "package.path = %q .. package.path; require('ext_io.plugin'); io.write(tostring(package.loaded['ext_io.host'] ~= nil or package.loaded['ext_io.host.core'] ~= nil))",
    root .. "/?.lua;" .. root .. "/?/init.lua;")
  local p = assert(io.popen(string.format("%q -e %q", lua, code)))
  local out = p:read("*a")
  p:close()
  t.eq(out, "false")
end)

-- The rest needs the C module, the engine and the guests.
local missing
if not pcall(require, "ext_io.host.core") then
  missing = "ext_io.host.core is not on package.cpath (scripts/ext-lua-host.sh builds it)"
else
  for _, name in ipairs({ "EXT_HOST_LIBRARY", "EXT_GO_GUEST", "EXT_GO_SHARED" }) do
    if (os.getenv(name) or "") == "" then
      missing = "set " .. name .. ", or run scripts/host-cases.sh"
      break
    end
  end
end
if missing then
  if REQUIRED then
    case("the host is available", function() error(missing) end)
  else
    print("skip test_host: " .. missing)
  end
  return cases
end

local host = require("ext_io.host")
local engine = host.Engine()
local GUEST, SHARED = os.getenv("EXT_GO_GUEST"), os.getenv("EXT_GO_SHARED")

local function expect_host_error(f, status, what)
  local ok, err = pcall(f)
  if ok then error((what or "call") .. " succeeded, want " .. status, 2) end
  if not host.is_host_error(err) then error((what or "call") .. ": " .. t.show(err) .. ", want " .. status, 2) end
  if status then t.eq(err.status, status, what) end
  return err
end

case("a missing library is reported", function()
  t.raises(function() host.Engine("/does/not/exist/libext_host.so") end)
end)

-- Every case of host-cases.json, on both reference guests.
local spec = t.fixture("host-cases.json")
local descriptor = t.fixture(spec.descriptor)
local contract = spec.contract

local function run_call(h, i, step)
  local where = string.format("step %d (%s)", i, step.call)
  local payload = step.payload
  if payload == json.null then payload = nil end
  local ok, value = pcall(h.call, h, contract, step.call, payload, (step.timeoutMs or 3000) / 1000)
  local outcome = "result"
  if not ok then
    if host.is_remote_error(value) then
      outcome = "error"
    elseif host.is_host_error(value) then
      outcome, value = "status", value.status
    else
      error(where .. ": " .. tostring(value))
    end
  end
  if step.fails then
    t.ok(outcome ~= "result", where .. " succeeded with " .. t.show(value))
  elseif step.result ~= nil then
    t.eq(outcome, "result", where .. ": " .. t.show(value))
    t.eq(value, step.result, where)
  elseif step.error ~= nil then
    t.eq(outcome, "error", where .. ": " .. t.show(value))
    local want = step.error
    t.eq(value.code, want.code, where)
    t.eq(value.message, want.message, where)
    t.eq(value.retry_after_ms or 0, want.retryAfterMilliseconds or 0, where)
  else
    t.eq(outcome, "status", where .. ": " .. t.show(value))
    local allowed = step.status
    if type(allowed) == "string" then allowed = { allowed } end
    local found = false
    for _, s in ipairs(allowed) do found = found or s == value end
    t.ok(found, where .. ": status " .. tostring(value) .. ", want " .. t.show(step.status))
  end
end

local function run_case(guest, path, c)
  local d = t.copy(descriptor)
  if c.revision then d.identity.revision = c.revision end
  local opts = {
    verify = c.verify == "deny" and function() return false end or nil,
    authorize = c.authorize == "deny" and function() return false end or nil,
  }
  local h
  if guest == "jsonline" then
    h = engine:process(d, path, opts)
  else
    h = engine:shared_library(d, path, opts)
  end
  local ok, err = pcall(function()
    local got = "ok"
    local started, e = pcall(h.start, h, 5)
    if not started then
      if not host.is_host_error(e) then error(e, 0) end
      got = e.status
    end
    t.eq(got, c.start or "ok", "start")
    if got ~= "ok" then return end
    for i, step in ipairs(c.steps or {}) do
      if step.close then
        h:close()
      elseif step.state then
        t.eq(h:state(), step.state, "step " .. i)
      else
        run_call(h, i, step)
      end
    end
  end)
  h:destroy()
  if not ok then error(err, 0) end
end

for _, guest in ipairs(spec.guests) do
  local path = guest == "jsonline" and GUEST or SHARED
  for _, c in ipairs(spec.cases) do
    case("host case " .. guest .. "/" .. c.name, function() run_case(guest, path, c) end)
  end
end

-- What the cases leave to each binding: policies, envelopes, reentry and ownership.
local echo = descriptor.contracts[1]

case("policies see JSON", function()
  local seen = {}
  local h = engine:process(descriptor, GUEST, {
    verify = function(d) seen.verify = d.identity.id; return true end,
    authorize = function(r) seen.operation, seen.payload = r.operation, r.payload; return true end,
  })
  h:start()
  t.eq(h:call(echo, "echo", { value = 1 }), { value = 1 })
  h:destroy()
  t.eq(seen, { verify = "ctx/conformance", operation = "echo", payload = { value = 1 } })
end)

case("a raising policy denies and keeps the session", function()
  local h = engine:process(descriptor, GUEST, { authorize = function() error("policy bug") end })
  h:start()
  expect_host_error(function() h:call(echo, "echo", { value = 1 }) end, "denied")
  t.eq(h:state(), "ready")
  h:destroy()
end)

case("a policy cannot reenter its host", function()
  local h
  local reentered
  h = engine:process(descriptor, GUEST, {
    authorize = function()
      local ok, err = pcall(h.state, h)
      reentered = ok and "allowed" or (host.is_host_error(err) and err.status or tostring(err))
      return true
    end,
  })
  h:start()
  t.eq(h:call(echo, "echo", { value = 2 }), { value = 2 })
  t.eq(reentered, "invalid")
  h:destroy()
end)

case("a policy that yields denies", function()
  local h = engine:process(descriptor, GUEST, { authorize = function() coroutine.yield() return true end })
  local co = coroutine.create(function()
    h:start()
    expect_host_error(function() h:call(echo, "echo", { value = 3 }) end, "denied")
  end)
  local ok, err = coroutine.resume(co)
  t.ok(ok, tostring(err))
  t.eq(coroutine.status(co), "dead")
  h:destroy()
end)

case("invoke takes envelopes", function()
  local h = engine:process(descriptor, GUEST)
  h:start()
  local request = json.object({
    apiVersion = "ext.plugin/v1", id = "r1", plugin = descriptor.identity,
    contract = json.object({ name = echo.name, version = echo.version }, { "name", "version" }),
    operation = "echo", deadline = "2099-01-01T00:00:00Z", payload = { value = 3 },
  }, { "apiVersion", "id", "plugin", "contract", "operation", "deadline", "payload" })
  local response = json.decode(h:invoke(json.encode(request)))
  t.eq({ response.id, response.payload }, { "r1", { value = 3 } })
  h:destroy()
end)

case("relative paths are refused", function()
  expect_host_error(function() engine:shared_library(descriptor, "relative/libguest.so") end, "invalid")
  expect_host_error(function() engine:process(descriptor, "relative/guest") end, "invalid")
end)

case("a destroyed host refuses use", function()
  local h = engine:process(descriptor, GUEST)
  h:destroy()
  h:destroy() -- once is enough; again is harmless
  h:close()
  expect_host_error(function() h:start() end, "closed")
end)

case("stderr is kept", function()
  local h = engine:process(descriptor, GUEST)
  h:start()
  t.eq(type(h:stderr()), "string")
  h:destroy()
end)

case("drain waits for the calls in flight and closes the session", function()
  local h = engine:process(descriptor, GUEST)
  h:start()
  t.eq(h:call(echo, "echo", { value = 4 }), { value = 4 })
  h:drain(1)
  t.eq(h:state(), "closed")
  expect_host_error(function() h:call(echo, "echo", { value = 4 }) end, "closed")
  h:destroy()
end)

case("collected hosts are freed", function()
  for _ = 1, 3 do
    local h = engine:process(descriptor, GUEST)
    h:start()
    h:call(echo, "echo", { value = 5 })
  end
  collectgarbage()
  collectgarbage()
end)

return cases
