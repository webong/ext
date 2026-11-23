#ifndef EXT_PLUGIN_INSTANCE_H
#define EXT_PLUGIN_INSTANCE_H
#include "ext_plugin_host.h"
#ifdef __cplusplus
extern "C" {
#endif
typedef struct ext_plugin_instances ext_plugin_instances;
typedef struct ext_plugin_lease ext_plugin_lease;
typedef struct {
  uint32_t struct_size;
  uint32_t
      capacity; /* 1..4096, including pending factories and retired values */
  void *user;
  ext_plugin_status (*validate)(void *, const uint8_t *, size_t);
  /* Configuration bytes and key are immutable borrowed copies. life is signaled
   * on manager close. Native callbacks must cooperate with both call and life.
   * Set value when ownership transfers, including failure requiring cleanup. */
  ext_plugin_status (*create)(void *, const ext_plugin_call_options *, const ext_plugin_cancel *life,
                       const uint8_t *key, size_t, const uint8_t *config,
                       size_t, void **value);
  ext_plugin_status (*dispose)(void *, void *value);
  /* Optional metadata only; borrowed key/revision, outside the manager lock.
   * Callbacks must not unwind or reenter this manager. */
  void (*observe)(void *, const uint8_t *key, size_t, const uint8_t *revision,
                  size_t, const char *state);
} ext_plugin_instance_options;
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_instances_create(const ext_plugin_instance_options *,
                                             ext_plugin_instances **);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_instances_configure(ext_plugin_instances *,
                                                const uint8_t *key, size_t,
                                                const uint8_t *revision, size_t,
                                                const uint8_t *config, size_t,
                                                const ext_plugin_call_options *);
/* A lease holds a value across replacements. Value and revision remain borrowed
 * until release. Exactly one release per C lease; bindings may add idempotence.
 */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_instances_acquire(ext_plugin_instances *,
                                              const uint8_t *key, size_t,
                                              ext_plugin_lease **);
EXT_PLUGIN_HOST_API void *ext_plugin_lease_value(const ext_plugin_lease *);
EXT_PLUGIN_HOST_API const uint8_t *ext_plugin_lease_revision(const ext_plugin_lease *, size_t *);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_lease_release(ext_plugin_lease *);
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_instances_remove(ext_plugin_instances *,
                                             const uint8_t *key, size_t);
/* Close stops admission permanently. Timed-out close can be retried. Leased
 * values are never destroyed. Disposals run outside locks; close starts a
 * background cleanup worker so a callback cannot overrun the caller deadline.
 */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_instances_close(ext_plugin_instances *,
                                            const ext_plugin_call_options *);
/* Only after successful close (or completed close with cleanup error), all
 * operations joined and all leases released. Returns EXT_PLUGIN_DRAINING if still
 * active. On success consumes the handle. */
EXT_PLUGIN_HOST_API ext_plugin_status ext_plugin_instances_destroy(ext_plugin_instances *);
#ifdef __cplusplus
}
#endif
#endif
