cmake_minimum_required(VERSION 3.18)
project(ext_plugin_java C)

# JNI binding over the guest half of the shared C engine. The same file builds on
# a desktop JDK and, unchanged, with the Android NDK toolchain file.
#
# EXT_WAMR_ROOT (a WAMR checkout, see scripts/plugin-wamr-source.sh) also builds
# libextwamrjni: the host half of the engine with the WAMR reactor backend, a separate
# library so the default guest SDK carries neither the host nor an interpreter.
set(EXT_WAMR_ROOT "" CACHE PATH "A WAMR checkout; builds the optional extwamrjni host")
if(EXT_WAMR_ROOT)
  # WAMR's hardware bound checks install signal handlers and probe the native stack, which the
  # JVM and ART own (the JVM aborts with SIGILL at first use), so check in software here.
  set(EXT_WAMR_DISABLE_HW_BOUND_CHECK ON CACHE BOOL "Software bound checks inside a JVM or ART" FORCE)
  set(EXT_BUILD_HOST ON CACHE BOOL "The WAMR host needs the host half" FORCE)
  set(EXT_WITH_WAMR ON CACHE BOOL "The WAMR reactor backend" FORCE)
else()
  set(EXT_BUILD_HOST OFF CACHE BOOL "The JNI binding needs only the guest core" FORCE)
endif()
add_subdirectory(../plugin-engine plugin-engine EXCLUDE_FROM_ALL)

add_library(extjni SHARED src/main/native/ext_jni.c)
target_link_libraries(extjni PRIVATE ext_guest_static)
set_target_properties(extjni PROPERTIES C_STANDARD 11 POSITION_INDEPENDENT_CODE ON)
if(NOT ANDROID)
  find_package(JNI REQUIRED)
  target_include_directories(extjni PRIVATE ${JNI_INCLUDE_DIRS})
endif()
target_compile_options(extjni PRIVATE -Wall -Wextra -Werror)

if(EXT_WAMR_ROOT)
  add_library(extwamrjni SHARED src/main/native/ext_wamr_jni.c)
  target_link_libraries(extwamrjni PRIVATE ext_wamr)
  set_target_properties(extwamrjni PROPERTIES C_STANDARD 11 POSITION_INDEPENDENT_CODE ON
    C_VISIBILITY_PRESET hidden)
  if(NOT ANDROID)
    target_include_directories(extwamrjni PRIVATE ${JNI_INCLUDE_DIRS})
  endif()
  target_compile_options(extwamrjni PRIVATE -Wall -Wextra -Werror)
endif()
