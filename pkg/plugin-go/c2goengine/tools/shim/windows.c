/* What ccgo's libc for Windows lacks and the MinGW headers expand to: fpclassify becomes
   __fpclassify and winpthreads' inline clock_gettime calls clock_gettime64. Defined here, in one
   source, because the force-included header is part of every translation unit. */
#include <math.h>
#include <sys/timeb.h>
#include <time.h>

int __fpclassify(double x) {
  union {
    double d;
    unsigned long long u;
  } v = {x};
  unsigned long long exponent = (v.u >> 52) & 0x7ff, mantissa = v.u & 0xfffffffffffffULL;
  if (exponent == 0x7ff)
    return mantissa ? FP_NAN : FP_INFINITE;
  if (exponent == 0)
    return mantissa ? FP_SUBNORMAL : FP_ZERO;
  return FP_NORMAL;
}

/* The wall clock comes from _ftime64 (milliseconds, which ccgo's libc has), the monotonic one from
   the performance counter. */
int QueryPerformanceCounter(long long *count);
int QueryPerformanceFrequency(long long *frequency);
int clock_gettime64(clockid_t clock, struct _timespec64 *t) {
  if (clock == CLOCK_REALTIME) {
    struct __timeb64 now;
    _ftime64(&now);
    t->tv_sec = now.time;
    t->tv_nsec = (long)now.millitm * 1000000L;
    return 0;
  }
  long long frequency = 0, count = 0;
  QueryPerformanceFrequency(&frequency);
  QueryPerformanceCounter(&count);
  t->tv_sec = count / frequency;
  t->tv_nsec = (long)((count % frequency) * 1000000000LL / frequency);
  return 0;
}
