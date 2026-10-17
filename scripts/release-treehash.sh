#!/usr/bin/env bash
# Second, independent check of the checksums a release tree records.
#
# Purpose: release-rehearse.sh asks the go command for each module's hash. This
# script computes the same hashes a different way, from the git tree of the commit
# with golang.org/x/mod/zip.CreateFromVCS (the module proxy's zip layout: it
# includes the repository-root LICENSE in the zip of a module that has none of its
# own, and leaves out nested modules), and compares them with every go.sum. The
# two methods share no code in this repository, so agreement rules out a mistake
# in either. Zips built any other way (for example zip.CreateFromDir on a
# directory) omit the root LICENSE and produce hashes that no proxy will ever
# serve; that mistake once made the go.sum files of a whole round wrong.
#
# Checks: every checksum for a listed module version in any go.sum of REVISION's
# tree equals the CreateFromVCS hash of that module at REVISION. Exit status 0
# only with no mismatch.
#
# Usage: scripts/release-treehash.sh VERSIONS [REVISION]
#   VERSIONS   file of "module version" lines (see release-common.sh)
#   REVISION   commit to check; default HEAD (use the commit you will tag)
# Needs: bash, git, awk, sed, go, and network access the first time, to fetch
# golang.org/x/mod (EXT_XMOD_VERSION selects the version, default v0.37.0).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-common.sh
. "$here/release-common.sh"

[ $# -ge 1 ] && [ $# -le 2 ] || release_die "usage: $0 VERSIONS [REVISION]"
release_init
versions=$1
rev=$(git -C "$ROOT" rev-parse --verify "${2:-HEAD}^{commit}")
release_versions "$versions" > /dev/null

# CreateFromVCS needs a repository with a .git directory; a linked worktree has
# only a pointer file, so use the repository that owns it.
common=$(cd "$(git -C "$ROOT" rev-parse --git-common-dir)" && pwd)
case $common in
  */.git) repo=${common%/.git} ;;
  *) release_die "cannot find the repository root from $common (a bare repository?)" ;;
esac

WORK=$(mktemp -d "${TMPDIR:-/tmp}/ext-treehash.XXXXXX")
trap release_clean EXIT
mkdir "$WORK/tool"
cat > "$WORK/tool/main.go" <<'GO'
// Prints the h1 hash of a module zip built from a git commit the way the module
// proxy builds it. usage: tool <repo> <rev> <module path> <version> <subdir>
package main

import (
	"fmt"
	"os"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

func main() {
	repo, rev, path, version, subdir := os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5]
	f, err := os.CreateTemp("", "treehash*.zip")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.Remove(f.Name())
	if err := modzip.CreateFromVCS(f, module.Version{Path: path, Version: version}, repo, rev, subdir); err != nil {
		fmt.Fprintln(os.Stderr, "create zip:", err)
		os.Exit(1)
	}
	f.Close()
	h, err := dirhash.HashZip(f.Name(), dirhash.Hash1)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(h)
}
GO
(
  cd "$WORK/tool"
  printf 'module treehash\n\ngo 1.23\n' > go.mod
  GOFLAGS=-modcacherw GOMODCACHE="$WORK/modcache" GOTOOLCHAIN=local go get "golang.org/x/mod@${EXT_XMOD_VERSION:-v0.37.0}" > "$WORK/get.log" 2>&1 \
    || { cat "$WORK/get.log" >&2; release_die "cannot fetch golang.org/x/mod"; }
  GOFLAGS=-modcacherw GOMODCACHE="$WORK/modcache" GOTOOLCHAIN=local go build -o "$WORK/treehash" . \
    || release_die "cannot build the hash tool"
)

want="$WORK/want"
: > "$want"
list=$(release_versions "$versions")
while read -r m v; do
  h=$("$WORK/treehash" "$repo" "$rev" "$PREFIX/$m" "$v" "$m") || release_die "cannot hash $m@$v at $rev"
  echo "$PREFIX/$m $v $h" >> "$want"
done <<EOF2
$list
EOF2

bad=0
checked=0
sums=$(git -C "$ROOT" ls-tree -r --name-only "$rev" | grep -E '(^|/)go\.sum$' || true)
for sum in $sums; do
  records=$(git -C "$ROOT" show "$rev:$sum" | grep -E "^$PREFIX/[^ ]+ v[^ /]+ h1:" || true)
  [ -n "$records" ] || continue
  while read -r path version hash; do
    expected=$(awk -v p="$path" -v v="$version" '$1 == p && $2 == v { print $3 }' "$want")
    [ -n "$expected" ] || continue
    checked=$((checked + 1))
    if [ "$expected" != "$hash" ]; then
      echo "MISMATCH $sum: $path $version records $hash, the git tree gives $expected"
      bad=$((bad + 1))
    fi
  done <<EOF2
$records
EOF2
done
echo "git-tree check of $(git -C "$ROOT" rev-parse --short "$rev"): $checked recorded checksums compared with $(wc -l < "$want" | tr -d ' ') zip hashes from CreateFromVCS: $bad mismatches"
[ "$bad" -eq 0 ]
