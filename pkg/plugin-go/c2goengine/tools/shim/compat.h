/* Included before every source of the host profile. ccgo's libc has two gaps in condition
   variables, and the engine meets both:

   - pthread_cond_init with a non-NULL attribute is a TODO there, so the attribute that makes a wait
     run on the monotonic clock is dropped (host.c asks for it on Linux);
   - a timed wait takes its absolute deadline on the real-time clock, while the engine's deadline is
     on the monotonic one wherever it asked for that attribute.

   ext_cond_timedwait (shim/missing.c) takes either kind of deadline. */
#include <pthread.h>
#include <time.h>
#define pthread_cond_init(c, a) pthread_cond_init((c), 0)
int ext_cond_timedwait(pthread_cond_t *, pthread_mutex_t *, const struct timespec *);
#define pthread_cond_timedwait(c, m, t) ext_cond_timedwait((c), (m), (t))
