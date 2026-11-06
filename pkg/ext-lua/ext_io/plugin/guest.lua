--- The guest: descriptor validation, request validation, the handler boundary and the serve loop.
local json = require("ext_io.json")
local core = require("ext_io.plugin.core")
local frames = require("ext_io.plugin.frames")
local default_clock = require("ext_io.plugin.clock")

local guest = {}

local null = json.null
local absent, only, set, ordered = core.absent, core.only, core.set, core.ordered
local is_identifier, is_revision = core.is_identifier, core.is_revision
local API_VERSION = core.API_VERSION

guest.DEFAULT_MAX_CALL_SECONDS = 30

-- --- the call in flight ---------------------------------------------------------------------

--- Details of the call being handled, for handlers that need them.
---
--- On a transport that carries frames (a guest served by `serve`) the call can also report
--- progress and call its host: see `progress`, `progress_limits` and `reverse`. This guest reads
--- inline, so `session_reverse` is always nil and a handler makes one reverse call at a time.
local CallInfo = {}
CallInfo.__index = CallInfo
guest.CallInfo = CallInfo

--- Seconds left before the call's deadline (on the guest's clock; see ext_io.plugin.clock).
function CallInfo:remaining()
  return self.deadline - self.clock.now()
end

--- Reports a progress event (see frames.Progress) to the host while the call runs. Events reach
--- the host in order, before the result. Returns frames.SENT, or frames.DROPPED when the rate
--- limit dropped it (routine: the next event that passes carries the count). Raises FrameError:
--- "unsupported" when the operation did not declare progress, the host asked for no events or
--- there is no frame channel (stop reporting), "invalid" for an event that breaks the rules
--- (nothing is sent), "closed" once the call is over.
function CallInfo:progress(event)
  if self.reporter == nil then core.raise_frame("unsupported", "no progress channel on this call") end
  return self.reporter:report(event)
end

local current = nil

--- The CallInfo of the in-flight call, or nil outside a handler.
function guest.current_call()
  return current
end

-- --- descriptor -----------------------------------------------------------------------------

local DESCRIPTOR_FIELDS = set({ "apiVersion", "identity", "contracts", "reverse" })
local IDENTITY_FIELDS = set({ "id", "revision", "version" })
local CONTRACT_FIELDS = set({ "name", "version", "operations" })
local OPERATION_FIELDS = set({ "name", "surface", "progress" })

local function invalid(message)
  error(message, 0)
end

local function validate_contracts(contracts, reverse)
  local seen = {}
  for i = 1, json.array_length(contracts) do
    local c = contracts[i]
    if not only(c, CONTRACT_FIELDS) then invalid("invalid contract") end
    if not is_identifier(c.name) or not is_revision(c.version) then
      invalid("contract requires a name and exact version")
    end
    local key = c.name .. "\0" .. c.version
    local ops = c.operations
    local count = json.array_length(ops)
    if seen[key] or not count or count < 1 or count > 256 then
      invalid("duplicate contract or invalid operation count")
    end
    seen[key] = true
    local names = {}
    for j = 1, count do
      local op = ops[j]
      if not only(op, OPERATION_FIELDS) then invalid("invalid operation") end
      local name, surface, progress = op.name, op.surface, op.progress
      -- Go leaves a member that is null alone, as if absent.
      if absent(surface) then surface = "" end
      if not is_identifier(name)
        or name == "plugin.hello"
        or names[name]
        or type(surface) ~= "string"
        or (surface ~= "" and not is_identifier(surface))
        or (not absent(progress) and type(progress) ~= "boolean")
        or (progress == true and reverse) -- a host operation does not report progress
      then
        invalid("invalid, reserved, or duplicate operation")
      end
      names[name] = true
    end
  end
end

--- Raises a string error unless d is a valid ext.plugin/v1 descriptor.
function guest.validate_descriptor(d)
  if not only(d, DESCRIPTOR_FIELDS) then
    invalid("descriptor must be an object with apiVersion, identity, contracts")
  end
  if d.apiVersion ~= API_VERSION then invalid("unsupported apiVersion") end
  local ident = d.identity
  if not only(ident, IDENTITY_FIELDS) then invalid("invalid identity") end
  if not is_identifier(ident.id) or not is_revision(ident.revision) then
    invalid("identity requires a bounded id and immutable revision")
  end
  if ident.version ~= nil and ident.version ~= "" and not is_revision(ident.version) then
    invalid("invalid identity version")
  end
  local count = json.array_length(d.contracts)
  if not count or count < 1 or count > 64 then invalid("declare 1..64 contracts") end
  validate_contracts(d.contracts, false)
  -- The host operations the plugin may call; none is a null or empty list.
  if not absent(d.reverse) then
    local n = json.array_length(d.reverse)
    if not n or n > 64 then invalid("declare at most 64 reverse contracts") end
    validate_contracts(d.reverse, true)
  end
end

--- Reads a descriptor from a plugin.json (ext.package/v1) or a bare descriptor file. Raises a
--- string error.
function guest.load_descriptor(path)
  local f, err = io.open(path, "rb")
  if not f then invalid("cannot open descriptor: " .. tostring(err)) end
  local raw = f:read(16 * 1024 * 1024 + 1) or ""
  f:close()
  if #raw > 16 * 1024 * 1024 then invalid("descriptor file exceeds 16 MiB") end
  local ok, data = pcall(core.decode_frame, raw)
  if not ok then invalid(tostring(data)) end
  if json.is_object(data) and data.descriptor ~= nil then
    if data.apiVersion ~= "ext.package/v1" then invalid("unsupported package apiVersion") end
    data = data.descriptor
  end
  guest.validate_descriptor(data)
  return data
end

-- A copy of a valid descriptor as JSON values: objects and arrays marked, keys in the order they
-- were decoded in (or the wire order for tables written in Lua).
local function copy_object(t, order, fields)
  local out, keys, seen = {}, {}, {}
  local function add(k)
    if t[k] ~= nil and not seen[k] then
      seen[k] = true
      keys[#keys + 1] = k
      local copy = fields and fields[k]
      out[k] = copy and copy(t[k]) or t[k]
    end
  end
  local recorded = json.key_order(t)
  if recorded then
    for i = 1, #recorded do add(recorded[i]) end
  end
  for i = 1, #order do add(order[i]) end
  return json.object(out, keys)
end

local function copy_array(t, copy)
  if t == null then return null end
  local out = {}
  for i = 1, json.array_length(t) do out[i] = copy(t[i]) end
  return json.array(out)
end

local function copy_operation(op)
  return copy_object(op, { "name", "surface", "progress" })
end

local function copy_contract(c)
  return copy_object(c, { "name", "version", "operations" }, {
    operations = function(ops) return copy_array(ops, copy_operation) end,
  })
end

local function copy_descriptor(d)
  return copy_object(d, { "apiVersion", "identity", "contracts", "reverse" }, {
    identity = function(ident) return copy_object(ident, { "id", "revision", "version" }) end,
    contracts = function(cs) return copy_array(cs, copy_contract) end,
    reverse = function(cs) return copy_array(cs, copy_contract) end,
  })
end

-- --- guest ------------------------------------------------------------------------------------

local HELLO_FIELDS = set({ "apiVersion", "id", "plugin", "contract", "operation", "surface", "deadline", "payload" })
local REQUEST_FIELDS = set({ "apiVersion", "id", "plugin", "contract", "operation", "surface", "deadline", "payload", "progress", "parent" })
local RATE_FIELDS = set({ "eventsPerSecond", "bytesPerSecond" })

-- The rate a request sets for a call's progress events: nil when absent, else events and bytes
-- per second with 0 for a member left out. Go decodes the members into int64s, so a number that
-- is not a whole one that fits is not a request at all.
local function rate_of(value)
  if absent(value) then return nil end
  if not only(value, RATE_FIELDS) then core.raise_protocol("invalid progress rate") end
  local events = core.int64(value, "eventsPerSecond")
  local bytes = core.int64(value, "bytesPerSecond")
  if events == nil or bytes == nil then core.raise_protocol("invalid progress rate") end
  return { events, bytes }
end

local function identity_of(obj, fields)
  local out = {}
  if absent(obj) then
    for i = 1, #fields do out[fields[i]] = "" end
    return out
  end
  if not only(obj, set(fields)) then core.raise_protocol("invalid envelope object") end
  for i = 1, #fields do
    local v = obj[fields[i]]
    if v == nil then v = "" end
    if type(v) ~= "string" then core.raise_protocol("invalid envelope field") end
    out[fields[i]] = v
  end
  return out
end

local function is_callable(f)
  if type(f) == "function" then return true end
  local mt = getmetatable(f)
  return type(mt) == "table" and mt.__call ~= nil
end

local function op_key(name, version, operation)
  return name .. "\0" .. version .. "\0" .. operation
end

--- An immutable descriptor plus a domain handler.
---
--- `handler(contract_name, operation, payload)` returns a JSON value (nil is null), or raises a
--- RemoteError (error(plugin.remote_error(...))) for a deliberate public failure. Any other error
--- becomes a "provider_error" carrying tostring(err) cut to 4096 bytes; pass
--- `redact_errors = true` to send a generic "operation_failed" instead, so internal text never
--- reaches the host. Calls are sequential.
local Guest = {}
Guest.__index = Guest
guest.Guest = Guest

--- `descriptor` is a table or the path of a plugin.json or descriptor file. `opts` holds
--- `max_call_seconds` (default 30), `redact_errors` (default false) and `clock`, a function
--- returning epoch seconds (default: see ext_io.plugin.clock).
function Guest.new(descriptor, handler, opts)
  opts = opts or {}
  if type(descriptor) == "string" then
    descriptor = guest.load_descriptor(descriptor)
  else
    guest.validate_descriptor(descriptor)
  end
  if not is_callable(handler) then error("handler is required", 2) end
  local max_call = opts.max_call_seconds or guest.DEFAULT_MAX_CALL_SECONDS
  if type(max_call) ~= "number" or not (max_call > 0) then error("max_call_seconds must be positive", 2) end
  local clock = default_clock
  if opts.clock ~= nil then
    local now = opts.clock
    clock = { now = now, monotonic_ns = function() return now() * 1e9 end, source = "option" }
  end
  local self = setmetatable({}, Guest)
  self.descriptor = copy_descriptor(descriptor)
  self.handler = handler
  self.max_call = max_call + 0.0
  self.redact = opts.redact_errors and true or false
  self.clock = clock
  self.ops = {} -- op_key -> {surface, reports progress}
  for _, c in ipairs(self.descriptor.contracts) do
    for _, op in ipairs(c.operations) do
      local surface = op.surface
      if absent(surface) then surface = "" end
      self.ops[op_key(c.name, c.version, op.name)] = { surface = surface, progress = op.progress == true }
    end
  end
  self.reverse = {} -- op_key -> true for host operations the plugin may call
  if not absent(self.descriptor.reverse) then
    for _, c in ipairs(self.descriptor.reverse) do
      for _, op in ipairs(c.operations) do self.reverse[op_key(c.name, c.version, op.name)] = true end
    end
  end
  local ident = self.descriptor.identity
  self.identity = { id = ident.id, revision = ident.revision, version = ident.version or "" }
  return self
end

setmetatable(Guest, { __call = function(_, ...) return Guest.new(...) end })

--- A copy of the descriptor, as the hello answer carries it.
function Guest:handshake()
  return copy_descriptor(self.descriptor)
end

-- tostring(err), with what is not UTF-8 replaced, cut to 4096 bytes on a character boundary.
local function error_text(err)
  local text = tostring(err)
  if not json.utf8_valid(text) then
    local chars = {}
    local i = 1
    while i <= #text do
      local c = text:byte(i)
      local len = c < 128 and 1 or c >= 240 and 4 or c >= 224 and 3 or c >= 192 and 2 or 1
      local seq = text:sub(i, i + len - 1)
      if len > 1 and #seq == len and json.utf8_valid(seq) then
        chars[#chars + 1] = seq
        i = i + len
      else
        chars[#chars + 1] = c < 128 and seq or "?"
        i = i + 1
      end
    end
    text = table.concat(chars)
  end
  if #text > core.MAX_MESSAGE_BYTES then
    text = text:sub(1, core.MAX_MESSAGE_BYTES)
    while #text > 0 and not json.utf8_valid(text) do text = text:sub(1, -2) end
  end
  return text
end

function Guest:failure(err)
  if self.redact then return ordered("code", "operation_failed", "message", "plugin operation failed") end
  return ordered("code", "provider_error", "message", error_text(err))
end

--- Validates one decoded request and returns the response object.
---
--- Raises ProtocolError for envelopes the Go host would not even decode (unknown fields, wrong
--- types); those end the stream. `link` is the connection the request came in on (serve passes
--- it): without one the call can neither report progress nor call its host.
function Guest:invoke(request, link)
  if not only(request, REQUEST_FIELDS) then core.raise_protocol("invalid request envelope") end
  local request_id = request.id
  if type(request_id) ~= "string" then core.raise_protocol("invalid request id") end
  local plugin = identity_of(request.plugin, { "id", "revision", "version" })
  local contract = identity_of(request.contract, { "name", "version" })
  for _, k in ipairs({ "apiVersion", "operation", "surface", "deadline" }) do
    if request[k] ~= nil and type(request[k]) ~= "string" then core.raise_protocol("invalid request field") end
  end
  if not absent(request.parent) and type(request.parent) ~= "string" then
    core.raise_protocol("invalid request parent")
  end
  local rate = rate_of(request.progress)
  local response = ordered("apiVersion", API_VERSION, "id", request_id)
  local deadline = core.parse_deadline(request.deadline)
  local operation = request.operation or ""
  local surface = request.surface or ""
  local op = self.ops[op_key(contract.name, contract.version, operation)]
  if request.apiVersion ~= API_VERSION
    or not is_revision(request_id)
    or deadline == nil
    or plugin.id ~= self.identity.id
    or plugin.revision ~= self.identity.revision
    or plugin.version ~= self.identity.version
    or op == nil
    or op.surface ~= surface
  then
    core.append(response, "error", ordered("code", "invalid_request", "message", "request does not match selected contract"))
    return response
  end
  local clock = self.clock
  local limit = math.min(deadline, clock.now() + self.max_call)
  local err, result
  if clock.now() >= limit then
    err = self:failure("deadline exceeded")
  else
    local info = setmetatable({
      request_id = request_id,
      contract_name = contract.name,
      contract_version = contract.version,
      operation = operation,
      surface = surface,
      deadline = limit, -- epoch seconds, on the guest's clock
      clock = clock,
    }, CallInfo)
    current = info
    local reporter, caller
    if link ~= nil then
      -- A call reports progress when its operation declared it and the host asked for events;
      -- it can call the host on the connection it came in on.
      if rate ~= nil and op.progress then
        info.progress_limits = {
          events_per_second = rate[1] > 0 and rate[1] or frames.DEFAULT_PROGRESS_EVENTS,
          bytes_per_second = rate[2] > 0 and rate[2] or frames.DEFAULT_PROGRESS_BYTES,
        }
        reporter = frames.Reporter.new(link, request_id, info.progress_limits, clock)
        info.reporter = reporter
      end
      caller = frames.ReverseCaller.new(link, self.reverse, request_id, limit, clock)
      info.reverse = caller
    end
    local payload = request.payload
    if payload == null then payload = nil end
    local ok, value = pcall(self.handler, contract.name, operation, payload)
    if ok then
      local encoded, why = pcall(core.encode_frame, value) -- surface unserializable results as provider errors
      if not encoded then
        err = self:failure(why)
      elseif clock.now() >= limit then
        err = self:failure("deadline exceeded")
      else
        result = value
      end
    elseif core.is_remote_error(value) then
      err = value:to_wire()
    else
      err = self:failure(value)
    end
    current = nil
    if caller ~= nil then caller.over = true end -- a request names its call, so a caller cannot outlive it
    if reporter ~= nil then reporter:finish() end -- a marker for events never reported, before the response
  end
  if err ~= nil then
    core.append(response, "error", err)
  else
    core.append(response, "payload", result == nil and null or result)
  end
  return response
end

local function answer_hello(g, request)
  if not only(request, HELLO_FIELDS) then core.raise_protocol("invalid hello") end
  local plugin = identity_of(request.plugin, { "id", "revision", "version" })
  local contract = identity_of(request.contract, { "name", "version" })
  local deadline = core.parse_deadline(request.deadline)
  local surface = request.surface
  if surface == nil then surface = "" end
  if request.apiVersion ~= API_VERSION
    or request.id ~= "hello"
    or request.operation ~= "plugin.hello"
    or plugin.id ~= "" or plugin.revision ~= "" or plugin.version ~= ""
    or contract.name ~= "" or contract.version ~= ""
    or surface ~= ""
    or request.payload ~= nil
    or deadline == nil
  then
    core.raise_protocol("invalid hello")
  end
  if g.clock.now() >= deadline then core.raise_protocol("hello deadline exceeded") end
  return ordered("apiVersion", API_VERSION, "id", "hello", "payload", g:handshake())
end

--- Serves `g` until the input ends. `input` has a `read` method like a Lua file (formats "*L",
--- or a byte count on Lua 5.1) and `output` has `write` and `flush`. Raises ProtocolError (with
--- error()) on violations.
---
--- A handler can report progress and call its host (see CallInfo). The host sends nothing during
--- a call but the answer to a reverse call: anything else ends the session with ProtocolError,
--- and the call in flight is not answered. When the input ends during a call, the call is
--- answered and the session ends.
function guest.serve(g, input, output)
  local link = frames.Link.new(input, output)
  local hello = false
  while true do
    local raw = core.read_frame(input)
    if raw == nil then return end
    local request = core.decode_frame(raw)
    local response
    if not hello then
      response = answer_hello(g, request)
      hello = true
    else
      response = g:invoke(request, link)
      if link.failure ~= nil then error(link.failure, 0) end -- the host broke the rules while the handler waited
    end
    local frame = core.encode_frame(response)
    if #frame > core.MAX_FRAME_BYTES then
      frame = core.encode_frame(ordered(
        "apiVersion", API_VERSION,
        "id", response.id,
        "error", ordered("code", "response_too_large", "message", "response exceeds the frame limit")
      ))
    end
    link:write(frame)
  end
end

--- Serves on this process's standard input and output. Returns at the end of the input.
---
--- `descriptor` is a table or the path of a plugin.json / descriptor file; `opts` as Guest.new.
--- Protocol frames go to the original io.stdout; while serving, `print`, `io.write` and
--- `io.stdout` go to standard error, so stray output from Lua code cannot corrupt the stream.
--- (Pure Lua cannot redirect file descriptor 1: a child process started with os.execute or
--- io.popen still writes to it.) Call once, from the guest's main chunk.
function guest.serve_stdio(descriptor, handler, opts)
  local g = Guest.new(descriptor, handler, opts)
  local out, input = io.stdout, io.stdin
  out:flush()
  local saved_print, saved_output = print, io.output()
  _G.print = function(...)
    local n = select("#", ...)
    local parts = {}
    for i = 1, n do parts[i] = tostring((select(i, ...))) end
    io.stderr:write(table.concat(parts, "\t"), "\n")
  end
  io.output(io.stderr)
  io.stdout = io.stderr
  local ok, err = pcall(guest.serve, g, input, out)
  io.stdout = out
  io.output(saved_output)
  _G.print = saved_print
  out:flush()
  if not ok then error(err, 0) end
end

return guest
