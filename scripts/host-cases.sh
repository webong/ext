#!/usr/bin/env bash
# Runs the shared host cases (lib/plugin/testdata/v1/host-cases.json) on every host that has a
# runner: the pure-Go host, the oracle (tests/crosslang TestHostCases), and the hosts that bind the
# C engine (pkg/ext-python, ext_io.host). It builds the engine's shared library and the reference
# guest twice: as a JSON-line child process and as a C ABI library.
#
# It runs on Linux, macOS and Windows. On Windows it needs a bash with cygpath (an MSYS2 shell, or
# Git Bash), CMake, and a C compiler that cgo can use for the Go-built C ABI guest (MinGW-w64 gcc).
# The engine itself is built with that compiler, or with another one named by EXT_ENGINE_CC (cl).
#
# Environment:
#   EXT_HOST_CASES_BUILD_DIR  where to build (default: a new temporary directory)
#   PYTHON                    the Python to run the Python host with (default: python3)
#   EXT_ENGINE_CC             the C compiler CMake builds the engine with (default: CMake's own
#                             choice); Go's cgo does not read it, so cl can build the engine while
#                             gcc builds the Go-built C ABI guest
set -euo pipefail
cd "$(dirname "$0")/.."
winpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
build_dir="${EXT_HOST_CASES_BUILD_DIR:-}"
if [[ -z "$build_dir" ]]; then build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ext-host-cases.XXXXXX")"; fi
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
python="${PYTHON:-python3}"

exe=""
generator=()
case "$(uname -s)" in
  Darwin) library=libext_host.dylib shared=dylib ;;
  MINGW*|MSYS*|CYGWIN*)
    # Go and Python are native programs: they get C:/ paths, which bash also accepts.
    build_dir="$(winpath "$build_dir")"
    library="" shared=dll exe=.exe
    if command -v ninja >/dev/null 2>&1; then generator=(-G Ninja); fi ;;
  *) library=libext_host.so shared=so ;;
esac
compiler=()
if [[ -n "${EXT_ENGINE_CC:-}" ]]; then compiler=("-DCMAKE_C_COMPILER=$EXT_ENGINE_CC"); fi

# quietly runs a build step without its output unless the step fails; Ninja prints compiler
# errors on stdout, so discarding stdout would hide them.
quietly() {
  local log="$build_dir/step.log"
  if ! "$@" >"$log" 2>&1; then cat "$log" >&2; return 1; fi
}

# stage_runtime copies the MinGW runtime DLLs a library imports next to it. Windows loads a
# library's dependencies from its own directory and the system directories, never from PATH, as
# the Go loader (LoadLibraryExW) and Python's ctypes both ask. Without ldd (Git Bash) there is
# nothing to copy: an MSVC build needs no runtime of its own.
stage_runtime() {
  command -v ldd >/dev/null 2>&1 || return 0
  local dir dependency
  dir="$(dirname "$1")"
  { ldd "$1" || true; } | awk '$3 ~ /^\/(mingw|ucrt|clang)[a-z0-9]*\/bin\// { print $3 }' |
    while read -r dependency; do [[ -e "$dir/${dependency##*/}" ]] || cp "$dependency" "$dir/"; done
}

# ${array[@]+...} so an empty array is not an unbound variable in the bash 3.2 macOS ships.
quietly cmake -S lib/plugin -B "$build_dir/engine" ${generator[@]+"${generator[@]}"} ${compiler[@]+"${compiler[@]}"} -DCMAKE_BUILD_TYPE=Release -DEXT_BUILD_SHARED=ON
quietly cmake --build "$build_dir/engine" --target ext_host_shared -j 4
go build -o "$build_dir/go-guest$exe" ./tests/crosslang/testdata/go-guest
go build -buildmode=c-shared -o "$build_dir/go-shared.$shared" ./tests/crosslang/testdata/go-shared

if [[ -z "$library" ]]; then
  # CMake names the DLL libext_host.dll with MinGW and ext_host.dll with MSVC, and a multi-config
  # generator puts it under Release/.
  for candidate in libext_host.dll ext_host.dll Release/ext_host.dll; do
    if [[ -f "$build_dir/engine/$candidate" ]]; then library="$candidate"; break; fi
  done
  if [[ -z "$library" ]]; then
    echo "the engine's shared library was not built:" >&2
    find "$build_dir/engine" -name '*.dll' >&2
    exit 1
  fi
  stage_runtime "$build_dir/engine/$library"
  stage_runtime "$build_dir/go-shared.$shared"
fi

export EXT_GO_GUEST="$build_dir/go-guest$exe" EXT_GO_SHARED="$build_dir/go-shared.$shared"
export EXT_HOST_LIBRARY="$build_dir/engine/$library" EXT_HOST_REQUIRED=1 EXT_CROSSLANG_REQUIRED=1

echo "== the Go host (the oracle)"
go test -count=1 -run 'TestHostCases$' ./tests/crosslang
echo "== the Python host ($("$python" --version 2>&1))"
"$python" -I -m unittest discover -s pkg/ext-python/tests -p 'test_host.py' -v
