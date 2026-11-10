#!/usr/bin/env bash
# Builds ExtIO::Host::Native, the native extension under the Ruby host (pkg/ext-ruby/ext), for one
# Ruby into <out-dir>/ext_io/host/native.<so|bundle>; put <out-dir> on the load path (RUBYLIB or
# -I) to load it. It needs the Ruby headers and ext_host.h only: the extension loads the engine's
# shared library at run time.
#
# On Windows it builds with the toolchain the Ruby was built by: RubyInstaller's MSYS2 devkit, or
# MSYS2's own Ruby and gcc, from an MSYS2 shell or Git Bash with that make on the path.
#
# Usage: scripts/ext-ruby-host.sh <out-dir>
# Environment:
#   EXT_RUBY  the Ruby to build for (default: ruby on the path)
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
out="${1:?usage: ext-ruby-host.sh <out-dir>}"
ruby="$(command -v "${EXT_RUBY:-ruby}")"
mkdir -p "$out/build" "$out/ext_io/host"
out="$(cd "$out" && pwd -P)"
extconf="$root/pkg/ext-ruby/ext/ext_io_host/extconf.rb"
# Ruby is a native program on Windows: give it a C:/ path.
if command -v cygpath >/dev/null 2>&1; then extconf="$(cygpath -m "$extconf")"; fi
(cd "$out/build" && "$ruby" "$extconf" >/dev/null && make -s >/dev/null)
dlext="$("$ruby" -rrbconfig -e 'print RbConfig::CONFIG["DLEXT"]')"
cp "$out/build/native.$dlext" "$out/ext_io/host/native.$dlext"
echo "built $out/ext_io/host/native.$dlext for $("$ruby" -v)"
