#ifndef EXT_PLATFORM_H
#define EXT_PLATFORM_H
/* Small portability shims shared by the engine. */
#include "ext_host.h"
#include <stddef.h>
#include <stdint.h>
#include <time.h>
#include "sync.h"

#ifdef _WIN32
#define ext_gmtime(seconds, out) ((void)gmtime_s((out), (seconds)))
#define ext_timegm _mkgmtime
#else
#define ext_gmtime(seconds, out) ((void)gmtime_r((seconds), (out)))
#define ext_timegm timegm
#endif

/* Fills out with n bytes from the operating system's random source. */
ext_status ext_random_bytes(uint8_t *out, size_t n);
#endif
