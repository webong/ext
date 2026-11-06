-- The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
-- docs/plugin-guest-frames.md): reports progress and calls its host. It reads inline, so it
-- answers the operations that need a reader of its own (idle, collect, burst) with the public
-- error "unsupported".
local here = (debug.getinfo(1, "S").source:match("^@(.*)[/\\][^/\\]*$")) or "."
package.path = here .. "/../?.lua;" .. here .. "/../?/init.lua;" .. package.path
local plugin = require("ext_io.plugin")
local json = plugin.json
local Progress = plugin.Progress

local DESCRIPTOR = here .. "/../../../lib/plugin/testdata/v1/frames-descriptor.json"
local HOST = { "host.svc", "v1" }

-- How a reverse call came out, in the shape the suite reads.
local function outcome(call)
  local ok, result = pcall(call)
  if ok then return { result = result == nil and json.null or result } end
  if plugin.is_remote_error(result) then
    local e = { code = result.code, message = result.message }
    if result.retry_after_ms and result.retry_after_ms ~= 0 then e.retry = result.retry_after_ms end
    return { error = e }
  end
  if plugin.is_frame_error(result) then
    local code = result.code
    if code ~= "closed" and code ~= "deadline" and code ~= "canceled" then code = "failed" end
    return { error = { other = code } }
  end
  error(result, 0)
end

local function report(call, event)
  local ok, result = pcall(call.progress, call, event)
  if ok then return result end
  return nil
end

local function handler(contract, operation, payload)
  local call = plugin.current_call()
  if not json.is_object(payload) then payload = {} end
  if operation == "stream" then
    report(call, Progress.fraction(1, 3))
    report(call, Progress.out("hello"))
    report(call, Progress.err("oops"))
    return { done = true }
  elseif operation == "flood" then
    local dropped = 0
    local count = payload.count
    if count == nil then count = 300 end
    for _ = 1, count do
      if report(call, Progress.log("x")) == plugin.DROPPED then dropped = dropped + 1 end
    end
    return { dropped = dropped }
  elseif operation == "limits" then
    local limits = call.progress_limits
    return {
      declared = limits ~= nil,
      events = limits and limits.events_per_second or 0,
      bytes = limits and limits.bytes_per_second or 0,
    }
  elseif operation == "split" then
    local pieces = plugin.split_progress_text(payload.text)
    for _, piece in ipairs(pieces) do report(call, Progress.out(piece)) end
    return { pieces = #pieces }
  elseif operation == "plain" then
    local ok, err = pcall(call.progress, call, Progress.log("never sent"))
    return { unsupported = (not ok) and err.code == "unsupported", limits = call.progress_limits ~= nil }
  elseif operation == "call" then
    if call.reverse == nil then return { error = { other = "unsupported" } } end
    local inner = payload.payload
    if inner == json.null then inner = nil end
    return outcome(function() return call.reverse:call(HOST, payload.op, inner) end)
  elseif operation == "twice" then
    local ok, first, second = pcall(function()
      return call.reverse:call(HOST, "sign", 1), call.reverse:call(HOST, "sign", 2)
    end)
    if not ok then error("a reverse call failed: " .. tostring(first)) end
    return { first = first, second = second }
  elseif operation == "idle" or operation == "collect" or operation == "burst" then
    error(plugin.remote_error("unsupported", "not supported here"))
  end
  error("unknown operation")
end

plugin.serve_stdio(DESCRIPTOR, handler, { redact_errors = true })
