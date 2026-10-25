#!/usr/bin/env bash
# Regenerates pkg/ext-go/cgoengine/_csrc from lib/plugin.
#
# A published Go module contains only its own directory, so the module that binds
# the C engine carries the sources it is built from. lib/plugin stays the canonical
# home (the iOS, Android, Rust and Zig bindings build from it); _csrc is a copy, and
# TestCSourcesMatchCanonical fails when the two differ. Run this after changing lib/plugin.
set -euo pipefail
cd "$(dirname "$0")/.."
src=lib/plugin
dst=pkg/ext-go/cgoengine/_csrc
rm -rf "$dst"
mkdir -p "$dst"
# Build outputs and editor files are not sources.
(cd "$src" && find . -type f \
  ! -name '.DS_Store' ! -path './build/*' ! -path './.cache/*' -print0) |
  while IFS= read -r -d '' f; do
    mkdir -p "$dst/$(dirname "$f")"
    cp -p "$src/$f" "$dst/$f"
  done
echo "synced $src -> $dst ($(find "$dst" -type f | wc -l | tr -d ' ') files)"
