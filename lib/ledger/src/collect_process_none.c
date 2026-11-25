/* Process collection where this library has no collector yet (the BSDs). */
#if !defined(__linux__) && !defined(__APPLE__) && !defined(_WIN32)
#include "collect.h"

int ll_processes(const ext_ledger_context *ctx, int64_t limit, lj_value *processes, lj_value **enumeration) {
  (void)ctx;
  (void)limit;
  (void)processes;
  *enumeration = NULL;
  return EXT_LEDGER_UNSUPPORTED;
}

int ll_process_one(const ext_ledger_context *ctx, int64_t pid, lj_value *processes) {
  (void)ctx;
  (void)pid;
  (void)processes;
  return EXT_LEDGER_UNSUPPORTED;
}

int ll_process_resources(const ext_ledger_context *ctx, int64_t pid, int64_t limit, lj_value *process) {
  (void)ctx;
  (void)pid;
  (void)limit;
  (void)process;
  return EXT_LEDGER_UNSUPPORTED;
}

int ll_process_start(int64_t pid, char *out, size_t n) {
  (void)pid;
  if (n) out[0] = 0;
  return EXT_LEDGER_UNSUPPORTED;
}

int ll_process_host_id(char out[25]) {
  (void)out;
  return EXT_LEDGER_UNSUPPORTED;
}
#endif
