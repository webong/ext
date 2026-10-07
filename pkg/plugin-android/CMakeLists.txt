cmake_minimum_required(VERSION 3.18)
project(ext_plugin_java C)

# JNI binding over the guest half of the shared C engine. The same file builds on
# a desktop JDK and, unchanged, with the Android NDK toolchain file.
set(EXT_BUILD_HOST OFF CACHE BOOL "The JNI binding needs only the guest core" FORCE)
add_subdirectory(../plugin-engine plugin-engine EXCLUDE_FROM_ALL)

add_library(extjni SHARED src/main/native/ext_jni.c)
target_link_libraries(extjni PRIVATE ext_guest_static)
set_target_properties(extjni PROPERTIES C_STANDARD 11 POSITION_INDEPENDENT_CODE ON)
if(NOT ANDROID)
  find_package(JNI REQUIRED)
  target_include_directories(extjni PRIVATE ${JNI_INCLUDE_DIRS})
endif()
target_compile_options(extjni PRIVATE -Wall -Wextra -Werror)
