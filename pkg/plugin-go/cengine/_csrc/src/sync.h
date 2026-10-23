#ifndef EXT_SYNC_H
#define EXT_SYNC_H
/* Threads, mutexes, condition variables and clocks. Everywhere but MSVC this is
 * <pthread.h>; MSVC has no pthreads, so the small subset the engine uses is
 * provided here over Win32 (SRW locks, condition variables, _beginthreadex). */
#if !defined(_MSC_VER)
#include <pthread.h>
#include <time.h>
#else
#define WIN32_LEAN_AND_MEAN
#include <errno.h>
#include <process.h>
#include <stdint.h>
#include <stdlib.h>
#include <time.h>
#include <windows.h>

typedef struct {
  HANDLE handle;
} pthread_t;
typedef struct {
  SRWLOCK lock;
} pthread_mutex_t;
#define PTHREAD_MUTEX_INITIALIZER {SRWLOCK_INIT}
typedef struct {
  CONDITION_VARIABLE variable;
} pthread_cond_t;

typedef struct {
  int unused;
} pthread_condattr_t;
static inline int pthread_condattr_init(pthread_condattr_t *a) {
  (void)a;
  return 0;
}
static inline int pthread_condattr_destroy(pthread_condattr_t *a) {
  (void)a;
  return 0;
}

#ifndef CLOCK_REALTIME
#define CLOCK_REALTIME 0
#define CLOCK_MONOTONIC 1
#endif

typedef struct {
  void *(*function)(void *);
  void *argument;
} ext_thread_start;

static unsigned __stdcall ext_thread_main(void *raw) {
  ext_thread_start start = *(ext_thread_start *)raw;
  free(raw);
  start.function(start.argument);
  return 0;
}
static inline int pthread_create(pthread_t *thread, const void *attributes,
                                 void *(*function)(void *), void *argument) {
  (void)attributes;
  ext_thread_start *start = malloc(sizeof(*start));
  if (!start)
    return ENOMEM;
  start->function = function;
  start->argument = argument;
  uintptr_t handle = _beginthreadex(NULL, 0, ext_thread_main, start, 0, NULL);
  if (!handle) {
    free(start);
    return EAGAIN;
  }
  thread->handle = (HANDLE)handle;
  return 0;
}
static inline int pthread_join(pthread_t thread, void **result) {
  if (result)
    *result = NULL;
  WaitForSingleObject(thread.handle, INFINITE);
  CloseHandle(thread.handle);
  return 0;
}
static inline int pthread_mutex_init(pthread_mutex_t *m, const void *attributes) {
  (void)attributes;
  InitializeSRWLock(&m->lock);
  return 0;
}
static inline int pthread_mutex_destroy(pthread_mutex_t *m) {
  (void)m;
  return 0;
}
static inline int pthread_mutex_lock(pthread_mutex_t *m) {
  AcquireSRWLockExclusive(&m->lock);
  return 0;
}
static inline int pthread_mutex_unlock(pthread_mutex_t *m) {
  ReleaseSRWLockExclusive(&m->lock);
  return 0;
}
static inline int pthread_cond_init(pthread_cond_t *c, const pthread_condattr_t *attributes) {
  (void)attributes;
  InitializeConditionVariable(&c->variable);
  return 0;
}
static inline int pthread_cond_destroy(pthread_cond_t *c) {
  (void)c;
  return 0;
}
static inline int pthread_cond_broadcast(pthread_cond_t *c) {
  WakeAllConditionVariable(&c->variable);
  return 0;
}
static inline int pthread_cond_signal(pthread_cond_t *c) {
  WakeConditionVariable(&c->variable);
  return 0;
}
static inline int pthread_cond_wait(pthread_cond_t *c, pthread_mutex_t *m) {
  SleepConditionVariableSRW(&c->variable, &m->lock, INFINITE, 0);
  return 0;
}

/* Wall clock as a timespec, from the precise system time. */
static inline int ext_clock_realtime(struct timespec *t) {
  FILETIME ft;
  GetSystemTimePreciseAsFileTime(&ft);
  uint64_t ticks = ((uint64_t)ft.dwHighDateTime << 32) | ft.dwLowDateTime;
  ticks -= 116444736000000000ull; /* 1601 to 1970, in 100 ns */
  t->tv_sec = (time_t)(ticks / 10000000ull);
  t->tv_nsec = (long)((ticks % 10000000ull) * 100ull);
  return 0;
}
static inline int clock_gettime(int clock, struct timespec *t) {
  if (clock == CLOCK_REALTIME)
    return ext_clock_realtime(t);
  LARGE_INTEGER frequency, count;
  QueryPerformanceFrequency(&frequency);
  QueryPerformanceCounter(&count);
  t->tv_sec = (time_t)(count.QuadPart / frequency.QuadPart);
  t->tv_nsec = (long)((count.QuadPart % frequency.QuadPart) * 1000000000ll /
                      frequency.QuadPart);
  return 0;
}
/* The deadline is on the real-time clock, as the engine computes it on Windows. */
static inline int pthread_cond_timedwait(pthread_cond_t *c, pthread_mutex_t *m,
                                         const struct timespec *deadline) {
  struct timespec now;
  ext_clock_realtime(&now);
  int64_t ms = ((int64_t)deadline->tv_sec - (int64_t)now.tv_sec) * 1000 +
               ((int64_t)deadline->tv_nsec - (int64_t)now.tv_nsec) / 1000000;
  if (ms < 0)
    ms = 0;
  if (ms > 0x7ffffff0)
    ms = 0x7ffffff0;
  if (!SleepConditionVariableSRW(&c->variable, &m->lock, (DWORD)ms, 0) &&
      GetLastError() == ERROR_TIMEOUT)
    return ETIMEDOUT;
  return 0;
}
static inline int nanosleep(const struct timespec *request, struct timespec *rest) {
  (void)rest;
  Sleep((DWORD)(request->tv_sec * 1000 + request->tv_nsec / 1000000));
  return 0;
}
#endif
#endif
