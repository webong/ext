#!/usr/bin/env bash
# Builds every module of a round on its own, before anything is tagged.
#
# Purpose: check-standalone-modules.sh proves that published modules build the way a
# consumer sees them, but only after the tags exist, when a failure can no longer be
# fixed. This script does the same for the commit you are about to tag: it tags every
# listed module in a throwaway repository, makes the go command fetch this
# repository's modules from it (and the published tags of modules the round does not
# release), exports each listed module's directory from REVISION by itself, and runs
# what a consumer's build would run with the workspace off and read-only go.mod and
# go.sum.
#
# Checks, for each listed module: "go vet ./..." on linux, darwin and windows, and
# "go mod tidy -diff". A missing or wrong go.sum record, a requirement on a version
# that does not exist, or code that needs something outside the module fails here.
# Exit status 0 only if every module passes.
#
# Usage: scripts/release-standalone.sh VERSIONS [REVISION]
#   VERSIONS   file of "module version" lines (see release-common.sh)
#   REVISION   commit to check; default HEAD (use the commit you will tag)
# Needs: bash, git, awk, tar, go, and the network or a warm module cache for
# third-party modules.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-common.sh
. "$here/release-common.sh"

[ $# -ge 1 ] && [ $# -le 2 ] || release_die "usage: $0 VERSIONS [REVISION]"
release_init
versions=$1
rev=$(git -C "$ROOT" rev-parse --verify "${2:-HEAD}^{commit}")
list=$(release_versions "$versions")

cache=$(go env GOMODCACHE)
release_workdir clone
trap release_clean EXIT
release_tag_rehearsal "$rev" "$versions"
release_hermetic_env
export GOPROXY="file://$cache/cache/download,https://proxy.golang.org,direct"
export GONOPROXY="$PREFIX" GONOSUMDB="$PREFIX" GOSUMDB=sum.golang.org
export GOFLAGS="-modcacherw -mod=readonly"

failed=0
while read -r m v; do
  src="$WORK/src/$m"
  mkdir -p "$src"
  git -C "$ROOT" archive "$rev" "$m" | tar -x -C "$WORK/src"
  ok=yes
  for goos in linux darwin windows; do
    # Not the translation of the C engine (pkg/ext-go/c2goengine/internal/ccgo): ccgo writes it and
    # it reports thousands of vet diagnostics that are not ours to fix.
    if ! out=$(cd "$src" && export GOOS=$goos && pkgs=$(go list ./...) && go vet $(printf '%s\n' "$pkgs" | grep -v '/internal/ccgo$') 2>&1); then
      echo "FAIL $m@$v: go vet (GOOS=$goos)"
      printf '%s\n' "$out" | head -8 | sed 's/^/    /'
      ok=no
      break
    fi
  done
  if [ "$ok" = yes ] && ! out=$(cd "$src" && go mod tidy -diff 2>&1); then
    echo "FAIL $m@$v: go mod tidy -diff"
    printf '%s\n' "$out" | head -12 | sed 's/^/    /'
    ok=no
  fi
  if [ "$ok" = yes ]; then echo "ok   $m@$v"; else failed=$((failed + 1)); fi
done <<EOF2
$list
EOF2
echo "standalone check of $(git -C "$ROOT" rev-parse --short "$rev"): $failed failed"
[ "$failed" -eq 0 ]
