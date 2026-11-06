#!/usr/bin/env bash
# Tests the Lua guest SDK (pkg/ext-lua) on one interpreter: its own tests, the conformance suite
# through the Go process host, and the frames suite (progress events and reverse calls) through
# the pure-Go session. EXT_LUA names the interpreter (default: lua on the path). With
# EXT_CROSSLANG_ENGINE=c (and EXT_CENGINE_BUILD_DIR, as for scripts/plugin-crosslang.sh) the frames
# suite also runs through the C engine.
set -euo pipefail
cd "$(dirname "$0")/.."

lua="${EXT_LUA:-lua}"
lua="$(command -v "$lua")"
"$lua" -v

"$lua" pkg/ext-lua/tests/run.lua

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-lua.XXXXXX")"
trap 'rm -rf "$build_dir"' EXIT
# The engine starts a guest with an empty environment, so the launcher names the interpreter and
# the script by absolute path.
cat >"$build_dir/ext-lua-frames-guest" <<LAUNCHER
#!/bin/sh
exec "$lua" "$PWD/pkg/ext-lua/tests/frames_guest.lua"
LAUNCHER
chmod +x "$build_dir/ext-lua-frames-guest"

go_tags=(-tags "")
case "${EXT_CROSSLANG_ENGINE:-go}" in
  go) ;;
  c)
    : "${EXT_CENGINE_BUILD_DIR:?set EXT_CENGINE_BUILD_DIR to the native C runner directory}"
    go_tags=(-tags ext_cengine)
    if [[ "${EXT_CENGINE_LINKAGE:-static}" == shared ]]; then go_tags=(-tags ext_cengine,ext_cengine_shared); fi
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -L$EXT_CENGINE_BUILD_DIR/release -Wl,-rpath,$EXT_CENGINE_BUILD_DIR/release"
    ;;
  *) echo 'EXT_CROSSLANG_ENGINE must be go or c' >&2; exit 1 ;;
esac

export EXT_LUA="$lua" EXT_LUA_FRAMES_GUEST="$build_dir/ext-lua-frames-guest"
(cd pkg/ext-go/engine && go test -count=1 -run 'TestLuaGuest$' -v ./plugintest)
go test "${go_tags[@]}" -count=1 -timeout=180s -run 'TestLuaFramesGuest$' -v ./tests/crosslang
