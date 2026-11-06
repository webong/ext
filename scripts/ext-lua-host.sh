#!/usr/bin/env bash
# Builds ext_io.host.core, the C module under the Lua host (pkg/ext-lua/ext_io/host), for one Lua,
# into <out-dir>/ext_io/host/core.so; put "<out-dir>/?.so" on package.cpath to load it. It needs
# the Lua headers and ext_host.h only: the module loads the engine's shared library at run time.
#
# Usage: scripts/ext-lua-host.sh <out-dir>
# Environment:
#   EXT_LUA     the interpreter to build for (default: lua on the path)
#   LUA_INCDIR  the directory of its lua.h (default: found beside the interpreter)
#   CC          the C compiler (default: cc)
set -euo pipefail
cd "$(dirname "$0")/.."
out="${1:?usage: ext-lua-host.sh <out-dir>}"
lua="$(command -v "${EXT_LUA:-lua}")"

# The LUA_VERSION_NUM the interpreter was built with: LuaJIT is 5.1's.
want="$("$lua" -e 'local v = _VERSION:match("%d+%.%d+"); local a, b = v:match("(%d+)%.(%d+)"); print(a * 100 + b)')"
incdir="${LUA_INCDIR:-}"
if [[ -z "$incdir" ]]; then
  prefix="$(cd "$(dirname "$lua")/.." && pwd)"
  for dir in "$prefix"/include/luajit-2.1 "$prefix"/include "$prefix"/include/lua* /usr/include/lua* /usr/include; do
    [[ -f "$dir/lua.h" ]] || continue
    expr="$(printf '#include "lua.h"\nLUA_VERSION_NUM\n' | "${CC:-cc}" -E -P -I"$dir" - 2>/dev/null | tail -1)"
    [[ "$expr" =~ ^[0-9\ ()*+]+$ ]] || continue
    num="$((expr))"
    if [[ "$num" == "$want" ]]; then incdir="$dir"; break; fi
  done
fi
[[ -n "$incdir" && -f "$incdir/lua.h" ]] || { echo "no lua.h for $("$lua" -v 2>&1); set LUA_INCDIR" >&2; exit 1; }

mkdir -p "$out/ext_io/host"
case "$(uname -s)" in
  Darwin) link=(-bundle -undefined dynamic_lookup) ;;
  *) link=(-shared -fPIC) ;;
esac
"${CC:-cc}" -O2 -std=c99 -Wall -Wextra -Werror -fvisibility=hidden "${link[@]}" \
  -I"$incdir" -Ilib/plugin/include pkg/ext-lua/ext_io/host/core.c -o "$out/ext_io/host/core.so"
echo "built $out/ext_io/host/core.so for $("$lua" -v 2>&1 | head -1) ($incdir)"
