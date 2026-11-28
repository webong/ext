#include "volume.h"

ll_volume_limits ll_volume_default_limits(void) {
  return (ll_volume_limits){10000, UINT64_C(1) << 30, 32};
}
int ll_volume_create(const char *path, ll_volume_limits limits, ll_volume **out) {
  (void)path; (void)limits;
  if (out) *out = 0;
  return LL_VOLUME_UNSUPPORTED;
}
int ll_volume_inspect(const char *path, ll_volume_limits limits, ll_volume **out) {
  (void)path; (void)limits;
  if (out) *out = 0;
  return LL_VOLUME_UNSUPPORTED;
}
int ll_volume_open(const char *path, uint32_t entries, uint64_t bytes, uint32_t depth,
                   int create, ll_volume **out) {
  (void)path; (void)entries; (void)bytes; (void)depth; (void)create;
  if (out) *out = 0;
  return LL_VOLUME_UNSUPPORTED;
}
int ll_volume_open_cancellable(const char *path, uint32_t entries, uint64_t bytes,
                   uint32_t depth, int create, int (*canceled)(void *),
                   void *context, ll_volume **out) {
  (void)path; (void)entries; (void)bytes; (void)depth; (void)create;
  (void)canceled; (void)context;
  if (out) *out = 0;
  return LL_VOLUME_UNSUPPORTED;
}
int ll_volume_info_get(const ll_volume *volume, ll_volume_info *out) {
  (void)volume; (void)out; return LL_VOLUME_UNSUPPORTED;
}
int ll_volume_remove(ll_volume *volume, int (*canceled)(void *), void *context) {
  (void)volume; (void)canceled; (void)context; return LL_VOLUME_UNSUPPORTED;
}
void ll_volume_close(ll_volume *volume) { (void)volume; }
