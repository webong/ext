-- LuaRocks package of the Lua host SDK (ext_io.host): an opt-in part beside the pure-Lua guest
-- (ext-io), with one C module that loads the C engine's shared library (libext_host) at run time.
-- It is built against the Lua headers and lib/plugin/include/ext_host.h, so it builds from a
-- checkout of the repository. `luarocks make ext-io-host-0.1.0-1.rockspec` here installs it.
rockspec_format = "3.0"
package = "ext-io-host"
version = "0.1.0-1"
source = {
  url = "git+https://github.com/webong/ext.git",
  tag = "ext-lua-v0.1.0",
  dir = "ext/pkg/ext-lua",
}
description = {
  summary = "ext for Lua: the host SDK for ext.plugin/v1 on the ext C engine",
  detailed = [[
    Runs ext plugins (JSON-line processes and C ABI libraries) from Lua through the ext C engine,
    which does admission, handshake checks, validation, deadlines and the session lifecycle. Needs
    the engine's shared library, libext_host, from an ext-engine release or a CMake build.
  ]],
  homepage = "https://github.com/webong/ext/tree/main/pkg/ext-lua",
  issues_url = "https://github.com/webong/ext/issues",
  license = "MIT",
  labels = { "ext", "plugin", "sdk", "host" },
}
dependencies = {
  "lua >= 5.1",
  "ext-io == 0.1.0",
}
build = {
  type = "builtin",
  modules = {
    ["ext_io.host"] = "ext_io/host/init.lua",
    ["ext_io.host.core"] = {
      sources = { "ext_io/host/core.c" },
      incdirs = { "../../lib/plugin/include" },
    },
  },
}
