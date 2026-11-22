/* Not part of miniz. Upstream generates miniz_export.h with CMake to mark its
 * public functions for a shared library. The engine compiles the decoder into
 * one of its own translation units (src/archive.c), which renames every
 * function of it, so the mark is empty. */
#ifndef MINIZ_EXPORT
#define MINIZ_EXPORT
#endif
