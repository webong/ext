#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT HUP INT TERM

GOCACHE=$(go env GOCACHE)
GOMODCACHE=$(go env GOMODCACHE)
export GOCACHE GOMODCACHE

export HOME="$TEST_ROOT/home"
export EXT_HOME="$TEST_ROOT/config"
export EXT_BIN_DIR="$TEST_ROOT/bin"
mkdir -p "$HOME" "$EXT_HOME" "$EXT_BIN_DIR" "$TEST_ROOT/project"
printf '%s\n' 'docker_default = "alpha"' > "$EXT_HOME/config.toml"
cp "$EXT_HOME/config.toml" "$TEST_ROOT/config.before"
printf '%s\n' 'profile = "client-a"' > "$TEST_ROOT/project/.ctx"

"$ROOT/install.sh" >/dev/null
test -x "$EXT_BIN_DIR/ext"
test ! -e "$EXT_BIN_DIR/ctx"
test ! -e "$EXT_BIN_DIR/ctn"
test ! -e "$EXT_BIN_DIR/cto"
test -d "$EXT_BIN_DIR/ext-packages/ext%2fctx"
test -d "$EXT_BIN_DIR/ext-packages/ext%2fctn"
test -d "$EXT_BIN_DIR/ext-packages/ext%2fcto"
"$EXT_BIN_DIR/ext" commands | grep -Eq '^ctx[[:space:]]+package'
"$EXT_BIN_DIR/ext" commands | grep -Eq '^ctn[[:space:]]+package'
"$EXT_BIN_DIR/ext" commands | grep -Eq '^cto[[:space:]]+package'
"$EXT_BIN_DIR/ext" ctx version | grep -Eq '^ctx '
"$EXT_BIN_DIR/ext" ctn version | grep -Eq '^ctn '
cmp "$TEST_ROOT/config.before" "$EXT_HOME/config.toml"
grep -Fxq 'profile = "client-a"' "$TEST_ROOT/project/.ctx"

# Archive staging needs several copies of the native bundle. Keep the source
# installation check runnable on small developer disks; CI runs the full path.
available_kib=$(df -Pk "$TEST_ROOT" | awk 'NR == 2 {print $4}')
if [ "$available_kib" -lt 1048576 ]; then
  printf 'ext source installer tests passed (archive check needs 1 GiB free)\n'
  exit 0
fi

stage="$TEST_ROOT/stage"
release="$TEST_ROOT/release"
mkdir -p "$stage/ext/bin" "$release"
cp "$EXT_BIN_DIR/ext" "$stage/ext/bin/ext"
cp -R "$EXT_BIN_DIR/ext-packages" "$stage/ext/bin/ext-packages"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) exit 1;; esac
asset="ext-$os-$arch.tar.gz"
tar -C "$stage" -czf "$release/$asset" ext
if command -v shasum >/dev/null 2>&1; then
  (cd "$release" && shasum -a 256 "$asset" > checksums.txt)
else
  (cd "$release" && sha256sum "$asset" > checksums.txt)
fi

EXT_HOME="$TEST_ROOT/remote-config" EXT_BIN_DIR="$TEST_ROOT/remote-bin" \
  EXT_INSTALL_SOURCE=0 EXT_RELEASE_BASE="file://$release" \
  "$ROOT/install.sh" --version 0.1.0-dev >/dev/null
"$TEST_ROOT/remote-bin/ext" commands | grep -Eq '^cto[[:space:]]+package'

printf 'tampered' >> "$release/$asset"
if EXT_HOME="$TEST_ROOT/bad-config" EXT_BIN_DIR="$TEST_ROOT/bad-bin" \
  EXT_INSTALL_SOURCE=0 EXT_RELEASE_BASE="file://$release" \
  "$ROOT/install.sh" --version 0.1.0-dev >"$TEST_ROOT/bad.log" 2>&1; then
  printf 'installer accepted a tampered archive\n' >&2
  exit 1
fi
grep -Fq 'checksum mismatch' "$TEST_ROOT/bad.log"
test ! -e "$TEST_ROOT/bad-bin/ext"
printf 'ext installer tests passed\n'
