#!/usr/bin/env bash
# Publish a staged tree to a dist/ branch of this repository and tag the commit.
#
# A dist/<name> branch holds the tree of a release that the consumer's tooling needs at a
# repository root: SwiftPM resolves a package from the root and this monorepo's root is not a
# package. The branch is never merged into main and this script is its only writer. The first
# release creates it with no history from main; every later release adds one commit on top of the
# last. It never force-pushes, and it builds the commit from a temporary index, so the clone it
# runs in keeps its checkout. See "Distribution branches" in docs/module-versioning.md.
#
# Usage: scripts/dist-branch.sh <dist/name> <tag> <archive.tar.gz> <commit subject> [<commit body>]
#
# Run it in a clone whose remote (DIST_REMOTE, default origin) is the repository and accepts the
# push. The tag must not exist on the remote yet. The branch and the tag are pushed together or
# not at all.
set -euo pipefail
usage='usage: scripts/dist-branch.sh <dist/name> <tag> <archive.tar.gz> <commit subject> [<commit body>]'
branch="${1:?$usage}"
tag="${2:?$usage}"
archive="${3:?$usage}"
subject="${4:?$usage}"
body="${5:-}"
remote="${DIST_REMOTE:-origin}"
[[ "$branch" == dist/?* ]] || { echo "the branch '$branch' must look like dist/<name>" >&2; exit 2; }
[[ -f "$archive" ]] || { echo "no archive at $archive" >&2; exit 2; }
archive="$(cd "$(dirname "$archive")" && pwd)/$(basename "$archive")"

if ! git config user.name >/dev/null || ! git config user.email >/dev/null; then
  export GIT_AUTHOR_NAME='github-actions[bot]' GIT_COMMITTER_NAME='github-actions[bot]'
  export GIT_AUTHOR_EMAIL='41898282+github-actions[bot]@users.noreply.github.com'
  export GIT_COMMITTER_EMAIL='41898282+github-actions[bot]@users.noreply.github.com'
fi

# Whether a ref exists on the remote: 0 yes, 2 no. Anything else (no network, no access) stops
# the script rather than being read as "no", which would start a second history for the branch.
remote_ref() {
  local status=0
  git ls-remote --exit-code "$@" >/dev/null 2>&1 || status=$?
  case "$status" in
    0 | 2) return "$status" ;;
    *) echo "cannot read $remote" >&2; exit 1 ;;
  esac
}

if remote_ref --tags "$remote" "refs/tags/$tag"; then
  echo "the tag $tag already exists on $remote" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir "$work/tree"
tar -xzf "$archive" -C "$work/tree"
[[ -n "$(ls -A "$work/tree")" ]] || { echo "the archive $archive is empty" >&2; exit 1; }

parent=""
if remote_ref --heads "$remote" "refs/heads/$branch"; then
  git fetch --quiet --depth=1 "$remote" "refs/heads/$branch"
  parent="$(git rev-parse FETCH_HEAD)"
fi

# The tree, staged in a temporary index so the caller's index and checkout stay as they are.
# -f keeps files the package's own .gitignore would hide.
gitdir="$(git rev-parse --absolute-git-dir)"
tree="$(cd "$work/tree" && GIT_DIR="$gitdir" GIT_WORK_TREE="$work/tree" GIT_INDEX_FILE="$work/index" \
  sh -c 'git add -A -f . && git write-tree')"

message="$subject"
if [[ -n "$body" ]]; then message="$subject"$'\n\n'"$body"; fi
commit="$(printf '%s\n' "$message" | git commit-tree "$tree" ${parent:+-p "$parent"})"
git tag "$tag" "$commit"
if ! git push --atomic "$remote" "$commit:refs/heads/$branch" "refs/tags/$tag"; then
  git tag -d "$tag" >/dev/null
  exit 1
fi
echo "pushed $commit to $branch and tagged it $tag"
