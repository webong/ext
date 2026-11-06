--- Guest SDK for ext.plugin/v1 over bounded JSON lines (pure Lua, no C modules).
---
---     local plugin = require("ext_io.plugin")
---
---     local function handler(contract, operation, payload)
---       if operation == "busy" then
---         error(plugin.remote_error("busy", "try later", 250))
---       end
---       return payload
---     end
---
---     plugin.serve_stdio("plugin.json", handler)
---
--- The module answers plugin.hello with the descriptor, validates every request the way the Go
--- plugin.ValidateRequest does, and calls the handler sequentially. It never writes to standard
--- output except protocol frames.
local json = require("ext_io.json")
local core = require("ext_io.plugin.core")
local frames = require("ext_io.plugin.frames")
local guest = require("ext_io.plugin.guest")
local clock = require("ext_io.plugin.clock")

local plugin = {
  json = json,
  frames = frames,
  clock = clock,

  API_VERSION = core.API_VERSION,
  MAX_FRAME_BYTES = core.MAX_FRAME_BYTES,
  MAX_RETRY_AFTER_MS = core.MAX_RETRY_AFTER_MS,
  MAX_MESSAGE_BYTES = core.MAX_MESSAGE_BYTES,
  null = json.null,

  remote_error = core.remote_error,
  is_remote_error = core.is_remote_error,
  frame_error = core.frame_error,
  is_frame_error = core.is_frame_error,
  protocol_error = core.protocol_error,
  is_protocol_error = core.is_protocol_error,

  SENT = frames.SENT,
  DROPPED = frames.DROPPED,
  Progress = frames.Progress,
  contract_ref = frames.contract_ref,
  split_progress_text = frames.split_progress_text,

  Guest = guest.Guest,
  current_call = guest.current_call,
  load_descriptor = guest.load_descriptor,
  validate_descriptor = guest.validate_descriptor,
  serve = guest.serve,
  serve_stdio = guest.serve_stdio,
}

-- The capability module loads on first use: plugin.capability.Router().
setmetatable(plugin, {
  __index = function(t, k)
    if k == "capability" then
      local m = require("ext_io.plugin.capability")
      rawset(t, k, m)
      return m
    end
  end,
})

return plugin
