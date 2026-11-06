-- Runs every test file in this directory: lua tests/run.lua [file-pattern [test-pattern]].
-- No dependencies; exits non-zero when a test fails.
local here = (debug.getinfo(1, "S").source:match("^@(.*)[/\\][^/\\]*$")) or "."
local root = here .. "/.."
package.path = root .. "/?.lua;" .. root .. "/?/init.lua;" .. here .. "/?.lua;" .. package.path
EXT_LUA_TESTS = here

local files = { "test_json", "test_guest", "test_fixtures", "test_serve_frames", "test_capability" }
local file_pattern, test_pattern = arg and arg[1], arg and arg[2]

local clock = require("ext_io.plugin.clock")
print(string.format("%s (clock: %s)", (rawget(_G, "jit") and jit.version) or _VERSION, clock.source))

local passed, failed = 0, {}
for _, name in ipairs(files) do
  if not file_pattern or name:find(file_pattern, 1, true) then
    local cases = require(name)
    for _, case in ipairs(cases) do
      if not test_pattern or case[1]:find(test_pattern, 1, true) then
        local started = os.clock()
        local ok, err = xpcall(case[2], debug.traceback)
        if ok then
          passed = passed + 1
          print(string.format("ok   %s: %s (%.2fs)", name, case[1], os.clock() - started))
        else
          failed[#failed + 1] = name .. ": " .. case[1]
          print(string.format("FAIL %s: %s\n%s", name, case[1], tostring(err)))
        end
      end
    end
  end
end
print(string.format("%d passed, %d failed", passed, #failed))
for _, f in ipairs(failed) do print("  " .. f) end
os.exit(#failed == 0 and 0 or 1)
