#ifndef EXT_INSTANCE_H
#define EXT_INSTANCE_H
#include "ext_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ext_instances ext_instances;
typedef struct ext_lease ext_lease;
typedef struct {
  uint32_t struct_size;
  uint32_t
      capacity; /* 1..4096, including pending factories and retired values */
  void *user;
  ext_status (*validate)(void *, const uint8_t *, size_t);
  /* Configuration bytes and key are immutable borrowed copies. life is signaled
   * on manager close. Native callbacks must cooperate with both call and life.
   * Set value when ownership transfers, including failure requiring cleanup. */
  ext_status (*create)(void *, const ext_call_options *, const ext_cancel *life,
                       const uint8_t *key, size_t, const uint8_t *config,
                       size_t, void **value);
  ext_status (*dispose)(void *, void *value);
  /* Optional metadata only; borrowed key/revision, outside the manager lock.
   * Callbacks must not unwind or reenter this manager. */
  void (*observe)(void *, const uint8_t *key, size_t, const uint8_t *revision,
                  size_t, const char *state);
} ext_instance_options;
EXT_HOST_API ext_status ext_instances_create(const ext_instance_options *,
                                             ext_instances **);
EXT_HOST_API ext_status ext_instances_configure(ext_instances *,
                                                const uint8_t *key, size_t,
                                                const uint8_t *revision, size_t,
                                                const uint8_t *config, size_t,
                                                const ext_call_options *);
/* A lease holds a value across replacements. Value and revision remain borrowed
 * until release. Exactly one release per C lease; bindings may add idempotence.
 */
EXT_HOST_API ext_status ext_instances_acquire(ext_instances *,
                                              const uint8_t *key, size_t,
                                              ext_lease **);
EXT_HOST_API void *ext_lease_value(const ext_lease *);
EXT_HOST_API const uint8_t *ext_lease_revision(const ext_lease *, size_t *);
EXT_HOST_API ext_status ext_lease_release(ext_lease *);
EXT_HOST_API ext_status ext_instances_remove(ext_instances *,
                                             const uint8_t *key, size_t);
/* Close stops admission permanently. Timed-out close can be retried. Leased
 * values are never destroyed. Disposals run outside locks; close starts a
 * background cleanup worker so a callback cannot overrun the caller deadline.
 */
EXT_HOST_API ext_status ext_instances_close(ext_instances *,
                                            const ext_call_options *);
/* Only after successful close (or completed close with cleanup error), all
 * operations joined and all leases released. Returns EXT_DRAINING if still
 * active. On success consumes the handle. */
EXT_HOST_API ext_status ext_instances_destroy(ext_instances *);
#ifdef __cplusplus
}
#endif
#endif
