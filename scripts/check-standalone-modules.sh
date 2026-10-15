#!/usr/bin/env bash
# Checks that every published module builds and tidies on its own, the way a
# consumer or `go install module@version` sees it: workspace off, direct module
# fetches, read-only go.mod and go.sum.
#
# A module whose go.mod requires a sibling module at a version that is not
# published yet (a release in progress) cannot be checked until that tag
# exists. It is skipped with a message and checked on a later run, so the
# check never fails for the chicken-and-egg order of a release. Product modules
# built with the workspace (src/ctx, src/ctn, examples) are not checked.
#
# Environment:
#   STANDALONE_OS   operating systems to vet, default "linux darwin windows"
#   STANDALONE_ONLY a space-separated list of module directories to check
set -u

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
prefix=github.com/webong/ext/
systems=${STANDALONE_OS:-"linux darwin windows"}

modules=$(awk '/^use \(/{u=1;next} u&&/^\)/{u=0} u{gsub(/[ \t]/,""); sub(/^\.\//,""); if ($0!="") print} /^use \.\//{sub(/^use \.\//,""); print}' go.work)
if [ -n "${STANDALONE_ONLY:-}" ]; then modules=$STANDALONE_ONLY; fi

published() { # <module path without prefix> <version>
  if git ls-remote --exit-code --tags origin "refs/tags/$1/$2" >/dev/null 2>&1; then return 0; fi
  # Without a reachable remote (a fork or an offline run) fall back to local tags.
  [ -n "$(git tag -l "$1/$2")" ]
}

failed=0 checked=0 skipped=0
for dir in $modules; do
  case $dir in src/ctx|src/ctn|examples) continue ;; esac
  [ -f "$dir/go.mod" ] || continue
  waiting=""
  while read -r path version; do
    [ -n "$path" ] || continue
    if ! published "$path" "$version"; then waiting="$waiting $path@$version"; fi
  done < <(sed -n 's|^[[:space:]]*require[[:space:]]*||; s|^[[:space:]]*'"${prefix//\//\\/}"'\([^[:space:]]*\)[[:space:]]\{1,\}\(v[^[:space:]]*\).*|\1 \2|p' "$dir/go.mod" | grep -E '^[a-z].* v[0-9]' | grep -v '^module ')
  if [ -n "$waiting" ]; then
    echo "skipped $dir: waiting for$waiting"
    skipped=$((skipped + 1))
    continue
  fi
  checked=$((checked + 1))
  ok=1
  for goos in $systems; do
    if ! (cd "$dir" && GOWORK=off GOPROXY=direct GOFLAGS=-mod=readonly GOOS=$goos go vet ./... >/tmp/standalone.$$ 2>&1); then
      echo "FAIL $dir: go vet (GOOS=$goos)"; sed 's/^/    /' /tmp/standalone.$$ | head -8; ok=0; break
    fi
  done
  if [ $ok = 1 ] && ! (cd "$dir" && GOWORK=off GOPROXY=direct go mod tidy -diff >/tmp/standalone.$$ 2>&1); then
    echo "FAIL $dir: go mod tidy -diff"; sed 's/^/    /' /tmp/standalone.$$ | head -12; ok=0
  fi
  if [ $ok = 1 ]; then echo "ok   $dir"; else failed=$((failed + 1)); fi
done
rm -f /tmp/standalone.$$
echo "checked $checked module(s), skipped $skipped, failed $failed"
[ $failed = 0 ]
