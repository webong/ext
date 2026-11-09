#!/usr/bin/env bash
# Builds ExtIO::Host::Native, the native extension under the Ruby host (pkg/ext-ruby/ext), for one
# Ruby into <out-dir>/ext_io/host/native.<so|bundle>; put <out-dir> on the load path (RUBYLIB or
# -I) to load it. It needs the Ruby headers and ext_host.h only: the extension loads the engine's
# shared library at run time.
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
(cd "$out/build" && "$ruby" "$root/pkg/ext-ruby/ext/ext_io_host/extconf.rb" >/dev/null && make -s >/dev/null)
dlext="$("$ruby" -rrbconfig -e 'print RbConfig::CONFIG["DLEXT"]')"
cp "$out/build/native.$dlext" "$out/ext_io/host/native.$dlext"
echo "built $out/ext_io/host/native.$dlext for $("$ruby" -v)"
