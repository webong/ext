-- The shared fixtures for progress events and reverse calls (lib/plugin/testdata/v1), which the Go
-- contract computes and every SDK reproduces: frames, the sender's rate limiter and the wire model.
local plugin = require("ext_io.plugin")
local core = require("ext_io.plugin.core")
local frames = require("ext_io.plugin.frames")
local t = require("support")

local tests = {}
local function test(name, f) tests[#tests + 1] = { name, f } end

-- Only a ProtocolError counts as a refusal: any other error is a bug in the judge.
local function accepts(judge, ...)
  local ok, err = pcall(judge, ...)
  if not ok and not plugin.is_protocol_error(err) then error(err, 0) end
  return ok
end

-- Collects the first few mismatches so a failure says what diverged.
local function mismatches()
  local m = { first = {}, count = 0 }
  function m:note(section, case, what)
    self.count = self.count + 1
    if #self.first < 12 then self.first[#self.first + 1] = section .. ": " .. case.name .. ": " .. what end
  end
  function m:check()
    if self.count > 0 then
      error(string.format("%d mismatches, the first:\n%s", self.count, table.concat(self.first, "\n")), 2)
    end
  end
  return m
end

test("frames are judged as the Go contract judges them", function()
  local root = t.fixture("frame-cases.json")
  local bad = mismatches()
  local total = 0
  local sections = {
    { "progress", function(raw) frames.validate_progress_frame(core.decode_frame(raw)) end },
    { "reverseRequest", function(raw) frames.validate_reverse_request(core.decode_frame(raw)) end },
    { "reverseResponse", function(raw, id) frames.validate_reverse_response(core.decode_frame(raw), id) end },
  }
  for _, s in ipairs(sections) do
    local section, judge = s[1], s[2]
    local cases = root[section]
    local valid = 0
    for _, c in ipairs(cases) do
      if c.valid then valid = valid + 1 end
    end
    t.ok(#cases >= 20 and valid >= 5 and #cases - valid >= 5, section .. " proves little")
    for _, case in ipairs(cases) do
      total = total + 1
      local got = accepts(judge, case.frame, case.id or "")
      if got ~= case.valid then bad:note(section, case, "accepted " .. tostring(got) .. ": " .. case.frame) end
    end
  end
  t.ok(total >= 500, "only " .. total .. " frame cases")
  bad:check()
end)

test("the rate limiter reproduces every step", function()
  local cases = t.fixture("progress-limiter-cases.json").cases
  t.ok(#cases >= 10)
  local bad = mismatches()
  for _, case in ipairs(cases) do
    local limiter = frames.Limiter.new(case.eventsPerSecond, case.bytesPerSecond, 0)
    for i, step in ipairs(case.steps) do
      local got = limiter:admit(step.textBytes, step.atMs * 1000000)
      local want = step.admitted
      if (got ~= nil) ~= want or (want and got ~= (step.dropped or 0)) then
        bad:note("limiter", case, string.format("step %d: %s, want %s %s", i, tostring(got), tostring(want), tostring(step.dropped or 0)))
      end
    end
    if limiter:pending() ~= case.pending then bad:note("limiter", case, "pending count") end
  end
  bad:check()
end)

test("the wire model is judged as the Go contract judges it", function()
  local root = t.fixture("model-cases.json")
  local bad = mismatches()
  t.ok(#root.descriptors > 100)
  for _, case in ipairs(root.descriptors) do
    local got, err = pcall(function() plugin.validate_descriptor(core.decode_frame(case.descriptor)) end)
    if not got and not plugin.is_protocol_error(err) and not (type(err) == "string" and not err:find(":%d+:")) then error(err, 0) end
    if got ~= case.valid then bad:note("descriptor", case, "accepted " .. tostring(got) .. ": " .. case.descriptor) end
  end
  local selected = root.requestDescriptor
  plugin.validate_descriptor(selected)
  t.ok(#root.requests > 100)
  for _, case in ipairs(root.requests) do
    local ran = {}
    local g = plugin.Guest(selected, function() ran[#ran + 1] = plugin.current_call() end)
    local link = frames.Link.new(t.reader(""), t.writer())
    local ok, err = pcall(function() g:invoke(core.decode_frame(case.request), link) end)
    if not ok and not plugin.is_protocol_error(err) then error(err, 0) end
    if (#ran > 0) ~= case.valid then
      bad:note("request", case, "accepted " .. tostring(#ran > 0) .. ": " .. case.request)
    elseif #ran > 0 then
      -- The call gets a reporter when its operation declared progress and the request carries a
      -- rate; the rate has its defaults filled in.
      local limits = ran[1].progress_limits
      if (limits ~= nil) ~= (case.reporter == true) then
        bad:note("request", case, "reporter " .. tostring(limits ~= nil))
      elseif limits ~= nil and (limits.events_per_second ~= case.events or limits.bytes_per_second ~= case.bytes) then
        bad:note("request", case, string.format("rate %s %s", tostring(limits.events_per_second), tostring(limits.bytes_per_second)))
      end
    end
  end
  bad:check()
end)

test("the frozen descriptors validate", function()
  for _, name in ipairs({ "descriptor.json", "descriptor-frames.json", "frames-descriptor.json" }) do
    plugin.load_descriptor(t.FIXTURES .. "/" .. name)
  end
end)

return tests
