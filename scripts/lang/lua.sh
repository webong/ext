#!/usr/bin/env bash
# The Lua guest SDK and host (pkg/ext-lua). Needs lua (EXT_LUA names it; default lua) and go.
#   test   the SDK's own tests and the guest under the Go hosts (scripts/ext-lua.sh)
#   rock   lint both rockspecs, install the SDK rock into a scratch tree and load it; needs luarocks
#          and Lua 5.4
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  scripts/ext-lua.sh
}

lang_cmd_rock() {
  local tree
  lang_temp
  tree="$LANG_TEMP/rocks"
  (cd pkg/ext-lua &&
    luarocks lint ext-io-0.1.0-1.rockspec &&
    luarocks lint ext-io-host-0.1.0-1.rockspec &&
    luarocks make --tree "$tree" ext-io-0.1.0-1.rockspec)
  (cd "$tree/.." &&
    LUA_PATH="$tree/share/lua/5.4/?.lua;$tree/share/lua/5.4/?/init.lua" \
      lua -e 'local p = require("ext_io.plugin"); assert(p.API_VERSION == "ext.plugin/v1" and p.capability.Router)')
}

lang_main "$0" "$@"
