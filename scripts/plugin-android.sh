#!/usr/bin/env bash
# Build and test the Java plugin SDK: the JNI library over the guest core of the
# shared C engine, the Java classes, and the conformance guest launcher.
# Usage: scripts/plugin-android.sh [build-dir]
# Leaves <build-dir>/ext-java-guest and <build-dir>/ext-java-frames-guest, launchers the Go
# cross-language suite runs (the second is the frames guest of docs/plugin-guest-frames.md).
set -euo pipefail
cd "$(dirname "$0")/.."
command -v javac >/dev/null 2>&1 || { echo 'plugin-android.sh needs a JDK (javac)' >&2; exit 1; }
build_dir="${1:-$(mktemp -d "${TMPDIR:-/tmp}/ext-java.XXXXXX")}"
mkdir -p "$build_dir"
build_dir="$(cd "$build_dir" && pwd -P)"
sdk=pkg/plugin-android

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

# The C engine starts a guest with an empty environment, so a launcher names java by absolute path.
java_bin="$(command -v java)"
for launcher in "ext-java-guest ConformanceGuest" "ext-java-frames-guest io.github.webong.ext.plugin.FramesGuest"; do
  set -- $launcher
  cat >"$build_dir/$1" <<LAUNCHER
#!/bin/sh
exec "$java_bin" -XX:TieredStopAtLevel=1 -Xshare:auto -Djava.library.path="$build_dir/native" -cp "$build_dir/classes" $2
LAUNCHER
  chmod +x "$build_dir/$1"
  echo "wrote $build_dir/$1"
done
