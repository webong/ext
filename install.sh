#!/usr/bin/env sh
# Install the ext CLI and its first-party command packages.
set -eu

BIN_DIR=${EXT_BIN_DIR:-"$HOME/.local/bin"}
CONFIG_DIR=${EXT_HOME:-"$HOME/.config/ext"}
VERSION=${EXT_VERSION:-}
SETUP=
ADAPTERS=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION=${2:?--version needs a value}; shift ;;
    --all|--minimal|--interactive) SETUP=${1#--} ;;
    --adapters) SETUP=adapters; ADAPTERS=${2:?--adapters needs a value}; shift ;;
    --adapters=*) SETUP=adapters; ADAPTERS=${1#--adapters=} ;;
    --ext) ;; # accepted by older source-install instructions
    *) printf 'ext: unknown installer option: %s\n' "$1" >&2; exit 2 ;;
  esac
  shift
done

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/ext-install.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
BUNDLE="$TMP/ext"

if [ -f "$ROOT/cmd/ext/main.go" ] && [ "${EXT_INSTALL_SOURCE:-1}" = 1 ]; then
  command -v go >/dev/null 2>&1 || { printf 'ext: Go is required for source installation\n' >&2; exit 1; }
  OS=$(go env GOOS)
  ARCH=$(go env GOARCH)
  mkdir -p "$BUNDLE/bin/ext-packages"
  (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$BUNDLE/bin/ext" ./cmd/ext)
  for product in ctx ctn cto; do
    package_dir="$BUNDLE/bin/ext-packages/ext%2f$product"
    mkdir -p "$package_dir"
    (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$package_dir/$product" "./src/$product/plugin")
    (cd "$ROOT" && go run ./cmd/ext/tools/package-product "$product" dev "$OS" "$ARCH" "$package_dir/$product")
  done
else
  command -v curl >/dev/null 2>&1 || { printf 'ext: curl is required for remote installation\n' >&2; exit 1; }
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) printf 'ext: unsupported architecture %s\n' "$ARCH" >&2; exit 1;; esac
  case "$OS" in darwin|linux) ;; *) printf 'ext: unsupported operating system %s\n' "$OS" >&2; exit 1;; esac
  if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL 'https://api.github.com/repos/webong/ext/releases?per_page=100' |
      grep -o '"tag_name": *"ext-v[^"]*"' | head -n 1 | sed 's/.*"ext-v\([^"]*\)"/\1/')
    [ -n "$VERSION" ] || { printf 'ext: no ext release was found\n' >&2; exit 1; }
  fi
  VERSION=${VERSION#ext-v}
  VERSION=${VERSION#v}
  ASSET="ext-$OS-$ARCH.tar.gz"
  BASE=${EXT_RELEASE_BASE:-"https://github.com/webong/ext/releases/download/ext-v$VERSION"}
  case "$BASE" in
    file://*) cp "${BASE#file://}/$ASSET" "$TMP/$ASSET"; cp "${BASE#file://}/checksums.txt" "$TMP/checksums.txt" ;;
    *) curl -fsSL "$BASE/$ASSET" -o "$TMP/$ASSET"; curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt" ;;
  esac
  EXPECTED=$(awk -v asset="$ASSET" '$2 == asset {print $1}' "$TMP/checksums.txt")
  [ -n "$EXPECTED" ] || { printf 'ext: checksum missing for %s\n' "$ASSET" >&2; exit 1; }
  if command -v shasum >/dev/null 2>&1; then ACTUAL=$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')
  else ACTUAL=$(sha256sum "$TMP/$ASSET" | awk '{print $1}'); fi
  [ "$EXPECTED" = "$ACTUAL" ] || { printf 'ext: checksum mismatch for %s\n' "$ASSET" >&2; exit 1; }
  tar -C "$TMP" -xzf "$TMP/$ASSET"
fi

[ -x "$BUNDLE/bin/ext" ] && [ -d "$BUNDLE/bin/ext-packages" ] || {
  printf 'ext: bundle is missing the CLI or product packages\n' >&2; exit 1;
}
mkdir -p "$BIN_DIR" "$CONFIG_DIR"
if [ -e "$BIN_DIR/ext" ] && ! "$BIN_DIR/ext" version >/dev/null 2>&1; then
  printf 'ext: %s exists and is not an ext executable\n' "$BIN_DIR/ext" >&2; exit 1
fi
cp "$BUNDLE/bin/ext" "$BIN_DIR/ext"
chmod +x "$BIN_DIR/ext"
rm -rf "$BIN_DIR/ext-packages"
cp -R "$BUNDLE/bin/ext-packages" "$BIN_DIR/ext-packages"
EXT_HOME="$CONFIG_DIR" "$BIN_DIR/ext" commands >/dev/null

if [ -n "$SETUP" ]; then
  if [ -f "$ROOT/cmd/ext/main.go" ]; then
    mkdir -p "$CONFIG_DIR/catalog/adapters"
    for source in "$ROOT"/adapters/*; do
      [ -d "$source" ] || continue
      cp -R "$source" "$CONFIG_DIR/catalog/adapters/$(basename "$source")"
    done
  fi
  case "$SETUP" in
    all|minimal) EXT_HOME="$CONFIG_DIR" EXT_BIN_DIR="$BIN_DIR" "$BIN_DIR/ext" ctx setup "--$SETUP" ;;
    adapters) EXT_HOME="$CONFIG_DIR" EXT_BIN_DIR="$BIN_DIR" "$BIN_DIR/ext" ctx setup --adapters "$ADAPTERS" ;;
    interactive) EXT_HOME="$CONFIG_DIR" EXT_BIN_DIR="$BIN_DIR" "$BIN_DIR/ext" ctx setup ;;
  esac
fi
printf 'Installed ext and its CTX, CTN and CTO packages in %s\n' "$BIN_DIR"
