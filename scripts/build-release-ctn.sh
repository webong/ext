#!/usr/bin/env sh
set -eu

VERSION=${1:-0.0.0-dev}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT=${2:-$ROOT/dist/release/ctn-$VERSION}

case "$OUTPUT" in ''|/|.) printf 'ctn: unsafe release output directory: %s\n' "$OUTPUT" >&2; exit 2;; esac
if [ -e "$OUTPUT" ]; then
  printf 'ctn: release output already exists: %s\n' "$OUTPUT" >&2
  exit 1
fi
mkdir -p "$OUTPUT"
OUTPUT=$(CDPATH= cd -- "$OUTPUT" && pwd)

build_bundle() {
  os=$1
  arch=$2
  extension=$3
  staging=$(mktemp -d "$OUTPUT/.ctn-release.XXXXXX")
  bundle="$staging/ctn"
  mkdir -p "$bundle/bin"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X github.com/webong/ext/ctn/internal/app.Version=${VERSION#v}" \
    -o "$bundle/bin/ctn$extension" ./src/ctn/cmd/ctn)
  cp "$ROOT/LICENSE" "$bundle/"
  if [ "$os" = windows ]; then
    (cd "$staging" && zip -qr "$OUTPUT/ctn-$os-$arch.zip" ctn)
  else
    tar -C "$staging" -czf "$OUTPUT/ctn-$os-$arch.tar.gz" ctn
  fi
  rm -rf "$staging"
}

build_bundle darwin amd64 ''
build_bundle darwin arm64 ''
build_bundle linux amd64 ''
build_bundle linux arm64 ''
build_bundle windows amd64 '.exe'
build_bundle windows arm64 '.exe'
