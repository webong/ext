/* lib/ledger as an ext plugin: the C plugin ABI (lib/plugin/include/ext_plugin.h,
 * ext.plugin/v1) over the public API, so any ext host loads the shared library on
 * demand and calls it by operation name with no binding of its own.
 *
 * The ABI is a contract, not code: its four exports, constants and JSON frames
 * are written here, and nothing of lib/plugin is linked.
 *
 * One contract, ext.graph v1. Objects a call opens (stores, recorders,
 * supervisors) are handles of the session that opened it, named in the payload
 * ("store": "s1"), and closed with the session. */
#include "process.h"
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#define LP_EXPORT __declspec(dllexport)
#define LP_CALL __cdecl
#else
#define LP_EXPORT __attribute__((visibility("default")))
#define LP_CALL
#endif

#define LP_ABI_VERSION 1u
#define LP_HANDSHAKE 1u
#define LP_INVOKE 2u
#define LP_OK 0u
#define LP_INVALID 1u
#define LP_CLOSED 2u
#define LP_FAILED 3u
#define LP_API "ext.plugin/v1"
#define LP_CONTRACT "ext.graph"
#define LP_CONTRACT_VERSION "v1"
#ifndef EXT_LEDGER_REVISION
#define EXT_LEDGER_REVISION "dev"
#endif

/* ---- operations ---- */

typedef struct lp_session lp_session;
typedef int (*lp_op)(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out);

typedef struct {
  const char *name;
  lp_op run;
} lp_operation;

static const lp_operation *find_operation(const char *name);
static void write_descriptor(lj_buf *b);

/* ---- sessions and their handles ---- */

enum { H_STORE = 1, H_RECORDER, H_SUPERVISOR };

typedef struct {
  char name[24];
  int kind;
  void *ptr;
  char store[24]; /* the store a recorder or supervisor is over */
} lp_handle;

struct lp_session {
  uint64_t id;
  int handshaken;
  lp_handle *handles;
  size_t nhandles;
  uint64_t next;
};

static ll_mutex *sessions_mu;
static lp_session **sessions;
static size_t nsessions;
static uint64_t next_session;

/* The table's lock, made once even when the first calls race. */
static ll_mutex *table_lock(void) {
#if defined(_MSC_VER) && !defined(__clang__)
  ll_mutex *m = (ll_mutex *)_InterlockedCompareExchangePointer((void *volatile *)&sessions_mu, NULL, NULL);
  if (m) return m;
  ll_mutex *fresh = ll_mutex_new();
  if (!fresh) return NULL;
  ll_mutex *was = (ll_mutex *)_InterlockedCompareExchangePointer((void *volatile *)&sessions_mu, fresh, NULL);
  if (was) {
    ll_mutex_free(fresh);
    return was;
  }
  return fresh;
#else
  ll_mutex *m = __atomic_load_n(&sessions_mu, __ATOMIC_ACQUIRE);
  if (m) return m;
  ll_mutex *fresh = ll_mutex_new(), *expected = NULL;
  if (!fresh) return NULL;
  if (__atomic_compare_exchange_n(&sessions_mu, &expected, fresh, 0, __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE)) return fresh;
  ll_mutex_free(fresh); /* another call made it first */
  return expected;
#endif
}

static lp_session *session_get(uint64_t id) {
  ll_mutex *mu = table_lock();
  if (!mu) return NULL;
  ll_mutex_lock(mu);
  lp_session *found = NULL;
  for (size_t i = 0; i < nsessions && !found; i++)
    if (sessions[i]->id == id) found = sessions[i];
  ll_mutex_unlock(mu);
  return found;
}

static lp_handle *handle_get(lp_session *s, const lj_value *in, const char *key, int kind) {
  const lj_value *v = lj_get(in, key);
  if (!v || v->type != LJ_STRING) return NULL;
  for (size_t i = 0; i < s->nhandles; i++)
    if (s->handles[i].kind == kind && strcmp(s->handles[i].name, v->u.string.ptr) == 0) return &s->handles[i];
  return NULL;
}

static lp_handle *handle_add(lp_session *s, int kind, void *ptr, const char *store) {
  lp_handle *grown = realloc(s->handles, (s->nhandles + 1) * sizeof *grown);
  if (!grown) return NULL;
  s->handles = grown;
  lp_handle *h = &s->handles[s->nhandles++];
  memset(h, 0, sizeof *h);
  const char prefix = kind == H_STORE ? 's' : kind == H_RECORDER ? 'r' : 'p';
  snprintf(h->name, sizeof h->name, "%c%llu", prefix, (unsigned long long)++s->next);
  h->kind = kind;
  h->ptr = ptr;
  if (store) snprintf(h->store, sizeof h->store, "%s", store);
  return h;
}

static void handle_close(lp_handle *h) {
  if (h->kind == H_SUPERVISOR) ext_ledger_supervisor_close(h->ptr);
  else if (h->kind == H_RECORDER) ext_ledger_recorder_close(h->ptr);
  else if (h->kind == H_STORE) ext_ledger_close(h->ptr);
}

static void handle_remove(lp_session *s, lp_handle *h) {
  handle_close(h);
  size_t i = (size_t)(h - s->handles);
  memmove(&s->handles[i], &s->handles[i + 1], (s->nhandles - i - 1) * sizeof *s->handles);
  s->nhandles--;
}

/* Closes what depends on a store before the store: supervisors, recorders, stores. */
static void session_free(lp_session *s) {
  for (int kind = H_SUPERVISOR; kind >= H_STORE; kind--)
    for (size_t i = 0; i < s->nhandles; i++)
      if (s->handles[i].kind == kind) handle_close(&s->handles[i]);
  free(s->handles);
  free(s);
}

/* ---- helpers ---- */

static const char *str_in(const lj_value *in, const char *key) {
  const lj_value *v = lj_get(in, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : NULL;
}

static int64_t int_in(const lj_value *in, const char *key, int64_t fallback) {
  const lj_value *v = lj_get(in, key);
  return v && v->type == LJ_NUMBER ? (int64_t)v->u.number : fallback;
}

/* A JSON member of the payload written back as text; NULL with *len 0 when absent. */
static char *text_of(const lj_value *in, const char *key, size_t *len) {
  *len = 0;
  const lj_value *v = lj_get(in, key);
  if (!v || v->type == LJ_NULL) return NULL;
  lj_buf b = {0};
  lj_write(&b, v);
  return lj_buf_take(&b, len);
}

/* Appends what the C API returned, or {} for nothing, and frees it. */
static int put_json(lj_buf *out, int rc, char *text, size_t len) {
  if (!rc) {
    if (text && len) lj_buf_put(out, text, len);
    else lj_buf_puts(out, "{}");
  }
  ext_ledger_free(text);
  return rc;
}

static int put_handle(lj_buf *out, const char *key, const char *name) {
  lj_buf_puts(out, "{\"");
  lj_buf_puts(out, key);
  lj_buf_puts(out, "\":");
  lj_write_string(out, name, strlen(name));
  lj_buf_putc(out, '}');
  return 0;
}

static void read_options(const lj_value *in, ext_ledger_options *o) {
  memset(o, 0, sizeof *o);
  const lj_value *v = lj_get(in, "options");
  if (!v || v->type != LJ_OBJECT) return;
  o->max_changes = int_in(v, "max_changes", 0);
  o->max_change_bytes = int_in(v, "max_change_bytes", 0);
  o->max_transaction_records = int_in(v, "max_transaction_records", 0);
  o->max_record_bytes = int_in(v, "max_record_bytes", 0);
  o->max_results = int_in(v, "max_results", 0);
  o->max_snapshot_records = int_in(v, "max_snapshot_records", 0);
}

/* ---- the store ---- */

/* {"path"} a file store, {"path","reader":true} a reader, {"sqlite"} a SQLite
 * database, {"memory":true} the session's own state; "options" the bounds. */
static int op_store_open(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  ext_ledger_options o;
  read_options(in, &o);
  const char *path = str_in(in, "path"), *sqlite = str_in(in, "sqlite");
  const lj_value *memory = lj_get(in, "memory"), *reader = lj_get(in, "reader");
  int is_reader = reader && reader->type == LJ_BOOL && reader->u.boolean;
  ext_ledger *store = NULL;
  int rc;
  if (path && *path) rc = is_reader ? ext_ledger_open_reader(ctx, path, &o, &store) : ext_ledger_open(ctx, path, &o, &store);
  else if (sqlite && *sqlite) rc = ext_ledger_open_sqlite(ctx, sqlite, &o, &store);
  else if (memory && memory->type == LJ_BOOL && memory->u.boolean) rc = ext_ledger_open_memory(&o, &store);
  else return EXT_LEDGER_INVALID;
  if (rc) return rc;
  lp_handle *h = handle_add(s, H_STORE, store, NULL);
  if (!h) {
    ext_ledger_close(store);
    return EXT_LEDGER_IO;
  }
  return put_handle(out, "store", h->name);
}

static int op_close(lp_session *s, const lj_value *in, lj_buf *out, const char *key, int kind) {
  lp_handle *h = handle_get(s, in, key, kind);
  if (!h) return EXT_LEDGER_NOT_FOUND;
  if (kind == H_STORE)
    for (size_t i = 0; i < s->nhandles; i++)
      if (s->handles[i].kind != H_STORE && strcmp(s->handles[i].store, h->name) == 0) return EXT_LEDGER_BUSY; /* close its users first */
  handle_remove(s, h);
  lj_buf_puts(out, "{}");
  return 0;
}

static int op_store_close(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  return op_close(s, in, out, "store", H_STORE);
}

#define STORE_OR_FAIL()                                    \
  lp_handle *h = handle_get(s, in, "store", H_STORE);      \
  if (!h) return EXT_LEDGER_NOT_FOUND;                     \
  ext_ledger *store = h->ptr

static int op_store_register(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  STORE_OR_FAIL();
  size_t len;
  char *schema = text_of(in, "schema", &len);
  if (!schema) return EXT_LEDGER_INVALID;
  int rc = ext_ledger_register(store, ctx, schema, len);
  free(schema);
  return put_json(out, rc, NULL, 0);
}

static int op_store_apply(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  STORE_OR_FAIL();
  size_t len, clen = 0;
  char *tx = text_of(in, "transaction", &len), *commit = NULL;
  if (!tx) return EXT_LEDGER_INVALID;
  int rc = ext_ledger_apply(store, ctx, tx, len, &commit, &clen);
  free(tx);
  return put_json(out, rc, commit, clen);
}

static int get_record(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out, int edge) {
  STORE_OR_FAIL();
  const char *ns = str_in(in, "namespace"), *id = str_in(in, "id");
  if (!ns || !id) return EXT_LEDGER_INVALID;
  char *text = NULL;
  size_t len = 0;
  int rc = edge ? ext_ledger_get_edge(store, ctx, ns, id, &text, &len) : ext_ledger_get_vertex(store, ctx, ns, id, &text, &len);
  return put_json(out, rc, text, len);
}

static int op_store_get_vertex(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) { return get_record(s, ctx, in, out, 0); }
static int op_store_get_edge(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) { return get_record(s, ctx, in, out, 1); }

static int query(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out, int edges) {
  STORE_OR_FAIL();
  size_t len, rlen = 0;
  char *q = text_of(in, "query", &len), *text = NULL;
  if (!q) return EXT_LEDGER_INVALID;
  int rc = edges ? ext_ledger_query_edges(store, ctx, q, len, &text, &rlen) : ext_ledger_query_vertices(store, ctx, q, len, &text, &rlen);
  free(q);
  return put_json(out, rc, text, rlen);
}

static int op_store_query_vertices(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) { return query(s, ctx, in, out, 0); }
static int op_store_query_edges(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) { return query(s, ctx, in, out, 1); }

static int op_store_traverse(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  STORE_OR_FAIL();
  const char *ns = str_in(in, "namespace"), *start = str_in(in, "start");
  if (!ns || !start) return EXT_LEDGER_INVALID;
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_traverse(store, ctx, ns, start, int_in(in, "max_depth", 0), int_in(in, "max_vertices", 0), &text, &len);
  return put_json(out, rc, text, len);
}

static int op_store_snapshot(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  STORE_OR_FAIL();
  const char *ns = str_in(in, "namespace");
  if (!ns) return EXT_LEDGER_INVALID;
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_snapshot(store, ctx, ns, &text, &len);
  return put_json(out, rc, text, len);
}

/* The changes after a cursor, at most limit (1024 by default): what a watch is, polled. */
static int op_store_changes(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  STORE_OR_FAIL();
  const char *ns = str_in(in, "namespace");
  int64_t after = int_in(in, "after", 0);
  if (!ns || after < 0) return EXT_LEDGER_INVALID;
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_changes(store, ctx, ns, (uint64_t)after, int_in(in, "limit", 1024), &text, &len); /* 1024 by default */
  return put_json(out, rc, text, len);
}

static int op_canonical_transaction(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)s;
  (void)ctx;
  size_t len, clen = 0;
  char *tx = text_of(in, "transaction", &len), *canonical = NULL, fp[65];
  if (!tx) return EXT_LEDGER_INVALID;
  int rc = ext_ledger_canonical_transaction(tx, len, &canonical, &clen, fp);
  free(tx);
  if (!rc) {
    lj_buf_puts(out, "{\"canonical\":");
    lj_buf_put(out, canonical, clen);
    lj_buf_puts(out, ",\"fingerprint\":\"");
    lj_buf_puts(out, fp);
    lj_buf_puts(out, "\"}");
  }
  ext_ledger_free(canonical);
  return rc;
}

/* ---- the recorder and the collectors ---- */

/* {"store"} a recorder of this machine, or {"store","environment"} of the one described. */
static int op_recorder_open(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  STORE_OR_FAIL();
  size_t len;
  char *env = text_of(in, "environment", &len);
  ext_ledger_recorder *r = NULL;
  int rc = env ? ext_ledger_recorder_open(store, ctx, env, len, &r) : ext_ledger_recorder_open_machine(store, ctx, &r);
  free(env);
  if (rc) return rc;
  char store_name[24];
  snprintf(store_name, sizeof store_name, "%s", h->name); /* handle_add may move h */
  lp_handle *rh = handle_add(s, H_RECORDER, r, store_name);
  if (!rh) {
    ext_ledger_recorder_close(r);
    return EXT_LEDGER_IO;
  }
  return put_handle(out, "recorder", rh->name);
}

static int op_recorder_close(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  return op_close(s, in, out, "recorder", H_RECORDER);
}

/* {"recorder","op","input"}: one ext_ledger_record operation. */
static int op_recorder_record(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  lp_handle *h = handle_get(s, in, "recorder", H_RECORDER);
  const char *op = str_in(in, "op");
  if (!h) return EXT_LEDGER_NOT_FOUND;
  if (!op) return EXT_LEDGER_INVALID;
  size_t len, rlen = 0;
  char *input = text_of(in, "input", &len), *text = NULL;
  int rc = ext_ledger_record(h->ptr, ctx, op, input, len, &text, &rlen);
  free(input);
  return put_json(out, rc, text, rlen);
}

static int op_recorder_scan(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  lp_handle *h = handle_get(s, in, "recorder", H_RECORDER);
  const char *what = str_in(in, "what");
  if (!h) return EXT_LEDGER_NOT_FOUND;
  if (!what) return EXT_LEDGER_INVALID;
  return put_json(out, ext_ledger_scan(h->ptr, ctx, what), NULL, 0);
}

static int op_recorder_vocabulary(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)s;
  (void)ctx;
  (void)in;
  lj_buf_puts(out, ext_ledger_recorder_vocabulary());
  return 0;
}

static int op_collect(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)s;
  const char *what = str_in(in, "what");
  if (!what) return EXT_LEDGER_INVALID;
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_collect(ctx, what, &text, &len);
  return put_json(out, rc, text, len);
}

static int op_machine_environment(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)s;
  (void)ctx;
  (void)in;
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_machine_environment(&text, &len);
  return put_json(out, rc, text, len);
}

/* ---- the supervisor ---- */

static int op_supervisor_open(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  STORE_OR_FAIL();
  ext_ledger_supervisor_options o;
  memset(&o, 0, sizeof o);
  const lj_value *v = lj_get(in, "options");
  if (v && v->type == LJ_OBJECT) {
    o.log_limit = int_in(v, "log_limit", 0);
    o.runtime_id = str_in(v, "runtime_id");
    const lj_value *req = lj_get(v, "require_checksum");
    o.require_checksum = req && req->type == LJ_BOOL && req->u.boolean;
    o.orphan_policy = str_in(v, "orphan_policy");
    o.lease_ms = int_in(v, "lease_ms", 0);
    o.retain_terminal = int_in(v, "retain_terminal", 0);
  }
  ext_ledger_supervisor *sup = NULL;
  int rc = ext_ledger_supervisor_open(store, &o, &sup);
  if (rc) return rc;
  char store_name[24];
  snprintf(store_name, sizeof store_name, "%s", h->name);
  lp_handle *ph = handle_add(s, H_SUPERVISOR, sup, store_name);
  if (!ph) {
    ext_ledger_supervisor_close(sup);
    return EXT_LEDGER_IO;
  }
  return put_handle(out, "supervisor", ph->name);
}

static int op_supervisor_close(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  return op_close(s, in, out, "supervisor", H_SUPERVISOR);
}

#define SUPERVISOR_OR_FAIL()                                       \
  lp_handle *h = handle_get(s, in, "supervisor", H_SUPERVISOR);    \
  if (!h) return EXT_LEDGER_NOT_FOUND;                             \
  ext_ledger_supervisor *sup = h->ptr

static int op_supervisor_start(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  SUPERVISOR_OR_FAIL();
  size_t len, slen, ilen = 0;
  char *spec = text_of(in, "spec", &len), *secrets = text_of(in, "secrets", &slen), *inst = NULL;
  int rc = spec ? ext_ledger_supervisor_start(sup, ctx, spec, len, secrets, slen, &inst, &ilen) : EXT_LEDGER_INVALID;
  free(spec);
  if (secrets) memset(secrets, 0, slen);
  free(secrets);
  return put_json(out, rc, inst, ilen);
}

static int op_supervisor_stop(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  SUPERVISOR_OR_FAIL();
  return put_json(out, ext_ledger_supervisor_stop(sup, ctx, str_in(in, "id")), NULL, 0);
}

static int op_supervisor_ready(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  SUPERVISOR_OR_FAIL();
  const lj_value *ready = lj_get(in, "ready");
  if (!ready || ready->type != LJ_BOOL) return EXT_LEDGER_INVALID;
  return put_json(out, ext_ledger_supervisor_ready(sup, str_in(in, "id"), int_in(in, "pid", 0), ready->u.boolean), NULL, 0);
}

static int op_supervisor_await(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  SUPERVISOR_OR_FAIL();
  const char *states = str_in(in, "states");
  if (!states) return EXT_LEDGER_INVALID;
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_supervisor_await(sup, ctx, str_in(in, "id"), states, &text, &len);
  return put_json(out, rc, text, len);
}

static int op_supervisor_health(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  SUPERVISOR_OR_FAIL();
  const lj_value *check = lj_get(in, "check");
  if (!check || check->type != LJ_BOOL) return EXT_LEDGER_INVALID;
  int healthy = 0;
  int rc = ext_ledger_supervisor_health(sup, str_in(in, "id"), check->u.boolean, &healthy);
  if (!rc) lj_buf_puts(out, healthy ? "{\"healthy\":true}" : "{\"healthy\":false}");
  return rc;
}

static int op_supervisor_instance(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  SUPERVISOR_OR_FAIL();
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_supervisor_instance(sup, str_in(in, "id"), &text, &len);
  return put_json(out, rc, text, len);
}

static int op_supervisor_instances(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  SUPERVISOR_OR_FAIL();
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_supervisor_instances(sup, &text, &len);
  if (!rc) {
    lj_buf_puts(out, "{\"instances\":");
    lj_buf_put(out, text, len);
    lj_buf_putc(out, '}');
  }
  ext_ledger_free(text);
  return rc;
}

/* The output tail as a JSON string: invalid UTF-8 becomes U+FFFD. */
static int op_supervisor_logs(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  SUPERVISOR_OR_FAIL();
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_supervisor_logs(sup, str_in(in, "id"), &text, &len);
  if (!rc) {
    lj_buf_puts(out, "{\"logs\":");
    lj_write_string(out, text, len);
    lj_buf_putc(out, '}');
  }
  ext_ledger_free(text);
  return rc;
}

static int op_supervisor_orphans(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  (void)ctx;
  SUPERVISOR_OR_FAIL();
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_supervisor_orphans(sup, &text, &len);
  if (!rc) {
    lj_buf_puts(out, "{\"orphans\":");
    lj_buf_put(out, text, len);
    lj_buf_putc(out, '}');
  }
  ext_ledger_free(text);
  return rc;
}

static int op_supervisor_resolve_orphan(lp_session *s, const ext_ledger_context *ctx, const lj_value *in, lj_buf *out) {
  SUPERVISOR_OR_FAIL();
  return put_json(out, ext_ledger_supervisor_resolve_orphan(sup, ctx, str_in(in, "id")), NULL, 0);
}

static const lp_operation operations[] = {
    {"store.open", op_store_open},
    {"store.close", op_store_close},
    {"store.register", op_store_register},
    {"store.apply", op_store_apply},
    {"store.get-vertex", op_store_get_vertex},
    {"store.get-edge", op_store_get_edge},
    {"store.query-vertices", op_store_query_vertices},
    {"store.query-edges", op_store_query_edges},
    {"store.traverse", op_store_traverse},
    {"store.snapshot", op_store_snapshot},
    {"store.changes", op_store_changes},
    {"store.canonical-transaction", op_canonical_transaction},
    {"recorder.open", op_recorder_open},
    {"recorder.close", op_recorder_close},
    {"recorder.record", op_recorder_record},
    {"recorder.scan", op_recorder_scan},
    {"recorder.vocabulary", op_recorder_vocabulary},
    {"collect", op_collect},
    {"machine-environment", op_machine_environment},
    {"supervisor.open", op_supervisor_open},
    {"supervisor.close", op_supervisor_close},
    {"supervisor.start", op_supervisor_start},
    {"supervisor.stop", op_supervisor_stop},
    {"supervisor.ready", op_supervisor_ready},
    {"supervisor.await", op_supervisor_await},
    {"supervisor.health", op_supervisor_health},
    {"supervisor.instance", op_supervisor_instance},
    {"supervisor.instances", op_supervisor_instances},
    {"supervisor.logs", op_supervisor_logs},
    {"supervisor.orphans", op_supervisor_orphans},
    {"supervisor.resolve-orphan", op_supervisor_resolve_orphan},
};

static const lp_operation *find_operation(const char *name) {
  for (size_t i = 0; i < sizeof operations / sizeof *operations; i++)
    if (strcmp(operations[i].name, name) == 0) return &operations[i];
  return NULL;
}

/* Operation names and error codes are ext.plugin/v1 identifiers, which have no
 * "_": the C API's code names are written with "-" instead. */
static void write_descriptor(lj_buf *b) {
  lj_buf_puts(b, "{\"apiVersion\":\"" LP_API "\",\"identity\":{\"id\":\"ext/ledger\",\"revision\":\"" EXT_LEDGER_REVISION "\"},"
                 "\"contracts\":[{\"name\":\"" LP_CONTRACT "\",\"version\":\"" LP_CONTRACT_VERSION "\",\"operations\":[");
  for (size_t i = 0; i < sizeof operations / sizeof *operations; i++) {
    if (i) lj_buf_putc(b, ',');
    lj_buf_puts(b, "{\"name\":\"");
    lj_buf_puts(b, operations[i].name);
    lj_buf_puts(b, "\"}");
  }
  lj_buf_puts(b, "]}]}");
}

/* ---- frames ---- */

static void write_error(lj_buf *b, const char *id, size_t id_len, int code, const char *message) {
  char name[32];
  snprintf(name, sizeof name, "%s", ext_ledger_code_name(code));
  for (char *p = name; *p; p++)
    if (*p == '_') *p = '-';
  lj_buf_puts(b, "{\"apiVersion\":\"" LP_API "\",\"id\":");
  lj_write_string(b, id, id_len);
  lj_buf_puts(b, ",\"error\":{\"code\":\"");
  lj_buf_puts(b, name);
  lj_buf_puts(b, "\",\"message\":");
  lj_write_string(b, message, strlen(message));
  lj_buf_puts(b, "}}");
}

/* Answers one Request: a Response with the payload, or with the error a domain failure is. */
static int invoke(lp_session *s, const char *request, size_t len, lj_buf *b) {
  int oom = 0;
  lj_value *req = lj_parse(request, len, &oom);
  if (!req || req->type != LJ_OBJECT) {
    lj_free(req);
    return -1; /* not a frame: a transport failure */
  }
  const lj_value *idv = lj_get(req, "id"), *contract = lj_get(req, "contract");
  const char *api = str_in(req, "apiVersion"), *op = str_in(req, "operation"), *deadline = str_in(req, "deadline");
  if (!idv || idv->type != LJ_STRING || !api || strcmp(api, LP_API) != 0) {
    lj_free(req);
    return -1;
  }
  const char *id = idv->u.string.ptr;
  size_t id_len = idv->u.string.len;
  ext_ledger_context ctx = {0, 0};
  ll_time t;
  if (deadline && ll_time_parse(deadline, strlen(deadline), &t) == 0) ctx.deadline_unix_ms = t.sec * 1000 + t.nsec / 1000000;
  const char *cname = contract ? str_in(contract, "name") : NULL, *cversion = contract ? str_in(contract, "version") : NULL;
  const lp_operation *operation = op ? find_operation(op) : NULL;
  if (!cname || strcmp(cname, LP_CONTRACT) != 0 || !cversion || strcmp(cversion, LP_CONTRACT_VERSION) != 0 || !operation) {
    write_error(b, id, id_len, EXT_LEDGER_UNSUPPORTED, "no such contract or operation");
    lj_free(req);
    return 0;
  }
  const lj_value *payload = lj_get(req, "payload");
  lj_value *empty = NULL;
  if (!payload || payload->type == LJ_NULL) payload = empty = lj_new(LJ_OBJECT);
  lj_buf result = {0};
  int rc = !payload || payload->type != LJ_OBJECT ? EXT_LEDGER_INVALID : operation->run(s, &ctx, payload, &result);
  if (!rc && result.oom) rc = EXT_LEDGER_IO;
  if (rc) {
    write_error(b, id, id_len, rc, ext_ledger_code_name(rc));
  } else {
    lj_buf_puts(b, "{\"apiVersion\":\"" LP_API "\",\"id\":");
    lj_write_string(b, id, id_len);
    lj_buf_puts(b, ",\"payload\":");
    lj_buf_put(b, result.ptr, result.len);
    lj_buf_putc(b, '}');
  }
  lj_buf_free(&result);
  lj_free(empty);
  lj_free(req);
  return 0;
}

/* ---- the ABI ---- */

LP_EXPORT uint32_t LP_CALL ext_plugin_abi_version(void) { return LP_ABI_VERSION; }

LP_EXPORT uint64_t LP_CALL ext_plugin_open(void) {
  ll_mutex *mu = table_lock();
  lp_session *s = mu ? calloc(1, sizeof *s) : NULL;
  if (!s) return 0;
  ll_mutex_lock(mu);
  lp_session **grown = realloc(sessions, (nsessions + 1) * sizeof *grown);
  if (!grown) {
    ll_mutex_unlock(mu);
    free(s);
    return 0;
  }
  sessions = grown;
  s->id = ++next_session;
  sessions[nsessions++] = s;
  ll_mutex_unlock(mu);
  return s->id;
}

LP_EXPORT uint32_t LP_CALL ext_plugin_call(uint64_t handle, uint32_t operation, const uint8_t *request, uint32_t request_len, uint8_t *response,
                                           uint32_t response_capacity, uint32_t *response_len) {
  if (!response_len) return LP_INVALID;
  *response_len = 0;
  lp_session *s = handle ? session_get(handle) : NULL;
  if (!s) return LP_CLOSED;
  if (!request && request_len) return LP_INVALID;
  lj_buf b = {0};
  if (operation == LP_HANDSHAKE) {
    lj_value *in = lj_parse((const char *)request, request_len, NULL);
    int ok = in && in->type == LJ_OBJECT && str_in(in, "deadline");
    lj_free(in);
    if (!ok) return LP_INVALID;
    write_descriptor(&b);
    s->handshaken = 1;
  } else if (operation == LP_INVOKE) {
    if (!s->handshaken) return LP_INVALID;
    if (invoke(s, (const char *)request, request_len, &b)) {
      lj_buf_free(&b);
      return LP_INVALID;
    }
  } else {
    return LP_INVALID;
  }
  uint32_t status = LP_OK;
  if (b.oom || b.len > response_capacity) status = LP_FAILED;
  else {
    memcpy(response, b.ptr, b.len);
    *response_len = (uint32_t)b.len;
  }
  lj_buf_free(&b);
  return status;
}

LP_EXPORT void LP_CALL ext_plugin_close(uint64_t handle) {
  ll_mutex *mu = table_lock();
  if (!mu || !handle) return;
  ll_mutex_lock(mu);
  lp_session *found = NULL;
  for (size_t i = 0; i < nsessions; i++)
    if (sessions[i]->id == handle) {
      found = sessions[i];
      sessions[i] = sessions[--nsessions];
      break;
    }
  ll_mutex_unlock(mu);
  if (found) session_free(found);
}
