-- Guest used by the Go conformance suite (pkg/ext-go/engine/plugintest, TestLuaGuest).
local here = (debug.getinfo(1, "S").source:match("^@(.*)[/\\][^/\\]*$")) or "."
package.path = here .. "/../?.lua;" .. here .. "/../?/init.lua;" .. package.path
local plugin = require("ext_io.plugin")

local function handler(contract, operation, payload)
  if operation == "wait" then
    -- Standard Lua cannot sleep; the host kills the process when its deadline passes.
    local started = os.time()
    while os.time() - started < 60 do end
  end
  if operation == "private-error" then error("secret diagnostic") end
  if operation == "public-error" then error(plugin.remote_error("busy", "try later", 10)) end
  return payload
end

plugin.serve_stdio(here .. "/descriptor.json", handler, { redact_errors = true })
