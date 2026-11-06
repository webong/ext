--- Strict JSON for ext-io (pure Lua; Lua 5.1 to 5.5 and LuaJIT).
--
-- Values map to Lua as follows:
--
--   JSON object  -> table with metatable `json.object_mt` (key order is remembered)
--   JSON array   -> table with metatable `json.array_mt`, elements 1..n
--   JSON null    -> the sentinel `json.null` (never nil, so it survives inside tables)
--   JSON string  -> Lua string (UTF-8)
--   JSON number  -> Lua number: an integer when the text has no fraction or exponent and the
--                   value fits (Lua 5.3+), else a float. Lua 5.1 and LuaJIT have only floats.
--   true/false   -> booleans
--
-- When encoding, a table marked with `json.object`/`json.array` is that kind. A plain table is
-- an array when its keys are exactly 1..n (n >= 1) and an object when its keys are all strings;
-- an empty plain table is an object (`{}`), so write `json.array()` for an empty array. Objects
-- are written in the order their keys were decoded or given to `json.object(t, keys)`; other keys
-- follow in sorted order. `nil` and `json.null` are written as null.
--
-- Decoding is strict: input must be UTF-8; duplicate keys, trailing data, NaN/Infinity, leading
-- zeros, control characters in strings, invalid escapes and unpaired surrogate escapes are
-- refused, as is nesting deeper than `max_depth` (default 1000; a scalar at depth n counts).
local json = {}

local byte, char, sub, find, format, concat = string.byte, string.char, string.sub, string.find, string.format, table.concat
local floor, huge = math.floor, math.huge
local mtype = math.type
local type, pairs, tostring, tonumber, setmetatable, getmetatable, error, pcall =
  type, pairs, tostring, tonumber, setmetatable, getmetatable, error, pcall

json.null = setmetatable({}, { __tostring = function() return "null" end, __name = "json.null" })
local null = json.null
json.object_mt = { __name = "json.object" }
json.array_mt = { __name = "json.array" }
local object_mt, array_mt = json.object_mt, json.array_mt

-- Key order of objects, and the source text of numbers whose value alone loses information
-- (a fraction or exponent, sixteen or more characters, or -0), by container. Weak keys, so
-- neither keeps a table alive.
local orders = setmetatable({}, { __mode = "k" })
local lexemes = setmetatable({}, { __mode = "k" })

-- Lua 5.1 patterns cannot hold a zero byte; %z is its class for one.
local ZERO
do
  local ok, at = pcall(find, "a\0", "[\0]")
  ZERO = (ok and at == 2) and "\0" or "%z"
end
local STRING_STOP = '["\\' .. ZERO .. '\1-\31]'
local ESCAPE_ASCII = '[' .. ZERO .. '\1-\31"\\\127]'
local ESCAPE_UTF8 = '[' .. ZERO .. '\1-\31"\\]'

--- Marks `t` (or a new table) as a JSON object. `keys`, when given, is the order to write it in.
function json.object(t, keys)
  t = t or {}
  setmetatable(t, object_mt)
  if keys then orders[t] = keys end
  return t
end

--- Marks `t` (or a new table) as a JSON array.
function json.array(t)
  return setmetatable(t or {}, array_mt)
end

--- The remembered key order of an object, or nil.
function json.key_order(t)
  return orders[t]
end

--- The source text of the number at `container[key]` when decoding kept it, or nil. Decoding
--- keeps it only when the value alone loses information: the text has a fraction or an exponent,
--- has sixteen or more characters, or is -0.
function json.number_text(container, key)
  local texts = lexemes[container]
  return texts and texts[key]
end

local function array_length(t)
  local n, max = 0, 0
  for k in pairs(t) do
    if type(k) ~= "number" or k < 1 or floor(k) ~= k then return nil end
    n = n + 1
    if k > max then max = k end
  end
  if n ~= max then return nil end
  return n
end

local function all_string_keys(t)
  for k in pairs(t) do
    if type(k) ~= "string" then return false end
  end
  return true
end

--- True when v is a table that encodes as an object (an empty plain table counts).
function json.is_object(v)
  if type(v) ~= "table" or v == null then return false end
  if getmetatable(v) == array_mt then return false end
  return all_string_keys(v)
end

--- True when v is a table that encodes as an array (an empty plain table counts).
function json.is_array(v)
  if type(v) ~= "table" or v == null then return false end
  if getmetatable(v) == object_mt then return false end
  return array_length(v) ~= nil
end

--- The length of v when it is an array, else nil.
function json.array_length(v)
  if not json.is_array(v) then return nil end
  return array_length(v)
end

-- --- UTF-8 ---------------------------------------------------------------------

--- True when s is well-formed UTF-8 (no overlong forms, surrogates or code points past U+10FFFF).
local function utf8_valid(s)
  local i = 1
  while true do
    i = find(s, "[\128-\255]", i)
    if not i then return true end
    local c = byte(s, i)
    if c >= 194 and c <= 223 then
      local c2 = byte(s, i + 1)
      if not c2 or c2 < 128 or c2 > 191 then return false end
      i = i + 2
    elseif c >= 224 and c <= 239 then
      local c2, c3 = byte(s, i + 1, i + 2)
      if not c3 then return false end
      local lo, hi = 128, 191
      if c == 224 then lo = 160 elseif c == 237 then hi = 159 end
      if c2 < lo or c2 > hi or c3 < 128 or c3 > 191 then return false end
      i = i + 3
    elseif c >= 240 and c <= 244 then
      local c2, c3, c4 = byte(s, i + 1, i + 3)
      if not c4 then return false end
      local lo, hi = 128, 191
      if c == 240 then lo = 144 elseif c == 244 then hi = 143 end
      if c2 < lo or c2 > hi or c3 < 128 or c3 > 191 or c4 < 128 or c4 > 191 then return false end
      i = i + 4
    else
      return false
    end
  end
end
json.utf8_valid = utf8_valid

--- The UTF-8 encoding of code point cp.
local function utf8_char(cp)
  if cp < 128 then return char(cp) end
  if cp < 2048 then return char(192 + floor(cp / 64), 128 + cp % 64) end
  if cp < 65536 then
    return char(224 + floor(cp / 4096), 128 + floor(cp / 64) % 64, 128 + cp % 64)
  end
  return char(240 + floor(cp / 262144), 128 + floor(cp / 4096) % 64, 128 + floor(cp / 64) % 64, 128 + cp % 64)
end
json.utf8_char = utf8_char

--- The code point of the well-formed sequence seq.
local function utf8_code(seq)
  local c1, c2, c3, c4 = byte(seq, 1, 4)
  if c1 < 128 then return c1 end
  if c1 < 224 then return (c1 - 192) * 64 + (c2 - 128) end
  if c1 < 240 then return (c1 - 224) * 4096 + (c2 - 128) * 64 + (c3 - 128) end
  return (c1 - 240) * 262144 + (c2 - 128) * 4096 + (c3 - 128) * 64 + (c4 - 128)
end
json.utf8_code = utf8_code

-- --- encoding --------------------------------------------------------------------

local short_escapes = { ['"'] = '\\"', ["\\"] = "\\\\", ["\b"] = "\\b", ["\f"] = "\\f", ["\n"] = "\\n", ["\r"] = "\\r", ["\t"] = "\\t" }
local escapes = {}
for c = 0, 31 do
  escapes[char(c)] = short_escapes[char(c)] or format("\\u%04x", c)
end
escapes['"'], escapes["\\"], escapes["\127"] = '\\"', "\\\\", "\\u007f"

local function escape_code(seq)
  local cp = utf8_code(seq)
  if cp < 65536 then return format("\\u%04x", cp) end
  cp = cp - 65536
  return format("\\u%04x\\u%04x", 55296 + floor(cp / 1024), 56320 + cp % 1024)
end

local function encode_string(s, ascii)
  if not utf8_valid(s) then error("json: string is not UTF-8", 0) end
  if ascii then
    s = s:gsub(ESCAPE_ASCII, escapes)
    if find(s, "[\128-\255]") then s = s:gsub("[\192-\247][\128-\191]*", escape_code) end
  else
    s = s:gsub(ESCAPE_UTF8, escapes)
  end
  return '"' .. s .. '"'
end

local function shortest(n)
  for precision = 15, 17 do
    local s = format("%." .. precision .. "g", n)
    if tonumber(s) == n then return s end
  end
  return format("%.17g", n)
end

-- A float as Python writes one: the shortest text that reads back, with ".0" when it looks whole.
local function float_text(n)
  local s = shortest(n)
  if not find(s, "[%.eEn]") then s = s .. ".0" end
  return s
end

local function encode_number(n, source)
  if n ~= n or n == huge or n == -huge then error("json: number is not finite", 0) end
  -- A decoded number written back unchanged keeps what its text said: an integer past what a
  -- Lua number holds exactly stays that integer, and a float stays a float on Lua 5.1.
  if source and tonumber(source) == n then
    if not find(source, "[%.eE]") then
      return source == "-0" and "0" or source
    end
    return float_text(n)
  end
  if mtype then
    if mtype(n) == "integer" then return format("%d", n) end
    return float_text(n)
  end
  if floor(n) == n and n > -9007199254740992 and n < 9007199254740992 then
    if n == 0 then return "0" end
    return format("%.0f", n)
  end
  return shortest(n)
end
json.encode_number = encode_number

local function object_keys(t)
  local keys, seen = {}, {}
  local order = orders[t]
  if order then
    for i = 1, #order do
      local k = order[i]
      if t[k] ~= nil and not seen[k] then
        keys[#keys + 1] = k
        seen[k] = true
      end
    end
  end
  local rest = {}
  for k in pairs(t) do
    if type(k) ~= "string" then error("json: object key is not a string", 0) end
    if not seen[k] then rest[#rest + 1] = k end
  end
  table.sort(rest)
  for i = 1, #rest do keys[#keys + 1] = rest[i] end
  return keys
end

local encode_value

local function encode_table(t, buf, ascii, visiting)
  local texts = lexemes[t]
  if visiting[t] then error("json: circular reference", 0) end
  local mt = getmetatable(t)
  local as_array
  local n
  if mt == array_mt then
    n = array_length(t)
    if not n then error("json: array has keys other than 1..n", 0) end
    as_array = true
  elseif mt == object_mt then
    as_array = false
  else
    if next(t) == nil then
      as_array = false
    else
      n = array_length(t)
      if n then
        as_array = true
      elseif all_string_keys(t) then
        as_array = false
      else
        error("json: table mixes array and object keys", 0)
      end
    end
  end
  visiting[t] = true
  if as_array then
    buf[#buf + 1] = "["
    for i = 1, n do
      if i > 1 then buf[#buf + 1] = "," end
      encode_value(t[i], buf, ascii, visiting, texts and texts[i])
    end
    buf[#buf + 1] = "]"
  else
    buf[#buf + 1] = "{"
    local keys = object_keys(t)
    for i = 1, #keys do
      if i > 1 then buf[#buf + 1] = "," end
      buf[#buf + 1] = encode_string(keys[i], ascii)
      buf[#buf + 1] = ":"
      encode_value(t[keys[i]], buf, ascii, visiting, texts and texts[keys[i]])
    end
    buf[#buf + 1] = "}"
  end
  visiting[t] = nil
end

encode_value = function(v, buf, ascii, visiting, source)
  local t = type(v)
  if v == nil or v == null then
    buf[#buf + 1] = "null"
  elseif t == "boolean" then
    buf[#buf + 1] = v and "true" or "false"
  elseif t == "number" then
    buf[#buf + 1] = encode_number(v, source)
  elseif t == "string" then
    buf[#buf + 1] = encode_string(v, ascii)
  elseif t == "table" then
    encode_table(v, buf, ascii, visiting)
  else
    error("json: cannot encode a " .. t, 0)
  end
end

--- Encodes v compactly. With `opts.ascii ~= false` (the default) every character past U+007E is
--- written as a \u escape, so the output is ASCII; with `opts.ascii == false` text stays UTF-8.
--- Raises a string error for values JSON cannot hold.
function json.encode(v, opts)
  local ascii = not (opts and opts.ascii == false)
  local buf = {}
  encode_value(v, buf, ascii, {})
  return concat(buf)
end

-- --- decoding --------------------------------------------------------------------

local simple_escapes = { [34] = '"', [92] = "\\", [47] = "/", [98] = "\b", [102] = "\f", [110] = "\n", [114] = "\r", [116] = "\t" }

--- Decodes the JSON text s. `opts.max_depth` bounds nesting (default 1000). Raises a string error.
function json.decode(s, opts)
  if type(s) ~= "string" then error("json: input is not a string", 0) end
  local max_depth = (opts and opts.max_depth) or 1000
  if not utf8_valid(s) then error("json: input is not UTF-8", 0) end
  local pos = 1
  local kept -- the text of the number just parsed, when it must be kept

  local function fail(what)
    error(format("json: %s at byte %d", what, pos), 0)
  end

  local function skip()
    local _, e = find(s, "^[ \t\r\n]*", pos)
    pos = e + 1
  end

  local function parse_string()
    local i = pos + 1
    local parts, n = nil, 0
    while true do
      local j = find(s, STRING_STOP, i)
      if not j then pos = #s + 1; fail("unterminated string") end
      local c = byte(s, j)
      if c == 34 then
        pos = j + 1
        if not parts then return sub(s, i, j - 1) end
        n = n + 1
        parts[n] = sub(s, i, j - 1)
        return concat(parts)
      elseif c == 92 then
        parts = parts or {}
        n = n + 1
        parts[n] = sub(s, i, j - 1)
        local e = byte(s, j + 1)
        if e == 117 then
          local hex = sub(s, j + 2, j + 5)
          pos = j
          if not find(hex, "^%x%x%x%x$") then fail("invalid \\u escape") end
          local cp = tonumber(hex, 16)
          local after = j + 6
          if cp >= 55296 and cp <= 56319 then
            local low = sub(s, after, after + 1) == "\\u" and sub(s, after + 2, after + 5)
            local lo = low and find(low, "^%x%x%x%x$") and tonumber(low, 16)
            if not lo or lo < 56320 or lo > 57343 then fail("unpaired surrogate escape") end
            cp = 65536 + (cp - 55296) * 1024 + (lo - 56320)
            after = after + 6
          elseif cp >= 56320 and cp <= 57343 then
            fail("unpaired surrogate escape")
          end
          n = n + 1
          parts[n] = utf8_char(cp)
          i = after
        else
          local r = e and simple_escapes[e]
          if not r then pos = j; fail("invalid escape") end
          n = n + 1
          parts[n] = r
          i = j + 2
        end
      else
        pos = j
        fail("control character in string")
      end
    end
  end

  local function parse_number()
    local start, i = pos, pos
    if byte(s, i) == 45 then i = i + 1 end
    local c = byte(s, i)
    if c == 48 then
      i = i + 1
    elseif c and c >= 49 and c <= 57 then
      local _, e = find(s, "^%d*", i + 1)
      i = e + 1
    else
      fail("invalid number")
    end
    local float = false
    if byte(s, i) == 46 then
      local _, e = find(s, "^%d+", i + 1)
      if not e then pos = i; fail("invalid number") end
      i = e + 1
      float = true
    end
    c = byte(s, i)
    if c == 101 or c == 69 then
      local _, e = find(s, "^[+-]?%d+", i + 1)
      if not e then pos = i; fail("invalid number") end
      i = e + 1
      float = true
    end
    local text = sub(s, start, i - 1)
    pos = i
    local value = tonumber(text)
    if float then
      value = value + 0.0
    elseif value == 0 then
      value = 0 -- -0 is the integer zero
    end
    if float or #text >= 16 or text == "-0" then kept = text end
    return value
  end

  local parse_value

  local function parse_object(depth)
    local obj, keys = setmetatable({}, object_mt), {}
    pos = pos + 1
    skip()
    if byte(s, pos) == 125 then
      pos = pos + 1
      orders[obj] = keys
      return obj
    end
    local texts
    while true do
      if byte(s, pos) ~= 34 then fail("expected a key") end
      local key = parse_string()
      if obj[key] ~= nil then fail("duplicate key") end
      skip()
      if byte(s, pos) ~= 58 then fail("expected ':'") end
      pos = pos + 1
      skip()
      kept = nil
      obj[key] = parse_value(depth + 1)
      if kept then
        texts = texts or {}
        texts[key] = kept
        kept = nil
      end
      keys[#keys + 1] = key
      skip()
      local c = byte(s, pos)
      if c == 44 then
        pos = pos + 1
        skip()
      elseif c == 125 then
        pos = pos + 1
        break
      else
        fail("expected ',' or '}'")
      end
    end
    orders[obj] = keys
    if texts then lexemes[obj] = texts end
    return obj
  end

  local function parse_array(depth)
    local arr, n = setmetatable({}, array_mt), 0
    pos = pos + 1
    skip()
    if byte(s, pos) == 93 then
      pos = pos + 1
      return arr
    end
    local texts
    while true do
      kept = nil
      n = n + 1
      arr[n] = parse_value(depth + 1)
      if kept then
        texts = texts or {}
        texts[n] = kept
        kept = nil
      end
      skip()
      local c = byte(s, pos)
      if c == 44 then
        pos = pos + 1
        skip()
      elseif c == 93 then
        pos = pos + 1
        break
      else
        fail("expected ',' or ']'")
      end
    end
    if texts then lexemes[arr] = texts end
    return arr
  end

  parse_value = function(depth)
    if depth > max_depth then fail("nesting too deep") end
    local c = byte(s, pos)
    if c == 123 then return parse_object(depth) end
    if c == 91 then return parse_array(depth) end
    if c == 34 then return parse_string() end
    if c == 45 or (c and c >= 48 and c <= 57) then return parse_number() end
    if sub(s, pos, pos + 3) == "true" then pos = pos + 4; return true end
    if sub(s, pos, pos + 4) == "false" then pos = pos + 5; return false end
    if sub(s, pos, pos + 3) == "null" then pos = pos + 4; return null end
    fail(c and "unexpected character" or "unexpected end of input")
  end

  skip()
  local value = parse_value(0)
  skip()
  if pos <= #s then fail("trailing data") end
  return value
end

return json
