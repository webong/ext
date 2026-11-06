--- What the guest and frames modules share: the wire limits, the patterns, the error types,
--- strict frame decoding and encoding, whole numbers as Go decodes them, and RFC 3339 time.
local json = require("ext_io.json")

local core = {}

local byte, sub, find, format = string.byte, string.sub, string.find, string.format
local floor = math.floor
local mtype = math.type

core.API_VERSION = "ext.plugin/v1"
core.MAX_FRAME_BYTES = 24 * 1024 * 1024
core.MAX_RETRY_AFTER_MS = 365 * 24 * 60 * 60 * 1000
core.MAX_MESSAGE_BYTES = 4096
core.MAX_DEPTH = 64

local null = json.null
core.null = null

-- --- patterns ---------------------------------------------------------------------

--- [a-z0-9][a-z0-9._/-]{0,255}
function core.is_identifier(v)
  return type(v) == "string" and #v >= 1 and #v <= 256 and find(v, "^[a-z0-9][a-z0-9._/%-]*$") ~= nil
end

--- [A-Za-z0-9][A-Za-z0-9._:+/-]{0,255}
function core.is_revision(v)
  return type(v) == "string" and #v >= 1 and #v <= 256 and find(v, "^[A-Za-z0-9][A-Za-z0-9._:+/%-]*$") ~= nil
end

--- A value that is absent: nil or JSON null.
function core.absent(v)
  return v == nil or v == null
end

--- True when t is an object whose keys are all in the set `allowed`.
function core.only(t, allowed)
  if not json.is_object(t) then return false end
  for k in pairs(t) do
    if not allowed[k] then return false end
  end
  return true
end

function core.set(list)
  local out = {}
  for i = 1, #list do out[list[i]] = true end
  return out
end

--- An object with keys in the given order: core.ordered("a", 1, "b", 2). A nil value leaves its
--- key out.
function core.ordered(...)
  local t, keys = {}, {}
  local n = select("#", ...)
  for i = 1, n, 2 do
    local k, v = select(i, ...)
    if v ~= nil then
      t[k] = v
      keys[#keys + 1] = k
    end
  end
  return json.object(t, keys)
end

--- Appends key k with value v to an object made by core.ordered.
function core.append(t, k, v)
  if t[k] == nil then
    local keys = json.key_order(t)
    if keys then keys[#keys + 1] = k end
  end
  t[k] = v
end

-- --- error types ---------------------------------------------------------------------

local function error_type(name)
  local mt = { __name = name }
  mt.__index = mt
  mt.__tostring = function(e) return e.message end
  return mt
end

--- The peer violated the framing or envelope rules; the stream is unusable.
local ProtocolError = error_type("ext_io.ProtocolError")
core.ProtocolError = ProtocolError

function core.protocol_error(message)
  return setmetatable({ message = message }, ProtocolError)
end

function core.is_protocol_error(v)
  return getmetatable(v) == ProtocolError
end

function core.raise_protocol(message)
  error(core.protocol_error(message), 0)
end

--- A whole number as Lua holds one: an integer (Lua 5.3+), or a float with no fraction on
--- Lua 5.1 and LuaJIT, where every number is a float.
local function is_integer(n)
  if type(n) ~= "number" then return false end
  if mtype then return mtype(n) == "integer" end
  return n == n and n ~= math.huge and n ~= -math.huge and floor(n) == n
end
core.is_integer = is_integer

--- A deliberate public failure sent to the host as the structured error.
local RemoteError = error_type("ext_io.RemoteError")
core.RemoteError = RemoteError

--- `code` must match the identifier syntax, `message` is UTF-8 of at most 4096 bytes, and
--- `retry_after_ms` is nil or a whole number of milliseconds in [0, one year]. Raise the result
--- with error(): error(plugin.remote_error("busy", "try later", 250)).
function core.remote_error(code, message, retry_after_ms)
  if not core.is_identifier(code) then
    error("error code must match [a-z0-9][a-z0-9._/-]{0,255}", 2)
  end
  if type(message) ~= "string" or #message > core.MAX_MESSAGE_BYTES or not json.utf8_valid(message) then
    error("error message must be UTF-8 of at most 4096 bytes", 2)
  end
  if retry_after_ms == null then retry_after_ms = nil end
  if retry_after_ms ~= nil and (not is_integer(retry_after_ms) or retry_after_ms < 0 or retry_after_ms > core.MAX_RETRY_AFTER_MS) then
    error("retry_after_ms must be a whole number in [0, one year]", 2)
  end
  return setmetatable({ code = code, message = message, retry_after_ms = retry_after_ms }, RemoteError)
end

function core.is_remote_error(v)
  return getmetatable(v) == RemoteError
end

function RemoteError:to_wire()
  local wire = core.ordered("code", self.code, "message", self.message)
  if self.retry_after_ms and self.retry_after_ms ~= 0 then
    core.append(wire, "retryAfterMilliseconds", self.retry_after_ms)
  end
  return wire
end

--- A progress or reverse-call operation failed locally (a host's refusal is a RemoteError).
--- `code` is "unsupported", "invalid", "closed" or "protocol".
local FrameError = error_type("ext_io.FrameError")
core.FrameError = FrameError

function core.frame_error(code, message)
  return setmetatable({ code = code, message = message or code }, FrameError)
end

function core.is_frame_error(v)
  return getmetatable(v) == FrameError
end

function core.raise_frame(code, message)
  error(core.frame_error(code, message), 0)
end

-- --- whole numbers as Go decodes them ---------------------------------------------------

-- Compares two decimal strings of digits without leading zeros.
local function decimal_compare(a, b)
  if #a ~= #b then return #a < #b and -1 or 1 end
  if a == b then return 0 end
  return a < b and -1 or 1
end
core.decimal_compare = decimal_compare

local function integer_text(n)
  if mtype and mtype(n) == "integer" then return format("%d", n) end
  return format("%.0f", n)
end

--- The decimal text of the whole number at container[key] (sign and digits, -0 as 0), or nil
--- when it is not a number Go would decode into an integer: a fraction or an exponent (1.0 and
--- 1e2 included) is not. Uses the text decoding kept, so values past 2^53 compare exactly.
local function whole_text(container, key)
  local v = container[key]
  if type(v) ~= "number" then return nil end
  local text = json.number_text(container, key)
  if text then
    if not find(text, "^%-?%d+$") then return nil end
  else
    if not is_integer(v) then return nil end
    text = integer_text(v)
  end
  if text == "-0" then text = "0" end
  return text
end

local UINT64_MAX = "18446744073709551615"
local INT64_MAX = "9223372036854775807"
local INT64_MIN_ABS = "9223372036854775808"

--- A member Go decodes into a uint64: absent and null are zero; anything but a whole number that
--- fits is refused. Returns the value and its decimal text, or nil.
function core.uint64(container, key)
  local v = container[key]
  if core.absent(v) then return 0, "0" end
  local text = whole_text(container, key)
  if not text or sub(text, 1, 1) == "-" or decimal_compare(text, UINT64_MAX) > 0 then return nil end
  return v, text
end

--- A member Go decodes into an int64, as core.uint64.
function core.int64(container, key)
  local v = container[key]
  if core.absent(v) then return 0, "0" end
  local text = whole_text(container, key)
  if not text then return nil end
  if sub(text, 1, 1) == "-" then
    if decimal_compare(sub(text, 2), INT64_MIN_ABS) > 0 then return nil end
  elseif decimal_compare(text, INT64_MAX) > 0 then
    return nil
  end
  return v, text
end

-- --- frames ---------------------------------------------------------------------------

--- Decodes one frame (bytes without the LF) with the Go decoder's rules. Raises ProtocolError.
function core.decode_frame(raw)
  if #raw > core.MAX_FRAME_BYTES then core.raise_protocol("frame too large") end
  local ok, value = pcall(json.decode, raw, { max_depth = core.MAX_DEPTH })
  if not ok then
    if core.is_protocol_error(value) then error(value, 0) end
    core.raise_protocol("malformed JSON: " .. tostring(value))
  end
  return value
end

--- Encodes a frame as ASCII JSON (without the LF). Raises a string error for values JSON cannot
--- hold.
function core.encode_frame(value)
  return json.encode(value)
end

-- Lua 5.1 has no "*L" read format, which keeps the line feed; it reads one byte at a time.
core.byte_reader = (_VERSION == "Lua 5.1" and not rawget(_G, "jit"))

--- Reads one LF-terminated frame from a stream with a `read` method; nil at a clean end.
--- Raises ProtocolError for a frame over the limit or an end inside a frame.
function core.read_frame(input)
  local line
  if core.byte_reader then
    local parts, n = {}, 0
    while true do
      local c = input:read(1)
      if c == nil or c == "" then break end
      n = n + 1
      parts[n] = c
      if c == "\n" or n > core.MAX_FRAME_BYTES + 1 then break end
    end
    if n > 0 then line = table.concat(parts) end
  else
    line = input:read("*L")
  end
  if line == nil or line == "" then return nil end
  if byte(line, -1) ~= 10 then
    if #line > core.MAX_FRAME_BYTES + 1 then core.raise_protocol("frame too large") end
    core.raise_protocol("unexpected EOF inside a frame")
  end
  return sub(line, 1, -2)
end

-- --- time -------------------------------------------------------------------------------

local ZERO_TIME = -62135596800

local function days_from_civil(y, m, d)
  if m <= 2 then y = y - 1 end
  local era = floor(y / 400)
  local yoe = y - era * 400
  local mp = (m + 9) % 12
  local doy = floor((153 * mp + 2) / 5) + d - 1
  local doe = yoe * 365 + floor(yoe / 4) - floor(yoe / 100) + doy
  return era * 146097 + doe - 719468
end

local function civil_from_days(z)
  z = z + 719468
  local era = floor(z / 146097)
  local doe = z - era * 146097
  local yoe = floor((doe - floor(doe / 1460) + floor(doe / 36524) - floor(doe / 146096)) / 365)
  local y = yoe + era * 400
  local doy = doe - (365 * yoe + floor(yoe / 4) - floor(yoe / 100))
  local mp = floor((5 * doy + 2) / 153)
  local d = doy - floor((153 * mp + 2) / 5) + 1
  local m = mp < 10 and mp + 3 or mp - 9
  if m <= 2 then y = y + 1 end
  return y, m, d
end

local function days_in_month(y, m)
  if m == 2 then
    return ((y % 4 == 0 and y % 100 ~= 0) or y % 400 == 0) and 29 or 28
  end
  return (m == 4 or m == 6 or m == 9 or m == 11) and 30 or 31
end

--- RFC 3339 to epoch seconds; nil when invalid or Go's zero time ("no deadline given").
function core.parse_deadline(v)
  if type(v) ~= "string" then return nil end
  local Y, M, D, h, mi, s, rest = v:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)[Tt](%d%d):(%d%d):(%d%d)(.*)$")
  if not Y then return nil end
  local micro = 0
  local frac = rest:match("^%.(%d+)")
  if frac then
    micro = tonumber(sub(frac .. "000000", 1, 6))
    rest = sub(rest, #frac + 2)
  end
  local offset
  if rest == "Z" or rest == "z" then
    offset = 0
  else
    local sign, oh, om = rest:match("^([+-])(%d%d):(%d%d)$")
    if not sign then return nil end
    offset = (tonumber(oh) * 3600 + tonumber(om) * 60) * (sign == "+" and 1 or -1)
    if offset <= -86400 or offset >= 86400 then return nil end
  end
  Y, M, D, h, mi, s = tonumber(Y), tonumber(M), tonumber(D), tonumber(h), tonumber(mi), tonumber(s)
  if Y < 1 or M < 1 or M > 12 or D < 1 or D > days_in_month(Y, M) or h > 23 or mi > 59 or s > 59 then
    return nil
  end
  local seconds = days_from_civil(Y, M, D) * 86400 + h * 3600 + mi * 60 + s - offset
  if seconds == ZERO_TIME and micro == 0 then return nil end
  return seconds + micro / 1e6
end

--- Epoch seconds as RFC 3339 in UTC with milliseconds, rounded down, so the instant is never
--- after the given one.
function core.format_time(seconds)
  local micros = floor(seconds * 1e6 + 0.5)
  local whole = floor(micros / 1e6)
  local ms = floor((micros - whole * 1e6) / 1000)
  local days = floor(whole / 86400)
  local rem = whole - days * 86400
  local y, m, d = civil_from_days(days)
  return format("%04d-%02d-%02dT%02d:%02d:%02d.%03dZ", y, m, d, floor(rem / 3600), floor(rem / 60) % 60, rem % 60, ms)
end

return core
