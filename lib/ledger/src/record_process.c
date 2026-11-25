/* Processes: observe_processes (recorder/v1). A snapshot of
 * res/system/service/process becomes the machine's process tree, the
 * executables and applications its processes run, and their resources.
 * Records are reconciled against the stored graph at its revision. Values take
 * the shapes the Go types give them when they are encoded, so the attributes
 * and identities are the same in every implementation. */
#include "recorder.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* ---- reading the observation ---- */

static const char *str(const lj_value *o, const char *key) { return rec_field(o, key); }

static double num(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_NUMBER ? v->u.number : 0;
}

static int valid_state(const char *s) {
  static const char *const states[] = {"complete", "partial", "permission-denied", "unsupported", "exited", "unavailable"};
  for (size_t i = 0; i < sizeof states / sizeof *states; i++)
    if (strcmp(s, states[i]) == 0) return 1;
  return 0;
}

static int resource_kind(const char *k) { return !strcmp(k, "file") || !strcmp(k, "mapping") || !strcmp(k, "socket") || !strcmp(k, "ipc"); }

/* A time as Go prints it after decoding it, or NULL when it is not a time. */
static lj_value *time_text(const char *s, ll_time *t) {
  ll_time local;
  if (!t) t = &local;
  if (ll_time_parse(s, strlen(s), t)) return NULL;
  lj_buf b = {0};
  ll_time_write(&b, t);
  lj_value *v = b.oom || b.len < 2 ? NULL : rec_strn(b.ptr + 1, b.len - 2);
  lj_buf_free(&b);
  return v;
}

static int before(const ll_time *a, const ll_time *b) { return a->sec < b->sec || (a->sec == b->sec && a->nsec < b->nsec); }

/* ---- the shapes of the Go types ---- */

static int put_str(lj_value *o, const char *key, const char *v, int omit_empty) {
  if (omit_empty && !*v) return 0;
  return rec_set(o, key, rec_str(v));
}

/* CollectionStatus: state, reason, detail, observed_at. */
static lj_value *status_shape(const lj_value *s) {
  lj_value *o = rec_obj();
  int bad = put_str(o, "state", str(s, "state"), 0) | put_str(o, "reason", str(s, "reason"), 1) | put_str(o, "detail", str(s, "detail"), 1);
  if (*str(s, "observed_at")) {
    lj_value *t = time_text(str(s, "observed_at"), NULL);
    bad |= t ? rec_set(o, "observed_at", t) : -1;
  }
  if (bad) {
    lj_free(o);
    return NULL;
  }
  return o;
}

/* ProcessInfo without its resources. */
static lj_value *process_shape(const lj_value *p) {
  lj_value *o = rec_obj();
  int bad = rec_set(o, "pid", rec_num(num(p, "pid"))) | rec_set(o, "parent_pid", rec_num(num(p, "parent_pid"))) |
            put_str(o, "name", str(p, "name"), 0) | put_str(o, "start_id", str(p, "start_id"), 1) | put_str(o, "executable", str(p, "executable"), 1) |
            put_str(o, "owner", str(p, "owner"), 1);
  const lj_value *app = lj_get(p, "application");
  if (app && app->type == LJ_OBJECT) {
    lj_value *a = rec_obj();
    bad |= put_str(a, "name", str(app, "name"), 0) | put_str(a, "path", str(app, "path"), 0) | rec_set(o, "application", a);
  }
  const lj_value *usage = lj_get(p, "usage");
  if (usage && usage->type == LJ_OBJECT) {
    lj_value *u = rec_obj();
    static const char *const keys[] = {"resident_bytes", "virtual_bytes", "cpu_seconds", "threads"};
    for (int i = 0; i < 4; i++) {
      const lj_value *v = lj_get(usage, keys[i]);
      if (v && v->type == LJ_NUMBER) bad |= rec_set(u, keys[i], rec_num(v->u.number));
    }
    bad |= rec_set(o, "usage", u);
  }
  const lj_value *coverage = lj_get(p, "coverage");
  if (coverage && coverage->type == LJ_OBJECT) {
    lj_value *c = rec_obj();
    for (size_t i = 0; i < coverage->u.object.len; i++) {
      const lj_member *m = &coverage->u.object.members[i];
      bad |= lj_object_set(c, m->key.ptr, m->key.len, status_shape(m->value));
    }
    bad |= rec_set(o, "coverage", c);
  } else {
    bad |= rec_set(o, "coverage", lj_new(LJ_NULL));
  }
  if (bad) {
    lj_free(o);
    return NULL;
  }
  return o;
}

static const char *const resource_fields[] = {"kind", "path", "descriptor", "protocol", "local_address", "remote_address", "endpoint", "state"};

static lj_value *resource_shape(const lj_value *r) {
  lj_value *o = rec_obj();
  int bad = 0;
  for (int i = 0; i < 8; i++) bad |= put_str(o, resource_fields[i], str(r, resource_fields[i]), i > 0);
  if (bad) {
    lj_free(o);
    return NULL;
  }
  return o;
}

/* A string as Go's encoding/json writes it: HTML characters and U+2028/U+2029 escaped. */
static void go_string(lj_buf *b, const char *s) {
  static const char hexd[] = "0123456789abcdef";
  lj_buf_putc(b, '"');
  for (const unsigned char *p = (const unsigned char *)s; *p; p++) {
    unsigned char c = *p;
    if (c == '"' || c == '\\') {
      lj_buf_putc(b, '\\');
      lj_buf_putc(b, (char)c);
    } else if (c == '\n') lj_buf_puts(b, "\\n");
    else if (c == '\r') lj_buf_puts(b, "\\r");
    else if (c == '\t') lj_buf_puts(b, "\\t");
    else if (c < 0x20 || c == '<' || c == '>' || c == '&') {
      char e[6] = {'\\', 'u', '0', '0', hexd[c >> 4], hexd[c & 15]};
      lj_buf_put(b, e, 6);
    } else if (c == 0xE2 && p[1] == 0x80 && (p[2] == 0xA8 || p[2] == 0xA9)) {
      lj_buf_puts(b, p[2] == 0xA8 ? "\\u2028" : "\\u2029");
      p += 2;
    } else lj_buf_putc(b, (char)c);
  }
  lj_buf_putc(b, '"');
}

/* json.Marshal of a ProcessResource: its fields in declaration order, empty ones left out but kind. */
static void go_resource(lj_buf *b, const lj_value *r) {
  lj_buf_putc(b, '{');
  int first = 1;
  for (int i = 0; i < 8; i++) {
    const char *v = str(r, resource_fields[i]);
    if (i > 0 && !*v) continue;
    if (!first) lj_buf_putc(b, ',');
    first = 0;
    lj_buf_putc(b, '"');
    lj_buf_puts(b, resource_fields[i]);
    lj_buf_puts(b, "\":");
    go_string(b, v);
  }
  lj_buf_putc(b, '}');
}

/* ---- the transaction ---- */

typedef struct {
  rec *r;
  const char *host;
  lj_value *provenance; /* source host-process, at the observation's time */
  lj_value *vertices, *edges; /* keyed by ID */
  int oom;
} proc_tx;

static void digest_of(char out[25], const lj_buf *b) { rec_digest(b->ptr ? b->ptr : "", b->len, out); }

static void add_vertex(proc_tx *t, const char *id, const char *kind, lj_value *attrs, const char *owner) {
  lj_value *v = rec_obj(), *labels = rec_obj();
  char k[96];
  snprintf(k, sizeof k, REC_NS "/%s", kind);
  int bad = (!attrs) | put_str(labels, "collector", "process", 0) | put_str(labels, "host_id", t->host, 0);
  if (owner) bad |= put_str(labels, "process_id", owner, 0);
  bad |= rec_set(v, "id", rec_str(id)) | rec_set(v, "kind", rec_str(k)) | rec_set(v, "labels", labels) |
         rec_set(v, "provenance", lj_clone(t->provenance));
  if (attrs && attrs->u.object.len) bad |= rec_set(v, "attributes", attrs);
  else lj_free(attrs);
  if (bad || lj_object_set(t->vertices, id, strlen(id), v)) t->oom = 1;
}

static void add_edge(proc_tx *t, const char *from, const char *to, const char *relation) {
  lj_buf key = {0};
  lj_buf_puts(&key, from);
  lj_buf_putc(&key, 0);
  lj_buf_puts(&key, relation);
  lj_buf_putc(&key, 0);
  lj_buf_puts(&key, to);
  char d[25], id[64];
  digest_of(d, &key);
  lj_buf_free(&key);
  snprintf(id, sizeof id, "process-edge/%s", d);
  lj_value *e = rec_edge(id, from, to, relation);
  if (!e || rec_set(e, "provenance", lj_clone(t->provenance)) || lj_object_set(t->edges, id, strlen(id), e)) t->oom = 1;
}

static char *instance_id(const char *host, const lj_value *p) {
  lj_buf b = {0};
  char pid[24];
  rec_itoa((int64_t)num(p, "pid"), pid);
  lj_buf_puts(&b, host);
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, pid);
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, str(p, "start_id"));
  if (b.oom) { lj_buf_free(&b); return NULL; }
  char d[25];
  digest_of(d, &b);
  lj_buf_free(&b);
  char *id = malloc(48);
  if (id) snprintf(id, 48, "host-process/%s", d);
  return id;
}

/* Expose the recorder's exact identity rule, without collecting or granting
 * authority. A missing birth token cannot identify a process instance. */
int ext_ledger_process_identity(const char *host, int64_t pid, const char *start,
                                char **out, size_t *out_len) {
  if (out) *out = NULL;
  if (!host || !*host || strlen(host) > 4096 || !start || !*start || strlen(start) > 4096 ||
      pid < 1 || pid > 2147483647 || !out || !out_len) return EXT_LEDGER_INVALID;
  lj_value *p = rec_obj();
  int bad = rec_set(p,"pid",rec_num((double)pid)) | rec_set(p,"start_id",rec_str(start));
  char *id = bad ? NULL : instance_id(host,p);
  lj_free(p);
  if (!id) return EXT_LEDGER_UNAVAILABLE;
  lj_value *v = rec_obj();
  bad = rec_set(v,"namespace",rec_str(REC_NS)) | rec_set(v,"id",rec_str(id)) |
    rec_set(v,"host_id",rec_str(host)) | rec_set(v,"pid",rec_num((double)pid)) | rec_set(v,"start_id",rec_str(start));
  free(id);
  lj_buf b = {0};
  if (!bad) lj_write(&b,v);
  lj_free(v);
  if (bad || b.oom) { lj_buf_free(&b); return EXT_LEDGER_UNAVAILABLE; }
  *out = lj_buf_take(&b,out_len);
  return *out ? EXT_LEDGER_OK : EXT_LEDGER_UNAVAILABLE;
}

static ll_time provenance_at(const lj_value *v) {
  ll_time t = {0, 0, 0, 0};
  const char *s = rec_field(lj_get(v, "provenance"), "at");
  if (*s) ll_time_parse(s, strlen(s), &t);
  return t;
}

static const char *label(const lj_value *v, const char *key) { return rec_field(lj_get(v, "labels"), key); }

static void mark(ll_map *m, const char *id, int *oom) {
  if (ll_map_put(m, id, strlen(id), (void *)1)) *oom = 1;
}

static const lj_value *find_process(const lj_value *processes, int64_t pid) {
  for (size_t i = 0; processes && i < processes->u.array.len; i++)
    if ((int64_t)num(processes->u.array.items[i], "pid") == pid) return processes->u.array.items[i];
  return NULL;
}

/* Builds the transaction that reconciles the snapshot with the observation. */
static int process_transaction(rec *r, const lj_value *snap, const lj_value *obs, lj_value **out) {
  *out = NULL;
  const char *host = str(obs, "host_id"), *scope = str(obs, "scope");
  ll_time observed;
  lj_value *observed_text = time_text(str(obs, "observed_at"), &observed);
  if (!observed_text) return EXT_LEDGER_INVALID;
  proc_tx t = {r, host, rec_obj(), rec_obj(), rec_obj(), 0};
  t.oom |= rec_set(t.provenance, "source", rec_str("host-process")) | rec_set(t.provenance, "operation", rec_str("process-observed")) |
           rec_set(t.provenance, "at", lj_clone(observed_text));
  const lj_value *processes = lj_get(obs, "processes");
  if (processes && processes->type != LJ_ARRAY) processes = NULL;
  const lj_value *svertices = lj_get(snap, "vertices"), *sedges = lj_get(snap, "edges");
  int rc = 0;

  ll_map old_vertices = {0}, old_edges = {0}, remove = {0}, ids = {0};
  for (size_t i = 0; svertices && i < svertices->u.array.len; i++) {
    const lj_value *v = svertices->u.array.items[i];
    if (ll_map_put(&old_vertices, str(v, "id"), strlen(str(v, "id")), (void *)v)) t.oom = 1;
  }
  for (size_t i = 0; sedges && i < sedges->u.array.len; i++) {
    const lj_value *e = sedges->u.array.items[i];
    if (ll_map_put(&old_edges, str(e, "id"), strlen(str(e, "id")), (void *)e)) t.oom = 1;
  }
  if (!ll_map_get(&old_vertices, "machine/local", 13)) {
    lj_value *m = rec_obj();
    t.oom |= put_str(m, "hostname", r->env.hostname, 0) | put_str(m, "os", r->env.os, 0) | put_str(m, "architecture", r->env.arch, 0);
    lj_value *v = rec_obj();
    t.oom |= rec_set(v, "id", rec_str("machine/local")) | rec_set(v, "kind", rec_str(REC_NS "/machine")) | rec_set(v, "attributes", m) |
             rec_set(v, "provenance", lj_clone(t.provenance)) | lj_object_set(t.vertices, "machine/local", 13, v);
  }
  const char *marker = "host-inventory/processes";
  const lj_value *old_marker = ll_map_get(&old_vertices, marker, strlen(marker));
  if (old_marker) {
    ll_time at = provenance_at(old_marker);
    if (before(&observed, &at)) rc = EXT_LEDGER_INVALID;
  }
  if (!rc && strcmp(scope, "all") == 0) {
    lj_value *a = rec_obj(), *unidentified = lj_new(LJ_ARRAY);
    t.oom |= put_str(a, "host_id", host, 0) | rec_set(a, "enumeration", status_shape(lj_get(obs, "enumeration"))) |
             rec_set(a, "count", rec_num(processes ? (double)processes->u.array.len : 0)) | rec_set(a, "observed_at", lj_clone(observed_text)) |
             put_str(a, "kind", "process", 0);
    for (size_t i = 0; processes && i < processes->u.array.len; i++)
      if (!*str(processes->u.array.items[i], "start_id") && lj_array_push(unidentified, process_shape(processes->u.array.items[i]))) t.oom = 1;
    if (unidentified && unidentified->u.array.len) t.oom |= rec_set(a, "unidentified_processes", unidentified);
    else lj_free(unidentified);
    add_vertex(&t, marker, "host-inventory", a, NULL);
    add_edge(&t, "machine/local", marker, "has-host-inventory");
  }
  for (size_t i = 0; processes && i < processes->u.array.len; i++) {
    const lj_value *p = processes->u.array.items[i];
    if (!*str(p, "start_id")) continue;
    char key[24], *id = instance_id(host, p);
    rec_itoa((int64_t)num(p, "pid"), key);
    if (!id || ll_map_put(&ids, key, strlen(key), id)) {
      free(id);
      t.oom = 1;
    }
  }
  /* Processes that were replaced by a new instance, or are gone from a complete enumeration. */
  for (size_t i = 0; !rc && svertices && i < svertices->u.array.len; i++) {
    const lj_value *v = svertices->u.array.items[i];
    if (strcmp(str(v, "kind"), REC_NS "/process") != 0) continue;
    int64_t pid = (int64_t)num(lj_get(v, "attributes"), "pid");
    const lj_value *incoming = find_process(processes, pid);
    int same_host = strcmp(label(v, "host_id"), host) == 0;
    char key[24];
    rec_itoa(pid, key);
    const char *new_id = ll_map_get(&ids, key, strlen(key));
    int replace = same_host && incoming && *str(incoming, "start_id") && (!new_id || strcmp(new_id, str(v, "id")) != 0);
    int absent = strcmp(scope, "all") == 0 && strcmp(str(lj_get(obs, "enumeration"), "state"), "complete") == 0 && (!same_host || !incoming);
    if (replace || absent) {
      ll_time at = provenance_at(v);
      if (before(&observed, &at)) rc = EXT_LEDGER_INVALID;
      mark(&remove, str(v, "id"), &t.oom);
    }
  }
  for (size_t i = 0; !rc && processes && i < processes->u.array.len; i++) {
    const lj_value *p = processes->u.array.items[i];
    char key[24];
    rec_itoa((int64_t)num(p, "pid"), key);
    const char *id = ll_map_get(&ids, key, strlen(key));
    if (!id) continue;
    const lj_value *old = ll_map_get(&old_vertices, id, strlen(id));
    if (old) {
      ll_time at = provenance_at(old);
      if (before(&observed, &at)) {
        rc = EXT_LEDGER_INVALID;
        break;
      }
    }
    lj_value *attrs = process_shape(p);
    if (!attrs) {
      t.oom = 1;
      break;
    }
    /* An identity-only scan keeps the dated resource coverage and usage it did not refresh. */
    if (old) {
      const lj_value *previous = lj_get(lj_get(old, "attributes"), "coverage");
      lj_value *coverage = lj_get(attrs, "coverage");
      if (coverage && coverage->type != LJ_OBJECT) {
        t.oom |= rec_set(attrs, "coverage", rec_obj());
        coverage = lj_get(attrs, "coverage");
      }
      const lj_value *given = lj_get(p, "coverage");
      for (size_t c = 0; previous && previous->type == LJ_OBJECT && c < previous->u.object.len; c++) {
        const lj_member *m = &previous->u.object.members[c];
        if (!resource_kind(m->key.ptr) && strcmp(m->key.ptr, "usage") != 0) continue;
        if (lj_get(given, m->key.ptr)) continue;
        t.oom |= lj_object_set(coverage, m->key.ptr, m->key.len, lj_clone(m->value));
        if (strcmp(m->key.ptr, "usage") == 0 && lj_get(lj_get(old, "attributes"), "usage"))
          t.oom |= rec_set(attrs, "usage", lj_clone(lj_get(lj_get(old, "attributes"), "usage")));
      }
    }
    t.oom |= put_str(attrs, "host_id", host, 0) | rec_set(attrs, "observed_at", lj_clone(observed_text));
    add_vertex(&t, id, "process", attrs, NULL);
    add_edge(&t, "machine/local", id, "runs");
    /* Each identity refresh replaces the parent, executable and application relations. */
    for (size_t e = 0; sedges && e < sedges->u.array.len; e++) {
      const lj_value *edge = sedges->u.array.items[e];
      const char *type = str(edge, "type");
      int parent_of = strcmp(type, REC_NS "/parent-of") == 0;
      int owned = parent_of ? strcmp(str(edge, "to"), id) == 0
                            : (strcmp(type, REC_NS "/executes") == 0 || strcmp(type, REC_NS "/belongs-to-application") == 0) &&
                                  strcmp(str(edge, "from"), id) == 0;
      if (!owned) continue;
      if (strcmp(scope, "process") == 0 && parent_of) {
        const lj_value *parent = ll_map_get(&old_vertices, str(edge, "from"), strlen(str(edge, "from")));
        if (parent && strcmp(label(parent, "host_id"), host) == 0 &&
            (int64_t)num(lj_get(parent, "attributes"), "pid") == (int64_t)num(p, "parent_pid"))
          continue;
      }
      ll_map_del(&old_edges, str(edge, "id"), strlen(str(edge, "id")));
    }
    char pkey[24];
    rec_itoa((int64_t)num(p, "parent_pid"), pkey);
    const char *parent = ll_map_get(&ids, pkey, strlen(pkey));
    if (parent && strcmp(parent, id) != 0) add_edge(&t, parent, id, "parent-of");
    char d[25], vid[64];
    if (*str(p, "executable")) {
      lj_buf k = {0};
      lj_buf_puts(&k, host);
      lj_buf_putc(&k, 0);
      lj_buf_puts(&k, str(p, "executable"));
      digest_of(d, &k);
      lj_buf_free(&k);
      snprintf(vid, sizeof vid, "process-executable/%s", d);
      lj_value *a = rec_obj();
      t.oom |= put_str(a, "path", str(p, "executable"), 0);
      add_vertex(&t, vid, "executable", a, NULL);
      add_edge(&t, id, vid, "executes");
    }
    const lj_value *app = lj_get(p, "application");
    if (app && app->type == LJ_OBJECT && *str(app, "path")) {
      lj_buf k = {0};
      lj_buf_puts(&k, host);
      lj_buf_putc(&k, 0);
      lj_buf_puts(&k, str(app, "path"));
      digest_of(d, &k);
      lj_buf_free(&k);
      snprintf(vid, sizeof vid, "process-application/%s", d);
      lj_value *a = rec_obj();
      t.oom |= put_str(a, "name", str(app, "name"), 0) | put_str(a, "path", str(app, "path"), 0);
      add_vertex(&t, vid, "application", a, NULL);
      add_edge(&t, id, vid, "belongs-to-application");
    }
    /* Resources of a kind this scan collected are replaced by what it found. */
    const lj_value *coverage = lj_get(p, "coverage");
    for (size_t v = 0; svertices && v < svertices->u.array.len; v++) {
      const lj_value *old_resource = svertices->u.array.items[v];
      if (strcmp(label(old_resource, "process_id"), id) != 0) continue;
      if (lj_get(coverage, str(lj_get(old_resource, "attributes"), "kind"))) mark(&remove, str(old_resource, "id"), &t.oom);
    }
    const lj_value *resources = lj_get(p, "resources");
    for (size_t k = 0; resources && resources->type == LJ_ARRAY && k < resources->u.array.len; k++) {
      const lj_value *res = resources->u.array.items[k];
      lj_value *a = resource_shape(res);
      t.oom |= !a || rec_set(a, "observed_at", lj_clone(observed_text));
      lj_buf key2 = {0};
      lj_buf_puts(&key2, id);
      go_resource(&key2, res);
      digest_of(d, &key2);
      lj_buf_free(&key2);
      snprintf(vid, sizeof vid, "process-resource/%s", d);
      add_vertex(&t, vid, "process-resource", a, id);
      add_edge(&t, id, vid, "uses-resource");
      ll_map_del(&remove, vid, strlen(vid));
    }
  }
  if (!rc) {
    /* The resources of removed processes go with them. */
    for (size_t i = 0; svertices && i < svertices->u.array.len; i++) {
      const lj_value *v = svertices->u.array.items[i];
      const char *owner = label(v, "process_id");
      if (*owner && ll_map_get(&remove, owner, strlen(owner))) mark(&remove, str(v, "id"), &t.oom);
    }
    LL_MAP_EACH(&old_edges, slot) {
      const lj_value *e = slot->value;
      if (ll_map_get(&remove, str(e, "from"), strlen(str(e, "from"))) || ll_map_get(&remove, str(e, "to"), strlen(str(e, "to"))))
        ll_map_del(&old_edges, LL_S(slot->key), slot->key.n);
    }
    for (size_t i = 0; i < t.edges->u.object.len; i++) {
      const lj_member *m = &t.edges->u.object.members[i];
      if (ll_map_put(&old_edges, m->key.ptr, m->key.len, m->value)) t.oom = 1;
    }
    /* Executables and applications this collector owns that nothing refers to any more. */
    ll_map referenced = {0};
    LL_MAP_EACH(&old_edges, slot) {
      const lj_value *e = slot->value;
      mark(&referenced, str(e, "from"), &t.oom);
      mark(&referenced, str(e, "to"), &t.oom);
    }
    for (size_t i = 0; svertices && i < svertices->u.array.len; i++) {
      const lj_value *v = svertices->u.array.items[i];
      const char *kind = str(v, "kind");
      if (strcmp(label(v, "collector"), "process") == 0 &&
          (strcmp(kind, REC_NS "/application") == 0 || strcmp(kind, REC_NS "/executable") == 0) &&
          !ll_map_get(&referenced, str(v, "id"), strlen(str(v, "id"))))
        mark(&remove, str(v, "id"), &t.oom);
    }
    ll_map_free(&referenced);
  }
  lj_value *tx = NULL;
  if (!rc && !t.oom) {
    tx = rec_obj();
    lj_value *dv = lj_new(LJ_ARRAY), *de = lj_new(LJ_ARRAY), *vs = lj_new(LJ_ARRAY), *es = lj_new(LJ_ARRAY);
    int bad = !dv || !de || !vs || !es;
    LL_MAP_EACH(&remove, slot) {
      lj_value *ref = rec_obj();
      bad |= rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_strn(LL_S(slot->key), slot->key.n)) | lj_array_push(dv, ref);
    }
    for (size_t i = 0; sedges && i < sedges->u.array.len; i++) {
      const char *id = str(sedges->u.array.items[i], "id");
      if (ll_map_get(&old_edges, id, strlen(id))) continue;
      lj_value *ref = rec_obj();
      bad |= rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_str(id)) | lj_array_push(de, ref);
    }
    for (size_t i = 0; i < t.vertices->u.object.len; i++) bad |= lj_array_push(vs, lj_clone(t.vertices->u.object.members[i].value));
    for (size_t i = 0; i < t.edges->u.object.len; i++) bad |= lj_array_push(es, lj_clone(t.edges->u.object.members[i].value));
    bad |= rec_set(tx, "namespace", rec_str(REC_NS)) | rec_set(tx, "expected_revision", rec_num(num(snap, "revision"))) |
           rec_set(tx, "delete_incident_edges", rec_bool(1)) | rec_set(tx, "delete_vertices", dv) | rec_set(tx, "delete_edges", de) |
           rec_set(tx, "vertices", vs) | rec_set(tx, "edges", es);
    if (bad) {
      lj_free(tx);
      tx = NULL;
      t.oom = 1;
    }
  }
  LL_MAP_EACH(&ids, slot) free(slot->value);
  ll_map_free(&ids);
  ll_map_free(&old_vertices);
  ll_map_free(&old_edges);
  ll_map_free(&remove);
  lj_free(t.provenance);
  lj_free(t.vertices);
  lj_free(t.edges);
  lj_free(observed_text);
  if (!rc && t.oom) rc = EXT_LEDGER_IO;
  *out = tx;
  return rc;
}

int rec_observe_processes(rec *r, const ext_ledger_context *ctx, const lj_value *in) {
  ll_time observed;
  const char *scope = str(in, "scope");
  lj_value *at = time_text(str(in, "observed_at"), &observed);
  int bad = !*str(in, "host_id") || !at || ll_time_is_zero(&observed);
  lj_free(at);
  if (bad) return EXT_LEDGER_INVALID;
  if (strcmp(scope, "all") != 0 && strcmp(scope, "process") != 0) return EXT_LEDGER_INVALID;
  const lj_value *processes = lj_get(in, "processes");
  size_t n = processes && processes->type == LJ_ARRAY ? processes->u.array.len : 0;
  if (strcmp(scope, "process") == 0 && n != 1) return EXT_LEDGER_INVALID;
  if (!valid_state(str(lj_get(in, "enumeration"), "state"))) return EXT_LEDGER_INVALID;
  for (size_t i = 0; i < n; i++) {
    const lj_value *p = processes->u.array.items[i];
    double pid = num(p, "pid");
    if (pid <= 0 || pid > 2147483647.0 || pid != floor(pid)) return EXT_LEDGER_INVALID;
    for (size_t k = 0; k < i; k++)
      if (num(processes->u.array.items[k], "pid") == pid) return EXT_LEDGER_INVALID;
    const lj_value *coverage = lj_get(p, "coverage");
    for (size_t c = 0; coverage && coverage->type == LJ_OBJECT && c < coverage->u.object.len; c++)
      if (!valid_state(str(coverage->u.object.members[c].value, "state"))) return EXT_LEDGER_INVALID;
    const lj_value *resources = lj_get(p, "resources");
    for (size_t k = 0; resources && resources->type == LJ_ARRAY && k < resources->u.array.len; k++) {
      const char *kind = str(resources->u.array.items[k], "kind");
      if (!resource_kind(kind) || !lj_get(coverage, kind)) return EXT_LEDGER_INVALID;
    }
  }
  for (int attempt = 0; attempt < 3; attempt++) {
    lj_value *snap, *tx;
    int rc = rec_snapshot(r, ctx, &snap);
    if (rc) return rc;
    rc = process_transaction(r, snap, in, &tx);
    lj_free(snap);
    if (rc) return rc;
    rc = rec_apply(r, ctx, tx);
    if (rc != EXT_LEDGER_CHANGED) return rc;
  }
  return EXT_LEDGER_BUSY; /* the process inventory changed during the observation */
}
