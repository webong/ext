#!/usr/bin/env bash
# Translates the C engine (lib/plugin) to Go with ccgo for the platform it runs on, tests the
# translation with the binding beside it (c2goengine with -tags ext_c2go, held to the differential
# checks), and stages the c2go driver's archive for an ext-plugin-vX.Y.Z release:
# dist/ext-plugin-X.Y.Z-c2go-<goos>-<goarch>.tar.gz (a go/ directory: the translation and the
# binding, named for the platform) with its SHA-256. It publishes nothing. Without a version it
# translates and tests only, as the c2goengine workflow does.
#
# Usage: scripts/c2goengine-translate.sh [version]
# EXT_C2GO_RACE=-race tests under the race detector. On Windows, run it from an MSYS2 shell with a C
# compiler (MINGW64, or CLANGARM64 with CC=clang).
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
version="${1:-}"
if [[ -n "$version" ]]; then
  declared="$(sed -n 's/^project(ext_plugin VERSION \([0-9.]*\) .*/\1/p' lib/plugin/CMakeLists.txt)"
  [[ "$version" == "$declared" ]] || { echo "version $version is not lib/plugin's $declared" >&2; exit 1; }
fi
goos="$(go env GOOS)" goarch="$(go env GOARCH)"
name="ext-plugin-$version-c2go-$goos-$goarch"
stage="$(mktemp -d "${TMPDIR:-/tmp}/ext-c2go.XXXXXX")"

echo "== translate lib/plugin for $goos/$goarch"
publish=()
if [[ -n "$version" ]]; then publish=(-publish "$stage/$name/go"); fi
(cd pkg/ext-go/c2goengine/tools && GOWORK=off go run . "${publish[@]}")

echo "== the translation with its binding, held to the differential checks"
# shellcheck disable=SC2086 # an optional flag
(cd pkg/ext-go/c2goengine && go test -tags ext_c2go -count=1 ${EXT_C2GO_RACE:-} ./...)

if [[ -z "$version" ]]; then
  echo "translated and tested $goos/$goarch (no version: nothing staged)"
  exit 0
fi
cp LICENSE "$stage/$name/LICENSE"
mkdir -p dist
# The archive is named relatively: GNU tar reads "C:/..." as a remote host.
(cd dist && tar -C "$stage" -czf "$name.tar.gz" "$name")
(cd dist && { sha256sum "$name.tar.gz" 2>/dev/null || shasum -a 256 "$name.tar.gz"; } >"$name.sha256")
echo "staged dist/$name.tar.gz"
