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
  local command = string.format("%q -e %q", lua, code)
  -- cmd /c drops the first and last quote of a line that starts with one: quote the whole line.
  if package.config:sub(1, 1) == "\\" then command = '"' .. command .. '"' end
  local p = assert(io.popen(command))
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

case("package writer scoped cleanup and ownership", function()
  local root = os.tmpname()
  os.remove(root)
  local lock = root .. "/.package-write.lock"
  local function remove_dir(path)
    if package.config:sub(1, 1) == "\\" then
      os.execute('rmdir "' .. path .. '"')
    else assert(os.remove(path)) end
  end
  local ok, err = pcall(function()
    local marker = {}
    local success, failure = pcall(function()
      engine:with_package_writer(root, function()
        expect_host_error(function() engine:package_writer(root) end, "exists")
        local code = string.format(
          "package.path=%q..package.path; local h=require('ext_io.host'); local ok,e=pcall(function() h.Engine():package_writer(%q) end); io.write(not ok and h.is_host_error(e) and e.status or 'unexpected')",
          t.ROOT .. "/?.lua;" .. t.ROOT .. "/?/init.lua;", root)
        local child = assert(io.popen(t.command(t.interpreter(), "-e", code)))
        local out = child:read("*a")
        child:close()
        t.eq(out, "exists", "cross-process contention")
        error(marker)
      end)
    end)
    t.eq(success, false)
    t.eq(failure, marker)
    local a, b, c = engine:with_package_writer(root, function() return 7, nil, 9 end)
    t.eq(a, 7); t.eq(b, nil); t.eq(c, 9)
    local writer = engine:package_writer(root)
    assert(os.rename(lock, lock .. ".old"))
    local replacement = engine:package_writer(root)
    expect_host_error(function() writer:close() end, "mismatch")
    writer:close()
    expect_host_error(function() engine:package_writer(root) end, "exists")
    replacement:close()
    remove_dir(lock .. ".old")
    engine:package_writer(root):close()
  end)
  remove_dir(root)
  if not ok then error(err, 0) end
end)

case("a missing library is reported", function()
  t.raises(function() host.Engine("/does/not/exist/libext_plugin_host.so") end)
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
    apiVersion = "ext.io.pc/v1", id = "r1", plugin = descriptor.identity,
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

-- Reverse services and the guest registry, against the engine's scripted guests:
-- EXT_REVERSE_TEST_GUEST (lib/plugin/tests/reverse.c) and EXT_REGISTRY_TEST_GUEST
-- (lib/plugin/tests/registry.c), which scripts/host-cases.sh builds.
local REVERSE, RELAY = os.getenv("EXT_REVERSE_TEST_GUEST") or "", os.getenv("EXT_REGISTRY_TEST_GUEST") or ""
if REVERSE == "" or RELAY == "" then
  if REQUIRED then
    case("the scripted guests are available", function() error("set EXT_REVERSE_TEST_GUEST and EXT_REGISTRY_TEST_GUEST") end)
  else
    print("skip the reverse and registry tests: set EXT_REVERSE_TEST_GUEST and EXT_REGISTRY_TEST_GUEST")
  end
  return cases
end

local function output(...)
  local args = { ... }
  for i, a in ipairs(args) do args[i] = string.format("%q", a) end
  local command = table.concat(args, " ")
  if package.config:sub(1, 1) == "\\" then command = '"' .. command .. '"' end
  local p = assert(io.popen(command))
  local out = p:read("*a")
  p:close()
  return out
end

local RUN = { name = "reverse.test", version = "v1" }
local reverse_descriptor = output(REVERSE, "--descriptor")
local handled

local function reverse_reply(mode, serve)
  local h = engine:process(reverse_descriptor, REVERSE, { arguments = { "--guest", mode } })
  if serve ~= false then
    h:serve(function(call)
      return call.remaining > 0 and call.request.operation ~= "secret"
    end, function(call)
      handled[#handled + 1] = call.request
      local op = call.request.operation
      if op == "sign" then return "signed" end
      if op == "empty" then return nil end
      if op == "errok" then error(host.remote_error("policy", "refused for you", 5), 0) end
      error("a private failure")
    end, { timeout = 0.15, max_payload_bytes = 64 })
  end
  h:start(5)
  local reply = h:call(RUN, "run", json.object({}), 5).reply
  h:destroy()
  return reply
end

case("reverse calls are served on the calling thread", function()
  handled = {}
  t.eq(reverse_reply("call:sign").payload, "signed")
  t.eq(handled[1].surface, "signing")
  t.eq(handled[1].parent, "1")
  t.eq(reverse_reply("call:empty").payload, json.null)
  local e = reverse_reply("call:errok").error
  t.eq({ e.code, e.message, e.retryAfterMilliseconds }, { "policy", "refused for you", 5 })
  t.eq(reverse_reply("call:fail").error.code, "operation_failed")
end)

case("reverse policy and refusals", function()
  handled = {}
  t.eq(reverse_reply("call:secret").error.code, "denied")
  t.eq(#handled, 0)
  t.eq(reverse_reply("call:nothere").error.code, "reverse_undeclared")
  t.eq(reverse_reply("call:sign", false).error.code, "reverse_unavailable")
end)

case("requests between calls wait for serve_reverse", function()
  handled = {}
  local h = engine:process(reverse_descriptor, REVERSE, { arguments = { "--guest", "idle" } })
  h:serve(function() return true end, function(call)
    handled[#handled + 1] = call.request
    return "signed"
  end)
  h:start(5)
  t.eq(h:serve_reverse(5), true)
  t.eq(handled[1].parent, nil)
  t.eq(h:call(RUN, "run", json.object({}), 5).reply.payload, "signed")
  t.eq(h:serve_reverse(0.05), false)
  h:destroy()
end)

local CASES_PATH = t.FIXTURES .. "/registry-cases.json"
local registry_cases = t.fixture("registry-cases.json")

local function relay(registry, name, deny_forward, deny_reverse)
  local h = engine:process(output(RELAY, "--descriptor", CASES_PATH, name), RELAY,
    { arguments = { "--guest", CASES_PATH, name }, authorize = function() return not deny_forward end })
  h:attach(registry, function() return not deny_reverse end)
  h:start(5)
  return h
end

local function contract(name)
  return { name = registry_cases.guests[name], version = "v1" }
end

local function listed(list, name)
  if list == nil or list == json.null then return false end
  for _, v in ipairs(list) do if v == name then return true end end
  return false
end

case("the guest registry answers the shared cases", function()
  for _, c in ipairs(registry_cases.cases) do
    local registry = host.Registry(engine)
    local hosts = {}
    for name in pairs(registry_cases.guests) do
      hosts[name] = relay(registry, name, listed(c.forwardDeny, name), listed(c.reverseDeny, name))
      registry:register(contract(name), hosts[name])
    end
    for _, e in ipairs(registry_cases.edges) do registry:route(e.from, e.to) end
    for i, step in ipairs(c.steps) do
      if step.unregister then
        registry:unregister(contract(step.unregister))
      else
        local got = hosts[step.call]:call(contract(step.call), step.operation, step.payload,
          (step.timeoutMs or 5000) / 1000)
        if step.result ~= nil then
          t.eq(got, step.result, c.name .. " step " .. i)
        else
          local code = got and got.error and got.error.code
          t.ok(listed(step.resultError, code), c.name .. " step " .. i .. ": " .. t.show(got))
        end
      end
    end
    for _, h in pairs(hosts) do h:destroy() end
  end
end)

case("the guest registry refuses what it cannot route", function()
  local registry = host.Registry(engine)
  local b = relay(registry, "b")
  expect_host_error(function() registry:register({ name = "Not A Name", version = "v1" }, b) end, "invalid")
  expect_host_error(function() registry:register({ name = "registry.other", version = "v1" }, b) end, "unsupported")
  registry:register(contract("b"), b)
  expect_host_error(function() registry:register(contract("b"), b) end, "exists")
  local from = { name = "registry.ask", version = "v1", operation = "echo-b" }
  local to = { name = "registry.b", version = "v1", operation = "echo" }
  registry:route(from, to)
  expect_host_error(function() registry:route(from, to) end, "exists")
  expect_host_error(function()
    registry:route({ name = "registry.ask", version = "v1", operation = "other" }, { name = "registry.b", version = "v1", operation = "Bad Name" })
  end, "invalid")
  expect_host_error(function() registry:unregister({ name = "registry.x", version = "v1" }) end, "not_found")
  registry:unregister(contract("b"))
  registry:register(contract("b"), b)
  t.raises(function() b:attach({}, function() return true end) end)
  b:destroy()
  -- A destroyed host has left the registry.
  expect_host_error(function() registry:unregister(contract("b")) end, "not_found")
  registry = nil
  collectgarbage()
  collectgarbage()
end)

return cases
