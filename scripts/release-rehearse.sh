#!/usr/bin/env bash
# Rehearses a release with the real go toolchain and checks the checksums the
# tree records.
#
# Purpose: a go.sum records the hash of every sibling module it requires, and a
# published tag can never be changed, so a wrong hash cannot be fixed afterwards.
# This script tags every module of the round in a throwaway bare repository, makes
# the go command resolve the repository from it (git "insteadOf"), and lets
# "go mod download" compute each module's hash. That is the code path the module
# proxy runs, so the result is what the proxy will compute for the same tree.
#
# Checks: every checksum for a listed module version in any go.sum of REVISION's
# tree equals the go command's hash (both the zip hash and the go.mod hash).
# Exit status 0 only with no mismatch.
#
# Usage: scripts/release-rehearse.sh [-o WANT_FILE] VERSIONS [REVISION]
#   VERSIONS   file of "module version" lines (see release-common.sh)
#   REVISION   commit to rehearse; default HEAD (use the commit you will tag)
#   -o FILE    also write the computed hashes ("path version hash" lines), which
#              release-layer.sh compares with the published bytes
# Needs: bash, git, awk, sed, go. No network and no credentials.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-common.sh
. "$here/release-common.sh"

want_file=
while getopts o: opt; do
  case $opt in
    o) want_file=$OPTARG ;;
    *) release_die "usage: $0 [-o WANT_FILE] VERSIONS [REVISION]" ;;
  esac
done
shift $((OPTIND - 1))
[ $# -ge 1 ] && [ $# -le 2 ] || release_die "usage: $0 [-o WANT_FILE] VERSIONS [REVISION]"
release_init
versions=$1
rev=$(git -C "$ROOT" rev-parse --verify "${2:-HEAD}^{commit}")
release_versions "$versions" > /dev/null

release_workdir
trap release_clean EXIT
release_tag_rehearsal "$rev" "$versions"
release_hermetic_env

want="$WORK/want"
: > "$want"
list=$(release_versions "$versions")
while read -r m v; do
  hashes=$(release_download "$m" "$v")
  echo "$PREFIX/$m $v ${hashes% *}" >> "$want"
  echo "$PREFIX/$m $v/go.mod ${hashes#* }" >> "$want"
done <<EOF2
$list
EOF2

bad=0
checked=0
sums=$(git -C "$ROOT" ls-tree -r --name-only "$rev" | grep -E '(^|/)go\.sum$' || true)
for sum in $sums; do
  records=$(git -C "$ROOT" show "$rev:$sum" | grep -E "^$PREFIX/[^ ]+ v[^ ]+ h1:" || true)
  [ -n "$records" ] || continue
  while read -r path version hash; do
    expected=$(awk -v p="$path" -v v="$version" '$1 == p && $2 == v { print $3 }' "$want")
    [ -n "$expected" ] || continue
    checked=$((checked + 1))
    if [ "$expected" != "$hash" ]; then
      echo "MISMATCH $sum: $path $version records $hash, the go command computes $expected"
      bad=$((bad + 1))
    fi
  done <<EOF2
$records
EOF2
done
echo "rehearsal of $(git -C "$ROOT" rev-parse --short "$rev"): $checked recorded checksums compared with the go command's own hashes for $(grep -vc '/go.mod ' "$want") modules: $bad mismatches"
if [ -n "$want_file" ]; then cp "$want" "$want_file"; fi
[ "$bad" -eq 0 ]
