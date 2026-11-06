--- Typed definitions of the SDK's optional capability contracts, with the same wire shapes and
--- limits as the Go library: ext.health, ext.configuration and ext.stream. `Router` is a handler
--- for serve_stdio that checks each payload and result against them before and after your
--- function runs.
---
---     local router = plugin.capability.Router()
---     router:health(function() return { status = "healthy", ready = true } end)
---     plugin.serve_stdio("plugin.json", router)
local json = require("ext_io.json")
local core = require("ext_io.plugin.core")

local capability = {}

capability.MAX_STREAM_ITEMS = 256
capability.MAX_STREAM_BATCH_BYTES = 1024 * 1024
local MAX_ID = 64
local MAX_NAME = 256
local STATUSES = { healthy = true, degraded = true, unhealthy = true }

local function invalid(message)
  error(message, 0)
end

-- value must be an object with exactly these keys.
local function exactly(value, fields)
  if not json.is_object(value) then invalid("expected an object") end
  local want = {}
  for i = 1, #fields do
    want[fields[i]] = true
    if value[fields[i]] == nil then invalid("object lacks " .. fields[i]) end
  end
  for k in pairs(value) do
    if not want[k] then invalid("object has an unexpected field " .. tostring(k)) end
  end
  return value
end

local function characters(s)
  local _, n = s:gsub("[^\128-\191]", "")
  return n
end

local function text(value, limit)
  if type(value) ~= "string" or value == "" or characters(value) > limit then
    invalid("expected a non-empty string of at most " .. limit .. " characters")
  end
end

-- A whole number: 1.0 and 1.5 are not (on Lua 5.1 and LuaJIT the decoded text decides).
local function integer(container, key)
  local v = container[key]
  if type(v) ~= "number" then invalid("expected an integer") end
  local source = json.number_text(container, key)
  if source then
    if not source:find("^%-?%d+$") then invalid("expected an integer") end
  elseif not core.is_integer(v) then
    invalid("expected an integer")
  end
end

function capability.health_request(value)
  return exactly(value, {})
end

function capability.health(value)
  exactly(value, { "status", "ready" })
  if not STATUSES[value.status] or type(value.ready) ~= "boolean" then invalid("invalid health") end
  return value
end

function capability.configuration(value)
  exactly(value, { "instance", "revision", "values" })
  text(value.instance, MAX_NAME)
  text(value.revision, MAX_NAME)
  return value
end

function capability.configured(value)
  exactly(value, { "instance", "revision" })
  if type(value.instance) ~= "string" or type(value.revision) ~= "string" then invalid("invalid configured") end
  return value
end

function capability.stream_open(value)
  return exactly(value, { "parameters" })
end

function capability.stream_opened(value)
  exactly(value, { "id" })
  text(value.id, MAX_ID)
  return value
end

function capability.stream_read(value)
  exactly(value, { "id", "sequence", "limit" })
  text(value.id, MAX_ID)
  integer(value, "sequence")
  integer(value, "limit")
  if value.sequence < 1 or value.limit < 1 or value.limit > capability.MAX_STREAM_ITEMS then invalid("invalid read") end
  return value
end

function capability.stream_close(value)
  exactly(value, { "id" })
  text(value.id, MAX_ID)
  return value
end

--- At most MAX_STREAM_ITEMS items and MAX_STREAM_BATCH_BYTES of JSON. An empty plain table of
--- items is an empty array; the batch is marked so it is sent as one.
function capability.stream_batch(value)
  exactly(value, { "items", "done" })
  local n = json.array_length(value.items)
  if not n or type(value.done) ~= "boolean" then invalid("invalid batch") end
  if n > capability.MAX_STREAM_ITEMS then invalid("too many items") end
  local size = 0
  for i = 1, n do
    size = size + #json.encode(value.items[i], { ascii = false })
    if size > capability.MAX_STREAM_BATCH_BYTES then invalid("batch too large") end
  end
  json.array(value.items)
  return value
end

--- Dispatches (contract, operation) to registered functions, validating the payload before the
--- call and the result after it. A payload that fails validation is an "invalid_input" remote
--- error; a result that fails is an internal error, because it is the plugin's own bug.
local Router = {}
Router.__index = Router
capability.Router = Router

function Router.new()
  return setmetatable({ routes = {} }, Router)
end

setmetatable(Router, { __call = function() return Router.new() end })

function Router:on(contract, version, operation, fn, check_input, check_output)
  local key = contract .. "\0" .. version .. "\0" .. operation
  if self.routes[key] then error("operation already registered", 2) end
  self.routes[key] = { fn = fn, check_input = check_input, check_output = check_output }
  return self
end

--- check() returns {status = ..., ready = ...}.
function Router:health(check)
  return self:on("ext.health", "v1", "check", function() return check() end, capability.health_request, capability.health)
end

--- update(instance, revision, values) returns nothing; the router answers.
function Router:configuration(update)
  local function run(payload)
    update(payload.instance, payload.revision, payload.values)
    return core.ordered("instance", payload.instance, "revision", payload.revision)
  end
  return self:on("ext.configuration", "v1", "update", run, capability.configuration, capability.configured)
end

--- open(parameters) -> id; read(id, sequence, limit) -> {items = ..., done = ...}; close(id).
function Router:stream(open, read, close)
  self:on("ext.stream", "v1", "open", function(p) return core.ordered("id", open(p.parameters)) end,
    capability.stream_open, capability.stream_opened)
  self:on("ext.stream", "v1", "read", function(p) return read(p.id, p.sequence, p.limit) end,
    capability.stream_read, capability.stream_batch)
  return self:on("ext.stream", "v1", "close", function(p) return close(p.id) or json.object() end,
    capability.stream_close, function(v) return exactly(v, {}) end)
end

--- The handler: contract is a name (version v1) or a {name, version} pair.
function Router:__call(contract, operation, payload)
  local name, version = contract, "v1"
  if type(contract) == "table" then name, version = contract.name or contract[1], contract.version or contract[2] end
  local entry = self.routes[tostring(name) .. "\0" .. tostring(version) .. "\0" .. tostring(operation)]
  if entry == nil then error(core.remote_error("unsupported", "operation is not implemented"), 0) end
  if entry.check_input and not pcall(entry.check_input, payload) then
    error(core.remote_error("invalid_input", "request payload does not satisfy operation contract"), 0)
  end
  local result = entry.fn(payload)
  if entry.check_output then entry.check_output(result) end -- an error here is the plugin's bug: an internal error
  return result
end

return capability
