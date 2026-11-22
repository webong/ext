#!/usr/bin/env bash
# Holds the cgo-free C ABI loader (pkg/ext-go/cengine/cguest on purego) to the same cases as the
# cgo loader: the cshared tests and the shared host cases (lib/plugin/testdata/v1/host-cases.json),
# both with CGO_ENABLED=0. The guests are built with cgo, since -buildmode=c-shared needs it; only
# the host is cgo-free. It also vets the loader's build tags on every platform it names.
#
# Runs on Linux, macOS and Windows (a bash with a C compiler cgo can use, such as MinGW-w64 gcc).
#
# Environment:
#   EXT_PUREGO_BUILD_DIR  where to build (default: a new temporary directory)
set -euo pipefail
cd "$(dirname "$0")/.."
winpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
build_dir="${EXT_PUREGO_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-purego.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(winpath "$(cd "$build_dir" && pwd -P)")"
exe=""
case "$(uname -s)" in
  Darwin) shared=dylib ;;
  MINGW*|MSYS*|CYGWIN*) shared=dll exe=.exe ;;
  *) shared=so ;;
esac

echo "== the loader's build tags (CGO_ENABLED=0)"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 freebsd/amd64 linux/386 linux/riscv64 windows/386; do
  GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 go vet ./pkg/ext-go/core/... ./pkg/ext-go/cengine/... ./pkg/ext-go/cguest/...
done

CGO_ENABLED=1 go build -o "$build_dir/go-guest$exe" ./tests/crosslang/testdata/go-guest
CGO_ENABLED=1 go build -buildmode=c-shared -o "$build_dir/go-shared.$shared" ./tests/crosslang/testdata/go-shared

export EXT_CSHARED_GUEST="$build_dir/go-shared.$shared" EXT_CSHARED_REQUIRED=1
export EXT_GO_GUEST="$build_dir/go-guest$exe" EXT_GO_SHARED="$build_dir/go-shared.$shared" EXT_CROSSLANG_REQUIRED=1
echo "== cshared without cgo (purego)"
(cd pkg/ext-go/core && CGO_ENABLED=0 go test -count=1 -v ./...) && (cd pkg/ext-go/cengine && CGO_ENABLED=0 go test -count=1 -v ./...) && (cd pkg/ext-go/cguest && CGO_ENABLED=0 go test -count=1 -v ./...)
echo "== the shared host cases on the Go host without cgo (purego)"
CGO_ENABLED=0 go test -count=1 -run 'TestHostCases$' ./tests/crosslang
echo "== cshared with cgo, on the same guest"
(cd pkg/ext-go/core && CGO_ENABLED=1 go test -count=1 ./...) && (cd pkg/ext-go/cengine && CGO_ENABLED=1 go test -count=1 ./...) && (cd pkg/ext-go/cguest && CGO_ENABLED=1 go test -count=1 ./...)
echo "cgo-free C ABI loader passed"
