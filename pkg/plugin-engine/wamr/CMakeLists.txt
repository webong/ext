# The optional WAMR reactor backend. Included by the engine's CMakeLists.txt only
# when EXT_WITH_WAMR is ON; WAMR is not vendored, so EXT_WAMR_ROOT must point at a
# checkout (see README.md for the pinned commit).
if(NOT EXT_WAMR_ROOT OR NOT EXISTS "${EXT_WAMR_ROOT}/build-scripts/runtime_lib.cmake")
  message(FATAL_ERROR "EXT_WITH_WAMR needs EXT_WAMR_ROOT to point at a WAMR checkout")
endif()

# Platform and CPU follow the toolchain; WAMR names them differently.
if(CMAKE_SYSTEM_NAME STREQUAL "Android")
  set(WAMR_BUILD_PLATFORM "android")
elseif(CMAKE_SYSTEM_NAME STREQUAL "iOS" OR CMAKE_SYSTEM_NAME STREQUAL "Darwin")
  # WAMR has one Darwin platform layer for macOS and iOS.
  set(WAMR_BUILD_PLATFORM "darwin")
else()
  set(WAMR_BUILD_PLATFORM "linux")
endif()
# The CPU comes from whichever variable the toolchain sets: the OS architecture
# list for Apple targets, the ABI for Android, the processor name otherwise.
if(CMAKE_OSX_ARCHITECTURES)
  list(GET CMAKE_OSX_ARCHITECTURES 0 ext_wamr_cpu)
elseif(ANDROID_ABI)
  set(ext_wamr_cpu "${ANDROID_ABI}")
else()
  set(ext_wamr_cpu "${CMAKE_SYSTEM_PROCESSOR}")
endif()
if(ext_wamr_cpu MATCHES "^(arm64|arm64-v8a|aarch64|ARM64)$")
  set(WAMR_BUILD_TARGET "AARCH64")
elseif(ext_wamr_cpu MATCHES "^(x86_64|AMD64|amd64)$")
  set(WAMR_BUILD_TARGET "X86_64")
else()
  message(FATAL_ERROR "EXT_WITH_WAMR: unsupported processor '${ext_wamr_cpu}'")
endif()

# An interpreter only: no JIT or ahead-of-time code, which iOS forbids. The host
# supplies the few WASI imports itself, so WAMR's own libc support stays off.
# Metering and the thread manager are what make deadlines and close interrupt a
# guest; without them a looping guest cannot be stopped.
set(WAMR_BUILD_INTERP 1)
set(WAMR_BUILD_FAST_INTERP 1)
set(WAMR_BUILD_AOT 0)
set(WAMR_BUILD_JIT 0)
set(WAMR_BUILD_FAST_JIT 0)
set(WAMR_BUILD_LIBC_BUILTIN 0)
set(WAMR_BUILD_LIBC_WASI 0)
# Current Rust and C toolchains emit reference types and bulk memory by default,
# so a reactor built with them will not load without these.
set(WAMR_BUILD_REF_TYPES 1)
set(WAMR_BUILD_BULK_MEMORY 1)
# WAMR bounds-checks guest memory with signal handlers by default. A process that
# installs its own (a JVM, so every Android app) can abort on them: the Android
# binding of this backend aborted with SIGILL at first use. Turning them off makes
# WAMR check bounds in software, which is slower but safe in such a process.
option(EXT_WAMR_DISABLE_HW_BOUND_CHECK "Check guest memory bounds in software, not with signal handlers" OFF)
if(EXT_WAMR_DISABLE_HW_BOUND_CHECK)
  set(WAMR_DISABLE_HW_BOUND_CHECK 1)
endif()
set(WAMR_BUILD_INSTRUCTION_METERING 1)
set(WAMR_BUILD_THREAD_MGR 1)
set(WAMR_ROOT_DIR "${EXT_WAMR_ROOT}")
include("${WAMR_ROOT_DIR}/build-scripts/runtime_lib.cmake")

add_library(ext_wamr_vm STATIC ${WAMR_RUNTIME_LIB_SOURCE})
target_link_libraries(ext_wamr_vm PUBLIC Threads::Threads m)
if(ANDROID)
  # WAMR's Android layer logs through liblog.
  target_link_libraries(ext_wamr_vm PUBLIC log)
endif()
add_library(ext_wamr STATIC ext_wamr.c)
target_include_directories(ext_wamr PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
target_link_libraries(ext_wamr PUBLIC ext_host_static ext_wamr_vm)
target_compile_options(ext_wamr PRIVATE -Wall -Wextra -Werror)
add_library(EXT::wamr ALIAS ext_wamr)

# EXT_WAMR_TEST_REACTOR names a reactor module (see docs/plugin-reactor-abi.md).
if(EXT_WAMR_TEST_REACTOR)
  add_executable(ext_wamr_test tests/wamr_test.c)
  target_link_libraries(ext_wamr_test PRIVATE ext_wamr Threads::Threads)
  target_compile_options(ext_wamr_test PRIVATE -Wall -Wextra -Werror -UNDEBUG)
  enable_testing()
  add_test(NAME ext_wamr_test COMMAND ext_wamr_test "${EXT_WAMR_TEST_REACTOR}")
endif()
