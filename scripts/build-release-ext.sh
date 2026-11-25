#!/usr/bin/env sh
# Builds the ext release archives: ext is the only public executable. CTX, CTN
# and CTO are verified first-party command packages in the archive.
set -eu

VERSION=${1:-0.1.0-dev}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT=${2:-$ROOT/dist/release/ext-$VERSION}

case "$OUTPUT" in ''|/|.) printf 'ext: unsafe release output directory: %s\n' "$OUTPUT" >&2; exit 2;; esac
if [ -e "$OUTPUT" ]; then
  printf 'ext: release output already exists: %s\n' "$OUTPUT" >&2
  exit 1
fi
mkdir -p "$OUTPUT"
OUTPUT=$(CDPATH= cd -- "$OUTPUT" && pwd)

embedded_version() { # <product>: the version label of that product's Go module
  product=$1
  prefix="src/$product/v"
  tag=$(git -C "$ROOT" describe --tags --match "$prefix*" --exact-match 2>/dev/null || true)
  if [ -n "$tag" ]; then printf '%s\n' "${tag#"$prefix"}"; return; fi
  last=$(git -C "$ROOT" tag -l "$prefix*" 2>/dev/null | sort -V | tail -n 1)
  revision=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
  if [ -n "$last" ]; then printf '%s+%s\n' "${last#"$prefix"}" "$revision"; else printf 'dev+%s\n' "$revision"; fi
}
CTX_VERSION=${CTX_VERSION:-$(embedded_version ctx)}
CTN_VERSION=${CTN_VERSION:-$(embedded_version ctn)}
CTO_VERSION=${CTO_VERSION:-$(embedded_version cto)}

NATIVE=${EXT_RELEASE_NATIVE_DIR:-$ROOT/dist/native}
TARGETS=${EXT_RELEASE_TARGETS:-"darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"}
# Validate all requested engines before compiling or emitting any archive.
verify=$(mktemp -d "$OUTPUT/.engine-check.XXXXXX")
for target in $TARGETS; do
  python3 "$ROOT/scripts/stage-cli-engine.py" verify "$NATIVE/$target" "$target" "$verify/$target"
done
rm -rf "$verify"

build_bundle() {
  os=$1
  arch=$2
  extension=$3
  staging=$(mktemp -d "$OUTPUT/.ext-release.XXXXXX")
  bundle="$staging/ext"
  mkdir -p "$bundle/bin/ext-packages"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X github.com/webong/ext/cmd/ext/internal/app.Version=${VERSION#v}" \
    -o "$bundle/bin/ext$extension" ./cmd/ext)
  for product in ctx ctn cto; do
    case "$product" in
      ctx) product_version=$CTX_VERSION ;;
      ctn) product_version=$CTN_VERSION ;;
      cto) product_version=$CTO_VERSION ;;
    esac
    package_dir="$bundle/bin/ext-packages/ext%2f$product"
    mkdir -p "$package_dir"
    (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -ldflags "-s -w -X github.com/webong/ext/src/$product/internal/app.Version=$product_version" \
      -o "$package_dir/$product$extension" "./src/$product/plugin")
    (cd "$ROOT" && go run ./cmd/ext/tools/package-product "$product" "$product_version" "$os" "$arch" "$package_dir/$product$extension")
  done
  python3 "$ROOT/scripts/stage-cli-engine.py" verify "$NATIVE/$os/$arch" "$os/$arch" "$bundle/bin/ext-lib"
  cp "$ROOT/LICENSE" "$bundle/"
  if [ "$os" = windows ]; then
    (cd "$staging" && zip -qr "$OUTPUT/ext-$os-$arch.zip" ext)
  else
    tar -C "$staging" -czf "$OUTPUT/ext-$os-$arch.tar.gz" ext
  fi
  rm -rf "$staging"
}

for target in $TARGETS; do
  os=${target%/*}; arch=${target#*/}; extension=''
  [ "$os" != windows ] || extension='.exe'
  build_bundle "$os" "$arch" "$extension"
done
