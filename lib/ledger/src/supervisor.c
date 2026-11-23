/* The process supervisor (pkg/ext-go/engine/ledger/supervisor in Go). It runs local component
 * processes and projects their observed lifecycle into the ext.runtime
 * namespace. An instance's state changes under its lock, and its graph fact is
 * applied before that lock is released, so an observer never sees a state the
 * graph has not recorded, and the facts of one instance land in order. */
#include "process.h"
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define RT "ext.runtime"

static const char *const ST_STARTING = "starting", *const ST_RUNNING = "running", *const ST_READY = "ready", *const ST_STOPPING = "stopping",
                         *const ST_STOPPED = "stopped", *const ST_CRASHED = "crashed", *const ST_RESTARTING = "restarting",
                         *const ST_ORPHANED = "orphaned";

typedef struct sv_managed sv_managed;
typedef struct {
  char *id, *identity;
  int64_t pid;
  int alive;
} sv_orphan;

struct ext_ledger_supervisor {
  ext_ledger_store *store;
  int64_t log_limit, lease_ms, retain;
  int require_checksum, terminate_orphans;
  char *runtime_id;
  char owner[33];
  ll_mutex *mu;
  ll_cond *cv;
  sv_managed *list;
  sv_orphan *orphans;
  size_t norphans;
  int closed, stopping;
  ll_thread *lease;
};

struct sv_managed {
  ext_ledger_supervisor *s;
  char id[33];
  lj_value *spec;    /* normalised: artifact, endpoints, connections, dependencies, environment, args */
  lj_value *secrets; /* NAME -> value for every launch; never projected, wiped when freed */
  char *command, *path, *dir;
  int64_t max_attempts, initial_delay, max_delay, readiness_timeout;
  int await_ready;      /* readiness is the caller's to report */
  int64_t ready_pid;    /* the launch a report is for */
  int ready_result;     /* 0 none yet, 1 ready, 2 not ready */
  ll_mutex *mu;
  ll_cond *cv;
  ext_ledger_context ctx; /* canceled when stopped */
  const char *state, *health, *last_error;
  int64_t pid, crash_count;
  char identity[65];
  ll_time started, updated;
  int has_exit, exit_code;
  int done, exited, refs, linked;
  ll_thread *thread;
  ll_mutex *log_mu;
  char *log;
  size_t log_len;
  sv_managed *next;
};

/* ---- small helpers ---- */

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

static char *copy_string(const char *s) {
  if (!s) return NULL;
  size_t n = strlen(s) + 1;
  char *c = malloc(n);
  if (c) memcpy(c, s, n);
  return c;
}

static const char *str_field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : NULL;
}

static ll_time now_utc(void) { return ll_time_utc(ll_time_now()); }

static lj_value *time_value(const ll_time *t) {
  lj_buf b = {0};
  ll_time_write(&b, t);
  lj_value *v = b.oom || b.len < 2 ? NULL : lj_new_string(b.ptr + 1, b.len - 2);
  lj_buf_free(&b);
  return v;
}

static int time_before(const ll_time *a, const ll_time *b) { return a->sec < b->sec || (a->sec == b->sec && a->nsec < b->nsec); }

static int write_out(lj_value *v, char **out, size_t *out_len) {
  if (!v) return EXT_LEDGER_IO;
  lj_buf b = {0};
  lj_write(&b, v);
  lj_free(v);
  size_t len = 0;
  char *text = lj_buf_take(&b, &len);
  if (!text) return EXT_LEDGER_IO;
  *out = text;
  if (out_len) *out_len = len;
  return 0;
}

static int apply(ext_ledger_store *store, lj_value *tx) {
  if (!tx) return EXT_LEDGER_IO;
  char *text;
  size_t len;
  int rc = write_out(tx, &text, &len);
  if (rc) return rc;
  rc = ext_ledger_store_apply(store, NULL, text, len, NULL, NULL);
  free(text);
  return rc;
}

static int snapshot(ext_ledger_store *store, lj_value **out) {
  char *text = NULL;
  size_t len = 0;
  *out = NULL;
  int rc = ext_ledger_store_snapshot(store, NULL, RT, &text, &len);
  if (rc) return rc;
  int oom = 0;
  *out = lj_parse(text, len, &oom);
  ext_ledger_free(text);
  return *out ? 0 : EXT_LEDGER_IO;
}

static int is_active(const char *state) {
  return state == ST_STARTING || state == ST_RUNNING || state == ST_READY || state == ST_RESTARTING;
}

static const char *state_of(const char *s) {
  const char *const all[] = {ST_STARTING, ST_RUNNING, ST_READY, ST_STOPPING, ST_STOPPED, ST_CRASHED, ST_RESTARTING, ST_ORPHANED};
  for (size_t i = 0; s && i < sizeof all / sizeof *all; i++)
    if (strcmp(s, all[i]) == 0) return all[i];
  return NULL;
}

/* ---- the spec ---- */

static int nonempty(const lj_value *o, const char *key) {
  const char *s = str_field(o, key);
  return s && *s;
}

static int64_t int_field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_NUMBER && v->u.number > 0 ? (int64_t)v->u.number : 0;
}

static int valid_checksum(const char *c) {
  if (!c || !*c) return 1;
  if (strncmp(c, "sha256:", 7) != 0 || strlen(c + 7) != 64) return 0;
  for (const char *p = c + 7; *p; p++)
    if (!((*p >= '0' && *p <= '9') || ((*p | 32) >= 'a' && (*p | 32) <= 'f'))) return 0;
  return 1;
}

/* The spec with only the fields the supervisor reads, and those checked. */
static int normalise(const ext_ledger_supervisor *s, const lj_value *in, lj_value **out) {
  *out = NULL;
  if (!in || in->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  const lj_value *art = lj_get(in, "artifact");
  if (!art || art->type != LJ_OBJECT || !nonempty(art, "id") || !nonempty(art, "revision") || !nonempty(in, "command")) return EXT_LEDGER_INVALID;
  const char *checksum = str_field(art, "checksum");
  if (!checksum) checksum = "";
  if (s->require_checksum && !*checksum) return EXT_LEDGER_INVALID;
  if (!valid_checksum(checksum)) return EXT_LEDGER_INVALID;
  lj_value *n = rec_obj(), *a = rec_obj();
  int bad = set(a, "id", rec_str(str_field(art, "id"))) | set(a, "revision", rec_str(str_field(art, "revision"))) | set(a, "checksum", rec_str(checksum)) |
            set(n, "artifact", a) | set(n, "command", rec_str(str_field(in, "command")));
  if (bad) {
    lj_free(n);
    return EXT_LEDGER_IO;
  }
  int rc = 0;
  const lj_value *v;
  if ((v = lj_get(in, "dir")) && v->type != LJ_STRING && v->type != LJ_NULL) rc = EXT_LEDGER_INVALID;
  if (!rc && v && v->type == LJ_STRING) bad |= set(n, "dir", lj_clone(v));
  lj_value *args = lj_new(LJ_ARRAY);
  if (!rc && (v = lj_get(in, "args")) && v->type != LJ_NULL) {
    if (v->type != LJ_ARRAY) rc = EXT_LEDGER_INVALID;
    for (size_t i = 0; !rc && i < v->u.array.len; i++) {
      if (v->u.array.items[i]->type != LJ_STRING || memchr(v->u.array.items[i]->u.string.ptr, 0, v->u.array.items[i]->u.string.len)) rc = EXT_LEDGER_INVALID;
      else bad |= lj_array_push(args, lj_clone(v->u.array.items[i]));
    }
  }
  bad |= set(n, "args", args);
  lj_value *env = rec_obj();
  if (!rc && (v = lj_get(in, "environment")) && v->type != LJ_NULL) {
    if (v->type != LJ_OBJECT) rc = EXT_LEDGER_INVALID;
    for (size_t i = 0; !rc && i < v->u.object.len; i++) {
      const lj_member *m = &v->u.object.members[i];
      if (!m->key.len || memchr(m->key.ptr, '=', m->key.len) || memchr(m->key.ptr, 0, m->key.len) || m->value->type != LJ_STRING) rc = EXT_LEDGER_INVALID;
      else bad |= lj_object_set(env, m->key.ptr, m->key.len, lj_clone(m->value));
    }
  }
  bad |= set(n, "environment", env);
  if (!rc && (v = lj_get(in, "secret_references")) && v->type != LJ_NULL) rc = EXT_LEDGER_INVALID; /* secrets are given to start */
  if (!rc && (v = lj_get(in, "await_readiness")) && v->type != LJ_NULL && v->type != LJ_BOOL) rc = EXT_LEDGER_INVALID;
  /* IPC endpoints require unique IDs, addresses, and transports. */
  lj_value *endpoints = lj_new(LJ_ARRAY);
  if (!rc && (v = lj_get(in, "endpoints")) && v->type != LJ_NULL) {
    if (v->type != LJ_ARRAY) rc = EXT_LEDGER_INVALID;
    for (size_t i = 0; !rc && i < v->u.array.len; i++) {
      const lj_value *e = v->u.array.items[i];
      if (e->type != LJ_OBJECT || !nonempty(e, "id") || !nonempty(e, "address") || !nonempty(e, "transport")) {
        rc = EXT_LEDGER_INVALID;
        break;
      }
      for (size_t j = 0; j < endpoints->u.array.len; j++)
        if (strcmp(rec_field(endpoints->u.array.items[j], "id"), str_field(e, "id")) == 0) rc = EXT_LEDGER_INVALID;
      lj_value *x = rec_obj();
      const char *dir = str_field(e, "direction");
      bad |= set(x, "id", rec_str(str_field(e, "id"))) | set(x, "address", rec_str(str_field(e, "address"))) | set(x, "transport", rec_str(str_field(e, "transport"))) |
             (dir && *dir ? set(x, "direction", rec_str(dir)) : 0) | lj_array_push(endpoints, x);
    }
  }
  bad |= set(n, "endpoints", endpoints);
  /* IPC connections require unique IDs and declared endpoint references. */
  lj_value *connections = lj_new(LJ_ARRAY);
  if (!rc && (v = lj_get(in, "connections")) && v->type != LJ_NULL) {
    if (v->type != LJ_ARRAY) rc = EXT_LEDGER_INVALID;
    for (size_t i = 0; !rc && i < v->u.array.len; i++) {
      const lj_value *c = v->u.array.items[i];
      if (c->type != LJ_OBJECT || !nonempty(c, "id")) {
        rc = EXT_LEDGER_INVALID;
        break;
      }
      int src = 0, dst = 0;
      const char *sid = str_field(c, "source_endpoint"), *tid = str_field(c, "target_endpoint");
      for (size_t j = 0; j < endpoints->u.array.len; j++) {
        const char *id = rec_field(endpoints->u.array.items[j], "id");
        src |= sid && strcmp(id, sid) == 0;
        dst |= tid && strcmp(id, tid) == 0;
      }
      for (size_t j = 0; j < connections->u.array.len; j++)
        if (strcmp(rec_field(connections->u.array.items[j], "id"), str_field(c, "id")) == 0) src = 0;
      if (!src || !dst) {
        rc = EXT_LEDGER_INVALID;
        break;
      }
      lj_value *x = rec_obj(), *meta = lj_get(c, "metadata"), *m = rec_obj();
      if (meta && meta->type == LJ_OBJECT)
        for (size_t j = 0; j < meta->u.object.len; j++) {
          if (meta->u.object.members[j].value->type != LJ_STRING) rc = EXT_LEDGER_INVALID;
          else bad |= lj_object_set(m, meta->u.object.members[j].key.ptr, meta->u.object.members[j].key.len, lj_clone(meta->u.object.members[j].value));
        }
      else if (meta && meta->type != LJ_NULL) rc = EXT_LEDGER_INVALID;
      bad |= set(x, "id", rec_str(str_field(c, "id"))) | set(x, "source_endpoint", rec_str(sid)) | set(x, "target_endpoint", rec_str(tid));
      if (m && m->u.object.len) bad |= set(x, "metadata", m);
      else lj_free(m);
      bad |= lj_array_push(connections, x);
    }
  }
  bad |= set(n, "connections", connections);
  lj_value *deps = lj_new(LJ_ARRAY);
  if (!rc && (v = lj_get(in, "dependencies")) && v->type != LJ_NULL) {
    if (v->type != LJ_ARRAY) rc = EXT_LEDGER_INVALID;
    for (size_t i = 0; !rc && i < v->u.array.len; i++) {
      if (v->u.array.items[i]->type != LJ_STRING || !v->u.array.items[i]->u.string.len) rc = EXT_LEDGER_INVALID;
      else bad |= lj_array_push(deps, lj_clone(v->u.array.items[i]));
    }
  }
  bad |= set(n, "dependencies", deps);
  if (!rc && (v = lj_get(in, "restart")) && v->type != LJ_NULL && v->type != LJ_OBJECT) rc = EXT_LEDGER_INVALID;
  if (bad && !rc) rc = EXT_LEDGER_IO;
  if (rc) {
    lj_free(n);
    return rc;
  }
  *out = n;
  return 0;
}

/* The artifact's SHA-256 against its checksum; nothing to check without one. */
static int verify_artifact(const char *path, const char *expected) {
  if (!expected || !*expected) return 0;
  ll_file *f;
  if (ll_file_open(path, LL_READ, &f)) return EXT_LEDGER_NOT_FOUND; /* cannot open artifact for verification */
  uint64_t size = 0;
  int rc = ll_file_size(f, &size);
  char *data = rc ? NULL : malloc(size ? (size_t)size : 1);
  size_t got = 0;
  if (!rc && !data) rc = EXT_LEDGER_IO;
  for (uint64_t off = 0; !rc && off < size; off += got) {
    if ((rc = ll_file_read_at(f, off, data + off, (size_t)(size - off), &got))) break;
    if (!got) rc = EXT_LEDGER_IO;
  }
  ll_file_close(f);
  if (rc) {
    free(data);
    return EXT_LEDGER_IO; /* cannot read artifact for verification */
  }
  char actual[65];
  ll_sha256_hex(data, (size_t)size, actual);
  free(data);
  unsigned diff = 0;
  for (int i = 0; i < 64; i++) {
    char e = expected[7 + i];
    if (e >= 'A' && e <= 'F') e = (char)(e + 32);
    diff |= (unsigned)(actual[i] ^ e);
  }
  return diff ? EXT_LEDGER_DENIED : 0; /* artifact checksum mismatch */
}

/* ---- instances ---- */

static void wipe(lj_value *secrets) {
  for (size_t i = 0; secrets && i < secrets->u.object.len; i++) {
    lj_value *v = secrets->u.object.members[i].value;
    memset(v->u.string.ptr, 0, v->u.string.len);
  }
  lj_free(secrets);
}

static void managed_free(sv_managed *m) {
  lj_free(m->spec);
  wipe(m->secrets);
  free(m->command);
  free(m->path);
  free(m->dir);
  free(m->log);
  ll_mutex_free(m->mu);
  ll_mutex_free(m->log_mu);
  ll_cond_free(m->cv);
  free(m);
}

/* Drops a reference; s->mu must be held. */
static void unref_locked(sv_managed *m) {
  if (--m->refs == 0) managed_free(m);
}

static void unref(sv_managed *m) {
  ext_ledger_supervisor *s = m->s;
  ll_mutex_lock(s->mu);
  unref_locked(m);
  ll_mutex_unlock(s->mu);
}

static sv_managed *lookup(ext_ledger_supervisor *s, const char *id) {
  if (!id) return NULL;
  ll_mutex_lock(s->mu);
  sv_managed *m = s->list;
  while (m && strcmp(m->id, id) != 0) m = m->next;
  if (m) m->refs++;
  ll_mutex_unlock(s->mu);
  return m;
}

static void sink(void *arg, const char *data, size_t n) {
  sv_managed *m = arg;
  size_t limit = (size_t)m->s->log_limit;
  ll_mutex_lock(m->log_mu);
  if (!m->log) m->log = malloc(limit);
  if (m->log) {
    if (n >= limit) {
      memcpy(m->log, data + n - limit, limit);
      m->log_len = limit;
    } else {
      if (m->log_len + n > limit) {
        size_t drop = m->log_len + n - limit;
        memmove(m->log, m->log + drop, m->log_len - drop);
        m->log_len -= drop;
      }
      memcpy(m->log + m->log_len, data, n);
      m->log_len += n;
    }
  }
  ll_mutex_unlock(m->log_mu);
}

/* The Instance JSON; m->mu must be held. */
static lj_value *instance_value(const sv_managed *m) {
  lj_value *o = rec_obj();
  int bad = set(o, "id", rec_str(m->id)) | set(o, "artifact", lj_clone(lj_get(m->spec, "artifact"))) | set(o, "state", rec_str(m->state)) |
            set(o, "updated_at", time_value(&m->updated)) | set(o, "crash_count", rec_num((double)m->crash_count));
  if (m->pid > 0) bad |= set(o, "pid", rec_num((double)m->pid));
  if (m->identity[0]) bad |= set(o, "process_identity", rec_str(m->identity));
  if (m->started.set) bad |= set(o, "started_at", time_value(&m->started));
  if (m->has_exit) bad |= set(o, "exit_code", rec_num(m->exit_code));
  if (m->health && *m->health) bad |= set(o, "health", rec_str(m->health));
  if (m->last_error && *m->last_error) bad |= set(o, "last_error", rec_str(m->last_error));
  const char *const lists[] = {"endpoints", "connections", "dependencies"};
  for (int i = 0; i < 3; i++) {
    const lj_value *l = lj_get(m->spec, lists[i]);
    if (l && l->u.array.len) bad |= set(o, lists[i], lj_clone(l));
  }
  if (bad) {
    lj_free(o);
    return NULL;
  }
  return o;
}

static lj_value *vertex(const char *id, const char *kind, lj_value *attrs, const char *operation) {
  lj_value *v = rec_obj(), *prov = rec_obj();
  char k[96];
  snprintf(k, sizeof k, RT "/%s", kind);
  int bad = (attrs == NULL) | set(prov, "source", rec_str("ext.supervisor")) | set(prov, "operation", rec_str(operation)) | set(v, "id", rec_str(id)) |
            set(v, "kind", rec_str(k)) | set(v, "provenance", prov);
  if (attrs && attrs->u.object.len) bad |= set(v, "attributes", attrs);
  else lj_free(attrs);
  if (bad) {
    lj_free(v);
    return NULL;
  }
  return v;
}

static lj_value *edge(const char *id, const char *from, const char *to, const char *type, lj_value *attrs) {
  lj_value *e = rec_obj();
  char t[96];
  snprintf(t, sizeof t, RT "/%s", type);
  int bad = set(e, "id", rec_str(id)) | set(e, "from", rec_str(from)) | set(e, "to", rec_str(to)) | set(e, "type", rec_str(t));
  if (attrs && attrs->u.object.len) bad |= set(e, "attributes", attrs);
  else lj_free(attrs);
  if (bad) {
    lj_free(e);
    return NULL;
  }
  return e;
}

static char *join2(const char *a, const char *b) {
  size_t la = strlen(a), lb = strlen(b);
  char *s = malloc(la + lb + 1);
  if (s) {
    memcpy(s, a, la);
    memcpy(s + la, b, lb + 1);
  }
  return s;
}

static char *join3(const char *a, const char *b, const char *c) {
  char *ab = join2(a, b), *abc = ab ? join2(ab, c) : NULL;
  free(ab);
  return abc;
}

/* Writes the graph fact of m's current state; m->mu must be held. */
static int project_locked(sv_managed *m) {
  ext_ledger_supervisor *s = m->s;
  const lj_value *art = lj_get(m->spec, "artifact");
  const char *aid = rec_field(art, "id"), *rev = rec_field(art, "revision"), *sum = rec_field(art, "checksum");
  lj_value *attrs = rec_obj();
  int bad = set(attrs, "artifact_id", rec_str(aid)) | set(attrs, "artifact_revision", rec_str(rev)) | set(attrs, "state", rec_str(m->state)) |
            set(attrs, "runtime_id", rec_str(s->runtime_id)) | set(attrs, "crash_count", rec_num((double)m->crash_count)) |
            set(attrs, "supervisor_owner", rec_str(s->owner));
  if (is_active(m->state) || m->state == ST_STOPPING) {
    ll_time until = now_utc();
    until.sec += s->lease_ms / 1000;
    until.nsec += (int32_t)((s->lease_ms % 1000) * 1000000);
    if (until.nsec >= 1000000000) {
      until.sec++;
      until.nsec -= 1000000000;
    }
    bad |= set(attrs, "lease_until", time_value(&until));
  }
  if (m->health && *m->health) bad |= set(attrs, "health", rec_str(m->health));
  if (*sum) bad |= set(attrs, "artifact_checksum", rec_str(sum));
  if (m->pid > 0) bad |= set(attrs, "pid", rec_num((double)m->pid)) | set(attrs, "process_identity", rec_str(m->identity)) | set(attrs, "isolated", rec_bool(1));
  if (m->last_error && *m->last_error) bad |= set(attrs, "last_error", rec_str(m->last_error));
  if (m->started.set) bad |= set(attrs, "started_at", time_value(&m->started));
  char *artifact = join2("artifact/", aid), *process = join2("process/", m->id), *runtime = join2("runtime/", s->runtime_id);
  char *spawned = join2("spawned-by/", m->id), *supervises = join2("supervises/", m->id);
  lj_value *tx = rec_obj(), *vs = lj_new(LJ_ARRAY), *es = lj_new(LJ_ARRAY), *aattrs = rec_obj(), *rattrs = rec_obj();
  bad |= !artifact || !process || !runtime || !spawned || !supervises;
  bad |= set(aattrs, "revision", rec_str(rev)) | set(aattrs, "checksum", rec_str(sum)) | set(rattrs, "runtime_id", rec_str(s->runtime_id));
  if (!bad) {
    bad |= lj_array_push(vs, vertex(artifact, "artifact", aattrs, "artifact-observed")) | lj_array_push(vs, vertex(process, "process-instance", attrs, "lifecycle-observed")) |
           lj_array_push(vs, vertex(runtime, "runtime", rattrs, "runtime-observed")) | lj_array_push(es, edge(spawned, process, artifact, "spawned-by", NULL)) |
           lj_array_push(es, edge(supervises, runtime, process, "supervises", NULL));
  } else {
    lj_free(attrs);
    lj_free(aattrs);
    lj_free(rattrs);
  }
  const lj_value *deps = lj_get(m->spec, "dependencies"), *eps = lj_get(m->spec, "endpoints"), *conns = lj_get(m->spec, "connections");
  for (size_t i = 0; !bad && i < deps->u.array.len; i++) {
    const char *dep = deps->u.array.items[i]->u.string.ptr;
    char *vid = join2("dependency/", dep), *eid = join3("depends-on/", m->id, "/"), *eid2 = eid ? join2(eid, dep) : NULL;
    lj_value *a = rec_obj();
    bad |= !vid || !eid2 || set(a, "component_id", rec_str(dep)) || lj_array_push(vs, vertex(vid, "component", a, "dependency-declared")) ||
           lj_array_push(es, edge(eid2, process, vid, "depends-on", NULL));
    free(vid);
    free(eid);
    free(eid2);
  }
  for (size_t i = 0; !bad && i < eps->u.array.len; i++) {
    const lj_value *e = eps->u.array.items[i];
    const char *id = rec_field(e, "id");
    char *vid = join2("endpoint/", id), *eid = join3("exposes-endpoint/", m->id, "/"), *eid2 = eid ? join2(eid, id) : NULL;
    lj_value *a = rec_obj();
    bad |= !vid || !eid2 || set(a, "address", rec_str(rec_field(e, "address"))) | set(a, "transport", rec_str(rec_field(e, "transport"))) |
                                set(a, "direction", rec_str(rec_field(e, "direction")));
    if (!bad) bad |= lj_array_push(vs, vertex(vid, "endpoint", a, "endpoint-observed")) | lj_array_push(es, edge(eid2, process, vid, "exposes-endpoint", NULL));
    free(vid);
    free(eid);
    free(eid2);
  }
  for (size_t i = 0; !bad && i < conns->u.array.len; i++) {
    const lj_value *c = conns->u.array.items[i];
    char *eid = join2("connected-to/", rec_field(c, "id")), *from = join2("endpoint/", rec_field(c, "source_endpoint")),
         *to = join2("endpoint/", rec_field(c, "target_endpoint"));
    const lj_value *meta = lj_get(c, "metadata");
    bad |= !eid || !from || !to || lj_array_push(es, edge(eid, from, to, "connected-to", meta ? lj_clone(meta) : NULL));
    free(eid);
    free(from);
    free(to);
  }
  free(artifact);
  free(process);
  free(runtime);
  free(spawned);
  free(supervises);
  bad |= set(tx, "namespace", rec_str(RT)) | set(tx, "vertices", vs) | set(tx, "edges", es);
  if (bad) {
    lj_free(tx);
    return EXT_LEDGER_IO;
  }
  return apply(s->store, tx);
}

static void prune_terminals(ext_ledger_supervisor *s);

/* Publishes m's state and releases m->mu, which the caller holds. Pruning reads
 * and rewrites the whole namespace, so it runs after the lock is released. */
static int project_unlock(sv_managed *m) {
  int rc = project_locked(m);
  ll_cond_broadcast(m->cv); /* for ext_ledger_supervisor_await */
  int terminal = m->state == ST_STOPPED || m->state == ST_CRASHED;
  ll_mutex_unlock(m->mu);
  if (!rc && terminal) prune_terminals(m->s);
  return rc;
}

static void transition(sv_managed *m, const char *state, const char *health, const char *error, int crashed) {
  ll_mutex_lock(m->mu);
  m->state = state;
  if (health) m->health = health;
  if (error) m->last_error = error;
  if (crashed) m->crash_count++;
  m->updated = now_utc();
  project_unlock(m);
}

static void record_failure(sv_managed *m, const char *reason) { transition(m, ST_CRASHED, "unhealthy", reason, 1); }
static void record_stopped(sv_managed *m) { transition(m, ST_STOPPED, "unhealthy", NULL, 0); }

static int canceled(sv_managed *m) { return ll_flag_load(&m->ctx.canceled) != 0; }

/* Sleeps for ms unless m is stopped first; 1 when the whole time passed. */
static int sleep_unless_stopped(sv_managed *m, int64_t ms) {
  int64_t until = ll_now_unix_ms() + ms;
  ll_mutex_lock(m->mu);
  while (!canceled(m)) {
    int64_t left = until - ll_now_unix_ms();
    if (left <= 0) break;
    ll_cond_wait(m->cv, m->mu, left);
  }
  int slept = !canceled(m);
  ll_mutex_unlock(m->mu);
  return slept;
}

static int should_restart(const sv_managed *m, int64_t attempt) { return m->max_attempts > 0 && attempt <= m->max_attempts; }

static int64_t backoff(const sv_managed *m, int64_t attempt) {
  int64_t d = m->initial_delay > 0 ? m->initial_delay : 100;
  for (int64_t i = 1; i < attempt && d < 30000; i++) d *= 2;
  int64_t max = m->max_delay > 0 ? m->max_delay : 30000;
  return d > max ? max : d;
}

/* The environment of a launch: this process's, then the spec's, then the
 * secrets; a later entry replaces an earlier one of the same name. */
static int launch_environment(sv_managed *m, char ***out) {
  *out = NULL;
  size_t n = 0;
  char **env = ll_environ_copy(&n);
  if (!env) return EXT_LEDGER_IO;
  const lj_value *extra = lj_get(m->spec, "environment");
  size_t nsecrets = m->secrets ? m->secrets->u.object.len : 0;
  char **all = realloc(env, (n + extra->u.object.len + nsecrets + 1) * sizeof *all);
  if (!all) {
    for (size_t i = 0; i < n; i++) free(env[i]);
    free(env);
    return EXT_LEDGER_IO;
  }
  env = all;
  int rc = 0;
  for (int pass = 0; pass < 2; pass++) {
    const lj_value *o = pass ? m->secrets : extra;
    for (size_t i = 0; !rc && o && i < o->u.object.len; i++) {
      const lj_member *e = &o->u.object.members[i];
      char *kv = join3(e->key.ptr, "=", e->value->u.string.ptr);
      if (!kv) rc = EXT_LEDGER_IO;
      else env[n++] = kv;
    }
  }
  /* Later entries win: drop each earlier entry whose name a later one repeats. */
  size_t kept = 0;
  for (size_t i = 0; i < n; i++) {
    const char *eq = strchr(env[i], '=');
    size_t klen = eq ? (size_t)(eq - env[i]) : strlen(env[i]);
    int later = 0;
    for (size_t j = i + 1; j < n && !later; j++) {
      const char *eq2 = strchr(env[j], '=');
      later = ll_env_same(env[i], klen, env[j], eq2 ? (size_t)(eq2 - env[j]) : strlen(env[j]));
    }
    if (later) free(env[i]);
    else env[kept++] = env[i];
  }
  env[kept] = NULL;
  if (rc) {
    for (size_t i = 0; i < kept; i++) {
      memset(env[i], 0, strlen(env[i]));
      free(env[i]);
    }
    free(env);
    return rc;
  }
  *out = env;
  return 0;
}

static void free_environment(char **env) {
  for (size_t i = 0; env && env[i]; i++) {
    memset(env[i], 0, strlen(env[i])); /* it may hold secrets */
    free(env[i]);
  }
  free(env);
}

/* Waits for the caller's report on this launch (ext_ledger_supervisor_ready),
 * for at most the readiness timeout; 0 when it reported the process ready. */
static int wait_readiness(sv_managed *m) {
  if (!m->await_ready) return 0;
  int64_t timeout = m->readiness_timeout > 0 ? m->readiness_timeout : 10000, until = ll_now_unix_ms() + timeout;
  ll_mutex_lock(m->mu);
  int64_t pid = m->pid;
  int rc = 0;
  while (!(m->ready_pid == pid && m->ready_result)) {
    if (canceled(m)) {
      rc = EXT_LEDGER_CANCELED;
      break;
    }
    int64_t left = until - ll_now_unix_ms();
    if (left <= 0) {
      rc = EXT_LEDGER_TIMEOUT;
      break;
    }
    ll_cond_wait(m->cv, m->mu, left < 20 ? left : 20);
  }
  if (!rc && m->ready_result != 1) rc = EXT_LEDGER_UNAVAILABLE;
  ll_mutex_unlock(m->mu);
  if (!rc && !ll_proc_alive(pid)) rc = EXT_LEDGER_UNAVAILABLE; /* process exited before readiness */
  return rc;
}

/* Waits for the process; when m is stopped (or stop is set), ends it politely, then by force after 5 s. */
static int wait_process(sv_managed *m, ll_proc *p, int stop) {
  int code = 0;
  while (!stop && !canceled(m))
    if (ll_proc_wait(p, 20, &code) == 0) return code;
  if (ll_proc_wait(p, 0, &code) == 0) return code;
  ll_proc_terminate(p, 0);
  if (ll_proc_wait(p, 5000, &code) == 0) return code;
  ll_proc_terminate(p, 1);
  ll_proc_wait(p, -1, &code);
  return code;
}

static void trim_instances(ext_ledger_supervisor *s, sv_managed *self);

static void run(void *arg) {
  sv_managed *m = arg;
  ext_ledger_supervisor *s = m->s;
  int64_t attempt = 0;
  const char *argv0 = m->command;
  for (;;) {
    ll_mutex_lock(m->mu);
    m->state = ST_STARTING;
    m->updated = now_utc();
    m->last_error = "";
    project_unlock(m);
    if (canceled(m)) {
      record_stopped(m);
      break;
    }
    if (verify_artifact(m->path, rec_field(lj_get(m->spec, "artifact"), "checksum"))) {
      record_failure(m, "artifact verification failed");
      break;
    }
    char **env;
    if (launch_environment(m, &env)) {
      record_failure(m, "process start failed");
      break;
    }
    const lj_value *args = lj_get(m->spec, "args");
    const char **argv = calloc(args->u.array.len + 2, sizeof *argv);
    if (!argv) {
      free_environment(env);
      record_failure(m, "process start failed");
      break;
    }
    argv[0] = argv0;
    for (size_t i = 0; i < args->u.array.len; i++) argv[i + 1] = args->u.array.items[i]->u.string.ptr;
    /* Any process given resolved secrets can print them. Discard its output
     * entirely; exact-value redaction cannot make arbitrary output safe. */
    int secret = m->secrets && m->secrets->u.object.len > 0;
    ll_proc_spec ps = {m->path, argv, m->dir, (const char *const *)env, secret ? NULL : sink, m};
    if (canceled(m)) {
      free(argv);
      free_environment(env);
      record_stopped(m);
      break;
    }
    ll_proc *p = NULL;
    int64_t pid = 0;
    int rc = ll_proc_spawn(&ps, &p, &pid);
    free(argv);
    free_environment(env);
    if (rc) {
      record_failure(m, "process start failed");
      attempt++;
      if (!should_restart(m, attempt)) break;
      if (!sleep_unless_stopped(m, backoff(m, attempt))) {
        record_stopped(m);
        break;
      }
      continue;
    }
    /* Some restricted hosts deny process metadata reads. The process can
     * still run, but recovery will never end an unverified PID. */
    char identity[65];
    if (ll_proc_identity(pid, identity)) identity[0] = 0;
    ll_mutex_lock(m->mu);
    m->pid = pid;
    memcpy(m->identity, identity, sizeof identity);
    m->state = ST_RUNNING;
    m->health = "starting";
    m->updated = now_utc();
    if (!m->started.set) m->started = m->updated;
    project_unlock(m);
    int ready = wait_readiness(m);
    if (ready == 0) {
      ll_mutex_lock(m->mu);
      if (!canceled(m) && m->state == ST_RUNNING) {
        m->state = ST_READY;
        m->health = "healthy";
        m->updated = now_utc();
      }
      project_unlock(m);
    }
    int code = wait_process(m, p, ready != 0);
    ll_proc_terminate(p, 1); /* clear descendants even if the leader exited first */
    ll_proc_close(p);
    ll_mutex_lock(m->mu);
    m->pid = 0;
    m->identity[0] = 0;
    m->has_exit = 1;
    m->exit_code = code;
    m->health = "unhealthy";
    m->updated = now_utc();
    if (canceled(m)) {
      m->state = ST_STOPPED;
      m->last_error = "";
    } else if (ready) {
      m->state = ST_CRASHED;
      m->crash_count++;
      m->last_error = "readiness or handshake failed";
    } else if (code != 0) {
      m->state = ST_CRASHED;
      m->crash_count++;
      m->last_error = "process exited unsuccessfully";
    } else {
      m->state = ST_STOPPED;
    }
    const char *state = m->state;
    project_unlock(m);
    if (state != ST_CRASHED) break;
    attempt++;
    if (!should_restart(m, attempt)) break;
    transition(m, ST_RESTARTING, NULL, NULL, 0);
    if (!sleep_unless_stopped(m, backoff(m, attempt))) {
      transition(m, ST_STOPPED, NULL, NULL, 0);
      break;
    }
  }
  ll_mutex_lock(m->mu);
  m->done = 1;
  ll_cond_broadcast(m->cv);
  ll_mutex_unlock(m->mu);
  trim_instances(s, m);
  ll_mutex_lock(s->mu);
  m->exited = 1; /* nothing of this thread touches m after this */
  ll_mutex_unlock(s->mu);
}

typedef struct {
  sv_managed *m;
  ll_time updated;
} sv_terminal;

static int terminal_cmp(const void *a, const void *b) {
  const sv_terminal *x = a, *y = b;
  return time_before(&x->updated, &y->updated) ? -1 : time_before(&y->updated, &x->updated) ? 1 : 0;
}

/* Forgets the oldest finished instances beyond the retention bound. An
 * instance whose thread is still finishing (self among them) is kept for now. */
static void trim_instances(ext_ledger_supervisor *s, sv_managed *self) {
  ll_mutex_lock(s->mu);
  size_t n = 0;
  for (sv_managed *m = s->list; m; m = m->next) n++;
  sv_terminal *items = n ? malloc(n * sizeof *items) : NULL;
  size_t k = 0;
  for (sv_managed *m = s->list; items && m; m = m->next) {
    ll_mutex_lock(m->mu);
    if (m->done && (m->state == ST_STOPPED || m->state == ST_CRASHED)) items[k++] = (sv_terminal){m, m->updated};
    ll_mutex_unlock(m->mu);
  }
  sv_managed *removed = NULL;
  if (items && (int64_t)k > s->retain) {
    qsort(items, k, sizeof *items, terminal_cmp);
    for (size_t i = 0; i < k - (size_t)s->retain; i++) {
      sv_managed *m = items[i].m;
      if (m == self || !m->exited) continue;
      for (sv_managed **pp = &s->list; *pp; pp = &(*pp)->next)
        if (*pp == m) {
          *pp = m->next;
          break;
        }
      m->linked = 0;
      m->next = removed;
      removed = m;
    }
  }
  free(items);
  ll_mutex_unlock(s->mu);
  while (removed) {
    sv_managed *m = removed;
    removed = m->next;
    ll_thread_join(m->thread);
    m->thread = NULL;
    unref(m);
  }
}

/* ---- recovery ---- */

static lj_value *recovered_vertex(const ext_ledger_supervisor *s, const lj_value *v, const char *state, int alive) {
  lj_value *out = rec_obj(), *attrs = lj_clone(lj_get(v, "attributes"));
  if (!attrs) attrs = rec_obj();
  int bad = !attrs || attrs->type != LJ_OBJECT;
  if (!bad) {
    bad |= set(attrs, "state", rec_str(state)) | set(attrs, "health", rec_str("unhealthy")) | set(attrs, "supervisor_owner", rec_str(s->owner));
    const char *drop[] = {"lease_until", "pid", "process_identity"};
    for (size_t d = 0; d < (alive ? 1u : 3u); d++)
      for (size_t i = 0; i < attrs->u.object.len; i++)
        if (strcmp(attrs->u.object.members[i].key.ptr, drop[d]) == 0) {
          lj_free(attrs->u.object.members[i].value);
          free(attrs->u.object.members[i].key.ptr);
          memmove(&attrs->u.object.members[i], &attrs->u.object.members[i + 1], (attrs->u.object.len - i - 1) * sizeof *attrs->u.object.members);
          attrs->u.object.len--;
          break;
        }
  }
  bad |= set(out, "id", rec_str(rec_field(v, "id"))) | set(out, "kind", rec_str(rec_field(v, "kind"))) | set(out, "attributes", attrs);
  const lj_value *labels = lj_get(v, "labels"), *prov = lj_get(v, "provenance");
  if (labels) bad |= set(out, "labels", lj_clone(labels));
  if (prov) bad |= set(out, "provenance", lj_clone(prov));
  if (bad) {
    lj_free(out);
    return NULL;
  }
  return out;
}

static int mark_recovered(ext_ledger_supervisor *s, const lj_value *v, const char *state, int alive) {
  lj_value *tx = rec_obj(), *vs = lj_new(LJ_ARRAY);
  if (set(tx, "namespace", rec_str(RT)) | lj_array_push(vs, recovered_vertex(s, v, state, alive)) | set(tx, "vertices", vs)) {
    lj_free(tx);
    return EXT_LEDGER_IO;
  }
  return apply(s->store, tx);
}

static int64_t int_attribute(const lj_value *attrs, const char *key) {
  const lj_value *v = lj_get(attrs, key);
  return v && v->type == LJ_NUMBER ? (int64_t)v->u.number : 0;
}

static int mine(const ext_ledger_supervisor *s, const lj_value *v, const char **state) {
  const lj_value *attrs = lj_get(v, "attributes");
  *state = rec_field(attrs, "state");
  return strcmp(rec_field(v, "kind"), RT "/process-instance") == 0 && strcmp(rec_field(attrs, "runtime_id"), s->runtime_id) == 0;
}

static int recover_orphans(ext_ledger_supervisor *s) {
  lj_value *snap;
  int rc = snapshot(s->store, &snap);
  if (rc) return rc;
  const lj_value *vs = lj_get(snap, "vertices");
  ll_time now = now_utc();
  for (size_t i = 0; !rc && vs && i < vs->u.array.len; i++) {
    const lj_value *v = vs->u.array.items[i], *attrs = lj_get(v, "attributes");
    const char *state;
    if (!mine(s, v, &state)) continue;
    const char *st = state_of(state);
    if (!st || !(is_active(st) || st == ST_STOPPING || st == ST_ORPHANED)) continue;
    const char *id = rec_field(v, "id");
    if (strncmp(id, "process/", 8) == 0) id += 8;
    int64_t pid = int_attribute(attrs, "pid");
    const char *identity = rec_field(attrs, "process_identity");
    int alive = pid > 0 && ll_proc_alive(pid);
    const char *lease = str_field(attrs, "lease_until");
    ll_time until;
    if (lease && ll_time_parse(lease, strlen(lease), &until) == 0 && time_before(&now, &until) && st != ST_ORPHANED) {
      rc = EXT_LEDGER_BUSY; /* the runtime still has a live supervisor lease */
      break;
    }
    if (!alive) {
      rc = mark_recovered(s, v, ST_STOPPED, 0);
      continue;
    }
    const lj_value *isolated = lj_get(attrs, "isolated");
    if (s->terminate_orphans && *identity && isolated && isolated->type == LJ_BOOL && isolated->u.boolean && ll_proc_terminate_orphan(pid, identity) == 0) {
      rc = mark_recovered(s, v, ST_STOPPED, 0);
      continue;
    }
    if ((rc = mark_recovered(s, v, ST_ORPHANED, 1))) break;
    sv_orphan *grown = realloc(s->orphans, (s->norphans + 1) * sizeof *grown);
    if (!grown) {
      rc = EXT_LEDGER_IO;
      break;
    }
    s->orphans = grown;
    sv_orphan *o = &s->orphans[s->norphans];
    o->id = copy_string(id);
    o->identity = copy_string(identity);
    o->pid = pid;
    o->alive = 1;
    if (!o->id || !o->identity) {
      free(o->id);
      free(o->identity);
      rc = EXT_LEDGER_IO;
    } else
      s->norphans++;
  }
  lj_free(snap);
  if (!rc) prune_terminals(s);
  return rc;
}

typedef struct {
  const lj_value *v;
  ll_time updated;
} sv_vertex;

static int vertex_cmp(const void *a, const void *b) {
  const sv_vertex *x = a, *y = b;
  return time_before(&x->updated, &y->updated) ? -1 : time_before(&y->updated, &x->updated) ? 1 : 0;
}

/* Deletes the oldest terminal instances of this runtime beyond the bound, and
 * the artifacts, endpoints and components only they used. A conflict with
 * another writer is left for the next terminal transition. */
static void prune_terminals(ext_ledger_supervisor *s) {
  lj_value *snap;
  if (snapshot(s->store, &snap)) return;
  const lj_value *vs = lj_get(snap, "vertices"), *es = lj_get(snap, "edges"), *revision = lj_get(snap, "revision");
  size_t nv = vs ? vs->u.array.len : 0, k = 0;
  sv_vertex *terminal = nv ? malloc(nv * sizeof *terminal) : NULL;
  for (size_t i = 0; terminal && i < nv; i++) {
    const lj_value *v = vs->u.array.items[i];
    const char *state;
    if (!mine(s, v, &state) || (strcmp(state, ST_STOPPED) != 0 && strcmp(state, ST_CRASHED) != 0)) continue;
    const char *at = rec_field(v, "updated_at");
    terminal[k].v = v;
    if (ll_time_parse(at, strlen(at), &terminal[k].updated)) memset(&terminal[k].updated, 0, sizeof terminal[k].updated);
    k++;
  }
  if (!terminal || (int64_t)k <= s->retain) {
    free(terminal);
    lj_free(snap);
    return;
  }
  qsort(terminal, k, sizeof *terminal, vertex_cmp);
  size_t remove = k - (size_t)s->retain;
  lj_value *tx = rec_obj(), *del = lj_new(LJ_ARRAY);
  int bad = set(tx, "namespace", rec_str(RT)) | set(tx, "delete_incident_edges", rec_bool(1)) | set(tx, "expected_revision", lj_clone(revision));
  for (size_t i = 0; i < remove; i++) {
    lj_value *ref = rec_obj();
    bad |= set(ref, "namespace", rec_str(RT)) | set(ref, "id", rec_str(rec_field(terminal[i].v, "id"))) | lj_array_push(del, ref);
  }
  for (size_t i = 0; !bad && i < nv; i++) {
    const lj_value *v = vs->u.array.items[i];
    const char *kind = rec_field(v, "kind");
    if (strcmp(kind, RT "/artifact") != 0 && strcmp(kind, RT "/endpoint") != 0 && strcmp(kind, RT "/component") != 0) continue;
    if (strcmp(rec_field(lj_get(v, "provenance"), "source"), "ext.supervisor") != 0) continue;
    const char *id = rec_field(v, "id");
    int used = 0;
    for (size_t j = 0; !used && es && j < es->u.array.len; j++) {
      const char *from = rec_field(es->u.array.items[j], "from"), *to = rec_field(es->u.array.items[j], "to");
      if (strcmp(from, id) != 0 && strcmp(to, id) != 0) continue;
      int gone = 0;
      for (size_t d = 0; d < remove && !gone; d++) {
        const char *did = rec_field(terminal[d].v, "id");
        gone = strcmp(did, from) == 0 || strcmp(did, to) == 0;
      }
      used = !gone;
    }
    if (!used) {
      lj_value *ref = rec_obj();
      bad |= set(ref, "namespace", rec_str(RT)) | set(ref, "id", rec_str(id)) | lj_array_push(del, ref);
    }
  }
  bad |= set(tx, "delete_vertices", del);
  free(terminal);
  lj_free(snap);
  if (bad) lj_free(tx);
  else apply(s->store, tx); /* EXT_LEDGER_CHANGED or NOT_FOUND: another writer retries it */
}

/* ---- leases ---- */

static void renew_leases(void *arg) {
  ext_ledger_supervisor *s = arg;
  ll_mutex_lock(s->mu);
  while (!s->stopping) {
    ll_cond_wait(s->cv, s->mu, s->lease_ms / 3 > 0 ? s->lease_ms / 3 : 1);
    if (s->stopping) break;
    for (sv_managed *m = s->list; m; m = m->next) {
      ll_mutex_lock(m->mu);
      if (!m->done && is_active(m->state)) project_locked(m);
      ll_mutex_unlock(m->mu);
    }
  }
  ll_mutex_unlock(s->mu);
}

/* ---- the API ---- */

static void supervisor_free(ext_ledger_supervisor *s) {
  for (size_t i = 0; i < s->norphans; i++) {
    free(s->orphans[i].id);
    free(s->orphans[i].identity);
  }
  free(s->orphans);
  free(s->runtime_id);
  ll_mutex_free(s->mu);
  ll_cond_free(s->cv);
  free(s);
}

int ext_ledger_supervisor_open(ext_ledger_store *store, const ext_ledger_supervisor_options *options, ext_ledger_supervisor **out) {
  if (!out) return EXT_LEDGER_INVALID;
  *out = NULL;
  if (!store) return EXT_LEDGER_INVALID; /* graph store is required */
  ext_ledger_supervisor_options o;
  memset(&o, 0, sizeof o);
  if (options) o = *options;
  if (o.log_limit <= 0) o.log_limit = 64 * 1024;
  if (!o.runtime_id || !*o.runtime_id) o.runtime_id = "local";
  if (o.lease_ms <= 0) o.lease_ms = 15000;
  if (o.lease_ms < 3) return EXT_LEDGER_INVALID; /* lease duration must be at least 3 milliseconds */
  if (o.retain_terminal <= 0) o.retain_terminal = 256;
  if (!o.orphan_policy || !*o.orphan_policy) o.orphan_policy = "mark";
  if (strcmp(o.orphan_policy, "mark") != 0 && strcmp(o.orphan_policy, "terminate") != 0) return EXT_LEDGER_INVALID;
  static const char schema[] = "{\"namespace\":\"" RT "\",\"version\":\"1\"}";
  int rc = ext_ledger_store_register(store, NULL, schema, sizeof schema - 1);
  if (rc) return rc;
  ext_ledger_supervisor *s = calloc(1, sizeof *s);
  if (!s) return EXT_LEDGER_IO;
  s->store = store;
  s->log_limit = o.log_limit;
  s->lease_ms = o.lease_ms;
  s->retain = o.retain_terminal;
  s->require_checksum = o.require_checksum;
  s->terminate_orphans = strcmp(o.orphan_policy, "terminate") == 0;
  s->runtime_id = copy_string(o.runtime_id);
  s->mu = ll_mutex_new();
  s->cv = ll_cond_new();
  if (!s->runtime_id || !s->mu || !s->cv || ll_random_hex(s->owner, 16)) rc = EXT_LEDGER_IO;
  if (!rc) rc = recover_orphans(s);
  if (!rc && ll_thread_start(&s->lease, renew_leases, s)) rc = EXT_LEDGER_IO;
  if (rc) {
    supervisor_free(s);
    return rc;
  }
  *out = s;
  return 0;
}

/* The secrets object: names as the environment takes them, string values. */
static int parse_secrets(const char *text, size_t len, lj_value **out) {
  *out = NULL;
  if (!text || !len) return 0;
  int oom = 0;
  lj_value *v = lj_parse(text, len, &oom);
  if (!v) return oom ? EXT_LEDGER_IO : EXT_LEDGER_INVALID;
  int ok = v->type == LJ_OBJECT;
  for (size_t i = 0; ok && i < v->u.object.len; i++) {
    const lj_member *m = &v->u.object.members[i];
    ok = m->key.len && !memchr(m->key.ptr, '=', m->key.len) && !memchr(m->key.ptr, 0, m->key.len) && m->value->type == LJ_STRING &&
         !memchr(m->value->u.string.ptr, 0, m->value->u.string.len);
  }
  if (!ok) {
    wipe(v);
    return EXT_LEDGER_INVALID;
  }
  *out = v;
  return 0;
}

int ext_ledger_supervisor_start(ext_ledger_supervisor *s, const ext_ledger_context *ctx, const char *spec, size_t len, const char *secrets,
                                size_t secrets_len, char **instance, size_t *instance_len) {
  if (instance) *instance = NULL;
  if (instance_len) *instance_len = 0;
  if (!s || !spec) return EXT_LEDGER_INVALID;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  int oom = 0;
  lj_value *in = lj_parse(spec, len, &oom), *n = NULL;
  if (!in) return oom ? EXT_LEDGER_IO : EXT_LEDGER_INVALID;
  rc = normalise(s, in, &n);
  lj_value *sec = NULL;
  if (!rc) rc = parse_secrets(secrets, secrets_len, &sec);
  sv_managed *m = rc ? NULL : calloc(1, sizeof *m);
  if (!m) wipe(sec);
  if (!rc && !m) rc = EXT_LEDGER_IO;
  if (!rc) {
    const lj_value *restart = lj_get(in, "restart");
    m->s = s;
    m->spec = n;
    n = NULL;
    m->command = copy_string(rec_field(m->spec, "command"));
    m->dir = copy_string(rec_field(m->spec, "dir"));
    m->max_attempts = restart ? int_field(restart, "max_attempts") : 0;
    m->initial_delay = restart ? int_field(restart, "initial_delay_ms") : 0;
    m->max_delay = restart ? int_field(restart, "max_delay_ms") : 0;
    m->readiness_timeout = int_field(in, "readiness_timeout_ms");
    m->secrets = sec;
    const lj_value *await = lj_get(in, "await_readiness");
    m->await_ready = await && await->type == LJ_BOOL && await->u.boolean;
    m->mu = ll_mutex_new();
    m->log_mu = ll_mutex_new();
    m->cv = ll_cond_new();
    m->state = ST_STARTING;
    m->health = "";
    m->last_error = "";
    m->updated = now_utc();
    m->refs = 1;
    if (!m->command || !m->dir || !m->mu || !m->log_mu || !m->cv || ll_random_hex(m->id, 16)) rc = EXT_LEDGER_IO;
  }
  lj_free(in);
  lj_free(n);
  /* As exec.Command does, a command that is not found keeps its name: verifying
   * it fails when there is a checksum, and starting it fails otherwise. */
  if (!rc && ll_look_path(m->command, &m->path) == EXT_LEDGER_IO) rc = EXT_LEDGER_IO;
  if (!rc && !m->path && !(m->path = copy_string(m->command))) rc = EXT_LEDGER_IO;
  if (!rc) rc = verify_artifact(m->path, rec_field(lj_get(m->spec, "artifact"), "checksum"));
  if (rc) {
    if (m) managed_free(m);
    return rc;
  }
  ll_mutex_lock(s->mu);
  if (s->closed) rc = EXT_LEDGER_UNAVAILABLE;                    /* supervisor is closed */
  else if (s->norphans) rc = EXT_LEDGER_BUSY;                    /* unresolved orphan process blocks new starts */
  else {
    m->next = s->list;
    s->list = m;
    m->linked = 1;
    m->refs++; /* the caller's, until the end of this call */
  }
  ll_mutex_unlock(s->mu);
  if (rc) {
    managed_free(m);
    return rc;
  }
  ll_mutex_lock(m->mu);
  rc = project_locked(m);
  char *text = NULL;
  size_t tlen = 0;
  if (!rc && instance) rc = write_out(instance_value(m), &text, &tlen);
  ll_mutex_unlock(m->mu);
  ll_mutex_lock(s->mu);
  if (!rc && ll_thread_start(&m->thread, run, m)) rc = EXT_LEDGER_IO;
  if (rc) {
    for (sv_managed **pp = &s->list; *pp; pp = &(*pp)->next)
      if (*pp == m) {
        *pp = m->next;
        break;
      }
    m->linked = 0;
    unref_locked(m);
  }
  unref_locked(m);
  ll_mutex_unlock(s->mu);
  if (rc) {
    free(text);
    return rc;
  }
  if (instance) *instance = text;
  if (instance_len) *instance_len = tlen;
  return 0;
}

int ext_ledger_supervisor_stop(ext_ledger_supervisor *s, const ext_ledger_context *ctx, const char *id) {
  if (!s) return EXT_LEDGER_INVALID;
  sv_managed *m = lookup(s, id);
  if (!m) return EXT_LEDGER_NOT_FOUND;
  ll_mutex_lock(m->mu);
  if (m->done) {
    ll_mutex_unlock(m->mu);
    unref(m);
    return 0;
  }
  m->state = ST_STOPPING;
  m->updated = now_utc();
  ll_flag_store(&m->ctx.canceled, 1);
  ll_cond_broadcast(m->cv);
  project_unlock(m);
  int rc = 0;
  ll_mutex_lock(m->mu);
  while (!m->done && !(rc = ll_ctx_check(ctx))) ll_cond_wait(m->cv, m->mu, 20);
  ll_mutex_unlock(m->mu);
  unref(m);
  return rc;
}

int ext_ledger_supervisor_health(ext_ledger_supervisor *s, const char *id, int check, int *healthy) {
  if (healthy) *healthy = 0;
  if (!s) return EXT_LEDGER_INVALID;
  sv_managed *m = lookup(s, id);
  if (!m) return EXT_LEDGER_NOT_FOUND;
  ll_mutex_lock(m->mu);
  int ok = check != 0 && m->state == ST_READY && m->pid > 0 && ll_proc_alive(m->pid);
  m->health = ok ? "healthy" : "unhealthy";
  m->updated = now_utc();
  int rc = project_unlock(m);
  unref(m);
  if (!rc && healthy) *healthy = ok;
  return rc;
}

int ext_ledger_supervisor_ready(ext_ledger_supervisor *s, const char *id, int64_t pid, int ready) {
  if (!s) return EXT_LEDGER_INVALID;
  sv_managed *m = lookup(s, id);
  if (!m) return EXT_LEDGER_NOT_FOUND;
  ll_mutex_lock(m->mu);
  int rc = 0;
  if (!m->await_ready) rc = EXT_LEDGER_INVALID; /* its spec does not await readiness */
  else if (m->state != ST_RUNNING || m->pid != pid || pid <= 0) rc = EXT_LEDGER_CHANGED; /* not this launch, or not waiting */
  else {
    m->ready_pid = pid;
    m->ready_result = ready ? 1 : 2;
    ll_cond_broadcast(m->cv);
  }
  ll_mutex_unlock(m->mu);
  unref(m);
  return rc;
}

static int state_listed(const char *states, const char *state) {
  size_t n = strlen(state);
  for (const char *p = states; p && *p;) {
    const char *end = strchr(p, ',');
    size_t k = end ? (size_t)(end - p) : strlen(p);
    if (k == n && strncmp(p, state, n) == 0) return 1;
    p = end ? end + 1 : NULL;
  }
  return 0;
}

int ext_ledger_supervisor_await(ext_ledger_supervisor *s, const ext_ledger_context *ctx, const char *id, const char *states, char **out,
                                size_t *out_len) {
  if (out) *out = NULL;
  if (!s || !states || !*states) return EXT_LEDGER_INVALID;
  sv_managed *m = lookup(s, id);
  if (!m) return EXT_LEDGER_NOT_FOUND;
  ll_mutex_lock(m->mu);
  int rc = 0;
  while (!state_listed(states, m->state)) {
    if ((rc = ll_ctx_check(ctx))) break;
    if (m->done) { /* it will change no more */
      rc = EXT_LEDGER_CHANGED;
      break;
    }
    ll_cond_wait(m->cv, m->mu, 20);
  }
  if (!rc && out) rc = write_out(instance_value(m), out, out_len);
  ll_mutex_unlock(m->mu);
  unref(m);
  return rc;
}

int ext_ledger_supervisor_instance(ext_ledger_supervisor *s, const char *id, char **out, size_t *out_len) {
  if (!s || !out) return EXT_LEDGER_INVALID;
  *out = NULL;
  sv_managed *m = lookup(s, id);
  if (!m) return EXT_LEDGER_NOT_FOUND;
  ll_mutex_lock(m->mu);
  int rc = write_out(instance_value(m), out, out_len);
  ll_mutex_unlock(m->mu);
  unref(m);
  return rc;
}

static int id_cmp(const void *a, const void *b) {
  return strcmp(rec_field(*(const lj_value *const *)a, "id"), rec_field(*(const lj_value *const *)b, "id"));
}

int ext_ledger_supervisor_instances(ext_ledger_supervisor *s, char **out, size_t *out_len) {
  if (!s || !out) return EXT_LEDGER_INVALID;
  *out = NULL;
  lj_value *a = lj_new(LJ_ARRAY);
  int bad = !a;
  ll_mutex_lock(s->mu);
  for (sv_managed *m = s->list; !bad && m; m = m->next) {
    ll_mutex_lock(m->mu);
    bad |= lj_array_push(a, instance_value(m));
    ll_mutex_unlock(m->mu);
  }
  ll_mutex_unlock(s->mu);
  if (bad) {
    lj_free(a);
    return EXT_LEDGER_IO;
  }
  qsort(a->u.array.items, a->u.array.len, sizeof *a->u.array.items, id_cmp);
  return write_out(a, out, out_len);
}

int ext_ledger_supervisor_logs(ext_ledger_supervisor *s, const char *id, char **out, size_t *out_len) {
  if (!s || !out) return EXT_LEDGER_INVALID;
  *out = NULL;
  sv_managed *m = lookup(s, id);
  if (!m) return EXT_LEDGER_NOT_FOUND;
  ll_mutex_lock(m->log_mu);
  char *copy = malloc(m->log_len + 1);
  if (copy) {
    if (m->log_len) memcpy(copy, m->log, m->log_len);
    copy[m->log_len] = 0;
    if (out_len) *out_len = m->log_len;
  }
  ll_mutex_unlock(m->log_mu);
  unref(m);
  if (!copy) return EXT_LEDGER_IO;
  *out = copy;
  return 0;
}

static int orphan_cmp(const void *a, const void *b) { return strcmp(rec_field(*(const lj_value *const *)a, "id"), rec_field(*(const lj_value *const *)b, "id")); }

int ext_ledger_supervisor_orphans(ext_ledger_supervisor *s, char **out, size_t *out_len) {
  if (!s || !out) return EXT_LEDGER_INVALID;
  *out = NULL;
  lj_value *a = lj_new(LJ_ARRAY);
  int bad = !a;
  ll_mutex_lock(s->mu);
  for (size_t i = 0; !bad && i < s->norphans; i++) {
    lj_value *o = rec_obj();
    bad |= set(o, "id", rec_str(s->orphans[i].id)) | set(o, "pid", rec_num((double)s->orphans[i].pid)) | set(o, "alive", rec_bool(s->orphans[i].alive)) |
           (*s->orphans[i].identity ? set(o, "process_identity", rec_str(s->orphans[i].identity)) : 0) | lj_array_push(a, o);
  }
  ll_mutex_unlock(s->mu);
  if (bad) {
    lj_free(a);
    return EXT_LEDGER_IO;
  }
  qsort(a->u.array.items, a->u.array.len, sizeof *a->u.array.items, orphan_cmp);
  return write_out(a, out, out_len);
}

int ext_ledger_supervisor_resolve_orphan(ext_ledger_supervisor *s, const ext_ledger_context *ctx, const char *id) {
  if (!s || !id) return EXT_LEDGER_INVALID;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  ll_mutex_lock(s->mu);
  size_t i = 0;
  while (i < s->norphans && strcmp(s->orphans[i].id, id) != 0) i++;
  int found = i < s->norphans;
  int64_t pid = found ? s->orphans[i].pid : 0;
  int alive = found && s->orphans[i].alive;
  char *identity = found ? copy_string(s->orphans[i].identity) : NULL;
  ll_mutex_unlock(s->mu);
  if (!found) return EXT_LEDGER_NOT_FOUND;
  if (!identity) return EXT_LEDGER_IO;
  if (alive && ll_proc_alive(pid)) {
    if (!*identity) rc = EXT_LEDGER_UNAVAILABLE; /* orphan process identity is unavailable */
    else rc = ll_proc_terminate_orphan(pid, identity);
  }
  free(identity);
  if (rc) return rc;
  char *vid = join2("process/", id), *text = NULL;
  size_t len = 0;
  if (!vid) return EXT_LEDGER_IO;
  rc = ext_ledger_store_get_vertex(s->store, ctx, RT, vid, &text, &len);
  free(vid);
  if (rc) return rc;
  int oom = 0;
  lj_value *v = lj_parse(text, len, &oom);
  ext_ledger_free(text);
  if (!v) return EXT_LEDGER_IO;
  const lj_value *attrs = lj_get(v, "attributes");
  if (strcmp(rec_field(attrs, "runtime_id"), s->runtime_id) != 0 || strcmp(rec_field(attrs, "state"), ST_ORPHANED) != 0) rc = EXT_LEDGER_CHANGED; /* orphan state changed */
  if (!rc) rc = mark_recovered(s, v, ST_STOPPED, 0);
  lj_free(v);
  if (rc) return rc;
  ll_mutex_lock(s->mu);
  for (i = 0; i < s->norphans; i++)
    if (strcmp(s->orphans[i].id, id) == 0) {
      free(s->orphans[i].id);
      free(s->orphans[i].identity);
      s->orphans[i] = s->orphans[--s->norphans];
      break;
    }
  ll_mutex_unlock(s->mu);
  prune_terminals(s);
  return 0;
}

void ext_ledger_supervisor_close(ext_ledger_supervisor *s) {
  if (!s) return;
  ll_mutex_lock(s->mu);
  s->closed = 1;
  size_t n = 0;
  for (sv_managed *m = s->list; m; m = m->next) n++;
  sv_managed **all = n ? malloc(n * sizeof *all) : NULL;
  size_t k = 0;
  for (sv_managed *m = s->list; all && m; m = m->next) {
    m->refs++;
    all[k++] = m;
  }
  ll_mutex_unlock(s->mu);
  for (size_t i = 0; i < k; i++) {
    ext_ledger_supervisor_stop(s, NULL, all[i]->id);
    unref(all[i]);
  }
  free(all);
  /* Every instance is done; take the rest of the list and join their threads. */
  ll_mutex_lock(s->mu);
  s->stopping = 1;
  ll_cond_broadcast(s->cv);
  sv_managed *rest = s->list;
  s->list = NULL;
  ll_mutex_unlock(s->mu);
  ll_thread_join(s->lease);
  while (rest) {
    sv_managed *m = rest;
    rest = m->next;
    ll_thread_join(m->thread);
    m->thread = NULL;
    m->linked = 0;
    unref(m);
  }
  supervisor_free(s);
}
