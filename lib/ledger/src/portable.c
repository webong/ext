/* Portable translation profile. Native collectors and process supervision
 * are unavailable; store and recorder rules are the canonical C sources. */
#include "collect.h"
#include <unistd.h>
int ext_ledger_collect(const ext_ledger_context *ctx, const char *what, char **out, size_t *out_len) { (void)ctx;(void)what;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_collect_process(const ext_ledger_context *ctx, int64_t pid, int64_t resource_limit, char **out, size_t *out_len) { (void)ctx;(void)pid;(void)resource_limit;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_recorder_scan(ext_ledger_recorder *recorder, const ext_ledger_context *ctx, const char *what) { (void)recorder;(void)ctx;(void)what; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_recorder_scan_process(ext_ledger_recorder *recorder, const ext_ledger_context *ctx, int64_t pid, int64_t resource_limit) { (void)recorder;(void)ctx;(void)pid;(void)resource_limit; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_collect_environment(char **out, size_t *out_len) { if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_open(ext_ledger_store *store, const ext_ledger_supervisor_options *options, ext_ledger_supervisor **out) { (void)store;(void)options;if (out) *out = NULL; return EXT_LEDGER_UNSUPPORTED; }
void ext_ledger_supervisor_close(ext_ledger_supervisor *sup) { (void)sup;  }
int ext_ledger_supervisor_start(ext_ledger_supervisor *sup, const ext_ledger_context *ctx, const char *spec, size_t len,
                                               const char *secrets, size_t secrets_len, char **instance, size_t *instance_len) { (void)sup;(void)ctx;(void)spec;(void)len;(void)secrets;(void)secrets_len;if (instance) *instance = NULL;if (instance_len) *instance_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_ready(ext_ledger_supervisor *sup, const char *id, int64_t pid, int ready) { (void)sup;(void)id;(void)pid;(void)ready; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_await(ext_ledger_supervisor *sup, const ext_ledger_context *ctx, const char *id, const char *states,
                                               char **out, size_t *out_len) { (void)sup;(void)ctx;(void)id;(void)states;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_stop(ext_ledger_supervisor *sup, const ext_ledger_context *ctx, const char *id) { (void)sup;(void)ctx;(void)id; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_health(ext_ledger_supervisor *sup, const char *id, int check, int *healthy) { (void)sup;(void)id;(void)check;if (healthy) *healthy = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_instance(ext_ledger_supervisor *sup, const char *id, char **out, size_t *out_len) { (void)sup;(void)id;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_instances(ext_ledger_supervisor *sup, char **out, size_t *out_len) { (void)sup;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_logs(ext_ledger_supervisor *sup, const char *id, char **out, size_t *out_len) { (void)sup;(void)id;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_orphans(ext_ledger_supervisor *sup, char **out, size_t *out_len) { (void)sup;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
int ext_ledger_supervisor_resolve_orphan(ext_ledger_supervisor *sup, const ext_ledger_context *ctx, const char *id) { (void)sup;(void)ctx;(void)id; return EXT_LEDGER_UNSUPPORTED; }
char *ll_machine_environment(void) { return NULL; }
int ll_os_exists(const char *path) { return access(path, F_OK) == 0; }
int ll_collect_shells(const ext_ledger_context *ctx, const char *search_path, lj_value **out) { (void)ctx;(void)search_path;if (out) *out = NULL; return EXT_LEDGER_UNSUPPORTED; }
int ll_collect_with_options(const ext_ledger_context *ctx, const char *what, int64_t limit, int64_t pid, int64_t resource_limit, char **out, size_t *out_len) { (void)ctx;(void)what;(void)limit;(void)pid;(void)resource_limit;if (out) *out = NULL;if (out_len) *out_len = 0; return EXT_LEDGER_UNSUPPORTED; }
