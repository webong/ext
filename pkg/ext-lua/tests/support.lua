--- Assertions, fixtures and in-memory streams for the tests (no dependencies).
local json = require("ext_io.json")

local support = {}

support.HERE = assert(rawget(_G, "EXT_LUA_TESTS"), "run the tests through tests/run.lua")
support.ROOT = support.HERE .. "/.."
support.FIXTURES = support.ROOT .. "/../../lib/plugin/testdata/v1"

local function show(v)
  if type(v) == "table" and v ~= json.null then
    local ok, s = pcall(json.encode, v)
    if ok then return s end
  end
  if type(v) == "string" then return string.format("%q", #v > 200 and v:sub(1, 200) .. "..." or v) end
  return tostring(v)
end
support.show = show

--- Deep equality of JSON-like values; metatables and key order do not count.
local function same(a, b)
  if a == b then return true end
  if type(a) ~= "table" or type(b) ~= "table" or a == json.null or b == json.null then return false end
  for k, v in pairs(a) do
    if not same(v, b[k]) then return false end
  end
  for k in pairs(b) do
    if a[k] == nil then return false end
  end
  return true
end
support.same = same

function support.eq(got, want, what)
  if not same(got, want) then
    error(string.format("%sgot %s, want %s", what and (what .. ": ") or "", show(got), show(want)), 2)
  end
end

function support.ok(cond, what)
  if not cond then error(what or "assertion failed", 2) end
end

--- Calls f and returns its error; fails when f does not raise.
function support.raises(f, what)
  local ok, err = pcall(f)
  if ok then error((what or "call") .. " did not raise", 2) end
  return err
end

function support.read_file(path)
  local f = assert(io.open(path, "rb"))
  local data = f:read("*a")
  f:close()
  return data
end

function support.write_file(path, data)
  local f = assert(io.open(path, "wb"))
  f:write(data)
  f:close()
end

function support.fixture(name)
  return json.decode(support.read_file(support.FIXTURES .. "/" .. name))
end

function support.copy(v)
  return json.decode(json.encode(v))
end

--- One frame per value, each followed by a line feed.
function support.frames(...)
  local out = {}
  for i = 1, select("#", ...) do out[i] = json.encode((select(i, ...))) .. "\n" end
  return table.concat(out)
end

--- A readable stream over a string, with the formats read_frame uses.
function support.reader(data)
  local pos = 1
  local r = {}
  function r:read(fmt)
    if pos > #data then return nil end
    if fmt == "*L" or fmt == "L" then
      local e = data:find("\n", pos, true) or #data
      local line = data:sub(pos, e)
      pos = e + 1
      return line
    end
    assert(type(fmt) == "number", "unsupported read format")
    local s = data:sub(pos, pos + fmt - 1)
    pos = pos + fmt
    return s
  end
  return r
end

--- A writable stream that keeps what is written.
function support.writer()
  local w = { parts = {} }
  function w:write(...)
    for i = 1, select("#", ...) do self.parts[#self.parts + 1] = select(i, ...) end
    return self
  end
  function w:flush() return true end
  function w:value() return table.concat(self.parts) end
  function w:lines()
    local out = {}
    for line in self:value():gmatch("([^\n]*)\n") do out[#out + 1] = line end
    return out
  end
  return w
end

--- The tests run on every interpreter; this is the one running them.
function support.interpreter()
  local i, exe = -1, nil
  while arg and arg[i] do
    exe = arg[i]
    i = i - 1
  end
  return exe
end

return support
