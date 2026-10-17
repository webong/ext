#!/usr/bin/env bash
# Tags one dependency layer of a release round, pushes it, and verifies the
# published bytes before anyone builds the next layer on them.
#
# Purpose: a layer records hashes of the layer below it in its go.sum. Tags can
# never be moved, so each layer is checked against what the world actually serves
# before the next is tagged; if anything differs, stop (the tags of a layer that
# differs cannot be fixed, only superseded by a new version).
#
# What it does, for the modules of LAYER (scripts/release-layers.sh lists them):
#   1. refuses to run if a tag already exists locally or on the remote;
#   2. creates annotated tags "<module>/<version>" on REVISION and pushes them to
#      the remote in one atomic push;
#   3. asks the go command for each module's hash from the remote directly
#      (GOPROXY=direct) and from proxy.golang.org, and compares the zip hash and
#      the go.mod hash with WANT, the file release-rehearse.sh -o wrote for the
#      same revision. The proxy may need a moment to see a new tag, so it is
#      retried for about two minutes.
# Without --push it only prints what it would tag (a dry run), because pushing
# a tag is irreversible. Exit status 0 only if every module verified.
#
# Usage: scripts/release-layer.sh [--push] VERSIONS WANT LAYER REVISION
#   VERSIONS   file of "module version" lines (see release-common.sh)
#   WANT       hashes written by scripts/release-rehearse.sh -o WANT VERSIONS REVISION
#   LAYER      layer number, from scripts/release-layers.sh output (0, 1, ...)
#   REVISION   the merged commit to tag; it must be the commit you rehearsed
# Environment: EXT_RELEASE_REMOTE names the git remote or gives its URL (default
# origin). A remote that is not on github.com (a test repository) is resolved
# through a git "insteadOf" rule and the proxy check is skipped.
# Next, after the last layer: scripts/check-standalone-modules.sh.
# Needs: bash, git, awk, sed, go, and the network for the remote and the proxy.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-common.sh
. "$here/release-common.sh"

push=no
if [ "${1:-}" = "--push" ]; then push=yes; shift; fi
[ $# -eq 4 ] || release_die "usage: $0 [--push] VERSIONS WANT LAYER REVISION"
release_init
versions=$1 want=$2 layer=$3
rev=$(git -C "$ROOT" rev-parse --verify "$4^{commit}")
[ -f "$want" ] || release_die "$want not found; run scripts/release-rehearse.sh -o $want $versions $4"
remote=${EXT_RELEASE_REMOTE:-origin}
url=$(git -C "$ROOT" remote get-url "$remote" 2>/dev/null || echo "$remote")

members=$(release_layers "$versions" | awk -v l="$layer" '$1 == l { print $2, $3 }')
[ -n "$members" ] || release_die "layer $layer is empty"

echo "layer $layer at $(git -C "$ROOT" rev-parse --short "$rev"), remote $url:"
while read -r m v; do
  echo "  $m/$v"
  if git -C "$ROOT" rev-parse -q --verify "refs/tags/$m/$v" > /dev/null; then release_die "tag $m/$v already exists locally"; fi
  if [ -n "$(git -C "$ROOT" ls-remote --tags "$remote" "refs/tags/$m/$v" 2>/dev/null)" ]; then release_die "tag $m/$v already exists on the remote"; fi
done <<EOF2
$members
EOF2
if [ "$push" != yes ]; then
  echo "dry run: nothing was tagged. Re-run with --push to create and push these tags."
  exit 0
fi

while read -r m v; do
  git -C "$ROOT" tag -a "$m/$v" -m "$m $v" "$rev"
done <<EOF2
$members
EOF2
refs=$(printf '%s\n' "$members" | awk '{ printf "refs/tags/%s/%s ", $1, $2 }')
# shellcheck disable=SC2086 # the refspecs are plain words
git -C "$ROOT" push --atomic "$remote" $refs || release_die "push failed; the local tags remain and can be deleted with git tag -d"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/ext-layer.XXXXXX")
trap release_clean EXIT
mkdir "$WORK/scratch"
printf 'module scratch\n\ngo 1.23\n' > "$WORK/scratch/go.mod"
export GOWORK=off GOTOOLCHAIN=local GOFLAGS=-modcacherw GOSUMDB=off GONOSUMDB='*'
case $url in
  https://github.com/*|git@github.com:*) via_proxy=yes ;;
  *)
    via_proxy=no
    printf '[url "%s"]\n\tinsteadOf = https://%s\n' "$url" "$PREFIX" > "$WORK/gitconfig"
    export GIT_CONFIG_GLOBAL="$WORK/gitconfig" GIT_CONFIG_NOSYSTEM=1 GIT_ALLOW_PROTOCOL=file:https:ssh
    ;;
esac

bad=0
# verify LABEL PROXY: compare each module's hashes with WANT
verify() {
  local label=$1 proxy=$2 failed=0 m v out sum gomod want_sum want_mod
  while read -r m v; do
    out=$(cd "$WORK/scratch" && GOPROXY=$proxy GOMODCACHE="$WORK/mc-$label" go mod download -json "$PREFIX/$m@$v" 2>&1) || out=
    sum=$(printf '%s\n' "$out" | sed -n 's/.*"Sum": "\(.*\)".*/\1/p')
    gomod=$(printf '%s\n' "$out" | sed -n 's/.*"GoModSum": "\(.*\)".*/\1/p')
    want_sum=$(awk -v p="$PREFIX/$m" -v v="$v" '$1 == p && $2 == v { print $3 }' "$want")
    want_mod=$(awk -v p="$PREFIX/$m" -v v="$v/go.mod" '$1 == p && $2 == v { print $3 }' "$want")
    if [ -n "$sum" ] && [ "$sum" = "$want_sum" ] && [ "$gomod" = "$want_mod" ]; then
      echo "ok   [$label] $m@$v $sum"
    else
      echo "BAD  [$label] $m@$v got '$sum' '$gomod', rehearsal said '$want_sum' '$want_mod'"
      failed=$((failed + 1))
    fi
  done <<EOF2
$members
EOF2
  return "$failed"
}

verify direct direct || bad=$((bad + 1))
if [ "$via_proxy" = yes ]; then
  try=0
  while :; do
    try=$((try + 1))
    if verify "proxy$try" https://proxy.golang.org; then break; fi
    [ "$try" -lt 6 ] || { bad=$((bad + 1)); break; }
    echo "the proxy does not serve it yet (or differs); retrying in 20 s"
    sleep 20
  done
fi
if [ "$bad" -eq 0 ]; then
  echo "layer $layer: VERIFIED"
else
  echo "layer $layer: FAILED; do not tag the next layer. Tags cannot be moved: fix the cause and release a new version."
  exit 1
fi
