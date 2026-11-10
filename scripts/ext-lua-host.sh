#!/usr/bin/env bash
# Builds ext_io.host.core, the C module under the Lua host (pkg/ext-lua/ext_io/host), for one Lua,
# into <out-dir>/ext_io/host/core.so (core.dll on Windows); put "<out-dir>/?.so" ("<out-dir>/?.dll")
# on package.cpath to load it. It needs the Lua headers and ext_host.h only: the module loads the
# engine's shared library at run time.
#
# On Windows a module links against the Lua library itself, so it also needs that library: the
# import library MSVC builds (lua54.lib), MinGW's (liblua.dll.a), or the DLL, which MinGW gcc links
# against directly. It runs in an MSYS2 shell or Git Bash, with gcc or with cl (CC=cl).
#
# Usage: scripts/ext-lua-host.sh <out-dir>
# Environment:
#   EXT_LUA     the interpreter to build for (default: lua on the path)
#   LUA_INCDIR  the directory of its lua.h (default: found beside the interpreter)
#   LUA_LIB     Windows only: the Lua library to link against (default: found beside the interpreter)
#   CC          the C compiler (default: cc; on Windows gcc, or cl)
set -euo pipefail
cd "$(dirname "$0")/.."
out="${1:?usage: ext-lua-host.sh <out-dir>}"
lua="$(command -v "${EXT_LUA:-lua}")"
windows=""
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) windows=1 ;; esac
cc="${CC:-$([[ -n "$windows" ]] && echo gcc || echo cc)}"
msvc=""
[[ "$(basename "$cc" .exe)" == cl ]] && msvc=1
# winpath gives a native program (cl, gcc) a C:/ path: MSYS2 does not convert -I/... arguments.
winpath() { if [[ -n "$windows" ]]; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
scratch="$(mktemp -d "${TMPDIR:-/tmp}/ext-lua-host.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT

# version_num prints the LUA_VERSION_NUM of the lua.h in a directory, or nothing: Lua 5.5 defines it
# as an expression, so it goes through the preprocessor and then bash arithmetic.
version_num() {
  local expr
  printf '#include "lua.h"\nLUA_VERSION_NUM\n' >"$scratch/version.c"
  if [[ -n "$msvc" ]]; then
    expr="$(cd "$scratch" && "$cc" -nologo -EP -I"$(winpath "$1")" version.c 2>/dev/null | tr -d '\r' | awk 'NF { last = $0 } END { print last }')"
  else
    expr="$("$cc" -E -P -I"$(winpath "$1")" "$(winpath "$scratch/version.c")" 2>/dev/null | tr -d '\r' | awk 'NF { last = $0 } END { print last }')"
  fi
  [[ "$expr" =~ ^[0-9\ ()*+]+$ ]] && echo "$((expr))"
}

# The LUA_VERSION_NUM the interpreter was built with: LuaJIT is 5.1's.
want="$("$lua" -e 'local v = _VERSION:match("%d+%.%d+"); local a, b = v:match("(%d+)%.(%d+)"); print(a * 100 + b)')"
incdir="${LUA_INCDIR:-}"
if [[ -z "$incdir" ]]; then
  prefix="$(cd "$(dirname "$lua")/.." && pwd)"
  for dir in "$prefix"/include/luajit-2.1 "$prefix"/include "$prefix"/include/lua* /usr/include/lua* /usr/include; do
    [[ -f "$dir/lua.h" ]] || continue
    if [[ "$(version_num "$dir")" == "$want" ]]; then incdir="$dir"; break; fi
  done
fi
[[ -n "$incdir" && -f "$incdir/lua.h" ]] || { echo "no lua.h for $("$lua" -v 2>&1); set LUA_INCDIR" >&2; exit 1; }

mkdir -p "$out/ext_io/host"
module="$out/ext_io/host/core.so"
if [[ -n "$windows" ]]; then
  module="$out/ext_io/host/core.dll"
  # The library the interpreter was built against: the import library, or its DLL beside lua.exe.
  lib="${LUA_LIB:-}"
  if [[ -z "$lib" ]]; then
    prefix="$(cd "$(dirname "$lua")/.." && pwd)"
    major="${want:0:1}" minor="${want:2}" # 504: 5 and 4
    for candidate in "$prefix/lib/lua$major$minor.lib" "$prefix/lib/liblua.dll.a" "$prefix/lib/liblua$major.$minor.dll.a" \
      "$prefix/bin/lua$major$minor.dll"; do
      if [[ -f "$candidate" ]]; then lib="$candidate"; break; fi
    done
  fi
  [[ -n "$lib" && -f "$lib" ]] || { echo "no Lua library for $("$lua" -v 2>&1); set LUA_LIB" >&2; exit 1; }
  if [[ -n "$msvc" ]]; then
    # link.exe by its full path: an MSYS2 or Git Bash /usr/bin on the path has a link of its own.
    linker="$(dirname "$(command -v "$cc")")/link.exe"
    { "$cc" -nologo -c -O2 -MD -W3 -WX -D_CRT_SECURE_NO_WARNINGS -I"$(winpath "$incdir")" \
        -I"$(winpath "$PWD/lib/plugin/include")" "$(winpath "$PWD/pkg/ext-lua/ext_io/host/core.c")" \
        -Fo"$(winpath "$scratch/core.obj")" &&
      "$linker" -nologo -dll -out:"$(winpath "$module")" "$(winpath "$scratch/core.obj")" "$(winpath "$lib")"
    } >"$scratch/cl.log" 2>&1 || { cat "$scratch/cl.log" >&2; exit 1; }
  else
    "$cc" -O2 -std=c99 -Wall -Wextra -Werror -shared -I"$(winpath "$incdir")" -Ilib/plugin/include \
      pkg/ext-lua/ext_io/host/core.c "$(winpath "$lib")" -o "$(winpath "$module")"
  fi
else
  case "$(uname -s)" in
    Darwin) link=(-bundle -undefined dynamic_lookup) ;;
    *) link=(-shared -fPIC) ;;
  esac
  "$cc" -O2 -std=c99 -Wall -Wextra -Werror -fvisibility=hidden "${link[@]}" \
    -I"$incdir" -Ilib/plugin/include pkg/ext-lua/ext_io/host/core.c -o "$module"
fi
echo "built $module for $("$lua" -v 2>&1 | head -1) ($incdir)"
