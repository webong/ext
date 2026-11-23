/* The ext.system recorder: its environment, the helpers its observations
 * share, and the dispatch of ext_ledger_recorder_record. It writes through the public
 * store API, so it records on every backend. */
#include "recorder.h"
#include "collect.h"
#include "vocabulary.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

const char *ext_ledger_recorder_vocabulary(void) { return ll_vocabulary; }

/* ---- the environment ---- */

static char *dup_field(const lj_value *o, const char *key) {
  const char *v = rec_field(o, key);
  size_t n = strlen(v);
  char *p = malloc(n + 1);
  if (p) memcpy(p, v, n + 1);
  return p;
}

static void env_free(rec_env *e) {
  free(e->hostname);
  free(e->os);
  free(e->arch);
  free(e->wd);
  free(e->host_env);
  lj_free(e->variables);
  ll_map_free(&e->paths);
}

static int env_decode(const lj_value *v, rec_env *e) {
  memset(e, 0, sizeof *e);
  if (!v || v->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  const lj_value *now = lj_get(v, "now");
  if (!now || now->type != LJ_STRING || ll_time_parse(now->u.string.ptr, now->u.string.len, &e->now)) return EXT_LEDGER_INVALID;
  e->hostname = dup_field(v, "hostname");
  e->os = dup_field(v, "os");
  e->arch = dup_field(v, "architecture");
  e->wd = dup_field(v, "working_directory");
  e->host_env = dup_field(v, "host_environment");
  if (!e->hostname || !e->os || !e->arch || !e->wd || !e->host_env) return EXT_LEDGER_IO;
  const lj_value *ppid = lj_get(v, "parent_pid");
  if (ppid && ppid->type == LJ_NUMBER) e->ppid = (int64_t)ppid->u.number;
  const lj_value *vars = lj_get(v, "variables");
  if (vars && vars->type == LJ_OBJECT && !(e->variables = lj_clone(vars))) return EXT_LEDGER_IO;
  const lj_value *paths = lj_get(v, "paths");
  for (size_t i = 0; paths && paths->type == LJ_ARRAY && i < paths->u.array.len; i++) {
    const lj_value *p = paths->u.array.items[i];
    if (p->type != LJ_STRING) continue;
    char *clean = rec_clean(p->u.string.ptr);
    if (!clean || ll_map_put(&e->paths, clean, strlen(clean), (void *)1)) {
      free(clean);
      return EXT_LEDGER_IO;
    }
    free(clean);
  }
  return 0;
}

const char *rec_getenv(const rec *r, const char *key) { return rec_field(r->env.variables, key); }

int rec_exists(const rec *r, const char *path) {
  if (r->env.machine) return ll_os_exists(path);
  char *clean = rec_clean(path);
  int found = clean && ll_map_get(&r->env.paths, clean, strlen(clean)) != NULL;
  free(clean);
  return found;
}

void rec_now_text(const rec *r, lj_buf *b) {
  ll_time t = ll_time_utc(r->env.machine ? ll_time_now() : r->env.now);
  lj_buf q = {0};
  ll_time_write(&q, &t);
  if (q.oom || q.len < 2) b->oom = 1;
  else lj_buf_put(b, q.ptr + 1, q.len - 2);
  lj_buf_free(&q);
}

/* ---- values ---- */

void rec_digest(const char *p, size_t n, char out[25]) {
  char full[65];
  ll_sha256_hex(p, n, full);
  memcpy(out, full, 24);
  out[24] = 0;
}

int rec_context_name(const char *s, size_t n) {
  if (n < 1 || n > 128) return 0;
  for (size_t i = 0; i < n; i++) {
    char c = s[i];
    int alnum = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9');
    if (i == 0 ? !alnum : !(alnum || c == '.' || c == '_' || c == ' ' || c == '+' || c == '-')) return 0;
  }
  return 1;
}

char *rec_itoa(int64_t v, char buf[24]) {
  snprintf(buf, 24, "%lld", (long long)v);
  return buf;
}

const char *rec_field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : "";
}

/* ---- POSIX paths ---- */

char *rec_clean(const char *p) {
  size_t n = strlen(p);
  if (n == 0) {
    char *dot = malloc(2);
    if (dot) memcpy(dot, ".", 2);
    return dot;
  }
  int rooted = p[0] == '/';
  char *out = malloc(n + 2);
  if (!out) return NULL;
  size_t w = 0;
  if (rooted) out[w++] = '/';
  size_t keep = w; /* where ".." may not back up past */
  for (size_t i = 0; i < n;) {
    size_t j = i;
    while (j < n && p[j] != '/') j++;
    size_t len = j - i;
    if (len == 0 || (len == 1 && p[i] == '.')) {
      /* skip */
    } else if (len == 2 && p[i] == '.' && p[i + 1] == '.') {
      if (w > keep) {
        while (w > keep && out[w - 1] != '/') w--;
        if (w > keep) w--; /* the separator */
      } else if (!rooted) {
        if (w > 0) out[w++] = '/';
        out[w++] = '.';
        out[w++] = '.';
        keep = w;
      }
    } else {
      if (w > (size_t)rooted || (!rooted && w > 0)) out[w++] = '/';
      memcpy(out + w, p + i, len);
      w += len;
    }
    i = j + 1;
  }
  if (w == 0) out[w++] = '.';
  out[w] = 0;
  return out;
}

char *rec_dir(const char *p) {
  size_t i = strlen(p);
  while (i > 0 && p[i - 1] != '/') i--;
  char *head = malloc(i + 1);
  if (!head) return NULL;
  memcpy(head, p, i);
  head[i] = 0;
  char *d = rec_clean(head);
  free(head);
  return d;
}

char *rec_base(const char *p) {
  size_t n = strlen(p);
  if (n == 0) return rec_clean("");
  while (n > 0 && p[n - 1] == '/') n--;
  if (n == 0) return rec_clean("/");
  size_t i = n;
  while (i > 0 && p[i - 1] != '/') i--;
  char *b = malloc(n - i + 1);
  if (!b) return NULL;
  memcpy(b, p + i, n - i);
  b[n - i] = 0;
  return b;
}

char *rec_join(const char *a, const char *b) {
  if (!*a && !*b) {
    char *e = malloc(1);
    if (e) *e = 0;
    return e;
  }
  if (!*a) return rec_clean(b);
  if (!*b) return rec_clean(a);
  size_t x = strlen(a), y = strlen(b);
  char *both = malloc(x + y + 2);
  if (!both) return NULL;
  memcpy(both, a, x);
  both[x] = '/';
  memcpy(both + x + 1, b, y + 1);
  char *c = rec_clean(both);
  free(both);
  return c;
}

int rec_is_abs(const char *p) { return p[0] == '/'; }

/* ---- building records ---- */

lj_value *rec_obj(void) { return lj_new(LJ_OBJECT); }
lj_value *rec_str(const char *s) { return s ? lj_new_string(s, strlen(s)) : NULL; }
lj_value *rec_strn(const char *s, size_t n) { return s ? lj_new_string(s, n) : NULL; }
lj_value *rec_num(double v) { return lj_new_number(v); }
lj_value *rec_bool(int v) {
  lj_value *b = lj_new(LJ_BOOL);
  if (b) b->u.boolean = v != 0;
  return b;
}

int rec_set(lj_value *o, const char *key, lj_value *v) {
  if (!o) {
    lj_free(v);
    return -1;
  }
  return lj_object_set(o, key, strlen(key), v);
}

lj_value *rec_vertex(const rec *r, const char *id, const char *kind, lj_value *attrs, const char *operation) {
  lj_value *v = rec_obj(), *prov = rec_obj();
  lj_buf at = {0};
  rec_now_text(r, &at);
  char k[128];
  snprintf(k, sizeof k, REC_NS "/%s", kind);
  int bad = !attrs || at.oom;
  bad |= rec_set(prov, "source", rec_str("ctx")) | rec_set(prov, "operation", rec_str(operation)) |
         rec_set(prov, "at", rec_strn(at.ptr ? at.ptr : "", at.len));
  bad |= rec_set(v, "id", rec_str(id)) | rec_set(v, "kind", rec_str(k)) | rec_set(v, "provenance", prov);
  if (attrs && attrs->u.object.len) bad |= rec_set(v, "attributes", attrs);
  else lj_free(attrs);
  lj_buf_free(&at);
  if (bad) {
    lj_free(v);
    return NULL;
  }
  return v;
}

lj_value *rec_edge(const char *id, const char *from, const char *to, const char *type) {
  lj_value *e = rec_obj();
  char t[128];
  snprintf(t, sizeof t, REC_NS "/%s", type);
  if (rec_set(e, "id", rec_str(id)) | rec_set(e, "from", rec_str(from)) | rec_set(e, "to", rec_str(to)) | rec_set(e, "type", rec_str(t))) {
    lj_free(e);
    return NULL;
  }
  return e;
}

/* ---- the store ---- */

static int parse_out(int rc, char *out, size_t len, lj_value **v) {
  *v = NULL;
  if (rc) return rc;
  *v = lj_parse(out, len, NULL);
  ext_ledger_free(out);
  return *v ? 0 : EXT_LEDGER_IO;
}

int rec_apply(rec *r, const ext_ledger_context *ctx, lj_value *tx) {
  if (!tx) return EXT_LEDGER_IO;
  lj_buf b = {0};
  lj_write(&b, tx);
  lj_free(tx);
  if (b.oom) {
    lj_buf_free(&b);
    return EXT_LEDGER_IO;
  }
  int rc = ext_ledger_store_apply(r->store, ctx, b.ptr, b.len, NULL, NULL);
  lj_buf_free(&b);
  return rc;
}

int rec_get(rec *r, const ext_ledger_context *ctx, int edge, const char *id, lj_value **out) {
  char *text = NULL;
  size_t len = 0;
  int rc = edge ? ext_ledger_store_get_edge(r->store, ctx, REC_NS, id, &text, &len) : ext_ledger_store_get_vertex(r->store, ctx, REC_NS, id, &text, &len);
  return parse_out(rc, text, len, out);
}

int rec_query_edges(rec *r, const ext_ledger_context *ctx, const char *type, const char *from, int64_t limit, lj_value **out) {
  lj_value *q = rec_obj();
  char t[128];
  snprintf(t, sizeof t, REC_NS "/%s", type);
  if (rec_set(q, "namespace", rec_str(REC_NS)) | rec_set(q, "type", rec_str(t)) | (from ? rec_set(q, "from", rec_str(from)) : 0) |
      rec_set(q, "limit", rec_num((double)limit))) {
    lj_free(q);
    return EXT_LEDGER_IO;
  }
  lj_buf b = {0};
  lj_write(&b, q);
  lj_free(q);
  char *text = NULL;
  size_t len = 0;
  int rc = b.oom ? EXT_LEDGER_IO : ext_ledger_store_query_edges(r->store, ctx, b.ptr, b.len, &text, &len);
  lj_buf_free(&b);
  return parse_out(rc, text, len, out);
}

int rec_snapshot(rec *r, const ext_ledger_context *ctx, lj_value **out) {
  char *text = NULL;
  size_t len = 0;
  int rc = ext_ledger_store_snapshot(r->store, ctx, REC_NS, &text, &len); /* before parse_out reads text and len */
  return parse_out(rc, text, len, out);
}

int rec_equal_attributes(const lj_value *a, const lj_value *b) {
  size_t na = 0, nb = 0;
  for (size_t i = 0; a && i < a->u.object.len; i++) {
    const lj_member *m = &a->u.object.members[i];
    if (strcmp(m->key.ptr, "observed_at") == 0) continue;
    na++;
    const lj_value *other = lj_get(b, m->key.ptr);
    if (!other || !lj_equal(m->value, other)) return 0;
  }
  for (size_t i = 0; b && i < b->u.object.len; i++)
    if (strcmp(b->u.object.members[i].key.ptr, "observed_at") != 0) nb++;
  return na == nb;
}

/* ---- opening and dispatch ---- */

int ext_ledger_recorder_open(ext_ledger_store *store, const ext_ledger_context *ctx, const char *environment, size_t len, ext_ledger_recorder **out) {
  if (out) *out = NULL;
  if (!store || !out || !environment) return EXT_LEDGER_INVALID;
  int oom = 0;
  lj_value *v = lj_parse(environment, len, &oom);
  if (!v) return oom ? EXT_LEDGER_IO : EXT_LEDGER_INVALID;
  rec *r = calloc(1, sizeof *r);
  int rc = r ? env_decode(v, &r->env) : EXT_LEDGER_IO;
  lj_free(v);
  if (!rc) rc = ext_ledger_store_register(store, ctx, ll_vocabulary, strlen(ll_vocabulary));
  if (rc) {
    if (r) env_free(&r->env);
    free(r);
    return rc;
  }
  r->store = store;
  *out = r;
  return 0;
}

int ext_ledger_recorder_open_machine(ext_ledger_store *store, const ext_ledger_context *ctx, ext_ledger_recorder **out) {
  if (out) *out = NULL;
  char *env = ll_machine_environment();
  if (!env) return EXT_LEDGER_IO;
  int rc = ext_ledger_recorder_open(store, ctx, env, strlen(env), out);
  free(env);
  if (!rc) (*out)->env.machine = 1;
  return rc;
}

void ext_ledger_recorder_close(ext_ledger_recorder *r) {
  if (!r) return;
  env_free(&r->env);
  free(r);
}

int ext_ledger_recorder_record(ext_ledger_recorder *r, const ext_ledger_context *ctx, const char *op, const char *input, size_t len, char **out,
                      size_t *out_len) {
  if (out) *out = NULL;
  if (out_len) *out_len = 0;
  if (!r || !op) return EXT_LEDGER_INVALID;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  lj_value *in = NULL;
  if (input && len) {
    int oom = 0;
    if (!(in = lj_parse(input, len, &oom))) return oom ? EXT_LEDGER_IO : EXT_LEDGER_INVALID;
    if (in->type != LJ_OBJECT) {
      lj_free(in);
      return EXT_LEDGER_INVALID;
    }
  }
  if (strcmp(op, "observe_shell") == 0) rc = rec_observe_shell(r, ctx, in);
  else if (strcmp(op, "observe_web") == 0) rc = rec_observe_web(r, ctx, in);
  else if (strcmp(op, "observe_host") == 0) rc = rec_observe_host(r, ctx, in);
  else if (strcmp(op, "observe_processes") == 0) rc = rec_observe_processes(r, ctx, in);
  else if (strcmp(op, "observe_inventory") == 0) rc = rec_observe_inventory(r, ctx, in);
  else if (strcmp(op, "resolve_inventory") == 0) rc = out ? rec_resolve_inventory(r, ctx, in, out, out_len) : EXT_LEDGER_INVALID;
  else if (strcmp(op, "read_host") == 0) rc = out ? rec_read_host(r, ctx, out, out_len) : EXT_LEDGER_INVALID;
  else rc = EXT_LEDGER_UNSUPPORTED;
  lj_free(in);
  return rc;
}
