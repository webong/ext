# cmake -S pkg/plugin/cengine -B build/wasi -DCTX_BUILD_HOST=OFF \
#   -DCMAKE_TOOLCHAIN_FILE="$PWD/pkg/plugin/cengine/cmake/wasi-zig.cmake" \
#   -DCTX_ZIG=/absolute/path/to/zig
set(CMAKE_SYSTEM_NAME Generic)
set(CMAKE_SYSTEM_PROCESSOR wasm32)
set(CTX_ZIG "" CACHE FILEPATH "Zig 0.17.0 executable")
if(NOT CTX_ZIG)
  message(FATAL_ERROR "Set CTX_ZIG to the pinned Zig executable")
endif()
set(CMAKE_TRY_COMPILE_PLATFORM_VARIABLES CTX_ZIG)
set(CMAKE_C_COMPILER "${CTX_ZIG}")
set(CMAKE_C_COMPILER_ARG1 cc)
set(CMAKE_C_FLAGS_INIT "-target wasm32-wasi")
set(CMAKE_TRY_COMPILE_TARGET_TYPE STATIC_LIBRARY)
# Native ar/ranlib (notably Apple's) cannot index WebAssembly object files.
set(CMAKE_C_ARCHIVE_CREATE "\"${CTX_ZIG}\" ar qc <TARGET> <LINK_FLAGS> <OBJECTS>")
set(CMAKE_C_ARCHIVE_FINISH "\"${CTX_ZIG}\" ranlib <TARGET>")
