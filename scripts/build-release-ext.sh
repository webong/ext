#!/usr/bin/env sh
# Builds the ext release archives: one binary per platform that runs ctx and ctn in
# process and the commands of installed packages. ext embeds the ctx and ctn
# sources of the revision it is built from, so each is labelled with the latest ctx
# or ctn release tag plus the revision, or with the exact tag when HEAD is tagged.
# CTX_VERSION and CTN_VERSION override that label.
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

embedded_version() { # <product>: the version label of that product's source in this checkout
  product=$1
  tag=$(git -C "$ROOT" describe --tags --match "$product-v*" --exact-match 2>/dev/null || true)
  if [ -n "$tag" ]; then printf '%s\n' "${tag#"$product"-v}"; return; fi
  last=$(git -C "$ROOT" tag -l "$product-v*" 2>/dev/null | sort -V | tail -n 1)
  revision=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
  if [ -n "$last" ]; then printf '%s+%s\n' "${last#"$product"-v}" "$revision"; else printf 'dev+%s\n' "$revision"; fi
}
CTX_VERSION=${CTX_VERSION:-$(embedded_version ctx)}
CTN_VERSION=${CTN_VERSION:-$(embedded_version ctn)}

build_bundle() {
  os=$1
  arch=$2
  extension=$3
  staging=$(mktemp -d "$OUTPUT/.ext-release.XXXXXX")
  bundle="$staging/ext"
  mkdir -p "$bundle/bin"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X github.com/webong/ext/cmd/ext/internal/app.Version=${VERSION#v} -X github.com/webong/ext/ctx/internal/app.Version=$CTX_VERSION -X github.com/webong/ext/ctn/internal/app.Version=$CTN_VERSION" \
    -o "$bundle/bin/ext$extension" ./cmd/ext)
  cp "$ROOT/LICENSE" "$bundle/"
  if [ "$os" = windows ]; then
    (cd "$staging" && zip -qr "$OUTPUT/ext-$os-$arch.zip" ext)
  else
    tar -C "$staging" -czf "$OUTPUT/ext-$os-$arch.tar.gz" ext
  fi
  rm -rf "$staging"
}

build_bundle darwin amd64 ''
build_bundle darwin arm64 ''
build_bundle linux amd64 ''
build_bundle linux arm64 ''
build_bundle windows amd64 '.exe'
build_bundle windows arm64 '.exe'
