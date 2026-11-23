/* The ext.system recorder's internals: its environment, and the helpers its
 * observations share to build records and talk to the store. */
#ifndef EXT_LEDGER_RECORDER_H
#define EXT_LEDGER_RECORDER_H

#include "ledger_internal.h"

#define REC_NS "ext.system"

typedef struct {
  ll_time now;
  char *hostname, *os, *arch, *wd, *host_env;
  int64_t ppid;
  lj_value *variables; /* object of strings, or NULL */
  ll_map paths;        /* cleaned path -> (void*)1 */
  int machine;         /* read the clock and the filesystem live instead of the fixed values */
} rec_env;

struct ext_ledger_recorder {
  ext_ledger_store *store;
  rec_env env;
};

typedef ext_ledger_recorder rec;

/* ---- the environment ---- */
const char *rec_getenv(const rec *r, const char *key); /* "" when unset */
int rec_exists(const rec *r, const char *path);
void rec_now_text(const rec *r, lj_buf *b); /* RFC 3339 with nanoseconds, UTC, unquoted */

/* ---- values ---- */
void rec_digest(const char *p, size_t n, char out[25]); /* the first 12 bytes of SHA-256, as hex */
int rec_context_name(const char *s, size_t n);          /* ^[A-Za-z0-9][A-Za-z0-9._ +-]{0,127}$ */
char *rec_itoa(int64_t v, char buf[24]);

/* POSIX path rules, as Go's path/filepath applies them on POSIX; every result is malloc'd. */
char *rec_clean(const char *p);
char *rec_dir(const char *p);
char *rec_base(const char *p);
char *rec_join(const char *a, const char *b);
int rec_is_abs(const char *p);
char *rec_redact_source(const char *source); /* without URL credentials */

/* ---- building records ----
 * Each returns NULL when memory runs out; a NULL argument where a value is
 * taken makes the result NULL too, so a chain of builders checks once. */
lj_value *rec_obj(void);
lj_value *rec_str(const char *s);
lj_value *rec_strn(const char *s, size_t n);
lj_value *rec_num(double v);
lj_value *rec_bool(int v);
int rec_set(lj_value *o, const char *key, lj_value *v); /* takes v; non-zero on failure */
lj_value *rec_vertex(const rec *r, const char *id, const char *kind, lj_value *attrs, const char *operation);
lj_value *rec_edge(const char *id, const char *from, const char *to, const char *type);
const char *rec_field(const lj_value *o, const char *key); /* a string field, "" when absent or not a string */

/* ---- the store ---- */
int rec_apply(rec *r, const ext_ledger_context *ctx, lj_value *tx); /* takes tx */
int rec_get(rec *r, const ext_ledger_context *ctx, int edge, const char *id, lj_value **out);
int rec_query_edges(rec *r, const ext_ledger_context *ctx, const char *type, const char *from, int64_t limit, lj_value **out);
int rec_snapshot(rec *r, const ext_ledger_context *ctx, lj_value **out);
int rec_equal_attributes(const lj_value *a, const lj_value *b); /* ignoring observed_at */

/* ---- the observations ---- */
int rec_observe_shell(rec *r, const ext_ledger_context *ctx, const lj_value *in);
int rec_observe_web(rec *r, const ext_ledger_context *ctx, const lj_value *in);
int rec_observe_host(rec *r, const ext_ledger_context *ctx, const lj_value *in);
int rec_read_host(rec *r, const ext_ledger_context *ctx, char **out, size_t *out_len);
int rec_observe_processes(rec *r, const ext_ledger_context *ctx, const lj_value *in);
int rec_observe_inventory(rec *r, const ext_ledger_context *ctx, const lj_value *in);
int rec_resolve_inventory(rec *r, const ext_ledger_context *ctx, const lj_value *in, char **out, size_t *out_len);

#endif
