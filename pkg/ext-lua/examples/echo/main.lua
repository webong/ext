-- An example guest: echoes payloads, counts with progress events and answers ext.health.
-- Run it from this directory with the SDK on the path, for example
--   LUA_PATH="../../?.lua;../../?/init.lua;;" lua main.lua
-- or after `luarocks make` in pkg/ext-lua. A host starts it as a process and speaks
-- ext.plugin/v1 over its standard input and output.
local here = (debug.getinfo(1, "S").source:match("^@(.*)[/\\][^/\\]*$")) or "."
package.path = here .. "/../../?.lua;" .. here .. "/../../?/init.lua;" .. package.path
local plugin = require("ext_io.plugin")

local health = plugin.capability.Router():health(function()
  return { status = "healthy", ready = true }
end)

local function handler(contract, operation, payload)
  if contract == "ext.health" then return health(contract, operation, payload) end
  if operation == "busy" then
    error(plugin.remote_error("busy", "try later", 250))
  elseif operation == "count" then
    local call = plugin.current_call()
    local n = type(payload) == "table" and payload.to or 3
    for i = 1, n do
      -- "unsupported" when the host asked for no events: stop reporting then.
      local ok = pcall(call.progress, call, plugin.Progress.fraction(i, n))
      if not ok then break end
    end
    return { counted = n, seconds_left = math.floor(call:remaining()) }
  end
  return payload
end

plugin.serve_stdio(here .. "/plugin.json", handler)
