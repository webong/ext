#!/usr/bin/env sh
set -eu

DST_BIN=${CTX_BIN_DIR:-$HOME/.local/bin}
# One home for every ext product. CTX_HOME is still honored, and an existing
# ~/.config/ctx installation keeps being used until ~/.config/ext exists.
if [ -n "${EXT_HOME:-}" ]; then CONFIG_DIR=$EXT_HOME
elif [ -n "${CTX_HOME:-}" ]; then CONFIG_DIR=$CTX_HOME
elif [ -d "$HOME/.config/ext" ] || [ ! -d "$HOME/.config/ctx" ]; then CONFIG_DIR=$HOME/.config/ext
else CONFIG_DIR=$HOME/.config/ctx
fi
VERSION=${CTX_VERSION:-latest}
CATALOG_ADAPTERS='docker podman nerdctl apple rancher_desktop orbstack docker_desktop firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium kube aws gcloud postgres mysql php jvm wasm claude_code codex git'
case "$(uname -s)" in
  Darwin) CREDENTIAL_ADAPTER=keychain ;;
  Linux) CREDENTIAL_ADAPTER=secret_service ;;
  *) CREDENTIAL_ADAPTER= ;;
esac
if [ -n "$CREDENTIAL_ADAPTER" ]; then CATALOG_ADAPTERS="$CATALOG_ADAPTERS $CREDENTIAL_ADAPTER"; fi
SETUP_MODE=minimal
SETUP_CHOSEN=0
SETUP_ADAPTERS=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --all) [ "$SETUP_CHOSEN" -eq 0 ] || { printf 'ctx: choose only one adapter selection mode\n' >&2; exit 2; }; SETUP_MODE=all; SETUP_CHOSEN=1 ;;
    --minimal) [ "$SETUP_CHOSEN" -eq 0 ] || { printf 'ctx: choose only one adapter selection mode\n' >&2; exit 2; }; SETUP_MODE=minimal; SETUP_CHOSEN=1 ;;
    --interactive) [ "$SETUP_CHOSEN" -eq 0 ] || { printf 'ctx: choose only one adapter selection mode\n' >&2; exit 2; }; SETUP_MODE=interactive; SETUP_CHOSEN=1 ;;
    --adapters) [ "$SETUP_CHOSEN" -eq 0 ] && [ "$#" -ge 2 ] || { printf 'ctx: --adapters needs a comma-separated value\n' >&2; exit 2; }; SETUP_MODE=selected; SETUP_CHOSEN=1; SETUP_ADAPTERS=$2; shift ;;
    --adapters=*) [ "$SETUP_CHOSEN" -eq 0 ] || { printf 'ctx: choose only one adapter selection mode\n' >&2; exit 2; }; SETUP_MODE=selected; SETUP_CHOSEN=1; SETUP_ADAPTERS=${1#--adapters=} ;;
    *) printf 'ctx: unknown installer option: %s\n' "$1" >&2; exit 2 ;;
  esac
  shift
done
if [ "$SETUP_MODE" = interactive ] && ! ( : </dev/tty >/dev/tty ) 2>/dev/null; then
  printf 'ctx: --interactive requires a terminal; use --adapters or --minimal\n' >&2
  exit 2
fi

ROOT=
if [ -z "${CTX_RELEASE_BASE:-}" ] && [ -f "$0" ]; then
  candidate=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
  if [ -f "$candidate/src/ctx/cmd/ctx/main.go" ]; then ROOT=$candidate; fi
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/ctx-install.XXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
bundle="$temporary/ctx"

if [ -n "$ROOT" ]; then
  command -v go >/dev/null 2>&1 || { printf 'ctx: Go is required when installing from source\n' >&2; exit 1; }
  mkdir -p "$bundle/bin"
  (cd "$ROOT" && go build -o "$bundle/bin/ctx" ./src/ctx/cmd/ctx)
else
  command -v curl >/dev/null 2>&1 || { printf 'ctx: curl is required for remote installation\n' >&2; exit 1; }
  os=$(uname -s)
  case "$os" in Darwin) os=darwin;; Linux) os=linux;; *) printf 'ctx: unsupported operating system: %s\n' "$os" >&2; exit 1;; esac
  arch=$(uname -m)
  case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) printf 'ctx: unsupported architecture: %s\n' "$arch" >&2; exit 1;; esac
  asset="ctx-$os-$arch.tar.gz"
  if [ -n "${CTX_RELEASE_BASE:-}" ]; then
    release_base=${CTX_RELEASE_BASE%/}
  else
    if [ "$VERSION" = latest ]; then
      # GitHub's "latest" is repository-wide and may be another product's release.
      tag=$(curl -fsSL "https://api.github.com/repos/webong/ext/releases?per_page=100" |
        grep -o '"tag_name": *"ctx-v[^"]*"' | head -n 1 | sed 's/.*"\(ctx-v[^"]*\)"/\1/')
      [ -n "$tag" ] || { printf 'ctx: no ctx release was found\n' >&2; exit 1; }
    else
      case "$VERSION" in ctx-v*) tag=$VERSION;; v*) tag=ctx-$VERSION;; *) tag=ctx-v$VERSION;; esac
    fi
    release_base="https://github.com/webong/ext/releases/download/$tag"
  fi
  archive="$temporary/$asset"
  curl -fsSL "$release_base/$asset" -o "$archive"
  curl -fsSL "$release_base/checksums.txt" -o "$temporary/checksums.txt"
  expected=$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset { print $1; exit }' "$temporary/checksums.txt")
  [ -n "$expected" ] || { printf 'ctx: release checksum is missing for %s\n' "$asset" >&2; exit 1; }
  if command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$archive" | awk '{print $1}')
  elif command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$archive" | awk '{print $1}')
  else printf 'ctx: shasum or sha256sum is required to verify the release\n' >&2; exit 1
  fi
  [ "$actual" = "$expected" ] || { printf 'ctx: release checksum verification failed for %s\n' "$asset" >&2; exit 1; }
  tar -xzf "$archive" -C "$temporary"
fi

if [ "$SETUP_MODE" != minimal ]; then
  if [ -n "$ROOT" ]; then
    mkdir -p "$bundle/adapters"
    for adapter in $CATALOG_ADAPTERS; do cp -R "$ROOT/adapters/$adapter" "$bundle/adapters/$adapter"; done
    (cd "$ROOT" && go build -o "$bundle/adapters/git/ctx-git" ./adapters/git/native)
    rm -rf "$bundle/adapters/git/native"
    (cd "$ROOT" && go build -o "$bundle/adapters/wasm/ctx-wasm" ./adapters/wasm/native)
    rm -rf "$bundle/adapters/wasm/native"
    for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do
      (cd "$ROOT" && go build -o "$bundle/adapters/$adapter/ctx-$adapter-share" "./adapters/$adapter/native")
      rm -rf "$bundle/adapters/$adapter/native"
    done
    if [ -n "$CREDENTIAL_ADAPTER" ]; then
      (cd "$ROOT" && go build -o "$bundle/adapters/$CREDENTIAL_ADAPTER/ctx-$(printf '%s' "$CREDENTIAL_ADAPTER" | tr _ -)" "./adapters/$CREDENTIAL_ADAPTER/native")
      rm -rf "$bundle/adapters/$CREDENTIAL_ADAPTER/native" "$bundle/adapters/$CREDENTIAL_ADAPTER/store"
    fi
    rm -rf "$bundle/adapters/chromium/engine"
		rm -rf "$bundle/adapters/firefox/engine"
  else
    catalog_asset="ctx-adapters-$os-$arch.tar.gz"
    catalog_archive="$temporary/$catalog_asset"
    curl -fsSL "$release_base/$catalog_asset" -o "$catalog_archive"
    expected=$(awk -v asset="$catalog_asset" '$2 == asset || $2 == "*" asset { print $1; exit }' "$temporary/checksums.txt")
    [ -n "$expected" ] || { printf 'ctx: release checksum is missing for %s\n' "$catalog_asset" >&2; exit 1; }
    if command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$catalog_archive" | awk '{print $1}')
    else actual=$(sha256sum "$catalog_archive" | awk '{print $1}'); fi
    [ "$actual" = "$expected" ] || { printf 'ctx: release checksum verification failed for %s\n' "$catalog_asset" >&2; exit 1; }
    tar -xzf "$catalog_archive" -C "$temporary"
  fi
fi

[ -x "$bundle/bin/ctx" ] || { printf 'ctx: native bundle is missing ctx\n' >&2; exit 1; }
target="$DST_BIN/ctx"
if [ -e "$target" ] || [ -L "$target" ]; then
  "$target" version 2>/dev/null | grep -Eq '^ctx ' || { printf 'ctx: %s exists; choose another CTX_BIN_DIR\n' "$target" >&2; exit 1; }
fi

MIGRATE_ADAPTERS=
for adapter in docker podman nerdctl; do
  shim="$DST_BIN/$adapter"
  if { [ -e "$shim" ] || [ -L "$shim" ]; } && \
    dd if="$shim" bs=256 count=1 2>/dev/null | grep -Eq 'ctx wrapper|dctx shim'; then
    MIGRATE_ADAPTERS="$MIGRATE_ADAPTERS $adapter"
  fi
done

mkdir -p "$DST_BIN" "$CONFIG_DIR/adapters"
cp "$bundle/bin/ctx" "$DST_BIN/ctx"
chmod +x "$DST_BIN/ctx"
if [ "$SETUP_MODE" != minimal ]; then
  mkdir -p "$CONFIG_DIR/catalog/adapters"
  for adapter in $CATALOG_ADAPTERS; do
    source_adapter="$bundle/adapters/$adapter"
    target_adapter="$CONFIG_DIR/catalog/adapters/$adapter"
    if [ -e "$target_adapter" ]; then rm -rf "$target_adapter"; fi
    cp -R "$source_adapter" "$target_adapter"
  done
  EXT_HOME="$CONFIG_DIR" CTX_BIN_DIR="$DST_BIN" "$DST_BIN/ctx" adapter refresh >/dev/null
  for adapter in $MIGRATE_ADAPTERS; do
    rm -f "$DST_BIN/$adapter"
    EXT_HOME="$CONFIG_DIR" CTX_BIN_DIR="$DST_BIN" "$DST_BIN/ctx" adapter add "$adapter" >/dev/null
  done
elif [ -n "$MIGRATE_ADAPTERS" ]; then
  printf 'Legacy shims were preserved; rerun with --adapters to migrate them.\n'
fi

case "$SETUP_MODE" in
  all) EXT_HOME="$CONFIG_DIR" CTX_BIN_DIR="$DST_BIN" "$DST_BIN/ctx" setup --all ;;
  minimal) : ;;
  selected) EXT_HOME="$CONFIG_DIR" CTX_BIN_DIR="$DST_BIN" "$DST_BIN/ctx" setup --adapters "$SETUP_ADAPTERS" ;;
  interactive)
    EXT_HOME="$CONFIG_DIR" CTX_BIN_DIR="$DST_BIN" "$DST_BIN/ctx" setup </dev/tty >/dev/tty
    ;;
esac

if [ ! -f "$CONFIG_DIR/config.toml" ]; then
  printf '%s\n' '# ctx configuration' '# docker_default = "desktop-linux"' '# podman_default = "podman-machine-default"' '# nerdctl_default = "default"' > "$CONFIG_DIR/config.toml"
fi

if [ "$SETUP_MODE" = minimal ]; then
  printf 'Installed ctx core in %s (no adapter catalog downloaded).\n' "$DST_BIN"
else
  printf 'Installed ctx and the optional adapter catalog in %s\n' "$DST_BIN"
fi
printf 'Config: %s/config.toml\n' "$CONFIG_DIR"
case ":$PATH:" in *":$DST_BIN:"*) ;; *) printf 'Add %s before Docker, Podman, and nerdctl on PATH.\n' "$DST_BIN";; esac
for rc in "$HOME/.zshrc" "$HOME/.bashrc"; do
  if [ -f "$rc" ] && grep -q 'dctx hook' "$rc"; then
    printf 'Old dctx shell hook found in %s; remove that line so it cannot override ctx.\n' "$rc"
  fi
done
