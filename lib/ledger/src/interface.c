/* The library interface: every object of the ledger (a store, a watch, a recorder, a supervisor)
 * behind four functions, opened from JSON, called by operation name with JSON in and JSON out, and
 * closed by handle. A binding in any language binds these four and nothing else; the typed API
 * stays for C callers. Handles are never reused; an object is freed once it is closed and no call
 * holds it, and an object with children (a store under a recorder) refuses to close. */
#include <stdlib.h>
#include <string.h>

#include "ext_ledger.h"
#include "json.h"
#include "ledger_internal.h"

enum { K_STORE = 1, K_WATCH, K_RECORDER, K_SUPERVISOR };

typedef struct entry {
  uint64_t id;
  int kind;
  void *ptr;
  struct entry *parent;
  int children, inflight, closing;
  char *runtime_id; /* a supervisor's runtime_id, which its options point at */
} entry;

static volatile int table_lock;
static entry **table;
static size_t table_len, table_cap;
static uint64_t next_id = 1;

static void lock(void) {
  while (!ll_flag_cas(&table_lock, 0, 1)) ll_sleep_ms(0);
}
static void unlock(void) { ll_flag_store(&table_lock, 0); }

static void destroy(entry *e) {
  switch (e->kind) {
    case K_STORE: ext_ledger_store_close(e->ptr); break;
    case K_WATCH: ext_ledger_store_watch_close(e->ptr); break;
    case K_RECORDER: ext_ledger_recorder_close(e->ptr); break;
    case K_SUPERVISOR: ext_ledger_supervisor_close(e->ptr); break;
  }
  free(e->runtime_id);
  free(e);
}

/* add records ptr under a new handle; parent (may be NULL) gains a child. */
static int add(int kind, void *ptr, entry *parent, char *runtime_id, uint64_t *handle) {
  entry *e = calloc(1, sizeof *e);
  if (!e) return EXT_LEDGER_UNAVAILABLE;
  e->kind = kind, e->ptr = ptr, e->parent = parent, e->runtime_id = runtime_id;
  lock();
  if (table_len == table_cap) {
    size_t cap = table_cap ? table_cap * 2 : 16;
    entry **grown = realloc(table, cap * sizeof *grown);
    if (!grown) {
      unlock();
      free(e);
      return EXT_LEDGER_UNAVAILABLE;
    }
    table = grown, table_cap = cap;
  }
  e->id = next_id++;
  table[table_len++] = e;
  if (parent) parent->children++;
  unlock();
  *handle = e->id;
  return EXT_LEDGER_OK;
}

/* acquire finds an open object of kind (0: any) and holds it for one call. */
static entry *acquire(uint64_t handle, int kind) {
  entry *found = NULL;
  lock();
  for (size_t i = 0; i < table_len; i++) {
    if (table[i]->id == handle) {
      if (!table[i]->closing && (!kind || table[i]->kind == kind)) {
        found = table[i];
        found->inflight++;
      }
      break;
    }
  }
  unlock();
  return found;
}

static void remove_locked(entry *e) {
  for (size_t i = 0; i < table_len; i++) {
    if (table[i] == e) {
      table[i] = table[--table_len];
      break;
    }
  }
  if (e->parent) e->parent->children--;
}

static void release(entry *e) {
  int free_it = 0;
  lock();
  if (--e->inflight == 0 && e->closing) {
    remove_locked(e);
    free_it = 1;
  }
  unlock();
  if (free_it) destroy(e);
}

/* ---- JSON helpers ---- */

static const char *str(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : NULL;
}

static int num(const lj_value *o, const char *key, int64_t *out) {
  const lj_value *v = lj_get(o, key);
  if (!v) return 0;
  if (v->type != LJ_NUMBER || v->u.number != (double)(int64_t)v->u.number) return -1;
  *out = (int64_t)v->u.number;
  return 1;
}

static int boolean(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_BOOL && v->u.boolean;
}

/* encoded writes o[key], which must be present, as JSON text into *text (malloc'd). */
static int encoded(const lj_value *o, const char *key, char **text, size_t *len) {
  const lj_value *v = lj_get(o, key);
  if (!v) return EXT_LEDGER_INVALID;
  lj_buf b = {0};
  lj_write(&b, v);
  *text = lj_buf_take(&b, len);
  return *text ? EXT_LEDGER_OK : EXT_LEDGER_UNAVAILABLE;
}

/* context fills ctx from the input's timeout_ms, when it has one. */
static int context(const lj_value *in, ext_ledger_context *ctx, const ext_ledger_context **out) {
  int64_t timeout = 0;
  int has = num(in, "timeout_ms", &timeout);
  if (has < 0 || (has && timeout < 0)) return EXT_LEDGER_INVALID;
  memset(ctx, 0, sizeof *ctx);
  *out = NULL;
  if (has) {
    ctx->deadline_unix_ms = ll_now_unix_ms() + timeout;
    *out = ctx;
  }
  return EXT_LEDGER_OK;
}

static int dup_out(const char *text, size_t len, char **out, size_t *out_len) {
  char *copy = malloc(len + 1);
  if (!copy) return EXT_LEDGER_UNAVAILABLE;
  memcpy(copy, text, len);
  copy[len] = 0;
  *out = copy, *out_len = len;
  return EXT_LEDGER_OK;
}

static int null_out(char **out, size_t *out_len) { return dup_out("null", 4, out, out_len); }

static int string_out(const char *s, size_t n, char **out, size_t *out_len) {
  lj_buf b = {0};
  lj_write_string(&b, s, n);
  *out = lj_buf_take(&b, out_len);
  return *out ? EXT_LEDGER_OK : EXT_LEDGER_UNAVAILABLE;
}

/* ---- opening ---- */

static int store_options(const lj_value *config, ext_ledger_store_options *o) {
  memset(o, 0, sizeof *o);
  const lj_value *v = lj_get(config, "options");
  if (!v) return EXT_LEDGER_OK;
  if (v->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  struct {
    const char *key;
    int64_t *field;
  } fields[] = {{"max_changes", &o->max_changes},
                {"max_change_bytes", &o->max_change_bytes},
                {"max_transaction_records", &o->max_transaction_records},
                {"max_record_bytes", &o->max_record_bytes},
                {"max_results", &o->max_results},
                {"max_snapshot_records", &o->max_snapshot_records}};
  for (size_t i = 0; i < sizeof fields / sizeof fields[0]; i++)
    if (num(v, fields[i].key, fields[i].field) < 0) return EXT_LEDGER_INVALID;
  return EXT_LEDGER_OK;
}

static int open_store(const lj_value *config, uint64_t *handle) {
  const char *backend = str(config, "backend"), *path = str(config, "path");
  ext_ledger_store_options options;
  ext_ledger_context c;
  const ext_ledger_context *ctx;
  int rc = store_options(config, &options);
  if (rc || (rc = context(config, &c, &ctx))) return rc;
  ext_ledger_store *store = NULL;
  if (!backend || !strcmp(backend, "memory")) {
    rc = ext_ledger_store_open_memory(&options, &store);
  } else if (!path) {
    return EXT_LEDGER_INVALID;
  } else if (!strcmp(backend, "file")) {
    rc = ext_ledger_store_open(ctx, path, &options, &store);
  } else if (!strcmp(backend, "reader")) {
    rc = ext_ledger_store_open_reader(ctx, path, &options, &store);
  } else if (!strcmp(backend, "sqlite")) {
    rc = ext_ledger_store_open_sqlite(ctx, path, &options, &store);
  } else {
    return EXT_LEDGER_INVALID;
  }
  if (rc) return rc;
  if ((rc = add(K_STORE, store, NULL, NULL, handle))) ext_ledger_store_close(store);
  return rc;
}

/* parent_store holds the store a child object names; the caller releases it. */
static int parent_store(const lj_value *config, entry **out) {
  int64_t h = 0;
  if (num(config, "store", &h) != 1 || h <= 0) return EXT_LEDGER_INVALID;
  *out = acquire((uint64_t)h, K_STORE);
  return *out ? EXT_LEDGER_OK : EXT_LEDGER_NOT_FOUND;
}

static int open_watch(const lj_value *config, uint64_t *handle) {
  entry *store;
  int rc = parent_store(config, &store);
  if (rc) return rc;
  const char *ns = str(config, "namespace");
  int64_t after = 0, buffer = 0;
  ext_ledger_context c;
  const ext_ledger_context *ctx;
  ext_ledger_store_watch *w = NULL;
  if (num(config, "after", &after) < 0 || after < 0 || num(config, "buffer", &buffer) < 0) rc = EXT_LEDGER_INVALID;
  if (!rc) rc = context(config, &c, &ctx);
  if (!rc) rc = ext_ledger_store_watch_open(store->ptr, ctx, ns ? ns : "", (uint64_t)after, buffer, &w);
  if (!rc && (rc = add(K_WATCH, w, store, NULL, handle))) ext_ledger_store_watch_close(w);
  release(store);
  return rc;
}

static int open_recorder(const lj_value *config, uint64_t *handle) {
  entry *store;
  int rc = parent_store(config, &store);
  if (rc) return rc;
  ext_ledger_context c;
  const ext_ledger_context *ctx;
  ext_ledger_recorder *r = NULL;
  rc = context(config, &c, &ctx);
  if (!rc) {
    if (lj_get(config, "environment")) {
      char *env = NULL;
      size_t n = 0;
      if (!(rc = encoded(config, "environment", &env, &n))) rc = ext_ledger_recorder_open(store->ptr, ctx, env, n, &r);
      free(env);
    } else if (boolean(config, "machine")) {
      rc = ext_ledger_recorder_open_machine(store->ptr, ctx, &r);
    } else {
      rc = EXT_LEDGER_INVALID;
    }
  }
  if (!rc && (rc = add(K_RECORDER, r, store, NULL, handle))) ext_ledger_recorder_close(r);
  release(store);
  return rc;
}

static int open_supervisor(const lj_value *config, uint64_t *handle) {
  entry *store;
  int rc = parent_store(config, &store);
  if (rc) return rc;
  ext_ledger_supervisor_options o;
  memset(&o, 0, sizeof o);
  char *runtime = NULL;
  const lj_value *v = lj_get(config, "options");
  if (v && v->type != LJ_OBJECT) rc = EXT_LEDGER_INVALID;
  if (!rc && v) {
    if (num(v, "log_limit", &o.log_limit) < 0 || num(v, "lease_ms", &o.lease_ms) < 0 || num(v, "retain_terminal", &o.retain_terminal) < 0)
      rc = EXT_LEDGER_INVALID;
    o.require_checksum = boolean(v, "require_checksum");
    o.orphan_policy = str(v, "orphan_policy");
    const char *id = str(v, "runtime_id");
    if (id) {
      size_t n = strlen(id);
      if ((runtime = malloc(n + 1))) memcpy(runtime, id, n + 1);
      else rc = EXT_LEDGER_UNAVAILABLE;
    }
    o.runtime_id = runtime;
  }
  ext_ledger_supervisor *sup = NULL;
  if (!rc) rc = ext_ledger_supervisor_open(store->ptr, &o, &sup);
  if (!rc) {
    if ((rc = add(K_SUPERVISOR, sup, store, runtime, handle))) ext_ledger_supervisor_close(sup);
    else runtime = NULL;
  }
  free(runtime);
  release(store);
  return rc;
}

uint32_t ext_ledger_abi_version(void) { return EXT_LEDGER_ABI_VERSION; }

int ext_ledger_open(const char *config, size_t len, uint64_t *handle) {
  if (!handle) return EXT_LEDGER_INVALID;
  *handle = 0;
  int oom = 0;
  lj_value *c = lj_parse(config ? config : "{}", config ? len : 2, &oom);
  if (!c) return oom ? EXT_LEDGER_UNAVAILABLE : EXT_LEDGER_INVALID;
  int rc = EXT_LEDGER_INVALID;
  const char *object = c->type == LJ_OBJECT ? str(c, "object") : NULL;
  if (object && !strcmp(object, "store")) rc = open_store(c, handle);
  else if (object && !strcmp(object, "watch")) rc = open_watch(c, handle);
  else if (object && !strcmp(object, "recorder")) rc = open_recorder(c, handle);
  else if (object && !strcmp(object, "supervisor")) rc = open_supervisor(c, handle);
  lj_free(c);
  return rc;
}

int ext_ledger_close(uint64_t handle) {
  entry *e = NULL;
  int rc = EXT_LEDGER_NOT_FOUND, free_it = 0;
  lock();
  for (size_t i = 0; i < table_len; i++) {
    if (table[i]->id == handle && !table[i]->closing) {
      e = table[i];
      if (e->children) {
        rc = EXT_LEDGER_BUSY;
      } else {
        e->closing = 1;
        rc = EXT_LEDGER_OK;
        if (!e->inflight) {
          remove_locked(e);
          free_it = 1;
        }
      }
      break;
    }
  }
  unlock();
  if (free_it) destroy(e);
  return rc;
}

/* ---- calling ---- */

static int call_library(const char *op, const lj_value *in, char **out, size_t *out_len) {
  ext_ledger_context c;
  const ext_ledger_context *ctx;
  int rc = context(in, &c, &ctx);
  if (rc) return rc;
  if (!strcmp(op, "collect")) {
    const char *what = str(in, "what");
    return what ? ext_ledger_collect(ctx, what, out, out_len) : EXT_LEDGER_INVALID;
  }
  if (!strcmp(op, "collect_environment")) return ext_ledger_collect_environment(out, out_len);
  if (!strcmp(op, "vocabulary")) {
    const char *v = ext_ledger_recorder_vocabulary();
    return dup_out(v, strlen(v), out, out_len);
  }
  if (!strcmp(op, "canonical_transaction")) {
    char *tx = NULL, *canonical = NULL, fp[65];
    size_t n = 0, cn = 0;
    if ((rc = encoded(in, "transaction", &tx, &n))) return rc;
    rc = ext_ledger_store_canonical_transaction(tx, n, &canonical, &cn, fp);
    free(tx);
    if (rc) return rc;
    lj_buf b = {0};
    lj_buf_puts(&b, "{\"canonical\":");
    lj_buf_put(&b, canonical, cn);
    lj_buf_puts(&b, ",\"fingerprint\":");
    lj_write_string(&b, fp, strlen(fp));
    lj_buf_putc(&b, '}');
    ext_ledger_free(canonical);
    *out = lj_buf_take(&b, out_len);
    return *out ? EXT_LEDGER_OK : EXT_LEDGER_UNAVAILABLE;
  }
  if (!strcmp(op, "code_name")) {
    int64_t code = 0;
    if (num(in, "code", &code) != 1) return EXT_LEDGER_INVALID;
    const char *name = ext_ledger_code_name((int)code);
    return string_out(name, strlen(name), out, out_len);
  }
  return EXT_LEDGER_UNSUPPORTED;
}

static int call_store(ext_ledger_store *s, const char *op, const lj_value *in, char **out, size_t *out_len) {
  ext_ledger_context c;
  const ext_ledger_context *ctx;
  int rc = context(in, &c, &ctx);
  if (rc) return rc;
  const char *ns = str(in, "namespace"), *id = str(in, "id");
  char *text = NULL;
  size_t n = 0;
  if (!strcmp(op, "register")) {
    if ((rc = encoded(in, "schema", &text, &n))) return rc;
    rc = ext_ledger_store_register(s, ctx, text, n);
    free(text);
    return rc ? rc : null_out(out, out_len);
  }
  if (!strcmp(op, "apply")) {
    if ((rc = encoded(in, "transaction", &text, &n))) return rc;
    rc = ext_ledger_store_apply(s, ctx, text, n, out, out_len);
    free(text);
    return rc;
  }
  if (!strcmp(op, "get_vertex") || !strcmp(op, "get_edge")) {
    if (!ns || !id) return EXT_LEDGER_INVALID;
    return op[4] == 'v' ? ext_ledger_store_get_vertex(s, ctx, ns, id, out, out_len) : ext_ledger_store_get_edge(s, ctx, ns, id, out, out_len);
  }
  if (!strcmp(op, "query_vertices") || !strcmp(op, "query_edges")) {
    if ((rc = encoded(in, "query", &text, &n))) return rc;
    rc = op[6] == 'v' ? ext_ledger_store_query_vertices(s, ctx, text, n, out, out_len) : ext_ledger_store_query_edges(s, ctx, text, n, out, out_len);
    free(text);
    return rc;
  }
  if (!strcmp(op, "traverse")) {
    const char *start = str(in, "start");
    int64_t depth = 0, max = 0;
    if (!ns || !start || num(in, "max_depth", &depth) < 0 || num(in, "max_vertices", &max) < 0) return EXT_LEDGER_INVALID;
    return ext_ledger_store_traverse(s, ctx, ns, start, depth, max, out, out_len);
  }
  if (!strcmp(op, "snapshot")) return ns ? ext_ledger_store_snapshot(s, ctx, ns, out, out_len) : EXT_LEDGER_INVALID;
  if (!strcmp(op, "changes")) {
    int64_t after = 0, limit = 0;
    if (num(in, "after", &after) < 0 || after < 0 || num(in, "limit", &limit) < 0) return EXT_LEDGER_INVALID;
    return ext_ledger_store_changes(s, ctx, ns ? ns : "", (uint64_t)after, limit, out, out_len);
  }
  return EXT_LEDGER_UNSUPPORTED;
}

static int call_watch(ext_ledger_store_watch *w, const char *op, const lj_value *in, char **out, size_t *out_len) {
  if (strcmp(op, "next")) return EXT_LEDGER_UNSUPPORTED;
  int64_t timeout = 0; /* a watch is polled: without timeout_ms, next does not wait */
  if (num(in, "timeout_ms", &timeout) < 0 || timeout < 0) return EXT_LEDGER_INVALID;
  return ext_ledger_store_watch_next(w, timeout, out, out_len);
}

static int call_recorder(ext_ledger_recorder *r, const char *op, const lj_value *in, char **out, size_t *out_len) {
  ext_ledger_context c;
  const ext_ledger_context *ctx;
  int rc = context(in, &c, &ctx);
  if (rc) return rc;
  if (!strcmp(op, "scan")) {
    const char *what = str(in, "what");
    if (!what) return EXT_LEDGER_INVALID;
    return (rc = ext_ledger_recorder_scan(r, ctx, what)) ? rc : null_out(out, out_len);
  }
  if (!strcmp(op, "record")) {
    const char *operation = str(in, "operation");
    char *input = NULL, *result = NULL;
    size_t n = 0, rn = 0;
    if (!operation) return EXT_LEDGER_INVALID;
    if (lj_get(in, "input") && (rc = encoded(in, "input", &input, &n))) return rc;
    rc = ext_ledger_recorder_record(r, ctx, operation, input, n, &result, &rn);
    free(input);
    if (rc) return rc;
    if (!result) return null_out(out, out_len);
    *out = result, *out_len = rn;
    return EXT_LEDGER_OK;
  }
  return EXT_LEDGER_UNSUPPORTED;
}

static int call_supervisor(ext_ledger_supervisor *sup, const char *op, const lj_value *in, char **out, size_t *out_len) {
  ext_ledger_context c;
  const ext_ledger_context *ctx;
  int rc = context(in, &c, &ctx);
  if (rc) return rc;
  const char *id = str(in, "id");
  if (!strcmp(op, "start")) {
    char *spec = NULL, *secrets = NULL;
    size_t n = 0, sn = 0;
    if ((rc = encoded(in, "spec", &spec, &n))) return rc;
    if (lj_get(in, "secrets") && (rc = encoded(in, "secrets", &secrets, &sn))) {
      free(spec);
      return rc;
    }
    rc = ext_ledger_supervisor_start(sup, ctx, spec, n, secrets, sn, out, out_len);
    free(spec);
    free(secrets);
    return rc;
  }
  if (!strcmp(op, "instances")) return ext_ledger_supervisor_instances(sup, out, out_len);
  if (!strcmp(op, "orphans")) return ext_ledger_supervisor_orphans(sup, out, out_len);
  if (!id) return strcmp(op, "ready") && strcmp(op, "await") && strcmp(op, "stop") && strcmp(op, "health") && strcmp(op, "instance") &&
                         strcmp(op, "logs") && strcmp(op, "resolve_orphan")
                     ? EXT_LEDGER_UNSUPPORTED
                     : EXT_LEDGER_INVALID;
  if (!strcmp(op, "ready")) {
    int64_t pid = 0;
    const lj_value *ready = lj_get(in, "ready");
    if (num(in, "pid", &pid) != 1 || !ready || ready->type != LJ_BOOL) return EXT_LEDGER_INVALID;
    return (rc = ext_ledger_supervisor_ready(sup, id, pid, ready->u.boolean)) ? rc : null_out(out, out_len);
  }
  if (!strcmp(op, "await")) {
    const lj_value *states = lj_get(in, "states");
    if (!states || states->type != LJ_ARRAY || !states->u.array.len) return EXT_LEDGER_INVALID;
    lj_buf b = {0};
    for (size_t i = 0; i < states->u.array.len; i++) {
      const lj_value *s = states->u.array.items[i];
      if (s->type != LJ_STRING || strchr(s->u.string.ptr, ',')) {
        lj_buf_free(&b);
        return EXT_LEDGER_INVALID;
      }
      if (i) lj_buf_putc(&b, ',');
      lj_buf_put(&b, s->u.string.ptr, s->u.string.len);
    }
    size_t n = 0;
    char *csv = lj_buf_take(&b, &n);
    if (!csv) return EXT_LEDGER_UNAVAILABLE;
    rc = ext_ledger_supervisor_await(sup, ctx, id, csv, out, out_len);
    free(csv);
    return rc;
  }
  if (!strcmp(op, "stop")) return (rc = ext_ledger_supervisor_stop(sup, ctx, id)) ? rc : null_out(out, out_len);
  if (!strcmp(op, "resolve_orphan")) return (rc = ext_ledger_supervisor_resolve_orphan(sup, ctx, id)) ? rc : null_out(out, out_len);
  if (!strcmp(op, "health")) {
    const lj_value *check = lj_get(in, "check");
    int healthy = 0;
    if (!check || check->type != LJ_BOOL) return EXT_LEDGER_INVALID;
    if ((rc = ext_ledger_supervisor_health(sup, id, check->u.boolean, &healthy))) return rc;
    return healthy ? dup_out("{\"healthy\":true}", 16, out, out_len) : dup_out("{\"healthy\":false}", 17, out, out_len);
  }
  if (!strcmp(op, "instance")) return ext_ledger_supervisor_instance(sup, id, out, out_len);
  if (!strcmp(op, "logs")) {
    char *logs = NULL;
    size_t n = 0;
    if ((rc = ext_ledger_supervisor_logs(sup, id, &logs, &n))) return rc;
    rc = string_out(logs ? logs : "", logs ? n : 0, out, out_len);
    ext_ledger_free(logs);
    return rc;
  }
  return EXT_LEDGER_UNSUPPORTED;
}

int ext_ledger_call(uint64_t handle, const char *op, const char *input, size_t len, char **out, size_t *out_len) {
  if (!op || !out || !out_len) return EXT_LEDGER_INVALID;
  *out = NULL, *out_len = 0;
  int oom = 0;
  lj_value *in = lj_parse(input && len ? input : "{}", input && len ? len : 2, &oom);
  if (!in) return oom ? EXT_LEDGER_UNAVAILABLE : EXT_LEDGER_INVALID;
  if (in->type != LJ_OBJECT) {
    lj_free(in);
    return EXT_LEDGER_INVALID;
  }
  int rc;
  if (handle == 0) {
    rc = call_library(op, in, out, out_len);
  } else {
    entry *e = acquire(handle, 0);
    if (!e) {
      lj_free(in);
      return EXT_LEDGER_NOT_FOUND;
    }
    switch (e->kind) {
      case K_STORE: rc = call_store(e->ptr, op, in, out, out_len); break;
      case K_WATCH: rc = call_watch(e->ptr, op, in, out, out_len); break;
      case K_RECORDER: rc = call_recorder(e->ptr, op, in, out, out_len); break;
      default: rc = call_supervisor(e->ptr, op, in, out, out_len); break;
    }
    release(e);
  }
  lj_free(in);
  if (rc && *out) {
    ext_ledger_free(*out);
    *out = NULL, *out_len = 0;
  }
  return rc;
}
