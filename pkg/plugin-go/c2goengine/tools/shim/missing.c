/* Functions the host profile needs that ccgo's libc does not define. */
#ifdef __APPLE__
#include <errno.h>
#include <pthread.h>
#include <time.h>

int pthread_condattr_init(pthread_condattr_t *a) { (void)a; return 0; }
int pthread_condattr_destroy(pthread_condattr_t *a) { (void)a; return 0; }
/* Waits on the real-time clock, as the dropped monotonic attribute makes every wait do. */
int pthread_cond_timedwait_relative_np(pthread_cond_t *c, pthread_mutex_t *m, const struct timespec *rel) {
  struct timespec abs;
  clock_gettime(CLOCK_REALTIME, &abs);
  abs.tv_sec += rel->tv_sec;
  abs.tv_nsec += rel->tv_nsec;
  if (abs.tv_nsec >= 1000000000L) { abs.tv_sec++; abs.tv_nsec -= 1000000000L; }
  return pthread_cond_timedwait(c, m, &abs);
}
#endif
