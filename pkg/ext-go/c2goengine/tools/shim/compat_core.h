/* Included before every source of every profile. Some compilers' system headers (MinGW's among them)
   mention 128-bit integers wherever the compiler says it has them, and ccgo cannot type-check them;
   saying it has none is enough for every header that guards its use. -U on the command line is not
   honored on every platform, a #undef after the compiler's own definitions is.
   MinGW is the exception: its _mingw.h declares __int128 itself when the macro is missing, and that
   declaration is the one ccgo cannot type-check, so there the macro stays (the engine never uses
   the type). */
#ifdef _WIN32
#ifndef __SIZEOF_INT128__
#define __SIZEOF_INT128__ 16
#endif
#else
#undef __SIZEOF_INT128__
#endif
