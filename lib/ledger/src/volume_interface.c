/* Bounded private-directory lifecycle through the ledger library interface.
 * The caller supplies the path and authorizes removal; the native backend
 * performs all filesystem traversal and same-file checks. */
#include "ext_ledger.h"
#include "json.h"
#include "ledger_internal.h"
#include "recorder.h"
#include "volume.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *field(const lj_value *input, const char *name) {
  const lj_value *value = lj_get(input, name);
  return value && value->type == LJ_STRING &&
         strlen(value->u.string.ptr) == value->u.string.len ? value->u.string.ptr : NULL;
}

static int bounded(const lj_value *input, const char *name, double maximum, uint64_t *out) {
  const lj_value *value = lj_get(input, name);
  *out = 0;
  if (!value) return 0;
  if (value->type != LJ_NUMBER || !isfinite(value->u.number) ||
      value->u.number < 0 || value->u.number > maximum ||
      floor(value->u.number) != value->u.number) return EXT_LEDGER_INVALID;
  *out = (uint64_t)value->u.number;
  return 0;
}

static int limits_of(const lj_value *input, ll_volume_limits *limits) {
  uint64_t entries, bytes, depth;
  if (bounded(input, "max_entries", 1000000, &entries) ||
      bounded(input, "max_bytes", (double)(UINT64_C(1) << 40), &bytes) ||
      bounded(input, "max_depth", 128, &depth)) return EXT_LEDGER_INVALID;
  *limits = (ll_volume_limits){(uint32_t)entries, bytes, (uint32_t)depth};
  return 0;
}

static int ledger_code(int code, const ext_ledger_context *ctx) {
  switch (code) {
    case LL_VOLUME_OK: return EXT_LEDGER_OK;
    case LL_VOLUME_INVALID: return EXT_LEDGER_INVALID;
    case LL_VOLUME_UNSAFE: return EXT_LEDGER_DENIED;
    case LL_VOLUME_STALE: return EXT_LEDGER_CHANGED;
    case LL_VOLUME_LIMIT: return EXT_LEDGER_TOO_LARGE;
    case LL_VOLUME_NOT_FOUND: return EXT_LEDGER_NOT_FOUND;
    case LL_VOLUME_UNSUPPORTED: return EXT_LEDGER_UNSUPPORTED;
    case LL_VOLUME_CANCELED: {
      int checked = ll_ctx_check(ctx);
      return checked ? checked : EXT_LEDGER_CANCELED;
    }
    default: return EXT_LEDGER_IO;
  }
}

static int canceled(void *context) {
  return ll_ctx_check((const ext_ledger_context *)context) != 0;
}

static int report(const char *path, const ll_volume_info *info, char **out, size_t *out_len) {
  char identity[40];
  snprintf(identity, sizeof identity, "%016llx:%016llx",
           (unsigned long long)info->device, (unsigned long long)info->inode);
  lj_value *value = rec_obj();
  if (!value) return EXT_LEDGER_IO;
  int bad = rec_set(value, "path", rec_str(path)) |
            rec_set(value, "identity", rec_str(identity)) |
            rec_set(value, "created", rec_bool(info->created)) |
            rec_set(value, "entries", rec_num(info->entries)) |
            rec_set(value, "bytes", rec_num((double)info->bytes));
  if (bad) { lj_free(value); return EXT_LEDGER_IO; }
  lj_buf buffer = {0};
  lj_write(&buffer, value);
  lj_free(value);
  *out = lj_buf_take(&buffer, out_len);
  return *out ? EXT_LEDGER_OK : EXT_LEDGER_IO;
}

int ll_volume_operation(const ext_ledger_context *ctx, const char *operation,
                        const lj_value *input, char **out, size_t *out_len) {
  if (!operation || !input || input->type != LJ_OBJECT || !out || !out_len)
    return EXT_LEDGER_INVALID;
  int checked = ll_ctx_check(ctx);
  if (checked) return checked;
  const char *path = field(input, "path");
  if (!path || !*path) return EXT_LEDGER_INVALID;
  ll_volume_limits limits;
  if (limits_of(input, &limits)) return EXT_LEDGER_INVALID;
  int create = strcmp(operation, "volume_create") == 0;
  int remove = strcmp(operation, "volume_remove") == 0;
  if (!create && !remove && strcmp(operation, "volume_inspect")) return EXT_LEDGER_UNSUPPORTED;
  const char *expected = remove ? field(input, "identity") : NULL;
  if (remove && (!expected || strlen(expected) != 33 || expected[16] != ':'))
    return EXT_LEDGER_INVALID;
  ll_volume *volume = NULL;
  int code = ll_volume_open_cancellable(path, limits.max_entries, limits.max_bytes,
                                       limits.max_depth, create, canceled, (void *)ctx, &volume);
  if (code) return ledger_code(code, ctx);
  ll_volume_info info;
  code = ll_volume_info_get(volume, &info);
  if (code) { ll_volume_close(volume); return ledger_code(code, ctx); }
  if (remove) {
    char identity[40];
    snprintf(identity, sizeof identity, "%016llx:%016llx",
             (unsigned long long)info.device, (unsigned long long)info.inode);
    if (strcmp(expected, identity)) { ll_volume_close(volume); return EXT_LEDGER_CHANGED; }
    code = ll_volume_remove(volume, canceled, (void *)ctx);
    ll_volume_close(volume);
    if (code) return ledger_code(code, ctx);
    *out = malloc(5);
    if (!*out) return EXT_LEDGER_IO;
    memcpy(*out, "null", 5);
    *out_len = 4;
    return EXT_LEDGER_OK;
  }
  code = report(path, &info, out, out_len);
  ll_volume_close(volume);
  return code;
}
