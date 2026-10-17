#!/usr/bin/env bash
# Computes the pins and go.sum records for a release round, before anything is tagged.
#
# Purpose: a module that requires another module of the round must record the
# hash of the version being released, and Go tags are immutable, so the hash has
# to be right before the tag exists. This script updates the go.mod and go.sum of
# every listed module in the working tree, layer by layer: it bumps each
# requirement on another listed module to its release version, drops go.sum
# records of listed versions, snapshots the finished layer into a throwaway bare
# repository as tagged versions, and lets "go mod tidy" fetch the lower layers
# from it with the real go toolchain, which records their true hashes. Modules
# outside the list are not touched; bump the product modules that build against
# local replace directives (go mod edit -require) yourself.
#
# Checks: the go command resolves every listed sibling a module requires at its
# release version; the result is written to go.mod and go.sum. It does not prove
# the result: commit it, then run release-rehearse.sh and release-treehash.sh on
# that commit. Running it again on the result changes nothing.
#
# Usage: scripts/release-pins.sh VERSIONS
#   VERSIONS   file of "module version" lines (see release-common.sh)
# Needs: bash, git, awk, sed, go, and the network for third-party modules not in
# the local module cache.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-common.sh
. "$here/release-common.sh"

[ $# -eq 1 ] || release_die "usage: $0 VERSIONS"
release_init
versions=$1
release_versions "$versions" > /dev/null

# Third-party modules come from the user's existing module cache and the public
# proxy; only this repository's modules are resolved from the scratch repository.
cache=$(go env GOMODCACHE)

release_workdir clone
trap release_clean EXIT
release_hermetic_env
export GOPROXY="file://$cache/cache/download,https://proxy.golang.org,direct"
export GONOPROXY="$PREFIX" GONOSUMDB="$PREFIX" GOSUMDB=sum.golang.org

layers=$(release_layers "$versions")
release_versions "$versions" > "$WORK/all"
levels=$(printf '%s\n' "$layers" | awk '{ print $1 }' | sort -un)
for level in $levels; do
  printf '%s\n' "$layers" | awk -v l="$level" '$1 == l { print $2, $3 }' > "$WORK/members"
  echo "== layer $level"
  while read -r m v <&3; do
    # point every requirement on another listed module at its release version
    while read -r p pv; do
      [ "$p" != "$m" ] || continue
      if grep -Eq "^[[:space:]]*(require[[:space:]]+)?$PREFIX/$p[[:space:]]+v" "$ROOT/$m/go.mod"; then
        (cd "$ROOT/$m" && go mod edit -require="$PREFIX/$p@$pv")
      fi
    done < "$WORK/all"
    # records of listed versions from an earlier attempt may be stale
    if [ -f "$ROOT/$m/go.sum" ]; then
      while read -r q qv; do
        awk -v a="$PREFIX/$q $qv " -v b="$PREFIX/$q $qv/go.mod " \
          'index($0, a) != 1 && index($0, b) != 1' "$ROOT/$m/go.sum" > "$ROOT/$m/go.sum.new"
        mv "$ROOT/$m/go.sum.new" "$ROOT/$m/go.sum"
      done < "$WORK/all"
    fi
    (cd "$ROOT/$m" && go mod tidy) || release_die "go mod tidy failed in $m"
    echo "   ok $m@$v"
  done 3< "$WORK/members"
  # snapshot the finished layer so the next one can fetch it
  index_file="$WORK/index"
  rm -f "$index_file"
  GIT_INDEX_FILE="$index_file" git -C "$ROOT" add -A
  tree=$(GIT_INDEX_FILE="$index_file" git -C "$ROOT" write-tree)
  commit=$(GIT_AUTHOR_NAME=release GIT_AUTHOR_EMAIL=release@example.invalid \
    GIT_COMMITTER_NAME=release GIT_COMMITTER_EMAIL=release@example.invalid \
    git -C "$ROOT" commit-tree "$tree" -m "snapshot of layer $level")
  git -C "$ROOT" push -q "$WORK/repo.git" "$commit:refs/heads/snapshot-$level"
  while read -r m v; do
    GIT_COMMITTER_NAME=release GIT_COMMITTER_EMAIL=release@example.invalid \
      git -C "$WORK/repo.git" tag -f -a "$m/$v" -m "$m $v" "$commit"
  done < "$WORK/members"
done
echo "done: review the changes, commit them, then run scripts/release-rehearse.sh and scripts/release-treehash.sh on that commit"
