-- The bundled JSON module: strictness, UTF-8, escapes, numbers and the object/array distinction.
local json = require("ext_io.json")
local t = require("support")

local tests = {}
local function test(name, f) tests[#tests + 1] = { name, f } end

local has_integers = math.type ~= nil

test("round trip keeps order, kinds and null", function()
  local text = '{"b":1,"a":[1,2,{}],"e":[],"n":null,"s":"x","t":true,"f":false}'
  local v = json.decode(text)
  t.eq(json.encode(v), text)
  t.ok(getmetatable(v.a[3]) == json.object_mt and getmetatable(v.e) == json.array_mt)
  t.ok(v.n == json.null)
end)

test("plain tables: arrays, objects and the empty table", function()
  t.eq(json.encode({ 1, 2, 3 }), "[1,2,3]")
  t.eq(json.encode({ b = 1, a = 2 }), '{"a":2,"b":1}')
  t.eq(json.encode({}), "{}")
  t.eq(json.encode(json.array()), "[]")
  t.eq(json.encode(json.object({ z = 1, a = 2 }, { "z", "a" })), '{"z":1,"a":2}')
  t.eq(json.encode({ a = { json.null, false } }), '{"a":[null,false]}')
  t.eq(json.encode(nil), "null")
  t.raises(function() json.encode({ 1, 2, x = 3 }) end, "mixed table")
  t.raises(function() json.encode({ [1] = 1, [3] = 3 }) end, "sparse array")
  t.raises(function() json.encode(json.array({ x = 1 })) end, "array with a string key")
  t.raises(function() json.encode(print) end, "function")
  local loop = {}
  loop.self = loop
  t.raises(function() json.encode(loop) end, "circular reference")
end)

test("strings: escapes, UTF-8 and surrogate pairs", function()
  t.eq(json.encode("a\"\\\b\f\n\r\t\1\127/"), '"a\\"\\\\\\b\\f\\n\\r\\t\\u0001\\u007f/"')
  t.eq(json.encode("\195\169\226\130\172\240\159\152\128"), '"\\u00e9\\u20ac\\ud83d\\ude00"')
  t.eq(json.encode("\195\169", { ascii = false }), '"\195\169"')
  t.eq(json.decode('"\\u00e9\\u20AC\\ud83d\\ude00\\/"'), "\195\169\226\130\172\240\159\152\128/")
  t.eq(json.decode('"\240\159\152\128"'), "\240\159\152\128")
  t.eq(json.decode('"a\\u0000b"'), "a\0b")
  t.eq(json.encode("a\0b"), '"a\\u0000b"')
  for _, bad in ipairs({
    '"\\ud800"', '"\\udc00"', '"\\ud800\\u0041"', '"\\ud800\\ud800"', '"\\ud83d x"',
    '"\\x41"', '"\\u12G4"', '"\\u12"', '"a\nb"', '"a\tb"', '"\0"', '"open',
  }) do
    t.raises(function() json.decode(bad) end, bad)
  end
  for _, bad in ipairs({ "\255", "\192\128", "\224\128\128", "\237\160\128", "\244\144\128\128", "\195", "\240\159\152" }) do
    t.raises(function() json.decode('"' .. bad .. '"') end, "invalid UTF-8")
    t.raises(function() json.encode(bad) end, "invalid UTF-8 encoded")
  end
end)

test("numbers: integers, floats and strict grammar", function()
  t.eq(json.encode(json.decode("[0,-0,12,-3,1.5,1e2,2.5E-3,123456789012345]")), "[0,0,12,-3,1.5,100.0,0.0025,123456789012345]")
  if has_integers then
    t.ok(math.type(json.decode("7")) == "integer")
    t.ok(math.type(json.decode("7.0")) == "float")
    t.ok(math.type(json.decode("1e2")) == "float")
    t.eq(json.encode(9007199254740993), "9007199254740993")
    t.eq(json.encode(2.0), "2.0")
  else
    t.eq(json.encode(2.0), "2")
  end
  t.eq(json.decode("0.1") + json.decode("0.2"), 0.1 + 0.2)
  t.eq(json.encode(0.1), "0.1")
  t.eq(json.encode(1 / 3), "0.3333333333333333")
  t.eq(json.decode("1e400"), math.huge)
  for _, bad in ipairs({ "01", "-", "1.", ".5", "1e", "+1", "NaN", "Infinity", "-Infinity", "0x10", "1_000" }) do
    t.raises(function() json.decode(bad) end, bad)
  end
  t.raises(function() json.encode(0 / 0) end, "NaN")
  t.raises(function() json.encode(math.huge) end, "infinity")
end)

test("decoded numbers written back keep their kind and digits", function()
  local text = '[18446744073709551615,-9223372036854775809,1.0,1e2,-0,0.5,12345678901234567890123]'
  t.eq(json.encode(json.decode(text)), '[18446744073709551615,-9223372036854775809,1.0,100.0,0,0.5,12345678901234567890123]')
  local v = json.decode('{"n":18446744073709551615}')
  v.n = 7
  t.eq(json.encode(v), '{"n":7}')
end)

test("number text is kept where the value loses it", function()
  local v = json.decode('{"a":1,"b":1.0,"c":18446744073709551615,"d":-0,"e":[1e2]}')
  t.eq(json.number_text(v, "a"), nil)
  t.eq(json.number_text(v, "b"), "1.0")
  t.eq(json.number_text(v, "c"), "18446744073709551615")
  t.eq(json.number_text(v, "d"), "-0")
  t.eq(json.number_text(v.e, 1), "1e2")
end)

test("structure: duplicates, trailing data, depth, literals", function()
  for _, bad in ipairs({
    '{"a":1,"a":2}', '{"a":1,"\\u0061":2}', "{} {}", "[1,2]x", "[1,]", '{"a":1,}', "{,}",
    "[", "]", "", "  ", "tru", "nul", "truex", "{'a':1}", '{"a" 1}', '{1:2}', "\239\187\191{}",
  }) do
    t.raises(function() json.decode(bad) end, bad)
  end
  t.eq(json.decode(" \t\r\n[ 1 , 2 ] \n"), { 1, 2 })
  t.eq(json.decode('{"":1}'), { [""] = 1 })
  local deep = string.rep("[", 64) .. string.rep("]", 64)
  json.decode(deep, { max_depth = 63 })
  t.raises(function() json.decode("[" .. deep .. "]", { max_depth = 63 }) end, "too deep")
  t.raises(function() json.decode(string.rep("[", 64) .. "0" .. string.rep("]", 64), { max_depth = 63 }) end, "scalar too deep")
end)

test("predicates", function()
  t.ok(json.is_object({}) and json.is_array({}))
  t.ok(json.is_object({ a = 1 }) and not json.is_array({ a = 1 }))
  t.ok(json.is_array({ 1 }) and not json.is_object({ 1 }))
  t.ok(not json.is_object(json.array()) and not json.is_array(json.object()))
  t.ok(not json.is_object(json.null) and not json.is_array(json.null))
  t.eq(json.array_length(json.decode("[1,null,3]")), 3)
end)

return tests
