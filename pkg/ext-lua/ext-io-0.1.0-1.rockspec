-- LuaRocks package of the Lua guest SDK. `luarocks make` in this directory installs the checkout;
-- a published rock is built from the tag named in source.
rockspec_format = "3.0"
package = "ext-io"
version = "0.1.0-1"
source = {
  url = "git+https://github.com/webong/ext.git",
  tag = "ext-lua-v0.1.0",
  dir = "ext/pkg/ext-lua",
}
description = {
  summary = "ext for Lua: the plugin guest SDK for ext.plugin/v1 over JSON lines (pure Lua)",
  detailed = [[
    A guest for the ext plugin protocol (ext.plugin/v1) that a host runs as a process and speaks
    to in JSON lines over standard input and output. Pure Lua with its own strict JSON module:
    no C modules and no native build. Runs on Lua 5.1 to 5.5 and LuaJIT.
  ]],
  homepage = "https://github.com/webong/ext/tree/main/pkg/ext-lua",
  issues_url = "https://github.com/webong/ext/issues",
  license = "MIT",
  labels = { "ext", "plugin", "sdk", "json" },
}
dependencies = {
  "lua >= 5.1",
}
build = {
  type = "builtin",
  modules = {
    ["ext_io"] = "ext_io/init.lua",
    ["ext_io.json"] = "ext_io/json.lua",
    ["ext_io.plugin"] = "ext_io/plugin/init.lua",
    ["ext_io.plugin.capability"] = "ext_io/plugin/capability.lua",
    ["ext_io.plugin.clock"] = "ext_io/plugin/clock.lua",
    ["ext_io.plugin.core"] = "ext_io/plugin/core.lua",
    ["ext_io.plugin.frames"] = "ext_io/plugin/frames.lua",
    ["ext_io.plugin.guest"] = "ext_io/plugin/guest.lua",
  },
  copy_directories = { "examples" },
}
test = {
  type = "command",
  script = "tests/run.lua",
}
