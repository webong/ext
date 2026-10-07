#!/usr/bin/env sh
set -eu

VERSION=${1:-0.8.0-dev}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT=${2:-$ROOT/dist/release/$VERSION}
CATALOG_ADAPTERS='docker podman nerdctl apple rancher_desktop orbstack docker_desktop firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium kube aws gcloud postgres mysql php jvm wasm evm claude_code codex git'

case "$OUTPUT" in ''|/|.) printf 'ctx: unsafe release output directory: %s\n' "$OUTPUT" >&2; exit 2;; esac
if [ -e "$OUTPUT" ]; then
  printf 'ctx: release output already exists: %s\n' "$OUTPUT" >&2
  exit 1
fi
mkdir -p "$OUTPUT"
OUTPUT=$(CDPATH= cd -- "$OUTPUT" && pwd)

build_bundle() {
  os=$1
  arch=$2
  extension=$3
  case "$os" in
    darwin) credential_adapter=keychain ;;
    linux) credential_adapter=secret_service ;;
    windows) credential_adapter=credman ;;
  esac
  catalog_adapters="$CATALOG_ADAPTERS $credential_adapter"
  staging=$(mktemp -d "$OUTPUT/.ctx-release.XXXXXX")
  bundle="$staging/ctx"
  mkdir -p "$bundle/bin"

  binary="$bundle/bin/ctx$extension"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X github.com/webong/ext/ctx/internal/app.Version=${VERSION#v}" \
    -o "$binary" ./src/ctx/cmd/ctx)
  cp "$ROOT/LICENSE" "$ROOT/README.md" "$bundle/"

  if [ "$os" = windows ]; then
    (cd "$staging" && zip -qr "$OUTPUT/ctx-$os-$arch.zip" ctx)
  else
    tar -C "$staging" -czf "$OUTPUT/ctx-$os-$arch.tar.gz" ctx
  fi

  mkdir -p "$bundle/adapters"
  for adapter in $catalog_adapters; do
    cp -R "$ROOT/adapters/$adapter" "$bundle/adapters/$adapter"
  done
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w" \
    -o "$bundle/adapters/git/ctx-git$extension" ./adapters/git/native)
  rm -rf "$bundle/adapters/git/native"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w" \
    -o "$bundle/adapters/wasm/ctx-wasm$extension" ./adapters/wasm/native)
  rm -rf "$bundle/adapters/wasm/native"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w" \
    -o "$bundle/adapters/evm/ctx-evm$extension" ./adapters/evm/native)
  rm -rf "$bundle/adapters/evm/native"
  case "$os" in
    darwin)
      case "$arch" in
        amd64) keychain_binary=${CTX_KEYCHAIN_AMD64:-} ;;
        arm64) keychain_binary=${CTX_KEYCHAIN_ARM64:-} ;;
      esac
      [ -n "$keychain_binary" ] && [ -f "$keychain_binary" ] || {
        printf 'ctx: a native macOS Keychain adapter binary is required for %s\n' "$arch" >&2
        exit 1
      }
      keychain_info=$(go version -m "$keychain_binary")
      printf '%s\n' "$keychain_info" | grep -Fq "GOOS=darwin" || {
        printf 'ctx: Keychain adapter is not a Darwin Go binary\n' >&2; exit 1;
      }
      printf '%s\n' "$keychain_info" | grep -Fq "GOARCH=$arch" || {
        printf 'ctx: Keychain adapter architecture does not match %s\n' "$arch" >&2; exit 1;
      }
      cp "$keychain_binary" "$bundle/adapters/keychain/ctx-keychain"
      chmod +x "$bundle/adapters/keychain/ctx-keychain"
      ;;
    linux|windows)
      credential_executable="ctx-$(printf '%s' "$credential_adapter" | tr _ -)$extension"
      (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -trimpath -ldflags "-s -w" \
        -o "$bundle/adapters/$credential_adapter/$credential_executable" "./adapters/$credential_adapter/native")
      ;;
  esac
  rm -rf "$bundle/adapters/$credential_adapter/native" "$bundle/adapters/$credential_adapter/store"
  for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do
    (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -ldflags "-s -w" \
      -o "$bundle/adapters/$adapter/ctx-$adapter-share$extension" "./adapters/$adapter/native")
    rm -rf "$bundle/adapters/$adapter/native"
  done
  rm -rf "$bundle/adapters/chromium/engine"
  rm -rf "$bundle/adapters/firefox/engine"
  if [ "$os" = windows ]; then
    (cd "$staging" && zip -qr "$OUTPUT/ctx-adapters-$os-$arch.zip" ctx/adapters)
  else
    tar -C "$staging" -czf "$OUTPUT/ctx-adapters-$os-$arch.tar.gz" ctx/adapters
  fi
  rm -rf "$staging"
}

build_bundle darwin amd64 ''
build_bundle darwin arm64 ''
build_bundle linux amd64 ''
build_bundle linux arm64 ''
build_bundle windows amd64 '.exe'
build_bundle windows arm64 '.exe'
