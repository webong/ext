/* Included before every source of the host profile. The libc that ccgo links the output against
   does not implement condition-variable attributes (pthread_cond_init with a non-NULL attribute is
   a TODO there), so the attribute is dropped: a wait then follows the real-time clock instead of
   the monotonic one the C engine asks for. */
#include <pthread.h>
#define pthread_cond_init(c, a) pthread_cond_init((c), 0)
