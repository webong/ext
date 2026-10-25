/* Functions the host profile needs that ccgo's libc does not define or does not do as the engine
   expects. See compat.h. */
#include <errno.h>
#include <pthread.h>
#include <time.h>

/* A timed wait that takes a deadline on the real-time clock or on the monotonic one. The engine's
   monotonic deadlines count from boot, a few billion seconds at most; a real-time deadline is
   past 10^9 seconds since 1970 since 2001. */
int ext_cond_timedwait(pthread_cond_t *c, pthread_mutex_t *m, const struct timespec *t) {
  struct timespec real = *t;
  if (t->tv_sec < 1000000000) {
    struct timespec mono, now;
    clock_gettime(CLOCK_MONOTONIC, &mono);
    clock_gettime(CLOCK_REALTIME, &now);
    long long ns = ((long long)t->tv_sec - mono.tv_sec) * 1000000000LL + ((long long)t->tv_nsec - mono.tv_nsec);
    if (ns < 0)
      ns = 0;
    long long total = (long long)now.tv_nsec + ns;
    real.tv_sec = now.tv_sec + (time_t)(total / 1000000000LL);
    real.tv_nsec = (long)(total % 1000000000LL);
  }
  return (pthread_cond_timedwait)(c, m, &real);
}

#ifdef __APPLE__
int pthread_condattr_init(pthread_condattr_t *a) { (void)a; return 0; }
int pthread_condattr_destroy(pthread_condattr_t *a) { (void)a; return 0; }
/* macOS waits for a duration: add it to the real-time clock. */
int pthread_cond_timedwait_relative_np(pthread_cond_t *c, pthread_mutex_t *m, const struct timespec *rel) {
  struct timespec abs;
  clock_gettime(CLOCK_REALTIME, &abs);
  abs.tv_sec += rel->tv_sec;
  abs.tv_nsec += rel->tv_nsec;
  if (abs.tv_nsec >= 1000000000L) {
    abs.tv_sec++;
    abs.tv_nsec -= 1000000000L;
  }
  return (pthread_cond_timedwait)(c, m, &abs);
}
#endif
