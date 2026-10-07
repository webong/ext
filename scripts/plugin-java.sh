#!/usr/bin/env bash
# Build and test the Java plugin SDK: the JNI library over the guest core of the
# shared C engine, the Java classes, and the conformance guest launcher.
# Usage: scripts/plugin-java.sh [build-dir]
# Leaves <build-dir>/ext-java-guest, a launcher the Go cross-language suite runs.
set -euo pipefail
cd "$(dirname "$0")/.."
command -v javac >/dev/null 2>&1 || { echo 'plugin-java.sh needs a JDK (javac)' >&2; exit 1; }
build_dir="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-java.XXXXXX")}"
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
sdk=pkg/plugin-java

cmake -S "$sdk" -B "$build_dir/native" -DCMAKE_BUILD_TYPE=Release >/dev/null
cmake --build "$build_dir/native" --target extjni >/dev/null
case "$(uname -s)" in Darwin) library=libextjni.dylib ;; *) library=libextjni.so ;; esac
test -f "$build_dir/native/$library"

mkdir -p "$build_dir/classes"
javac -Xlint:all -Werror -d "$build_dir/classes" \
  "$sdk"/src/main/java/io/github/webong/ext/plugin/*.java \
  "$sdk"/src/test/java/io/github/webong/ext/plugin/*.java \
  "$sdk"/examples/ConformanceGuest.java
java -ea -Djava.library.path="$build_dir/native" -cp "$build_dir/classes" \
  io.github.webong.ext.plugin.GuestTests

cat >"$build_dir/ext-java-guest" <<LAUNCHER
#!/bin/sh
exec java -XX:TieredStopAtLevel=1 -Xshare:auto -Djava.library.path="$build_dir/native" -cp "$build_dir/classes" ConformanceGuest
LAUNCHER
chmod +x "$build_dir/ext-java-guest"
echo "wrote $build_dir/ext-java-guest"
