#!/usr/bin/env sh
set -eu

printf 'install-native.sh is deprecated; use install.sh instead.\n' >&2
if [ -f "$0" ]; then
  ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
  if [ -f "$ROOT/install.sh" ]; then exec "$ROOT/install.sh" "$@"; fi
fi

command -v curl >/dev/null 2>&1 || { printf 'ctx: curl is required for remote installation\n' >&2; exit 1; }
INSTALL_URL=${CTX_INSTALL_URL:-https://raw.githubusercontent.com/webong/ext/main/install.sh}
curl -fsSL "$INSTALL_URL" | sh -s -- "$@"
