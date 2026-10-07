#!/usr/bin/env bash
# Verify that installed headers/targets work outside the engine build tree.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${EXT_CENGINE_BUILD_DIR:?set EXT_CENGINE_BUILD_DIR to the native C runner directory}"
build_dir="$(cd "$EXT_CENGINE_BUILD_DIR" && pwd -P)"
cmake --install "$build_dir/release" --prefix "$build_dir/install"
mkdir -p "$build_dir/consumer"
cat > "$build_dir/consumer/CMakeLists.txt" <<'CMAKE'
cmake_minimum_required(VERSION 3.18)
project(ext_installed_consumer LANGUAGES C)
find_package(ext_host 0.2.0 EXACT CONFIG REQUIRED)
foreach(binding IN ITEMS guest guest_shared host host_shared)
  if(TARGET EXT::${binding})
    add_executable(check_${binding} "${EXT_GUEST_CHECK}")
    target_link_libraries(check_${binding} PRIVATE EXT::${binding})
    target_compile_options(check_${binding} PRIVATE -UNDEBUG)
  endif()
endforeach()
CMAKE
cmake -S "$build_dir/consumer" -B "$build_dir/consumer-build" \
  -DCMAKE_PREFIX_PATH="$build_dir/install" -DEXT_GUEST_CHECK="$PWD/pkg/plugin-engine/tests/guest_core.c"
cmake --build "$build_dir/consumer-build" -j 4
for binding in guest guest_shared host host_shared; do
  if [[ -x "$build_dir/consumer-build/check_$binding" ]]; then "$build_dir/consumer-build/check_$binding"; fi
done
echo "Verified installed SDK: $build_dir/install"
