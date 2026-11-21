/* Host inventory: observe_host and read_host (recorder/v1). The inventory
 * kinds are those of res/system: shells, filesystems, webviews, the desktop,
 * surfaces (displays and windows), accessibility, resources and permissions.
 * An absent kind keeps what was recorded of it; an empty list removes it. */
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* ---- the res/system rules the projection uses ---- */

/* Whether a host path is absolute by the rules of the recorder's OS (filepath.IsAbs). */
static int host_abs(const rec *r, const char *p) {
  if (strcmp(r->env.os, "windows") != 0) return p[0] == '/';
  int drive = ((p[0] | 32) >= 'a' && (p[0] | 32) <= 'z') && p[1] == ':' && (p[2] == '\\' || p[2] == '/');
  return drive || ((p[0] == '\\' || p[0] == '/') && (p[1] == '\\' || p[1] == '/'));
}

/* The key a host path is compared and recorded under: cleaned, lower-cased on Windows. */
static char *path_key(const rec *r, const char *path) {
  char *k = rec_clean(path);
  if (k && strcmp(r->env.os, "windows") == 0)
    for (char *p = k; *p; p++)
      if (*p >= 'A' && *p <= 'Z') *p = (char)(*p + 32);
  return k;
}

/* A filesystem source without URL credentials, as res/system's RedactSource. */
char *rec_redact_source(const char *s) {
  size_t n = strlen(s), start;
  const char *sep = strstr(s, "://");
  if (n >= 2 && s[0] == '/' && s[1] == '/') start = 2;
  else if (sep) start = (size_t)(sep - s) + 3;
  else start = n + 1;
  char *out = malloc(n + 1);
  if (!out) return NULL;
  memcpy(out, s, n + 1);
  if (start > n) return out;
  size_t end = start;
  while (end < n && s[end] != '/' && s[end] != '?' && s[end] != '#') end++;
  size_t at = end;
  while (at > start && s[at - 1] != '@') at--;
  if (at > start) { /* drop start..at (the userinfo and its @) */
    memmove(out + start, s + at, n - at + 1);
  }
  return out;
}

static const lj_value *field_value(const lj_value *o, const char *path) {
  /* a dotted path: "bounds.width" */
  char key[64];
  const lj_value *v = o;
  while (v && *path) {
    const char *dot = strchr(path, '.');
    size_t n = dot ? (size_t)(dot - path) : strlen(path);
    if (n >= sizeof key) return NULL;
    memcpy(key, path, n);
    key[n] = 0;
    v = lj_get(v, key);
    path += n + (dot ? 1 : 0);
  }
  return v;
}

static const char *str_at(const lj_value *o, const char *path) {
  const lj_value *v = field_value(o, path);
  return v && v->type == LJ_STRING ? v->u.string.ptr : "";
}

static double num_at(const lj_value *o, const char *path) {
  const lj_value *v = field_value(o, path);
  return v && v->type == LJ_NUMBER ? v->u.number : 0;
}

static int bool_at(const lj_value *o, const char *path) {
  const lj_value *v = field_value(o, path);
  return v && v->type == LJ_BOOL && v->u.boolean;
}

/* A time field that is set and not the zero time. */
static int time_set(const lj_value *o, const char *path) {
  const char *s = str_at(o, path);
  ll_time t;
  return *s && ll_time_parse(s, strlen(s), &t) == 0 && !ll_time_is_zero(&t);
}

/* A time field as Go prints it after parsing it. */
static lj_value *time_value(const char *s) {
  ll_time t;
  if (ll_time_parse(s, strlen(s), &t)) t = (ll_time){1, -62135596800LL, 0, 0};
  lj_buf b = {0};
  ll_time_write(&b, &t);
  lj_value *v = b.oom || b.len < 2 ? NULL : rec_strn(b.ptr + 1, b.len - 2);
  lj_buf_free(&b);
  return v;
}

static const lj_value *array_at(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_ARRAY ? v : NULL;
}

static int present(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type != LJ_NULL;
}

/* ---- observe_host ---- */

typedef struct {
  rec *r;
  lj_value *vertices, *edges; /* objects keyed by ID */
  ll_map kinds, types;        /* managed kinds and relationship types, by full name */
  lj_buf now;
  int oom;
} host_tx;

static void manage(host_tx *h, const char *kind, const char *type) {
  char k[96];
  snprintf(k, sizeof k, REC_NS "/%s", kind);
  if (ll_map_put(&h->kinds, k, strlen(k), (void *)1)) h->oom = 1;
  snprintf(k, sizeof k, REC_NS "/%s", type);
  if (ll_map_put(&h->types, k, strlen(k), (void *)1)) h->oom = 1;
}

static void add(host_tx *h, const char *id, const char *kind, const char *relation, lj_value *attrs) {
  lj_value *v = rec_vertex(h->r, id, kind, attrs, "host-observed");
  char edge_id[160];
  snprintf(edge_id, sizeof edge_id, "machine-%s", id);
  lj_value *e = rec_edge(edge_id, "machine/local", id, relation);
  if (!v || !e || lj_object_set(h->vertices, id, strlen(id), v) || lj_object_set(h->edges, edge_id, strlen(edge_id), e)) h->oom = 1;
}

static lj_value *inventory_attrs(host_tx *h, const char *kind, double count, const char *environment) {
  lj_value *a = rec_obj();
  if (rec_set(a, "kind", rec_str(kind)) | rec_set(a, "count", rec_num(count)) | rec_set(a, "observed_at", rec_strn(h->now.ptr, h->now.len)) |
      rec_set(a, "environment", rec_str(environment)))
    h->oom = 1;
  return a;
}

static void digest_id(char *out, size_t n, const char *prefix, const char *value, size_t len) {
  char d[25];
  rec_digest(value, len, d);
  snprintf(out, n, "%s%s", prefix, d);
}

static char *env_digest(const rec *r, const char *a, const char *b, const char *c) {
  lj_buf s = {0};
  lj_buf_puts(&s, r->env.host_env);
  const char *parts[3] = {a, b, c};
  for (int i = 0; i < 3 && parts[i]; i++) {
    lj_buf_putc(&s, 0);
    lj_buf_puts(&s, parts[i]);
  }
  char *d = malloc(25);
  if (d && !s.oom) rec_digest(s.ptr, s.len, d);
  else {
    free(d);
    d = NULL;
  }
  lj_buf_free(&s);
  return d;
}

static int observe_shells(host_tx *h, const lj_value *shells) {
  rec *r = h->r;
  manage(h, "shell", "has-shell");
  const char *search = present(r->env.variables, "PATH") ? rec_getenv(r, "PATH") : "";
  char *env = env_digest(r, search, rec_getenv(r, "SHELL"), rec_getenv(r, "ComSpec"));
  if (!env) return EXT_LEDGER_IO;
  add(h, "host-inventory/shells", "host-inventory", "has-host-inventory", inventory_attrs(h, "shell", (double)shells->u.array.len, env));
  free(env);
  for (size_t i = 0; i < shells->u.array.len; i++) {
    const lj_value *s = shells->u.array.items[i];
    const char *path = str_at(s, "path");
    if (!host_abs(r, path) || !*str_at(s, "name")) return EXT_LEDGER_INVALID;
    char *key = path_key(r, path), id[64];
    if (!key) return EXT_LEDGER_IO;
    digest_id(id, sizeof id, "host-shell/", key, strlen(key));
    free(key);
    lj_value *a = rec_obj();
    const lj_value *sources = lj_get(s, "sources");
    if (rec_set(a, "name", rec_str(str_at(s, "name"))) | rec_set(a, "path", rec_str(path)) |
        rec_set(a, "resolved_path", rec_str(str_at(s, "resolved_path"))) | rec_set(a, "configured_default", rec_bool(bool_at(s, "configured_default"))) |
        rec_set(a, "sources", sources ? lj_clone(sources) : lj_new(LJ_NULL)))
      h->oom = 1;
    add(h, id, "shell", "has-shell", a);
  }
  return 0;
}

static int observe_filesystems(host_tx *h, const lj_value *filesystems) {
  rec *r = h->r;
  manage(h, "filesystem", "has-filesystem");
  add(h, "host-inventory/filesystems", "host-inventory", "has-host-inventory",
      inventory_attrs(h, "filesystem", (double)filesystems->u.array.len, r->env.host_env));
  for (size_t i = 0; i < filesystems->u.array.len; i++) {
    const lj_value *f = filesystems->u.array.items[i];
    const char *mount = str_at(f, "mount_point");
    if (!host_abs(r, mount)) return EXT_LEDGER_INVALID;
    char *source = rec_redact_source(str_at(f, "source")), *key = path_key(r, mount);
    if (!source || !key) {
      free(source);
      free(key);
      return EXT_LEDGER_IO;
    }
    lj_buf idb = {0};
    lj_buf_puts(&idb, key);
    lj_buf_putc(&idb, 0);
    lj_buf_puts(&idb, source);
    lj_buf_putc(&idb, 0);
    lj_buf_puts(&idb, str_at(f, "type"));
    char id[64];
    digest_id(id, sizeof id, "host-filesystem/", idb.ptr ? idb.ptr : "", idb.len);
    lj_buf_free(&idb);
    free(key);
    lj_value *a = rec_obj();
    int bad = rec_set(a, "mount_point", rec_str(mount)) | rec_set(a, "source", rec_str(source)) | rec_set(a, "type", rec_str(str_at(f, "type"))) |
              rec_set(a, "read_only", rec_bool(bool_at(f, "read_only")));
    free(source);
    const lj_value *space = lj_get(f, "space");
    if (space && space->type == LJ_OBJECT) {
      lj_value *sp = rec_obj();
      bad |= rec_set(sp, "total_bytes", rec_num(num_at(space, "total_bytes"))) | rec_set(sp, "free_bytes", rec_num(num_at(space, "free_bytes"))) |
             rec_set(sp, "available_bytes", rec_num(num_at(space, "available_bytes"))) | rec_set(a, "space", sp);
    }
    static const char *const optional[] = {"label", "drive_kind", "detail_error"};
    for (int k = 0; k < 3; k++)
      if (*str_at(f, optional[k])) bad |= rec_set(a, optional[k], rec_str(str_at(f, optional[k])));
    if (bad) h->oom = 1;
    add(h, id, "filesystem", "has-filesystem", a);
  }
  return 0;
}

static char *webview_identity(const rec *r, const lj_value *w, size_t *len) {
  char *key = path_key(r, str_at(w, "location"));
  if (!key) return NULL;
  lj_buf b = {0};
  lj_buf_puts(&b, str_at(w, "name"));
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, str_at(w, "scope"));
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, key);
  free(key);
  return lj_buf_take(&b, len);
}

static int observe_webviews(host_tx *h, const lj_value *webviews) {
  rec *r = h->r;
  manage(h, "webview", "has-webview");
  char *env = env_digest(r, rec_getenv(r, "LD_LIBRARY_PATH"), NULL, NULL);
  if (!env) return EXT_LEDGER_IO;
  add(h, "host-inventory/webviews", "host-inventory", "has-host-inventory", inventory_attrs(h, "webview", (double)webviews->u.array.len, env));
  free(env);
  static const char *const required[] = {"name", "engine", "api", "location", "scope", "source"};
  static const char *const optional[] = {"version", "abi_version", "architecture", "resolved_path", "detail_error"};
  for (size_t i = 0; i < webviews->u.array.len; i++) {
    const lj_value *w = webviews->u.array.items[i];
    lj_value *a = rec_obj();
    for (int k = 0; k < 6; k++) {
      if (!*str_at(w, required[k])) {
        lj_free(a);
        return EXT_LEDGER_INVALID;
      }
      if (rec_set(a, required[k], rec_str(str_at(w, required[k])))) h->oom = 1;
    }
    for (int k = 0; k < 5; k++)
      if (*str_at(w, optional[k]) && rec_set(a, optional[k], rec_str(str_at(w, optional[k])))) h->oom = 1;
    size_t identity_len = 0;
    char *identity = webview_identity(r, w, &identity_len), id[64];
    if (!identity) {
      lj_free(a);
      return EXT_LEDGER_IO;
    }
    digest_id(id, sizeof id, "host-webview/", identity, identity_len);
    free(identity);
    add(h, id, "webview", "has-webview", a);
  }
  return 0;
}

/* The inventory vertex of a kind observed at its own time. */
static lj_value *observed_inventory(host_tx *h, const char *kind, double count, const lj_value *source, const char *platform_key) {
  lj_value *a = rec_obj();
  if (rec_set(a, "kind", rec_str(kind)) | rec_set(a, "count", rec_num(count)) | rec_set(a, "observed_at", time_value(str_at(source, "observed_at"))) |
      rec_set(a, "environment", rec_str(h->r->env.host_env)))
    h->oom = 1;
  if (platform_key && rec_set(a, "platform", rec_str(str_at(source, platform_key)))) h->oom = 1;
  return a;
}

static int observe_desktop(host_tx *h, const lj_value *d) {
  if (!*str_at(d, "platform") || !time_set(d, "observed_at") || !*str_at(d, "recording.status") || !time_set(d, "recording.checked_at"))
    return EXT_LEDGER_INVALID;
  manage(h, "desktop", "has-desktop");
  add(h, "host-inventory/desktop", "host-inventory", "has-host-inventory", observed_inventory(h, "desktop", 1, d, NULL));
  add(h, "host-desktop/local", "desktop", "has-desktop", lj_clone(d));
  return 0;
}

static int observe_surfaces(host_tx *h, const lj_value *s) {
  const lj_value *displays = array_at(s, "displays"), *windows = array_at(s, "windows");
  if (!*str_at(s, "platform") || !*str_at(s, "coordinate_space") || !time_set(s, "observed_at") || !*str_at(s, "display_status.state") ||
      !*str_at(s, "window_status.state"))
    return EXT_LEDGER_INVALID;
  manage(h, "display", "has-display");
  manage(h, "window", "has-window");
  size_t nd = displays ? displays->u.array.len : 0, nw = windows ? windows->u.array.len : 0;
  lj_value *a = observed_inventory(h, "surface", (double)(nd + nw), s, "platform");
  if (rec_set(a, "display_count", rec_num((double)nd)) | rec_set(a, "window_count", rec_num((double)nw)) |
      rec_set(a, "coordinate_space", rec_str(str_at(s, "coordinate_space"))) | rec_set(a, "display_status", lj_clone(lj_get(s, "display_status"))) |
      rec_set(a, "window_status", lj_clone(lj_get(s, "window_status"))))
    h->oom = 1;
  add(h, "host-inventory/surfaces", "host-inventory", "has-host-inventory", a);
  for (size_t i = 0; i < nd; i++) {
    const lj_value *d = displays->u.array.items[i];
    const char *id = str_at(d, "id");
    if (!*id || num_at(d, "bounds.width") <= 0 || num_at(d, "bounds.height") <= 0) return EXT_LEDGER_INVALID;
    char vid[64];
    digest_id(vid, sizeof vid, "host-display/", id, strlen(id));
    add(h, vid, "display", "has-display", lj_clone(d));
  }
  for (size_t i = 0; i < nw; i++) {
    const lj_value *w = windows->u.array.items[i];
    const char *id = str_at(w, "id");
    if (!*id || num_at(w, "pid") <= 0 || !*str_at(w, "process_start_id") || num_at(w, "bounds.width") <= 0 || num_at(w, "bounds.height") <= 0)
      return EXT_LEDGER_INVALID;
    char vid[64];
    digest_id(vid, sizeof vid, "host-window/", id, strlen(id));
    add(h, vid, "window", "has-window", lj_clone(w));
  }
  return 0;
}

static int observe_accessibility(host_tx *h, const lj_value *a) {
  const lj_value *elements = array_at(a, "elements");
  if (!*str_at(a, "platform") || !time_set(a, "observed_at") || !*str_at(a, "status.state")) return EXT_LEDGER_INVALID;
  manage(h, "accessibility-element", "has-accessibility-element");
  size_t n = elements ? elements->u.array.len : 0;
  lj_value *attrs = observed_inventory(h, "accessibility", (double)n, a, "platform");
  if (rec_set(attrs, "status", lj_clone(lj_get(a, "status")))) h->oom = 1;
  add(h, "host-inventory/accessibility", "host-inventory", "has-host-inventory", attrs);
  for (size_t i = 0; i < n; i++) {
    const lj_value *e = elements->u.array.items[i];
    const char *id = str_at(e, "id");
    if (!*id || num_at(e, "pid") <= 0 || !*str_at(e, "process_start_id") || !*str_at(e, "role")) return EXT_LEDGER_INVALID;
    char vid[64];
    digest_id(vid, sizeof vid, "host-accessibility/", id, strlen(id));
    add(h, vid, "accessibility-element", "has-accessibility-element", lj_clone(e));
  }
  return 0;
}

static int observe_resources(host_tx *h, const lj_value *res) {
  if (!*str_at(res, "os") || !*str_at(res, "architecture") || !time_set(res, "observed_at") || num_at(res, "logical_cpus") <= 0 ||
      !*str_at(res, "cpu_status.state") || !*str_at(res, "memory_status.state") || !*str_at(res, "uptime_status.state") ||
      !*str_at(res, "container.state"))
    return EXT_LEDGER_INVALID;
  manage(h, "host-resource", "has-host-resource");
  add(h, "host-inventory/resources", "host-inventory", "has-host-inventory", observed_inventory(h, "resource", 1, res, "os"));
  add(h, "host-resources/local", "host-resource", "has-host-resource", lj_clone(res));
  return 0;
}

static int observe_permissions(host_tx *h, const lj_value *p) {
  if (!*str_at(p, "platform") || !time_set(p, "observed_at") || !*str_at(p, "accessibility.state") || !*str_at(p, "screen_recording.state"))
    return EXT_LEDGER_INVALID;
  manage(h, "permission", "has-permission");
  add(h, "host-inventory/permissions", "host-inventory", "has-host-inventory", observed_inventory(h, "permission", 2, p, "platform"));
  static const char *const names[2][2] = {{"accessibility", "accessibility"}, {"screen-recording", "screen_recording"}};
  for (int i = 0; i < 2; i++) {
    lj_value *a = lj_clone(lj_get(p, names[i][1]));
    if (rec_set(a, "name", rec_str(names[i][0]))) h->oom = 1;
    char id[64];
    snprintf(id, sizeof id, "host-permission/%s", names[i][0]);
    add(h, id, "permission", "has-permission", a);
  }
  return 0;
}

static lj_value *values_of(const lj_value *keyed) {
  lj_value *a = lj_new(LJ_ARRAY);
  for (size_t i = 0; a && i < keyed->u.object.len; i++)
    if (lj_array_push(a, lj_clone(keyed->u.object.members[i].value))) {
      lj_free(a);
      return NULL;
    }
  return a;
}

/* Replaces the managed kinds in one transaction at the snapshot's revision, retrying a race. */
static int reconcile(host_tx *h, const ext_ledger_context *ctx) {
  for (int attempt = 0; attempt < 3; attempt++) {
    lj_value *snap;
    int rc = rec_snapshot(h->r, ctx, &snap);
    if (rc) return rc;
    lj_value *tx = rec_obj(), *dv = lj_new(LJ_ARRAY), *de = lj_new(LJ_ARRAY);
    int bad = !dv || !de;
    const lj_value *vertices = array_at(snap, "vertices"), *edges = array_at(snap, "edges");
    for (size_t i = 0; vertices && i < vertices->u.array.len; i++) {
      const lj_value *v = vertices->u.array.items[i];
      const char *kind = rec_field(v, "kind"), *id = rec_field(v, "id");
      if (!ll_map_get(&h->kinds, kind, strlen(kind)) || lj_get(h->vertices, id)) continue;
      lj_value *ref = rec_obj();
      bad |= rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_str(id)) | (ref ? lj_array_push(dv, ref) : -1);
    }
    for (size_t i = 0; edges && i < edges->u.array.len; i++) {
      const lj_value *e = edges->u.array.items[i];
      const char *type = rec_field(e, "type"), *id = rec_field(e, "id");
      if (!ll_map_get(&h->types, type, strlen(type)) || lj_get(h->edges, id)) continue;
      lj_value *ref = rec_obj();
      bad |= rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_str(id)) | (ref ? lj_array_push(de, ref) : -1);
    }
    bad |= rec_set(tx, "namespace", rec_str(REC_NS)) | rec_set(tx, "expected_revision", rec_num(num_at(snap, "revision"))) |
           rec_set(tx, "delete_incident_edges", rec_bool(1)) | rec_set(tx, "delete_vertices", dv) | rec_set(tx, "delete_edges", de) |
           rec_set(tx, "vertices", values_of(h->vertices)) | rec_set(tx, "edges", values_of(h->edges));
    lj_free(snap);
    if (bad) {
      lj_free(tx);
      return EXT_LEDGER_IO;
    }
    rc = rec_apply(h->r, ctx, tx);
    if (rc != EXT_LEDGER_CHANGED) return rc;
  }
  return EXT_LEDGER_BUSY; /* the host inventory changed during the observation */
}

int rec_observe_host(rec *r, const ext_ledger_context *ctx, const lj_value *in) {
  static const char *const kinds[] = {"shells", "filesystems", "webviews", "desktop", "surfaces", "accessibility", "permissions", "resources"};
  int any = 0;
  for (size_t i = 0; i < sizeof kinds / sizeof *kinds; i++) any |= present(in, kinds[i]);
  if (!any) return 0;
  host_tx h;
  memset(&h, 0, sizeof h);
  h.r = r;
  h.vertices = rec_obj();
  h.edges = rec_obj();
  rec_now_text(r, &h.now);
  lj_value *machine = rec_obj();
  if (rec_set(machine, "hostname", rec_str(r->env.hostname)) | rec_set(machine, "os", rec_str(r->env.os)) |
      rec_set(machine, "architecture", rec_str(r->env.arch)))
    h.oom = 1;
  lj_value *mv = rec_vertex(r, "machine/local", "machine", machine, "host-observed");
  if (!mv || !h.vertices || !h.edges || lj_object_set(h.vertices, "machine/local", 13, mv)) h.oom = 1;
  int rc = 0;
  const lj_value *v;
  if (!rc && (v = array_at(in, "shells"))) rc = observe_shells(&h, v);
  if (!rc && (v = array_at(in, "filesystems"))) rc = observe_filesystems(&h, v);
  if (!rc && (v = array_at(in, "webviews"))) rc = observe_webviews(&h, v);
  if (!rc && present(in, "desktop")) rc = observe_desktop(&h, lj_get(in, "desktop"));
  if (!rc && present(in, "surfaces")) rc = observe_surfaces(&h, lj_get(in, "surfaces"));
  if (!rc && present(in, "accessibility")) rc = observe_accessibility(&h, lj_get(in, "accessibility"));
  if (!rc && present(in, "resources")) rc = observe_resources(&h, lj_get(in, "resources"));
  if (!rc && present(in, "permissions")) rc = observe_permissions(&h, lj_get(in, "permissions"));
  if (!rc && h.oom) rc = EXT_LEDGER_IO;
  if (!rc) rc = reconcile(&h, ctx);
  lj_free(h.vertices);
  lj_free(h.edges);
  ll_map_free(&h.kinds);
  ll_map_free(&h.types);
  lj_buf_free(&h.now);
  return rc;
}

/* ---- read_host ---- */

static lj_value *status_value(const lj_value *s) {
  if (s && s->type == LJ_OBJECT) return lj_clone(s);
  lj_value *o = rec_obj();
  if (rec_set(o, "state", rec_str(""))) {
    lj_free(o);
    return NULL;
  }
  return o;
}

static lj_value *zero_time(void) { return rec_str("0001-01-01T00:00:00Z"); }

static lj_value *typed(const lj_value *attrs, const char *const *strings, const char *const *bools, const char *const *optional) {
  lj_value *o = rec_obj();
  int bad = !o;
  for (int i = 0; strings && strings[i]; i++) bad |= rec_set(o, strings[i], rec_str(str_at(attrs, strings[i])));
  for (int i = 0; bools && bools[i]; i++) bad |= rec_set(o, bools[i], rec_bool(bool_at(attrs, bools[i])));
  for (int i = 0; optional && optional[i]; i++)
    if (*str_at(attrs, optional[i])) bad |= rec_set(o, optional[i], rec_str(str_at(attrs, optional[i])));
  if (bad) {
    lj_free(o);
    return NULL;
  }
  return o;
}

typedef struct {
  char *key;
  lj_value *value;
} keyed;

static int compare_keyed(const void *a, const void *b) {
  const keyed *x = a, *y = b;
  return strcmp(x->key ? x->key : "", y->key ? y->key : "");
}

/* Sorts records by: 0 path key, 1 mount point key, 2 webview identity, 3 id. */
static void sort_array(const rec *r, lj_value *a, int by) {
  if (!a || a->u.array.len < 2) return;
  size_t n = a->u.array.len;
  keyed *k = calloc(n, sizeof *k);
  if (!k) return;
  for (size_t i = 0; i < n; i++) {
    lj_value *v = a->u.array.items[i];
    k[i].value = v;
    switch (by) {
    case 0: k[i].key = path_key(r, str_at(v, "path")); break;
    case 1: k[i].key = path_key(r, str_at(v, "mount_point")); break;
    case 2: { /* the identity holds NULs; sort it on its printable form */
      size_t n = 0;
      if ((k[i].key = webview_identity(r, v, &n)))
        for (size_t c = 0; c < n; c++)
          if (!k[i].key[c]) k[i].key[c] = 1;
      break;
    }
    default: {
      const char *id = str_at(v, "id");
      if ((k[i].key = malloc(strlen(id) + 1))) memcpy(k[i].key, id, strlen(id) + 1);
    }
    }
  }
  qsort(k, n, sizeof *k, compare_keyed);
  for (size_t i = 0; i < n; i++) {
    a->u.array.items[i] = k[i].value;
    free(k[i].key);
  }
  free(k);
}

static lj_value *ensure(lj_value *result, const char *key, const char *const *arrays, const char *const *statuses) {
  lj_value *o = lj_get(result, key);
  if (o) return o;
  o = rec_obj();
  int bad = rec_set(o, "platform", rec_str("")) | rec_set(o, "observed_at", zero_time());
  for (int i = 0; arrays && arrays[i]; i++) bad |= rec_set(o, arrays[i], lj_new(LJ_ARRAY));
  for (int i = 0; statuses && statuses[i]; i++) bad |= rec_set(o, statuses[i], status_value(NULL));
  if (bad || rec_set(result, key, o)) return NULL;
  return lj_get(result, key);
}

int rec_read_host(rec *r, const ext_ledger_context *ctx, char **out, size_t *out_len) {
  lj_value *snap;
  int rc = rec_snapshot(r, ctx, &snap);
  if (rc) return rc;
  static const char *const surface_arrays[] = {"displays", "windows", NULL}, *const surface_statuses[] = {"display_status", "window_status", NULL};
  static const char *const access_arrays[] = {"elements", NULL}, *const access_statuses[] = {"status", NULL};
  static const char *const perm_statuses[] = {"accessibility", "screen_recording", NULL};
  static const char *const shell_strings[] = {"name", "path", "resolved_path", NULL}, *const shell_bools[] = {"configured_default", NULL};
  static const char *const fs_strings[] = {"mount_point", "source", "type", NULL}, *const fs_bools[] = {"read_only", NULL};
  static const char *const fs_optional[] = {"label", "drive_kind", "detail_error", NULL};
  static const char *const wv_strings[] = {"name", "engine", "api", "location", "scope", "source", NULL};
  static const char *const wv_optional[] = {"version", "abi_version", "architecture", "resolved_path", "detail_error", NULL};
  static const char *const perm_strings[] = {"state", NULL}, *const perm_optional[] = {"reason", "scope", "detail", NULL};
  lj_value *result = rec_obj(), *shells = lj_new(LJ_ARRAY), *filesystems = lj_new(LJ_ARRAY), *webviews = lj_new(LJ_ARRAY),
           *observed = rec_obj();
  int bad = !result || !shells || !filesystems || !webviews || !observed;
  const lj_value *vertices = array_at(snap, "vertices");
  for (size_t i = 0; !bad && vertices && i < vertices->u.array.len; i++) {
    const lj_value *v = vertices->u.array.items[i];
    const char *kind = rec_field(v, "kind");
    const lj_value *a = lj_get(v, "attributes");
    if (strncmp(kind, REC_NS "/", sizeof REC_NS) != 0) continue;
    kind += sizeof REC_NS;
    if (strcmp(kind, "host-inventory") == 0) {
      const char *ikind = str_at(a, "kind");
      ll_time t;
      if (ll_time_parse(str_at(a, "observed_at"), strlen(str_at(a, "observed_at")), &t)) {
        bad = 1;
        rc = EXT_LEDGER_IO;
        break;
      }
      bad |= rec_set(observed, ikind, time_value(str_at(a, "observed_at")));
      lj_value *o = NULL;
      if (strcmp(ikind, "surface") == 0 && (o = ensure(result, "surfaces", surface_arrays, surface_statuses)))
        bad |= rec_set(o, "observed_at", time_value(str_at(a, "observed_at"))) | rec_set(o, "platform", rec_str(str_at(a, "platform"))) |
               rec_set(o, "coordinate_space", rec_str(str_at(a, "coordinate_space"))) |
               rec_set(o, "display_status", status_value(lj_get(a, "display_status"))) | rec_set(o, "window_status", status_value(lj_get(a, "window_status")));
      if (strcmp(ikind, "accessibility") == 0 && (o = ensure(result, "accessibility", access_arrays, access_statuses)))
        bad |= rec_set(o, "observed_at", time_value(str_at(a, "observed_at"))) | rec_set(o, "platform", rec_str(str_at(a, "platform"))) |
               rec_set(o, "status", status_value(lj_get(a, "status")));
      if (strcmp(ikind, "permission") == 0 && (o = ensure(result, "permissions", NULL, perm_statuses)))
        bad |= rec_set(o, "observed_at", time_value(str_at(a, "observed_at"))) | rec_set(o, "platform", rec_str(str_at(a, "platform")));
    } else if (strcmp(kind, "shell") == 0) {
      lj_value *s = typed(a, shell_strings, shell_bools, NULL);
      const lj_value *sources = lj_get(a, "sources");
      bad |= rec_set(s, "sources", sources ? lj_clone(sources) : lj_new(LJ_NULL)) | lj_array_push(shells, s);
    } else if (strcmp(kind, "filesystem") == 0) {
      lj_value *f = typed(a, fs_strings, fs_bools, fs_optional);
      const lj_value *space = lj_get(a, "space");
      if (space && space->type == LJ_OBJECT) bad |= rec_set(f, "space", lj_clone(space));
      bad |= lj_array_push(filesystems, f);
    } else if (strcmp(kind, "webview") == 0) {
      bad |= lj_array_push(webviews, typed(a, wv_strings, NULL, wv_optional));
    } else if (strcmp(kind, "desktop") == 0) {
      bad |= rec_set(result, "desktop", a ? lj_clone(a) : rec_obj());
    } else if (strcmp(kind, "display") == 0 || strcmp(kind, "window") == 0) {
      lj_value *o = ensure(result, "surfaces", surface_arrays, surface_statuses);
      bad |= !o || lj_array_push(lj_get(o, kind[0] == 'd' ? "displays" : "windows"), a ? lj_clone(a) : rec_obj());
    } else if (strcmp(kind, "accessibility-element") == 0) {
      lj_value *o = ensure(result, "accessibility", access_arrays, access_statuses);
      bad |= !o || lj_array_push(lj_get(o, "elements"), a ? lj_clone(a) : rec_obj());
    } else if (strcmp(kind, "permission") == 0) {
      const char *name = str_at(a, "name");
      const char *key = strcmp(name, "accessibility") == 0 ? "accessibility" : strcmp(name, "screen-recording") == 0 ? "screen_recording" : NULL;
      if (!key) continue;
      lj_value *o = ensure(result, "permissions", NULL, perm_statuses);
      bad |= !o || rec_set(o, key, typed(a, perm_strings, NULL, perm_optional));
    }
  }
  lj_free(snap);
  if (!bad) {
    sort_array(r, shells, 0);
    sort_array(r, filesystems, 1);
    sort_array(r, webviews, 2);
    lj_value *o;
    if ((o = lj_get(result, "surfaces"))) {
      sort_array(r, lj_get(o, "displays"), 3);
      sort_array(r, lj_get(o, "windows"), 3);
    }
    if ((o = lj_get(result, "accessibility"))) sort_array(r, lj_get(o, "elements"), 3);
    bad |= rec_set(result, "shells", shells) | rec_set(result, "filesystems", filesystems) | rec_set(result, "webviews", webviews) |
           rec_set(result, "observed_at", observed);
    shells = filesystems = webviews = observed = NULL;
  }
  lj_free(shells);
  lj_free(filesystems);
  lj_free(webviews);
  lj_free(observed);
  if (bad) {
    lj_free(result);
    return rc ? rc : EXT_LEDGER_IO;
  }
  lj_buf b = {0};
  lj_write(&b, result);
  lj_free(result);
  char *p = lj_buf_take(&b, out_len);
  if (!p) return EXT_LEDGER_IO;
  *out = p;
  return 0;
}
