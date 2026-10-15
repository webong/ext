#define _POSIX_C_SOURCE 200809L
#include "ext_instance.h"
#include "sha256.h"
#include "wire.h"
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct instance instance;
struct instance {
  instance *next;
  uint8_t key[256], revision[256], digest[32];
  size_t key_len, revision_len;
  void *value;
  int published;
  size_t leases;
  enum { PENDING, LIVE, RETIRED, DISPOSING } state;
};
struct ext_instances {
  pthread_mutex_t mu;
  pthread_cond_t changed;
  pthread_t worker;
  ext_instance_options options;
  ext_cancel *life;
  instance *entries;
  size_t active;
  int closed;
  ext_status cleanup;
};
struct ext_lease {
  ext_instances *manager;
  instance *entry;
};
static int64_t now(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (int64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}
static int equal(const instance *e, const uint8_t *key, size_t len) {
  return e->key_len == len && !memcmp(e->key, key, len);
}
static instance *lookup(ext_instances *m, const uint8_t *key, size_t len,
                        int pending) {
  for (instance *e = m->entries; e; e = e->next)
    if (e->state == (pending ? PENDING : LIVE) && equal(e, key, len))
      return e;
  return NULL;
}
static int text(const uint8_t *p, size_t n) { return p && n && n <= 256; }
static int call(const ext_call_options *o) {
  return o && o->struct_size == sizeof(*o) && o->timeout_ms;
}
static ext_status stopped(const ext_call_options *o, int64_t end) {
  return ext_cancel_is_signaled(o->cancel) ? EXT_CANCELED
         : now() >= end                    ? EXT_TIMEOUT
                                           : EXT_OK;
}
static void observe(ext_instances *m, const uint8_t *key, size_t k,
                    const uint8_t *rev, size_t r, const char *state) {
  if (m->options.observe)
    m->options.observe(m->options.user, key, k, rev, r, state);
}
/* Caller has claimed DISPOSING under lock; no new lease can see the entry. */
static ext_status dispose(ext_instances *m, instance *e) {
  ext_status s =
      e->value ? m->options.dispose(m->options.user, e->value) : EXT_OK;
  if (e->published)
    observe(m, e->key, e->key_len, e->revision, e->revision_len, "disposed");
  pthread_mutex_lock(&m->mu);
  if (!m->cleanup && s)
    m->cleanup = s;
  instance **p = &m->entries;
  while (*p != e)
    p = &(*p)->next;
  *p = e->next;
  m->active--;
  pthread_cond_broadcast(&m->changed);
  pthread_mutex_unlock(&m->mu);
  free(e);
  return s;
}
static void *cleanup_worker(void *arg) {
  ext_instances *m = arg;
  pthread_mutex_lock(&m->mu);
  for (;;) {
    if (m->closed && !m->active)
      break;
    instance *e = NULL;
    if (m->closed)
      for (e = m->entries; e; e = e->next)
        if (e->state == RETIRED && !e->leases)
          break;
    if (!e) {
      pthread_cond_wait(&m->changed, &m->mu);
      continue;
    }
    e->state = DISPOSING;
    pthread_mutex_unlock(&m->mu);
    dispose(m, e);
    pthread_mutex_lock(&m->mu);
  }
  pthread_mutex_unlock(&m->mu);
  return NULL;
}
ext_status ext_instances_create(const ext_instance_options *o,
                                ext_instances **out) {
  if (!out)
    return EXT_INVALID;
  *out = NULL;
  if (!o || o->struct_size != sizeof(*o) || !o->capacity ||
      o->capacity > 4096 || !o->validate || !o->create || !o->dispose)
    return EXT_INVALID;
  ext_instances *m = calloc(1, sizeof(*m));
  if (!m)
    return EXT_NOMEM;
  m->options = *o;
  ext_status s = ext_cancel_create(&m->life);
  if (s) {
    free(m);
    return s;
  }
  if (pthread_mutex_init(&m->mu, NULL)) {
    ext_cancel_destroy(m->life);
    free(m);
    return EXT_IO;
  }
  if (pthread_cond_init(&m->changed, NULL)) {
    pthread_mutex_destroy(&m->mu);
    ext_cancel_destroy(m->life);
    free(m);
    return EXT_IO;
  }
  if (pthread_create(&m->worker, NULL, cleanup_worker, m)) {
    pthread_cond_destroy(&m->changed);
    pthread_mutex_destroy(&m->mu);
    ext_cancel_destroy(m->life);
    free(m);
    return EXT_IO;
  }
  *out = m;
  return EXT_OK;
}
ext_status ext_instances_configure(ext_instances *m, const uint8_t *key,
                                   size_t key_len, const uint8_t *revision,
                                   size_t revision_len, const uint8_t *config,
                                   size_t config_len,
                                   const ext_call_options *o) {
  if (!m || !text(key, key_len) || !text(revision, revision_len) || !call(o))
    return EXT_INVALID;
  int64_t end = now() + o->timeout_ms;
  ext_status s = stopped(o, end);
  if (s)
    return s;
  yyjson_doc *doc = ext_parse(config, config_len);
  if (!doc)
    return EXT_INVALID;
  yyjson_doc_free(doc);
  uint8_t *copy = malloc(config_len);
  instance *e = calloc(1, sizeof(*e));
  if (!copy || !e) {
    free(copy);
    free(e);
    return EXT_NOMEM;
  }
  memcpy(copy, config, config_len);
  memcpy(e->key, key, key_len);
  memcpy(e->revision, revision, revision_len);
  e->key_len = key_len;
  e->revision_len = revision_len;
  ext_engine_sha256(copy, config_len, e->digest);
  s = m->options.validate(m->options.user, copy, config_len);
  if (s)
    goto reject;
  if ((s = stopped(o, end)))
    goto reject;
  pthread_mutex_lock(&m->mu);
  instance *old = lookup(m, key, key_len, 0);
  if (m->closed)
    s = EXT_CLOSED;
  else if (lookup(m, key, key_len, 1))
    s = EXT_UPDATING;
  else if (old && old->revision_len == revision_len &&
           !memcmp(old->revision, revision, revision_len))
    s = memcmp(old->digest, e->digest, 32) ? EXT_MISMATCH : EXT_OK;
  else if (m->active >= m->options.capacity)
    s = EXT_CAPACITY;
  else {
    e->next = m->entries;
    m->entries = e;
    m->active++;
    pthread_mutex_unlock(&m->mu);
    ext_call_options bounded = *o;
    bounded.timeout_ms = (uint32_t)(end - now());
    if ((s = stopped(o, end)) == EXT_OK)
      s = m->options.create(m->options.user, &bounded, m->life, e->key,
                            e->key_len, copy, config_len, &e->value);
    free(copy);
    copy = NULL;
    if (!s)
      s = stopped(o, end);
    pthread_mutex_lock(&m->mu);
    if (m->closed && !s)
      s = EXT_CLOSED;
    if (s) {
      e->state = DISPOSING;
      pthread_mutex_unlock(&m->mu);
      ext_status cleanup = dispose(m, e);
      return s ? s : cleanup;
    }
    old = lookup(m, e->key, e->key_len, 0);
    uint8_t old_revision[256];
    size_t old_len = 0;
    int cleanup_old = 0;
    if (old) {
      old_len = old->revision_len;
      memcpy(old_revision, old->revision, old_len);
      old->state = RETIRED;
      if (!old->leases) {
        old->state = DISPOSING;
        cleanup_old = 1;
      }
    }
    e->state = LIVE;
    e->published = 1;
    /* Keep the new entry alive while its observer receives borrowed metadata.
     */
    e->leases++;
    pthread_mutex_unlock(&m->mu);
    observe(m, e->key, e->key_len, e->revision, e->revision_len, "configured");
    if (old)
      observe(m, e->key, e->key_len, old_revision, old_len, "retired");
    if (cleanup_old)
      s = dispose(m, old);
    pthread_mutex_lock(&m->mu);
    e->leases--;
    int cleanup_new = e->state == RETIRED && !e->leases;
    if (cleanup_new)
      e->state = DISPOSING;
    pthread_mutex_unlock(&m->mu);
    if (cleanup_new) {
      ext_status cleanup = dispose(m, e);
      if (!s)
        s = cleanup;
    }
    return s;
  }
  pthread_mutex_unlock(&m->mu);
reject:
  free(copy);
  free(e);
  return s;
}
ext_status ext_instances_acquire(ext_instances *m, const uint8_t *key,
                                 size_t len, ext_lease **out) {
  if (!out)
    return EXT_INVALID;
  *out = NULL;
  if (!m || !key)
    return EXT_INVALID;
  ext_lease *l = malloc(sizeof(*l));
  if (!l)
    return EXT_NOMEM;
  pthread_mutex_lock(&m->mu);
  instance *e = lookup(m, key, len, 0);
  ext_status s = m->closed ? EXT_CLOSED : !e ? EXT_NOT_FOUND : EXT_OK;
  if (!s) {
    e->leases++;
    l->manager = m;
    l->entry = e;
    *out = l;
  }
  pthread_mutex_unlock(&m->mu);
  if (s)
    free(l);
  return s;
}
void *ext_lease_value(const ext_lease *l) { return l ? l->entry->value : NULL; }
const uint8_t *ext_lease_revision(const ext_lease *l, size_t *len) {
  if (!l || !len)
    return NULL;
  *len = l->entry->revision_len;
  return l->entry->revision;
}
ext_status ext_lease_release(ext_lease *l) {
  if (!l)
    return EXT_INVALID;
  ext_instances *m = l->manager;
  instance *e = l->entry;
  pthread_mutex_lock(&m->mu);
  e->leases--;
  int cleanup = e->state == RETIRED && !e->leases;
  if (cleanup)
    e->state = DISPOSING;
  pthread_mutex_unlock(&m->mu);
  free(l);
  return cleanup ? dispose(m, e) : EXT_OK;
}
ext_status ext_instances_remove(ext_instances *m, const uint8_t *key,
                                size_t len) {
  if (!m || !key)
    return EXT_INVALID;
  pthread_mutex_lock(&m->mu);
  instance *e = lookup(m, key, len, 0);
  ext_status s = m->closed                ? EXT_CLOSED
                 : lookup(m, key, len, 1) ? EXT_UPDATING
                 : !e                     ? EXT_NOT_FOUND
                                          : EXT_OK;
  int cleanup = 0;
  if (!s) {
    e->state = RETIRED;
    if (!e->leases) {
      e->state = DISPOSING;
      cleanup = 1;
    }
  }
  pthread_mutex_unlock(&m->mu);
  return cleanup ? dispose(m, e) : s;
}
ext_status ext_instances_close(ext_instances *m, const ext_call_options *o) {
  if (!m || !call(o))
    return EXT_INVALID;
  int64_t end = now() + o->timeout_ms;
  pthread_mutex_lock(&m->mu);
  if (!m->closed) {
    m->closed = 1;
    ext_cancel_signal(m->life);
    for (instance *e = m->entries; e; e = e->next)
      if (e->state == LIVE)
        e->state = RETIRED;
    pthread_cond_broadcast(&m->changed);
  }
  ext_status s = EXT_OK;
  while (m->active) {
    if ((s = stopped(o, end)))
      break;
    /* Short real-time slices preserve cancellation and monotonic total timeout.
     */
    struct timespec t;
    clock_gettime(CLOCK_REALTIME, &t);
    t.tv_nsec += 10000000;
    if (t.tv_nsec >= 1000000000) {
      t.tv_sec++;
      t.tv_nsec -= 1000000000;
    }
    pthread_cond_timedwait(&m->changed, &m->mu, &t);
  }
  if (!s)
    s = m->cleanup;
  pthread_mutex_unlock(&m->mu);
  return s;
}
ext_status ext_instances_destroy(ext_instances *m) {
  if (!m)
    return EXT_INVALID;
  pthread_mutex_lock(&m->mu);
  int busy = !m->closed || m->active;
  pthread_mutex_unlock(&m->mu);
  if (busy)
    return EXT_DRAINING;
  pthread_join(m->worker, NULL);
  pthread_cond_destroy(&m->changed);
  pthread_mutex_destroy(&m->mu);
  ext_cancel_destroy(m->life);
  free(m);
  return EXT_OK;
}
