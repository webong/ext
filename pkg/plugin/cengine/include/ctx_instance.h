#ifndef CTX_INSTANCE_H
#define CTX_INSTANCE_H
#include "ctx_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ctx_instances ctx_instances;
typedef struct ctx_lease ctx_lease;
typedef struct {
  uint32_t struct_size;
  uint32_t
      capacity; /* 1..4096, including pending factories and retired values */
  void *user;
  ctx_status (*validate)(void *, const uint8_t *, size_t);
  /* Configuration bytes and key are immutable borrowed copies. life is signaled
   * on manager close. Native callbacks must cooperate with both call and life.
   * Set value when ownership transfers, including failure requiring cleanup. */
  ctx_status (*create)(void *, const ctx_call_options *, const ctx_cancel *life,
                       const uint8_t *key, size_t, const uint8_t *config,
                       size_t, void **value);
  ctx_status (*dispose)(void *, void *value);
  /* Optional metadata only; borrowed key/revision, outside the manager lock.
   * Callbacks must not unwind or reenter this manager. */
  void (*observe)(void *, const uint8_t *key, size_t, const uint8_t *revision,
                  size_t, const char *state);
} ctx_instance_options;
CTX_HOST_API ctx_status ctx_instances_create(const ctx_instance_options *,
                                             ctx_instances **);
CTX_HOST_API ctx_status ctx_instances_configure(ctx_instances *,
                                                const uint8_t *key, size_t,
                                                const uint8_t *revision, size_t,
                                                const uint8_t *config, size_t,
                                                const ctx_call_options *);
/* A lease holds a value across replacements. Value and revision remain borrowed
 * until release. Exactly one release per C lease; bindings may add idempotence.
 */
CTX_HOST_API ctx_status ctx_instances_acquire(ctx_instances *,
                                              const uint8_t *key, size_t,
                                              ctx_lease **);
CTX_HOST_API void *ctx_lease_value(const ctx_lease *);
CTX_HOST_API const uint8_t *ctx_lease_revision(const ctx_lease *, size_t *);
CTX_HOST_API ctx_status ctx_lease_release(ctx_lease *);
CTX_HOST_API ctx_status ctx_instances_remove(ctx_instances *,
                                             const uint8_t *key, size_t);
/* Close stops admission permanently. Timed-out close can be retried. Leased
 * values are never destroyed. Disposals run outside locks; close starts a
 * background cleanup worker so a callback cannot overrun the caller deadline.
 */
CTX_HOST_API ctx_status ctx_instances_close(ctx_instances *,
                                            const ctx_call_options *);
/* Only after successful close (or completed close with cleanup error), all
 * operations joined and all leases released. Returns CTX_DRAINING if still
 * active. On success consumes the handle. */
CTX_HOST_API ctx_status ctx_instances_destroy(ctx_instances *);
#ifdef __cplusplus
}
#endif
#endif
