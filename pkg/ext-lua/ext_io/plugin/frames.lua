--- Progress events and reverse calls for a guest (docs/plugin-guest-frames.md).
--
-- A guest on a transport that carries frames can report progress while a call runs and call its
-- host. The rules are the Go contract's, pinned by
-- lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases}.json. This guest runs *inline*: it
-- has no reader of its own, so the code that waits for the host's answer to a reverse call reads
-- the connection itself, which suffices because the host sends nothing but answers during a
-- call. A call therefore has one reverse call in flight and no session-scoped caller exists.
local json = require("ext_io.json")
local core = require("ext_io.plugin.core")

local frames = {}

local null = json.null
local absent, only, set, ordered = core.absent, core.only, core.set, core.ordered
local is_identifier, is_revision = core.is_identifier, core.is_revision
local API_VERSION = core.API_VERSION

frames.MAX_PROGRESS_TEXT = 2048
frames.DEFAULT_PROGRESS_EVENTS = 50
frames.DEFAULT_PROGRESS_BYTES = 64 * 1024

--- What progress() returns: the event was written, or the rate limit dropped it (routine).
frames.SENT = "sent"
frames.DROPPED = "dropped"

local function violation()
  core.raise_protocol("plugin protocol violation")
end

-- --- events ------------------------------------------------------------------------------

--- One progress event: a "progress" count of `completed` out of `total` (zero when not known),
--- or a "log" line, optionally on a `stream` ("out" or "err"). Text is at most 2048 bytes of
--- UTF-8; use split_progress_text to cut a longer line.
local Progress = {}
frames.Progress = Progress

function Progress.new(kind, stream, text, completed, total)
  return {
    kind = kind,
    stream = stream == nil and "" or stream,
    text = text == nil and "" or text,
    completed = completed == nil and 0 or completed,
    total = total == nil and 0 or total,
  }
end

function Progress.log(text) return Progress.new("log", "", text) end
--- A log line of a program's standard output.
function Progress.out(text) return Progress.new("log", "out", text) end
--- A log line of a program's standard error.
function Progress.err(text) return Progress.new("log", "err", text) end
--- `completed` of `total`; zero when the total is not known.
function Progress.fraction(completed, total) return Progress.new("progress", "", "", completed, total) end

setmetatable(Progress, { __call = function(_, ...) return Progress.new(...) end })

--- The length of text in bytes of UTF-8, or -1 when it is not UTF-8.
function frames.utf8_length(text)
  if type(text) ~= "string" or not json.utf8_valid(text) then return -1 end
  return #text
end

local REPLACEMENT = "\239\191\189"

-- The characters of s: each well-formed UTF-8 sequence, or U+FFFD for a sequence that is not
-- text (an encoded surrogate counts as one character, other stray bytes one each).
local function characters(s)
  local out, i, n = {}, 1, #s
  while i <= n do
    local c = s:byte(i)
    local len = c < 128 and 1 or c >= 240 and c <= 247 and 4 or c >= 224 and 3 or c >= 192 and 2 or 0
    local seq = len > 0 and s:sub(i, i + len - 1) or nil
    if seq and #seq == len and json.utf8_valid(seq) then
      out[#out + 1] = seq
      i = i + len
    elseif seq and len == 3 and c == 237 and #seq == 3 and seq:find("^\237[\160-\191][\128-\191]$") then
      out[#out + 1] = REPLACEMENT -- a surrogate
      i = i + 3
    else
      out[#out + 1] = REPLACEMENT
      i = i + 1
    end
  end
  return out
end

--- Cuts text into pieces that each fit one event: at most 2048 bytes of UTF-8, never ending inside
--- a character. A sequence that is not UTF-8 (an encoded surrogate, a stray byte) becomes U+FFFD.
function frames.split_progress_text(text)
  local pieces, piece, size = {}, {}, 0
  local chars = characters(text)
  for i = 1, #chars do
    local ch = chars[i]
    if size + #ch > frames.MAX_PROGRESS_TEXT then
      pieces[#pieces + 1] = table.concat(piece)
      piece, size = {}, 0
    end
    piece[#piece + 1] = ch
    size = size + #ch
  end
  if #piece > 0 then pieces[#pieces + 1] = table.concat(piece) end
  return pieces
end

-- --- the sender's rate limit ------------------------------------------------------------------

--- A token bucket counted in events and bytes of text, with a burst of twice the rate, and a
--- count of events dropped since the last one that passed. Time is a monotonic clock in
--- nanoseconds, given by the caller.
local Limiter = {}
Limiter.__index = Limiter
frames.Limiter = Limiter

function Limiter.new(events_per_second, bytes_per_second, now_ns)
  events_per_second = events_per_second or 0
  bytes_per_second = bytes_per_second or 0
  local self = setmetatable({}, Limiter)
  self.event_rate = (events_per_second > 0 and events_per_second or frames.DEFAULT_PROGRESS_EVENTS) + 0.0
  self.byte_rate = (bytes_per_second > 0 and bytes_per_second or frames.DEFAULT_PROGRESS_BYTES) + 0.0
  self.events = 2 * self.event_rate
  self.bytes = 2 * self.byte_rate
  self.last = now_ns or 0
  self.dropped = 0
  return self
end

--- The number of events dropped since the previous one that passed (which the caller attaches to
--- this event), or nil when the rate limit drops it.
function Limiter:admit(text_bytes, now_ns)
  local delta = now_ns - self.last
  -- Seconds as Go's Duration.Seconds computes them.
  local whole
  if delta >= 0 then
    whole = math.floor(delta / 1e9)
    if whole * 1e9 > delta then whole = whole - 1 end
  else
    whole = -math.floor(-delta / 1e9)
    if whole * 1e9 < delta then whole = whole + 1 end
  end
  local elapsed = whole + (delta - whole * 1e9) / 1e9
  if elapsed > 0 then
    self.events = math.min(self.events + elapsed * self.event_rate, 2 * self.event_rate)
    self.bytes = math.min(self.bytes + elapsed * self.byte_rate, 2 * self.byte_rate)
    self.last = now_ns
  end
  if self.events < 1 or self.bytes < text_bytes then
    self.dropped = self.dropped + 1
    return nil
  end
  self.events = self.events - 1
  self.bytes = self.bytes - text_bytes
  local reported = self.dropped
  self.dropped = 0
  return reported
end

--- The drops not yet reported, reset to zero.
function Limiter:pending()
  local reported = self.dropped
  self.dropped = 0
  return reported
end

-- --- judging frames ----------------------------------------------------------------------------

local function strict(v, allowed)
  if not only(v, allowed) then violation() end
  return v
end

-- A member Go decodes into a string: absent and null are empty.
local function text_of(m, key)
  local v = m[key]
  if absent(v) then return "" end
  if type(v) ~= "string" then violation() end
  return v
end

-- A member Go decodes into a uint64: the value and its decimal text.
local function count_of(m, key)
  local v, text = core.uint64(m, key)
  if v == nil then violation() end
  return v, text
end

-- `over` is completed > total.
local function check_event(kind, stream, text, over, total_zero)
  local size = frames.utf8_length(text)
  if (kind ~= "progress" and kind ~= "log")
    or (stream ~= "" and stream ~= "out" and stream ~= "err")
    or (stream ~= "" and kind ~= "log")
    or size < 0
    or size > frames.MAX_PROGRESS_TEXT
    or (not total_zero and over)
  then
    violation()
  end
end

local PROGRESS_FIELDS = set({ "frame", "apiVersion", "parent", "kind", "stream", "text", "completed", "total", "dropped" })

--- Judges a progress frame (a decoded value): returns its parent (the call it belongs to) and
--- the event as a table {kind, stream, text, completed, total, dropped}; raises ProtocolError.
function frames.validate_progress_frame(v)
  strict(v, PROGRESS_FIELDS)
  local parent = text_of(v, "parent")
  local kind, stream, text = text_of(v, "kind"), text_of(v, "stream"), text_of(v, "text")
  local completed, ctext = count_of(v, "completed")
  local total, ttext = count_of(v, "total")
  local dropped = count_of(v, "dropped")
  if text_of(v, "frame") ~= "progress" or text_of(v, "apiVersion") ~= API_VERSION or not is_revision(parent) then
    violation()
  end
  check_event(kind, stream, text, core.decimal_compare(ctext, ttext) > 0, ttext == "0")
  return parent, { kind = kind, stream = stream, text = text, completed = completed, total = total, dropped = dropped }
end

local REVERSE_REQUEST_FIELDS = set({ "frame", "apiVersion", "id", "parent", "contract", "operation", "deadline", "payload" })
local CONTRACT_FIELDS = set({ "name", "version" })

--- Judges a reverse request frame, as the host reads it; raises ProtocolError.
function frames.validate_reverse_request(v)
  strict(v, REVERSE_REQUEST_FIELDS)
  local ref = strict(v.contract, CONTRACT_FIELDS)
  local parent = text_of(v, "parent")
  if text_of(v, "frame") ~= "reverse_request"
    or text_of(v, "apiVersion") ~= API_VERSION
    or not is_revision(text_of(v, "id"))
    or (parent ~= "" and not is_revision(parent))
    or not is_identifier(text_of(ref, "name"))
    or not is_revision(text_of(ref, "version"))
    or not is_identifier(text_of(v, "operation"))
    or core.parse_deadline(v.deadline) == nil
  then
    violation()
  end
end

local REVERSE_RESPONSE_FIELDS = set({ "frame", "apiVersion", "id", "payload", "error" })
local ERROR_FIELDS = set({ "code", "message", "retryAfterMilliseconds" })

--- Judges a reverse response, which a guest accepts only for the request it is waiting on.
--- Returns `true, payload` or `false, RemoteError`; raises ProtocolError.
function frames.validate_reverse_response(v, request_id)
  strict(v, REVERSE_RESPONSE_FIELDS)
  if text_of(v, "frame") ~= "reverse_response" or text_of(v, "apiVersion") ~= API_VERSION or text_of(v, "id") ~= request_id then
    violation()
  end
  local failed = not absent(v.error)
  -- A response carries a payload (null counts) or an error, never both or neither.
  if failed == (v.payload ~= nil) then violation() end
  if not failed then return true, v.payload end
  local e = strict(v.error, ERROR_FIELDS)
  local retry, rtext = count_of(e, "retryAfterMilliseconds")
  local message = text_of(e, "message")
  local size = frames.utf8_length(message)
  if not is_identifier(text_of(e, "code"))
    or size < 0
    or size > core.MAX_MESSAGE_BYTES
    or core.decimal_compare(rtext, "31536000000") > 0
  then
    violation()
  end
  return false, core.remote_error(e.code, message, retry)
end

-- --- the connection --------------------------------------------------------------------------------

--- One connection with the host: frames written one at a time, and the read side that the code
--- waiting for an answer drives. `failure` is set when the host broke the rules.
local Link = {}
Link.__index = Link
frames.Link = Link

function Link.new(input, output)
  return setmetatable({ input = input, output = output, issued = 0, ended = false, failure = nil }, Link)
end

--- Writes one encoded frame and its line feed. Raises a string error when the stream fails.
function Link:write(frame)
  local ok, err = self.output:write(frame, "\n")
  if not ok then error("write failed: " .. tostring(err), 0) end
  ok, err = self.output:flush()
  if not ok and err ~= nil then error("flush failed: " .. tostring(err), 0) end
end

local function violated(self)
  self.failure = core.protocol_error("plugin protocol violation")
  self.ended = true
  return core.frame_error("protocol", "plugin protocol violation")
end

--- Calls a host operation and waits for the answer. `parent` is the id of the call it belongs
--- to; `deadline` is epoch seconds. Returns the host's payload; raises RemoteError or FrameError.
function Link:reverse(parent, name, version, operation, payload, deadline)
  if self.ended then core.raise_frame("closed", "plugin connection ended") end
  self.issued = self.issued + 1
  local request_id = "g" .. self.issued
  local frame = ordered(
    "frame", "reverse_request",
    "apiVersion", API_VERSION,
    "id", request_id,
    "parent", (parent ~= nil and parent ~= "") and parent or nil,
    "contract", ordered("name", name, "version", version),
    "operation", operation,
    "deadline", type(deadline) == "number" and core.format_time(deadline) or nil,
    "payload", payload == nil and null or payload
  )
  local ok, data = pcall(function()
    frames.validate_reverse_request(frame)
    local encoded = core.encode_frame(frame)
    if #encoded > core.MAX_FRAME_BYTES then error("frame too large") end
    core.decode_frame(encoded) -- the payload must read back as the host reads it
    return encoded
  end)
  if not ok then core.raise_frame("invalid", "reverse request breaks the rules") end
  if not pcall(self.write, self, data) then
    self.ended = true
    core.raise_frame("closed", "plugin connection ended")
  end
  -- The host sends nothing but this answer during a call.
  local read_ok, raw = pcall(core.read_frame, self.input)
  if not read_ok then
    if core.is_protocol_error(raw) then error(violated(self), 0) end
    raw = nil
  end
  if raw == nil then
    self.ended = true
    core.raise_frame("closed", "plugin connection ended")
  end
  local judged, accepted, result = pcall(function()
    local answer = core.decode_frame(raw)
    if not json.is_object(answer) or answer.frame ~= "reverse_response" or answer.id ~= request_id then
      violation()
    end
    return frames.validate_reverse_response(answer, request_id)
  end)
  if not judged then error(violated(self), 0) end
  if not accepted then error(result, 0) end
  return result
end

--- Calls an operation the host offers (declared in the descriptor's `reverse` list) and waits for
--- the answer. A guest that reads inline makes one call at a time.
local ReverseCaller = {}
ReverseCaller.__index = ReverseCaller
frames.ReverseCaller = ReverseCaller

--- Several reverse calls cannot be outstanding at once.
ReverseCaller.concurrent = false

function ReverseCaller.new(link, declared, parent, deadline, clock)
  return setmetatable({ link = link, declared = declared, parent = parent, deadline = deadline, clock = clock, over = false }, ReverseCaller)
end

--- A contract reference: {name, version} or {name = ..., version = ...}.
function frames.contract_ref(name, version)
  return { name = name, version = version, name, version }
end

--- Calls `operation` of `contract` (a {name, version} pair) with a JSON payload and returns the
--- host's payload. The host's refusal or error raises a RemoteError with its code
--- ("reverse_undeclared" when the descriptor does not list it, "denied", "busy", "too_large",
--- "expired", ...); a local failure raises a FrameError. `timeout` seconds bound the deadline the
--- host is given, never past the call's own; the wait itself ends with the host's answer or the
--- end of the connection, since standard input cannot be read with a timeout.
function ReverseCaller:call(contract, operation, payload, timeout)
  local name = contract and (contract.name or contract[1])
  local version = contract and (contract.version or contract[2])
  local key = tostring(name) .. "\0" .. tostring(version) .. "\0" .. tostring(operation)
  if type(name) ~= "string" or type(version) ~= "string" or type(operation) ~= "string" or not self.declared[key] then
    error(core.remote_error("reverse_undeclared", "reverse call refused"), 0)
  end
  if self.over then core.raise_frame("closed", "the call is over") end
  local deadline = self.deadline
  if timeout ~= nil then
    deadline = math.min(deadline, self.clock.now() + math.max(0, timeout))
  end
  return self.link:reverse(self.parent, name, version, operation, payload, deadline)
end

--- Sends the progress events of one call, within the rate limit the host set.
local Reporter = {}
Reporter.__index = Reporter
frames.Reporter = Reporter

function Reporter.new(link, parent, limits, clock)
  local self = setmetatable({ link = link, parent = parent, clock = clock, finished = false }, Reporter)
  self.limiter = Limiter.new(limits.events_per_second, limits.bytes_per_second, clock.monotonic_ns())
  return self
end

local function count_ok(n)
  return core.is_integer(n) and n >= 0 and n < 18446744073709551616
end

--- Events reach the host in the order they are reported, and none after the call is over.
function Reporter:report(event)
  local ok = pcall(function()
    if type(event) ~= "table"
      or type(event.kind) ~= "string"
      or type(event.stream) ~= "string"
      or type(event.text) ~= "string"
      or not count_ok(event.completed)
      or not count_ok(event.total)
    then
      violation()
    end
    check_event(event.kind, event.stream, event.text, event.completed > event.total, event.total == 0)
  end)
  if not ok then core.raise_frame("invalid", "progress event breaks the rules") end
  local dropped = self.limiter:admit(#event.text, self.clock.monotonic_ns())
  if dropped == nil then return frames.DROPPED end
  if self.finished then core.raise_frame("closed", "the call is over") end
  self:write(event.kind, event.stream, event.text, event.completed, event.total, dropped)
  return frames.SENT
end

--- Ends reporting after a marker for events never reported: nothing may follow the response.
function Reporter:finish()
  if self.finished then return end
  self.finished = true
  local pending = self.limiter:pending()
  if pending > 0 then
    pcall(self.write, self, "log", "", "", 0, 0, pending) -- the connection may be gone
  end
end

function Reporter:write(kind, stream, text, completed, total, dropped)
  local frame = ordered("frame", "progress", "apiVersion", API_VERSION, "parent", self.parent, "kind", kind)
  if stream ~= "" then core.append(frame, "stream", stream) end
  if text ~= "" then core.append(frame, "text", text) end
  if completed ~= 0 then core.append(frame, "completed", completed) end
  if total ~= 0 then core.append(frame, "total", total) end
  if dropped ~= 0 then core.append(frame, "dropped", dropped) end
  if not pcall(self.link.write, self.link, core.encode_frame(frame)) then
    core.raise_frame("closed", "plugin connection ended")
  end
end

return frames
