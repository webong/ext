#!/usr/bin/env bash
# Fetches the adapter repositories and lays them out as one directory, one
# subdirectory per adapter, the way ext's integration tests expect them.
# Adapters are not part of ext; they live in their own repositories:
#
#   xallet/tab  browsers and AI
#   xallet/zip  package managers, runtimes and toolchains
#   xallet/vat  containers, virtual machines and cloud
#   xallet/tip  operating system and devices
#
# Prints the directory. Tests read it from EXT_ADAPTERS:
#
#   export EXT_ADAPTERS=$(scripts/adapters.sh)
#
# Each adapter is copied, not linked: ext's package loader refuses symbolic links.
# The directory also holds .repos, one "<adapter> <repository checkout>" line per
# adapter. Build Go adapters from their repository checkout so its go.work applies.
#
# Environment:
#   EXT_ADAPTERS_CACHE  where repositories are cloned, default ${TMPDIR:-/tmp}/ext-adapters
#   EXT_ADAPTERS_REF    branch or tag to fetch from every repository, default main
#   EXT_ADAPTERS_LOCAL  a directory holding local checkouts named tab, zip, vat and tip,
#                       used instead of cloning (for working on ext and an adapter together)
set -euo pipefail

repos="tab zip vat tip"
cache=${EXT_ADAPTERS_CACHE:-${TMPDIR:-/tmp}/ext-adapters}
ref=${EXT_ADAPTERS_REF:-main}
layout="$cache/adapters"
mkdir -p "$cache"

for repo in $repos; do
  if [ -n "${EXT_ADAPTERS_LOCAL:-}" ]; then
    source="$EXT_ADAPTERS_LOCAL/$repo"
    [ -d "$source" ] || { echo "adapters: $source does not exist" >&2; exit 1; }
    ln -sfn "$(cd "$source" && pwd)" "$cache/$repo"
  elif [ -d "$cache/$repo/.git" ] && [ ! -L "$cache/$repo" ]; then
    git -C "$cache/$repo" fetch -q --depth 1 origin "$ref" >&2
    git -C "$cache/$repo" checkout -q --detach FETCH_HEAD >&2
  else
    rm -rf "$cache/$repo"
    git clone -q --depth 1 --branch "$ref" "https://github.com/xallet/$repo.git" "$cache/$repo" >&2
  fi
done

rm -rf "$layout"
mkdir -p "$layout"
for repo in $repos; do
  for dir in "$cache/$repo"/*/; do
    name=$(basename "$dir")
    if [ -e "$layout/$name" ]; then
      echo "adapters: $name exists in more than one repository" >&2
      exit 1
    fi
    cp -R "${dir%/}" "$layout/$name"
    printf '%s %s\n' "$name" "$(cd -P "$cache/$repo" && pwd)" >> "$layout/.repos"
  done
done
echo "$layout"
