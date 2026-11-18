#!/usr/bin/env bash
# Checks scripts/dist-branch.sh against a throwaway remote: the first release creates the branch
# without history from main, the next adds one commit, a repeated tag, a tag of the wrong shape and
# a bad branch name are refused, and the caller's checkout is never touched. It runs for dist/ext-ios
# (bare X.Y.Z tags) and dist/ext-macos (ext-macos-X.Y.Z tags) on one remote.
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

# One release history per branch, on one remote: dist/ext-ios with the bare tags SwiftPM resolves,
# dist/ext-macos with its own prefixed tags, since tags are repository-wide.
release_cycle() {
  local branch="$1" prefix="$2"
  "$script" "$branch" "${prefix}0.1.0" "$work/one.tar.gz" "$branch 0.1.0" 'staged by a test' >/dev/null
  [[ "$(remote rev-list --count "$branch")" == 1 ]] || fail "$branch: the first release is not a single commit"
  [[ -z "$(remote merge-base main "$branch" 2>/dev/null || true)" ]] || fail "$branch: the branch shares history with main"
  [[ "$(remote cat-file -p "$branch:Package.swift")" == one ]] || fail "$branch: an ignored file was left out"
  [[ "$(remote cat-file -p "$branch:Sources/x.swift")" == x ]] || fail "$branch: a nested file was left out"
  [[ "$(remote rev-parse "${prefix}0.1.0")" == "$(remote rev-parse "$branch")" ]] || fail "$branch: the tag is not on the branch tip"
  [[ "$(remote log -1 --format=%an "$branch")" == 'github-actions[bot]' ]] || fail "$branch: the default identity was not used"
  [[ -z "$(git status --porcelain)" && "$(ls)" == README ]] || fail "$branch: the caller checkout changed"
  local first tip
  first="$(remote rev-parse "$branch")"

  "$script" "$branch" "${prefix}0.1.1" "$work/two.tar.gz" "$branch 0.1.1" >/dev/null
  [[ "$(remote rev-list --count "$branch")" == 2 ]] || fail "$branch: the second release did not add one commit"
  [[ "$(remote rev-parse "$branch^")" == "$first" ]] || fail "$branch: the second release is not on top of the first"
  [[ "$(remote cat-file -p "$branch:Package.swift")" == two ]] || fail "$branch: the tree was not replaced"
  remote cat-file -e "$branch:Sources/x.swift" 2>/dev/null && fail "$branch: a file of the previous release remains"
  [[ "$(remote rev-parse "${prefix}0.1.0")" == "$first" ]] || fail "$branch: an earlier tag moved"
  tip="$(remote rev-parse "$branch")"

  ! "$script" "$branch" "${prefix}0.1.1" "$work/one.tar.gz" 'again' 2>/dev/null || fail "$branch: a repeated tag was accepted"
  [[ "$(remote rev-parse "$branch")" == "$tip" ]] || fail "$branch: a refused release moved the branch"
}
release_cycle dist/ext-ios ''
release_cycle dist/ext-macos ext-macos-
[[ -z "$(remote merge-base dist/ext-ios dist/ext-macos 2>/dev/null || true)" ]] || fail 'the two dist branches share history'

# Each branch takes only its own tag shape.
! "$script" dist/ext-macos 0.2.0 "$work/one.tar.gz" 'bare' 2>/dev/null || fail 'dist/ext-macos accepted a bare tag'
! "$script" dist/ext-ios ext-ios-0.2.0 "$work/one.tar.gz" 'prefixed' 2>/dev/null || fail 'dist/ext-ios accepted a prefixed tag'
! "$script" dist/ext-macos ext-ios-0.2.0 "$work/one.tar.gz" 'other' 2>/dev/null || fail "dist/ext-macos accepted another branch's tag"
! remote rev-parse -q --verify 0.2.0 >/dev/null || fail 'a refused release left a tag behind'
! "$script" main 0.2.0 "$work/one.tar.gz" 'wrong' 2>/dev/null || fail 'a branch outside dist/ was accepted'
! remote rev-parse -q --verify 0.2.0 >/dev/null || fail 'a refused release left a tag behind'
! DIST_REMOTE="$work/missing.git" "$script" dist/ext-ios 0.2.0 "$work/one.tar.gz" 'no remote' 2>/dev/null || fail 'an unreachable remote was accepted'
! git rev-parse -q --verify refs/tags/0.2.0 >/dev/null || fail 'a failed release left a tag in the caller clone'

echo 'dist-branch: ok'
