# cmake -S pkg/plugin-engine -B build/wasi -DEXT_BUILD_HOST=OFF \
#   -DCMAKE_TOOLCHAIN_FILE="$PWD/pkg/plugin-engine/cmake/wasi-zig.cmake" \
#   -DEXT_ZIG=/absolute/path/to/zig
set(CMAKE_SYSTEM_NAME Generic)
set(CMAKE_SYSTEM_PROCESSOR wasm32)
set(EXT_ZIG "" CACHE FILEPATH "Zig 0.17.0 executable")
if(NOT EXT_ZIG)
  message(FATAL_ERROR "Set EXT_ZIG to the pinned Zig executable")
endif()
set(CMAKE_TRY_COMPILE_PLATFORM_VARIABLES EXT_ZIG)
set(CMAKE_C_COMPILER "${EXT_ZIG}")
set(CMAKE_C_COMPILER_ARG1 cc)
set(CMAKE_C_FLAGS_INIT "-target wasm32-wasi")
set(CMAKE_TRY_COMPILE_TARGET_TYPE STATIC_LIBRARY)
# Native ar/ranlib (notably Apple's) cannot index WebAssembly object files.
set(CMAKE_C_ARCHIVE_CREATE "\"${EXT_ZIG}\" ar qc <TARGET> <LINK_FLAGS> <OBJECTS>")
set(CMAKE_C_ARCHIVE_FINISH "\"${EXT_ZIG}\" ranlib <TARGET>")
