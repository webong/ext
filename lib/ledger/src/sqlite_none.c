/* The SQLite backend when the library is built without EXT_LEDGER_SQLITE. */
#include "ledger_internal.h"

int ll_sqlite_open(const ext_ledger_context *ctx, const char *path, ll_state *state, ll_backend **out) {
  (void)ctx;
  (void)path;
  (void)state;
  (void)out;
  return EXT_LEDGER_UNSUPPORTED;
}

const char *const *ll_sqlite_layout(void) { return NULL; }
