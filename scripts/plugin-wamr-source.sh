#!/usr/bin/env bash
# Print the path of a WAMR checkout at the commit pinned in scripts/plugin-wamr.sh, fetching
# it on first use. WAMR is not vendored (Apache-2.0 with the LLVM exception; ship its license
# with any binary that links it). Override with EXT_WAMR_ROOT; the cache is
# ${EXT_CACHE:-$HOME/.cache/ext}/wamr-<commit>.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -n "${EXT_WAMR_ROOT:-}" ]]; then echo "$EXT_WAMR_ROOT"; exit 0; fi
commit="$(sed -n 's/^WAMR_COMMIT=//p' scripts/plugin-wamr.sh)"
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || { echo 'could not read the pinned WAMR commit from scripts/plugin-wamr.sh' >&2; exit 1; }
root="${EXT_CACHE:-$HOME/.cache/ext}/wamr-$commit"
if [[ "$(git -C "$root" rev-parse HEAD 2>/dev/null || true)" != "$commit" ]]; then
  mkdir -p "$root"
  git -C "$root" init -q
  git -C "$root" remote get-url origin >/dev/null 2>&1 || git -C "$root" remote add origin https://github.com/bytecodealliance/wasm-micro-runtime
  git -C "$root" fetch -q --depth 1 origin "$commit" >&2
  git -C "$root" checkout -q FETCH_HEAD >&2
fi
echo "$root"
