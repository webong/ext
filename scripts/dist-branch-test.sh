#!/usr/bin/env bash
# Checks scripts/dist-branch.sh against a throwaway remote: the first release creates the branch
# without history from main, the next adds one commit, a repeated tag and a bad branch name are
# refused, and the caller's checkout is never touched.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
script="$here/dist-branch.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
# No configured identity, so the script's own default is what commits.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null

fail() { echo "FAIL: $*" >&2; exit 1; }
remote() { git --git-dir="$work/remote.git" "$@"; }

git init -q --bare "$work/remote.git"
git clone -q "$work/remote.git" "$work/clone" 2>/dev/null
cd "$work/clone"
git checkout -q -b main
echo main >README
git add README
git -c user.name=t -c user.email=t@t commit -q -m main
git push -q origin main

# The first archive has a .gitignore that would hide Package.swift from a plain `git add`.
mkdir -p "$work/a/Sources" "$work/b"
echo one >"$work/a/Package.swift"
echo x >"$work/a/Sources/x.swift"
echo '*.swift' >"$work/a/.gitignore"
tar -czf "$work/one.tar.gz" -C "$work/a" .
echo two >"$work/b/Package.swift"
tar -czf "$work/two.tar.gz" -C "$work/b" .

"$script" dist/pkg 0.1.0 "$work/one.tar.gz" 'pkg 0.1.0' 'staged by a test' >/dev/null
[[ "$(remote rev-list --count dist/pkg)" == 1 ]] || fail 'the first release is not a single commit'
[[ -z "$(remote merge-base main dist/pkg 2>/dev/null || true)" ]] || fail 'the branch shares history with main'
[[ "$(remote cat-file -p dist/pkg:Package.swift)" == one ]] || fail 'an ignored file was left out'
[[ "$(remote cat-file -p dist/pkg:Sources/x.swift)" == x ]] || fail 'a nested file was left out'
[[ "$(remote rev-parse 0.1.0)" == "$(remote rev-parse dist/pkg)" ]] || fail 'the tag is not on the branch tip'
[[ "$(remote log -1 --format=%an dist/pkg)" == 'github-actions[bot]' ]] || fail 'the default identity was not used'
[[ -z "$(git status --porcelain)" && "$(ls)" == README ]] || fail 'the caller checkout changed'
first="$(remote rev-parse dist/pkg)"

"$script" dist/pkg 0.1.1 "$work/two.tar.gz" 'pkg 0.1.1' >/dev/null
[[ "$(remote rev-list --count dist/pkg)" == 2 ]] || fail 'the second release did not add one commit'
[[ "$(remote rev-parse dist/pkg^)" == "$first" ]] || fail 'the second release is not on top of the first'
[[ "$(remote cat-file -p dist/pkg:Package.swift)" == two ]] || fail 'the tree was not replaced'
remote cat-file -e dist/pkg:Sources/x.swift 2>/dev/null && fail 'a file of the previous release remains'
[[ "$(remote rev-parse 0.1.0)" == "$first" ]] || fail 'an earlier tag moved'
tip="$(remote rev-parse dist/pkg)"

! "$script" dist/pkg 0.1.1 "$work/one.tar.gz" 'again' 2>/dev/null || fail 'a repeated tag was accepted'
[[ "$(remote rev-parse dist/pkg)" == "$tip" ]] || fail 'a refused release moved the branch'
! "$script" main 0.2.0 "$work/one.tar.gz" 'wrong' 2>/dev/null || fail 'a branch outside dist/ was accepted'
! remote rev-parse -q --verify 0.2.0 >/dev/null || fail 'a refused release left a tag behind'
! DIST_REMOTE="$work/missing.git" "$script" dist/pkg 0.2.0 "$work/one.tar.gz" 'no remote' 2>/dev/null || fail 'an unreachable remote was accepted'
! git rev-parse -q --verify refs/tags/0.2.0 >/dev/null || fail 'a failed release left a tag in the caller clone'

echo 'dist-branch: ok'
