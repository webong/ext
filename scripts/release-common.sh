#!/usr/bin/env bash
# Shared helpers for scripts/release-*.sh. Source it; it is not run on its own.
#
# A release round is described by a versions file: one "module version" pair per
# line, the module as a directory path relative to the repository root (for
# example "pkg/ext-go/engine v0.4.0"). Blank lines and "#" comments are
# ignored. The scripts need only bash (3.2 or later), git, awk, sed and Go.
#
# The module path prefix is github.com/webong/ext; set EXT_MODULE_PREFIX to use
# these scripts on a fork.

release_die() {
  echo "error: $*" >&2
  exit 1
}

release_init() {
  ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || release_die "run this inside a checkout of the repository"
  PREFIX=${EXT_MODULE_PREFIX:-github.com/webong/ext}
}

# release_versions FILE prints the validated pairs, one per line.
release_versions() {
  [ -f "$1" ] || release_die "versions file $1 not found"
  awk -v root="$ROOT" '
    { sub(/#.*/, ""); gsub(/^[ \t]+|[ \t]+$/, "") }
    $0 == "" { next }
    NF != 2 { printf "bad line in %s: %s\n", FILENAME, $0 > "/dev/stderr"; bad = 1; next }
    $2 !~ /^v[0-9]+\.[0-9]+\.[0-9]+$/ { printf "not a release version: %s %s\n", $1, $2 > "/dev/stderr"; bad = 1; next }
    seen[$1]++ { printf "listed twice: %s\n", $1 > "/dev/stderr"; bad = 1; next }
    { gomod = root "/" $1 "/go.mod"
      if ((getline line < gomod) < 0) { printf "no go.mod for %s\n", $1 > "/dev/stderr"; bad = 1 }
      close(gomod); print $1, $2 }
    END { exit bad }
  ' "$1" || release_die "invalid versions file $1"
}

# release_layers FILE prints "layer module version" in dependency order. Layer 0
# holds modules that require no other listed module; a module sits one layer above
# the highest listed module it requires. Modules in one layer do not depend on
# each other, so they can be tagged together, and a layer is tagged and verified
# before the next one, because the next one records its hashes.
release_layers() {
  release_versions "$1" | awk -v root="$ROOT" -v prefix="$PREFIX/" '
    { name[NR] = $1; ver[NR] = $2; index_of[$1] = NR; n = NR }
    END {
      for (i = 1; i <= n; i++) {
        file = root "/" name[i] "/go.mod"
        while ((getline line < file) > 0) {
          sub(/\/\/.*/, "", line)
          if (match(line, prefix "[^ \t]+")) {
            dep = substr(line, RSTART + length(prefix), RLENGTH - length(prefix))
            if ((dep in index_of) && dep != name[i]) { count[i]++; needs[i, count[i]] = index_of[dep] }
          }
        }
        close(file)
      }
      for (pass = 0; pass <= n; pass++) {
        changed = 0
        for (i = 1; i <= n; i++)
          for (k = 1; k <= count[i]; k++) {
            j = needs[i, k]
            if (level[i] < level[j] + 1) { level[i] = level[j] + 1; changed = 1 }
          }
        if (!changed) break
      }
      if (changed) { print "the listed modules require each other in a cycle" > "/dev/stderr"; exit 1 }
      for (i = 1; i <= n; i++) print level[i] + 0, name[i], ver[i]
    }
  ' | sort -k1,1n -k2,2
}

# release_workdir [clone] creates a scratch directory holding a bare repository
# that stands in for the real remote, and a git config that makes the go command
# fetch the repository from it. It starts empty, or as a clone of this repository
# (with its published tags) when asked. Sets WORK. The caller removes it with
# release_clean. Tags made in it are forced, so a version that already exists
# can be rehearsed.
release_workdir() {
  WORK=$(mktemp -d "${TMPDIR:-/tmp}/ext-release.XXXXXX")
  if [ "${1:-empty}" = clone ]; then
    # the published tags are needed too, for modules the round does not release
    git -C "$ROOT" fetch -q --tags origin 2>/dev/null || true
    git clone -q --bare "$ROOT" "$WORK/repo.git"
  else
    git init -q --bare "$WORK/repo.git"
  fi
  printf '[url "file://%s"]\n\tinsteadOf = https://%s\n' "$WORK/repo.git" "$PREFIX" > "$WORK/gitconfig"
  mkdir "$WORK/scratch"
  printf 'module scratch\n\ngo 1.23\n' > "$WORK/scratch/go.mod"
}

release_clean() {
  [ -n "${WORK:-}" ] && [ -d "$WORK" ] || return 0
  chmod -R u+w "$WORK" 2>/dev/null || true
  rm -rf "$WORK"
}

# release_tag_rehearsal REVISION VERSIONS tags every listed module at REVISION in
# the scratch repository.
release_tag_rehearsal() {
  local list m v
  list=$(release_versions "$2")
  git -C "$ROOT" push -q "$WORK/repo.git" "$1:refs/heads/rehearsal"
  while read -r m v; do
    GIT_COMMITTER_NAME=release GIT_COMMITTER_EMAIL=release@example.invalid \
      git -C "$WORK/repo.git" tag -f -a "$m/$v" -m "$m $v" "$1"
  done <<EOF2
$list
EOF2
}

# release_hermetic_env exports the environment in which the go command resolves
# the repository from the scratch repository only. The module cache is fresh
# (Go indexes cache directories by path and assumes their contents never change,
# so a reused cache serves stale versions), and the workspace is off.
release_hermetic_env() {
  export GIT_CONFIG_GLOBAL="$WORK/gitconfig" GIT_CONFIG_NOSYSTEM=1 GIT_ALLOW_PROTOCOL=file:https:ssh
  export GOWORK=off GOTOOLCHAIN=local GOFLAGS=-modcacherw GOMODCACHE="$WORK/modcache"
  export GOPROXY=direct GOSUMDB=off GONOSUMDB='*'
}

# release_download MODULE VERSION prints "Sum GoModSum" as the go command computes
# them. This is the code path the module proxy runs, so it is the authority on
# what the published module's hash will be.
release_download() {
  local out sum gomod
  out=$(cd "$WORK/scratch" && go mod download -json "$PREFIX/$1@$2" 2>&1) || release_die "go mod download $1@$2 failed: $out"
  sum=$(printf '%s\n' "$out" | sed -n 's/.*"Sum": "\(.*\)".*/\1/p')
  gomod=$(printf '%s\n' "$out" | sed -n 's/.*"GoModSum": "\(.*\)".*/\1/p')
  [ -n "$sum" ] && [ -n "$gomod" ] || release_die "no hash reported for $1@$2: $out"
  echo "$sum $gomod"
}
