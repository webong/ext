/* Adapter inventory: observe_inventory and resolve_inventory (recorder/v1).
 * Adapters, what they declare, the contexts they listed, the resources they
 * observed, the programs they run and the installers of those programs, and
 * the user's aliases. Context names that look like paths, URLs or opaque values
 * are kept only as digests. A list Go leaves nil is null in the graph, an empty
 * one is []; both forms are kept as Go writes them. */
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *str(const lj_value *o, const char *key) { return rec_field(o, key); }

static const lj_value *array(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_ARRAY ? v : NULL;
}

static int boolean(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_BOOL && v->u.boolean;
}

/* The digest of parts joined by NUL. */
static void digest_parts(char out[25], const char *const *parts, int n) {
  lj_buf b = {0};
  for (int i = 0; i < n; i++) {
    if (i) lj_buf_putc(&b, 0);
    lj_buf_puts(&b, parts[i]);
  }
  rec_digest(b.ptr ? b.ptr : "", b.len, out);
  lj_buf_free(&b);
}

static void id2(char *out, size_t size, const char *prefix, const char *a, const char *b) {
  const char *parts[2] = {a, b};
  char d[25];
  digest_parts(d, parts, b ? 2 : 1);
  snprintf(out, size, "%s%s", prefix, d);
}

static int cmp_strings(const void *a, const void *b) {
  const lj_value *x = *(lj_value *const *)a, *y = *(lj_value *const *)b;
  return strcmp(x->u.string.ptr, y->u.string.ptr);
}

/* The string elements of an array, sorted when asked; null when there are none, as Go's nil slice is. */
static lj_value *strings_or_null(const lj_value *a, int sorted) {
  lj_value *out = lj_new(LJ_ARRAY);
  for (size_t i = 0; out && a && i < a->u.array.len; i++)
    if (a->u.array.items[i]->type == LJ_STRING && lj_array_push(out, lj_clone(a->u.array.items[i]))) {
      lj_free(out);
      return NULL;
    }
  if (!out) return NULL;
  if (!out->u.array.len) {
    lj_free(out);
    return lj_new(LJ_NULL);
  }
  if (sorted) qsort(out->u.array.items, out->u.array.len, sizeof *out->u.array.items, cmp_strings);
  return out;
}

/* A set of strings, as a sorted array (never null). */
typedef struct {
  ll_map m;
} set;

static void set_add(set *s, const char *v, int *oom) {
  if (ll_map_put(&s->m, v, strlen(v), (void *)1)) *oom = 1;
}

static lj_value *set_sorted(set *s) {
  lj_value *a = lj_new(LJ_ARRAY);
  LL_MAP_EACH(&s->m, slot) {
    if (lj_array_push(a, rec_strn(LL_S(slot->key), slot->key.n))) {
      lj_free(a);
      return NULL;
    }
  }
  if (a && a->u.array.len) qsort(a->u.array.items, a->u.array.len, sizeof *a->u.array.items, cmp_strings);
  return a;
}

static int set_has(const set *s, const char *v) { return ll_map_get(&s->m, v, strlen(v)) != NULL; }

typedef struct {
  rec *r;
  lj_value *vertices, *edges; /* keyed by ID */
  lj_buf now;
  int oom;
} inv_tx;

static void put_vertex(inv_tx *t, const char *id, const char *kind, lj_value *attrs, const char *source, const char *operation) {
  lj_value *v = rec_obj(), *prov = rec_obj();
  char k[96];
  snprintf(k, sizeof k, REC_NS "/%s", kind);
  int bad = !attrs | rec_set(prov, "source", rec_str(source)) | rec_set(prov, "operation", rec_str(operation)) |
            rec_set(prov, "at", rec_strn(t->now.ptr, t->now.len)) | rec_set(v, "id", rec_str(id)) | rec_set(v, "kind", rec_str(k)) |
            rec_set(v, "provenance", prov);
  if (attrs && attrs->u.object.len) bad |= rec_set(v, "attributes", attrs);
  else lj_free(attrs);
  if (bad || lj_object_set(t->vertices, id, strlen(id), v)) t->oom = 1;
}

static void put_edge(inv_tx *t, const char *id, const char *from, const char *to, const char *type, lj_value *attrs) {
  lj_value *e = rec_edge(id, from, to, type);
  int bad = !e;
  if (attrs) bad |= rec_set(e, "attributes", attrs);
  if (bad || lj_object_set(t->edges, id, strlen(id), e)) t->oom = 1;
}

static const lj_value *vertex(const inv_tx *t, const char *id) { return lj_get(t->vertices, id); }

static void context_id(char *out, size_t n, const char *adapter, const char *selection) { id2(out, n, "context/", adapter, selection); }

/* ---- programs and installers ---- */

typedef struct {
  const lj_value *first; /* the first candidate for the file */
  char *file;
  set names, installers;
  int on_path;
  const char *version;
} merged;

static void observe_programs(inv_tx *t, const lj_value *adapters, const lj_value *declared) {
  merged *files = NULL;
  size_t nfiles = 0, cap = 0;
  for (size_t a = 0; adapters && a < adapters->u.array.len; a++) {
    const lj_value *programs = array(adapters->u.array.items[a], "programs");
    for (size_t p = 0; programs && p < programs->u.array.len; p++) {
      const lj_value *c = programs->u.array.items[p];
      const char *file = *str(c, "realpath") ? str(c, "realpath") : str(c, "path");
      if (!*file) continue;
      merged *m = NULL;
      for (size_t i = 0; i < nfiles && !m; i++)
        if (strcmp(files[i].file, file) == 0) m = &files[i];
      if (!m) {
        if (nfiles == cap) {
          cap = cap ? cap * 2 : 8;
          merged *grown = realloc(files, cap * sizeof *files);
          if (!grown) {
            t->oom = 1;
            break;
          }
          files = grown;
        }
        m = &files[nfiles++];
        memset(m, 0, sizeof *m);
        m->first = c;
        m->file = malloc(strlen(file) + 1);
        if (m->file) memcpy(m->file, file, strlen(file) + 1);
        else t->oom = 1;
        m->version = "";
      }
      set_add(&m->names, str(c, "name"), &t->oom);
      const lj_value *installers = array(c, "installers");
      for (size_t i = 0; installers && i < installers->u.array.len; i++)
        if (installers->u.array.items[i]->type == LJ_STRING) set_add(&m->installers, installers->u.array.items[i]->u.string.ptr, &t->oom);
      m->on_path |= boolean(c, "on_path");
      if (!*m->version) m->version = str(c, "version");
    }
  }
  /* Every installer adapter and declared installer is a vertex, and so is any other installer a program names. */
  ll_map sources = {0}; /* name -> set* */
  set installer_adapters = {{0}};
  for (size_t a = 0; adapters && a < adapters->u.array.len; a++) {
    const lj_value *ad = adapters->u.array.items[a];
    const lj_value *roles = array(ad, "role");
    int installer = 0;
    for (size_t i = 0; roles && i < roles->u.array.len && !installer; i++)
      installer = roles->u.array.items[i]->type == LJ_STRING && strcmp(roles->u.array.items[i]->u.string.ptr, "installer") == 0;
    if (!installer || !*str(ad, "name")) continue;
    set *s = ll_map_get(&sources, str(ad, "name"), strlen(str(ad, "name")));
    if (!s && (!(s = calloc(1, sizeof *s)) || ll_map_put(&sources, str(ad, "name"), strlen(str(ad, "name")), s))) t->oom = 1;
    if (s) set_add(s, "adapter", &t->oom);
    set_add(&installer_adapters, str(ad, "name"), &t->oom);
  }
  for (size_t i = 0; declared && i < declared->u.array.len; i++) {
    const lj_value *d = declared->u.array.items[i];
    const char *name = str(d, "name"), *source = *str(d, "source") ? str(d, "source") : "config";
    if (!*name) continue;
    set *s = ll_map_get(&sources, name, strlen(name));
    if (!s && (!(s = calloc(1, sizeof *s)) || ll_map_put(&sources, name, strlen(name), s))) t->oom = 1;
    if (s) set_add(s, source, &t->oom);
  }
  ll_map program_ids = {0}; /* file or path -> id (owned) */
  for (size_t i = 0; i < nfiles; i++) {
    merged *m = &files[i];
    char id[64], edge[64], d[25];
    id2(id, sizeof id, "program/", m->file, NULL);
    lj_value *installers = set_sorted(&m->installers), *a = rec_obj();
    t->oom |= rec_set(a, "names", set_sorted(&m->names)) | rec_set(a, "path", rec_str(str(m->first, "path"))) |
              rec_set(a, "realpath", rec_str(m->file)) | rec_set(a, "on_path", rec_bool(m->on_path)) | rec_set(a, "installers", lj_clone(installers));
    if (*m->version) t->oom |= rec_set(a, "version", rec_str(m->version));
    put_vertex(t, id, "program", a, "ctx", "program-discovered");
    rec_digest(m->file, strlen(m->file), d);
    snprintf(edge, sizeof edge, "machine-program/%s", d);
    put_edge(t, edge, "machine/local", id, "has-program", NULL);
    for (size_t k = 0; installers && k < installers->u.array.len; k++) {
      const char *name = installers->u.array.items[k]->u.string.ptr;
      if (!ll_map_get(&sources, name, strlen(name))) { /* reported by neither an adapter nor the configuration: recognised */
        set *s = calloc(1, sizeof *s);
        if (!s || ll_map_put(&sources, name, strlen(name), s)) t->oom = 1;
        else set_add(s, "recognised", &t->oom);
      }
      char to[64];
      id2(edge, sizeof edge, "program-installer/", m->file, name);
      id2(to, sizeof to, "installer/", name, NULL);
      put_edge(t, edge, id, to, "installed-by", NULL);
    }
    lj_free(installers);
    char *copy1 = malloc(strlen(id) + 1), *copy2 = malloc(strlen(id) + 1);
    if (copy1) memcpy(copy1, id, strlen(id) + 1);
    if (copy2) memcpy(copy2, id, strlen(id) + 1);
    free(ll_map_get(&program_ids, m->file, strlen(m->file)));
    if (!copy1 || ll_map_put(&program_ids, m->file, strlen(m->file), copy1)) t->oom = 1;
    const char *path = str(m->first, "path");
    free(ll_map_get(&program_ids, path, strlen(path)));
    if (!copy2 || ll_map_put(&program_ids, path, strlen(path), copy2)) t->oom = 1;
  }
  LL_MAP_EACH(&sources, slot) {
    char id[64];
    const char *name = LL_S(slot->key);
    id2(id, sizeof id, "installer/", name, NULL);
    lj_value *a = rec_obj();
    t->oom |= rec_set(a, "name", rec_str(name)) | rec_set(a, "sources", set_sorted(slot->value));
    int is_adapter = set_has(&installer_adapters, name);
    if (is_adapter) t->oom |= rec_set(a, "adapter", rec_str(name));
    put_vertex(t, id, "installer", a, "ctx", "installer-declared");
    char adapter_id[160];
    snprintf(adapter_id, sizeof adapter_id, "adapter/%s", name);
    if (is_adapter && vertex(t, adapter_id)) {
      char edge[64];
      id2(edge, sizeof edge, "installer-adapter/", name, NULL);
      put_edge(t, edge, id, adapter_id, "implemented-by", NULL);
    }
  }
  /* A context that names the file it runs is joined to its program; an installer it suggests is a hint, kept apart. */
  ll_map suggested = {0}; /* program id -> set* */
  for (size_t i = 0; i < t->vertices->u.object.len; i++) {
    const lj_value *v = t->vertices->u.object.members[i].value;
    if (strcmp(str(v, "kind"), REC_NS "/context") != 0) continue;
    const lj_value *metadata = lj_get(lj_get(v, "attributes"), "metadata");
    const char *executable = str(metadata, "executable");
    const char *program = *executable ? ll_map_get(&program_ids, executable, strlen(executable)) : NULL;
    if (!program) continue;
    char edge[64];
    id2(edge, sizeof edge, "context-program/", str(v, "id"), NULL);
    put_edge(t, edge, str(v, "id"), program, "uses-program", NULL);
    const char *hint = str(metadata, "suggested_installer");
    if (*hint) {
      set *s = ll_map_get(&suggested, program, strlen(program));
      if (!s && (!(s = calloc(1, sizeof *s)) || ll_map_put(&suggested, program, strlen(program), s))) t->oom = 1;
      if (s) set_add(s, hint, &t->oom);
    }
  }
  LL_MAP_EACH(&suggested, slot) {
    lj_value *v = lj_get(t->vertices, LL_S(slot->key));
    lj_value *a = lj_get(v, "attributes");
    const lj_value *confirmed = array(a, "installers");
    set open = {{0}};
    LL_MAP_EACH(&((set *)slot->value)->m, hint) {
      int found = 0;
      for (size_t k = 0; confirmed && k < confirmed->u.array.len && !found; k++)
        found = strcmp(confirmed->u.array.items[k]->u.string.ptr, LL_S(hint->key)) == 0;
      if (!found) set_add(&open, LL_S(hint->key), &t->oom);
    }
    if (open.m.len) t->oom |= rec_set(a, "suggested_installers", set_sorted(&open));
    ll_map_free(&open.m);
  }
  LL_MAP_EACH(&suggested, slot) {
    ll_map_free(&((set *)slot->value)->m);
    free(slot->value);
  }
  ll_map_free(&suggested);
  LL_MAP_EACH(&program_ids, slot) free(slot->value);
  ll_map_free(&program_ids);
  LL_MAP_EACH(&sources, slot) {
    ll_map_free(&((set *)slot->value)->m);
    free(slot->value);
  }
  ll_map_free(&sources);
  ll_map_free(&installer_adapters.m);
  for (size_t i = 0; i < nfiles; i++) {
    free(files[i].file);
    ll_map_free(&files[i].names.m);
    ll_map_free(&files[i].installers.m);
  }
  free(files);
}

/* ---- observe_inventory ---- */

static int inventory_kind(const char *kind) {
  static const char *const kinds[] = {"inventory", "adapter", "capability", "support", "context", "resource", "alias", "program", "installer"};
  if (strncmp(kind, REC_NS "/", sizeof REC_NS) != 0) return 0;
  for (size_t i = 0; i < sizeof kinds / sizeof *kinds; i++)
    if (strcmp(kind + sizeof REC_NS, kinds[i]) == 0) return 1;
  return 0;
}

static int inventory_type(const char *type) {
  static const char *const types[] = {"has-adapter", "supports",    "supports-kind", "offers",         "contains",    "relates",
                                      "resolves-to", "has-program", "installed-by",  "implemented-by", "uses-program"};
  if (strncmp(type, REC_NS "/", sizeof REC_NS) != 0) return 0;
  for (size_t i = 0; i < sizeof types / sizeof *types; i++)
    if (strcmp(type + sizeof REC_NS, types[i]) == 0) return 1;
  return 0;
}

static void observe_adapter(inv_tx *t, const lj_value *ad, set *observed) {
  const char *name = str(ad, "name");
  if (!*name) return;
  char adapter_id[160], edge[160];
  snprintf(adapter_id, sizeof adapter_id, "adapter/%s", name);
  lj_value *a = rec_obj();
  t->oom |= rec_set(a, "name", rec_str(name)) | rec_set(a, "runtime", rec_str(str(ad, "runtime"))) |
            rec_set(a, "surfaces", strings_or_null(array(ad, "surfaces"), 1)) | rec_set(a, "supports", strings_or_null(array(ad, "supports"), 1)) |
            rec_set(a, "engine", strings_or_null(array(ad, "engine"), 1)) | rec_set(a, "role", strings_or_null(array(ad, "role"), 1)) |
            rec_set(a, "browser_share", strings_or_null(array(ad, "browser_share"), 0)) | rec_set(a, "selector", rec_str(str(ad, "selector"))) |
            rec_set(a, "trusted", rec_bool(boolean(ad, "trusted"))) | rec_set(a, "list_supported", rec_bool(boolean(ad, "list_supported"))) |
            rec_set(a, "observe_supported", rec_bool(boolean(ad, "observe_supported"))) | rec_set(a, "listed", rec_bool(boolean(ad, "listed"))) |
            rec_set(a, "discovery_status", rec_str(str(ad, "discovery_status")));
  put_vertex(t, adapter_id, "adapter", a, "ctx", "adapter-inventory");
  snprintf(edge, sizeof edge, "machine-adapter/%s", name);
  put_edge(t, edge, "machine/local", adapter_id, "has-adapter", NULL);
  const lj_value *capabilities = array(ad, "capabilities"), *supports = array(ad, "supports");
  for (size_t i = 0; capabilities && i < capabilities->u.array.len; i++) {
    if (capabilities->u.array.items[i]->type != LJ_STRING) continue;
    const char *c = capabilities->u.array.items[i]->u.string.ptr;
    char id[64];
    id2(id, sizeof id, "capability/", c, NULL);
    lj_value *attrs = rec_obj();
    t->oom |= rec_set(attrs, "name", rec_str(c));
    put_vertex(t, id, "capability", attrs, "ctx", "adapter-declared");
    id2(edge, sizeof edge, "adapter-capability/", name, c);
    put_edge(t, edge, adapter_id, id, "supports", NULL);
  }
  for (size_t i = 0; supports && i < supports->u.array.len; i++) {
    if (supports->u.array.items[i]->type != LJ_STRING) continue;
    const char *k = supports->u.array.items[i]->u.string.ptr;
    char id[64];
    id2(id, sizeof id, "support/", k, NULL);
    lj_value *attrs = rec_obj();
    t->oom |= rec_set(attrs, "name", rec_str(k));
    put_vertex(t, id, "support", attrs, "ctx", "adapter-declared");
    id2(edge, sizeof edge, "adapter-support/", name, k);
    put_edge(t, edge, adapter_id, id, "supports-kind", NULL);
  }
  if (!boolean(ad, "trusted") || !boolean(ad, "listed")) return;
  const lj_value *contexts = array(ad, "contexts");
  for (size_t i = 0; contexts && i < contexts->u.array.len; i++) {
    const lj_value *c = contexts->u.array.items[i];
    const char *selection = str(c, "selection");
    if (!*selection) continue;
    char id[64], d[25];
    context_id(id, sizeof id, name, selection);
    rec_digest(selection, strlen(selection), d);
    lj_value *attrs = rec_obj();
    t->oom |= rec_set(attrs, "adapter", rec_str(name)) | rec_set(attrs, "runtime", rec_str(str(ad, "runtime"))) |
              rec_set(attrs, "selection_digest", rec_str(d));
    static const char *const passed[] = {"capabilities", "supports", "browser_share"};
    for (int k = 0; k < 3; k++) {
      const lj_value *v = lj_get(c, passed[k]);
      if (v && v->type != LJ_NULL) t->oom |= rec_set(attrs, passed[k], lj_clone(v));
    }
    const lj_value *meta = lj_get(c, "attributes");
    if (meta && meta->type == LJ_OBJECT && meta->u.object.len) t->oom |= rec_set(attrs, "metadata", lj_clone(meta));
    if (rec_context_name(selection, strlen(selection))) t->oom |= rec_set(attrs, "selection", rec_str(selection));
    else t->oom |= rec_set(attrs, "selection_redacted", rec_bool(1));
    put_vertex(t, id, "context", attrs, name, "adapter-list");
    id2(edge, sizeof edge, "adapter-context/", name, selection);
    put_edge(t, edge, adapter_id, id, "offers", NULL);
    set_add(observed, id, &t->oom);
  }
  const lj_value *resources = array(ad, "resources");
  for (size_t i = 0; resources && i < resources->u.array.len; i++) {
    const lj_value *res = resources->u.array.items[i];
    const char *rid = str(res, "id"), *kind = str(res, "kind"), *context = str(res, "context");
    char ctx_id[64];
    context_id(ctx_id, sizeof ctx_id, name, context);
    if (!*rid || !*kind || !set_has(observed, ctx_id)) continue;
    char id[64], d[25];
    id2(id, sizeof id, "resource/", name, rid);
    rec_digest(rid, strlen(rid), d);
    lj_value *attrs = rec_obj();
    t->oom |= rec_set(attrs, "adapter", rec_str(name)) | rec_set(attrs, "kind", rec_str(kind)) | rec_set(attrs, "id_digest", rec_str(d));
    const lj_value *meta = lj_get(res, "attributes");
    if (meta && meta->type == LJ_OBJECT && meta->u.object.len) t->oom |= rec_set(attrs, "metadata", lj_clone(meta));
    put_vertex(t, id, "resource", attrs, name, "adapter-observe");
    const char *parts[3] = {name, context, rid};
    digest_parts(d, parts, 3);
    snprintf(edge, sizeof edge, "context-resource/%s", d);
    put_edge(t, edge, ctx_id, id, "contains", NULL);
  }
  const lj_value *relations = array(ad, "relations");
  for (size_t i = 0; relations && i < relations->u.array.len; i++) {
    const lj_value *rel = relations->u.array.items[i];
    const char *kind = str(rel, "kind");
    char from[64], to[64], d[25];
    id2(from, sizeof from, "resource/", name, str(rel, "from"));
    id2(to, sizeof to, "resource/", name, str(rel, "to"));
    if (!*kind || !vertex(t, from) || !vertex(t, to)) continue;
    const char *parts[4] = {name, str(rel, "from"), str(rel, "to"), kind};
    digest_parts(d, parts, 4);
    snprintf(edge, sizeof edge, "resource-relation/%s", d);
    lj_value *attrs = rec_obj();
    t->oom |= rec_set(attrs, "kind", rec_str(kind));
    put_edge(t, edge, from, to, "relates", attrs);
  }
}

static void observe_aliases(inv_tx *t, const lj_value *aliases, const set *observed) {
  for (size_t i = 0; aliases && i < aliases->u.array.len; i++) {
    const lj_value *al = aliases->u.array.items[i];
    const char *space = str(al, "space"), *name = str(al, "name"), *selection = str(al, "selection");
    if (!*name || !*space) continue;
    char alias_id[64], ctx_id[64], d[25];
    id2(alias_id, sizeof alias_id, "alias/", space, name);
    context_id(ctx_id, sizeof ctx_id, str(al, "adapter"), selection);
    rec_digest(selection, strlen(selection), d);
    int discovered = set_has(observed, ctx_id);
    lj_value *a = rec_obj();
    t->oom |= rec_set(a, "space", rec_str(space)) | rec_set(a, "name", rec_str(name)) | rec_set(a, "adapter", rec_str(str(al, "adapter"))) |
              rec_set(a, "selection_digest", rec_str(d)) | rec_set(a, "discovered", rec_bool(discovered));
    if (rec_context_name(selection, strlen(selection))) t->oom |= rec_set(a, "selection", rec_str(selection));
    const lj_value *meta = lj_get(al, "metadata");
    if (meta && meta->type == LJ_OBJECT && meta->u.object.len) t->oom |= rec_set(a, "metadata", lj_clone(meta));
    put_vertex(t, alias_id, "alias", a, "ctx", "alias-declared");
    if (discovered) {
      char edge[64];
      id2(edge, sizeof edge, "alias-context/", space, name);
      put_edge(t, edge, alias_id, ctx_id, "resolves-to", NULL);
    }
  }
}

int rec_observe_inventory(rec *r, const ext_ledger_context *ctx, const lj_value *in) {
  inv_tx t;
  memset(&t, 0, sizeof t);
  t.r = r;
  t.vertices = rec_obj();
  t.edges = rec_obj();
  rec_now_text(r, &t.now);
  t.oom = !t.vertices || !t.edges || t.now.oom;
  lj_value *machine = rec_obj(), *inventory = rec_obj();
  t.oom |= rec_set(machine, "hostname", rec_str(r->env.hostname)) | rec_set(machine, "os", rec_str(r->env.os)) |
           rec_set(machine, "architecture", rec_str(r->env.arch)) | rec_set(inventory, "observed_at", rec_strn(t.now.ptr, t.now.len));
  put_vertex(&t, "machine/local", "machine", machine, "ctx", "inventory-observed");
  put_vertex(&t, "inventory/current", "inventory", inventory, "ctx", "inventory-observed");
  set observed = {{0}};
  const lj_value *adapters = array(in, "adapters");
  for (size_t i = 0; adapters && i < adapters->u.array.len; i++) observe_adapter(&t, adapters->u.array.items[i], &observed);
  observe_programs(&t, adapters, array(in, "installers"));
  observe_aliases(&t, array(in, "aliases"), &observed);
  ll_map_free(&observed.m);
  int rc = t.oom ? EXT_LEDGER_IO : 0;
  for (int attempt = 0; !rc && attempt < 3; attempt++) {
    lj_value *snap;
    if ((rc = rec_snapshot(r, ctx, &snap))) break;
    lj_value *tx = rec_obj(), *dv = lj_new(LJ_ARRAY), *de = lj_new(LJ_ARRAY), *vs = lj_new(LJ_ARRAY), *es = lj_new(LJ_ARRAY);
    int bad = !tx || !dv || !de || !vs || !es;
    const lj_value *svertices = array(snap, "vertices"), *sedges = array(snap, "edges");
    ll_map current_v = {0}, current_e = {0};
    for (size_t i = 0; svertices && i < svertices->u.array.len; i++) {
      const lj_value *v = svertices->u.array.items[i];
      const char *id = str(v, "id");
      bad |= ll_map_put(&current_v, id, strlen(id), (void *)v);
      if (inventory_kind(str(v, "kind")) && !lj_get(t.vertices, id)) {
        lj_value *ref = rec_obj();
        bad |= rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_str(id)) | lj_array_push(dv, ref);
      }
    }
    for (size_t i = 0; sedges && i < sedges->u.array.len; i++) {
      const lj_value *e = sedges->u.array.items[i];
      const char *id = str(e, "id");
      bad |= ll_map_put(&current_e, id, strlen(id), (void *)e);
      if (inventory_type(str(e, "type")) && !lj_get(t.edges, id)) {
        lj_value *ref = rec_obj();
        bad |= rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_str(id)) | lj_array_push(de, ref);
      }
    }
    for (size_t i = 0; i < t.vertices->u.object.len; i++) {
      const lj_member *m = &t.vertices->u.object.members[i];
      const lj_value *cur = ll_map_get(&current_v, m->key.ptr, m->key.len);
      const lj_value *a = lj_get(m->value, "attributes"), *b = cur ? lj_get(cur, "attributes") : NULL;
      int same = cur && strcmp(str(cur, "kind"), str(m->value, "kind")) == 0 && ((!a && !b) || (a && b && lj_equal(a, b)));
      if (!same) bad |= lj_array_push(vs, lj_clone(m->value));
    }
    for (size_t i = 0; i < t.edges->u.object.len; i++) {
      const lj_member *m = &t.edges->u.object.members[i];
      const lj_value *cur = ll_map_get(&current_e, m->key.ptr, m->key.len);
      int same = cur && !strcmp(str(cur, "from"), str(m->value, "from")) && !strcmp(str(cur, "to"), str(m->value, "to")) &&
                 !strcmp(str(cur, "type"), str(m->value, "type"));
      if (!same) bad |= lj_array_push(es, lj_clone(m->value));
    }
    size_t changes = vs->u.array.len + es->u.array.len + dv->u.array.len + de->u.array.len;
    bad |= rec_set(tx, "namespace", rec_str(REC_NS)) | rec_set(tx, "expected_revision", rec_num(lj_get(snap, "revision")->u.number)) |
           rec_set(tx, "delete_incident_edges", rec_bool(1)) | rec_set(tx, "delete_vertices", dv) | rec_set(tx, "delete_edges", de) |
           rec_set(tx, "vertices", vs) | rec_set(tx, "edges", es);
    ll_map_free(&current_v);
    ll_map_free(&current_e);
    lj_free(snap);
    if (bad) {
      lj_free(tx);
      rc = EXT_LEDGER_IO;
      break;
    }
    if (!changes) {
      lj_free(tx);
      break;
    }
    rc = rec_apply(r, ctx, tx);
    if (rc != EXT_LEDGER_CHANGED) break;
    rc = attempt == 2 ? EXT_LEDGER_BUSY : 0; /* the inventory changed during the observation */
  }
  lj_free(t.vertices);
  lj_free(t.edges);
  lj_buf_free(&t.now);
  return rc;
}

/* ---- resolve_inventory ---- */

typedef struct {
  const char *adapter, *selection;
  size_t index;
  lj_value *value;
} candidate;

static int cmp_candidates(const void *a, const void *b) {
  const candidate *x = a, *y = b;
  int c = strcmp(x->adapter, y->adapter);
  if (!c) c = strcmp(x->selection, y->selection);
  if (!c) c = x->index < y->index ? -1 : x->index > y->index;
  return c;
}

/* Appends the string elements of a to out. */
static int append_strings(lj_value *out, const lj_value *a) {
  for (size_t i = 0; a && a->type == LJ_ARRAY && i < a->u.array.len; i++)
    if (a->u.array.items[i]->type == LJ_STRING && lj_array_push(out, lj_clone(a->u.array.items[i]))) return -1;
  return 0;
}

static void sort_strings(lj_value *a) {
  if (a && a->type == LJ_ARRAY && a->u.array.len > 1) qsort(a->u.array.items, a->u.array.len, sizeof *a->u.array.items, cmp_strings);
}

int rec_resolve_inventory(rec *r, const ext_ledger_context *ctx, const lj_value *in, char **out, size_t *out_len) {
  const char *runtime = str(in, "runtime");
  lj_value *snap;
  int rc = rec_snapshot(r, ctx, &snap);
  if (rc) return rc;
  const lj_value *vertices = array(snap, "vertices"), *edges = array(snap, "edges");
  ll_map adapters = {0}, names = {0}, programs = {0}; /* name -> adapter; capability/support id -> name; id -> program */
  const char *observed_at = "";
  int bad = 0;
  for (size_t i = 0; vertices && i < vertices->u.array.len; i++) {
    const lj_value *v = vertices->u.array.items[i], *a = lj_get(v, "attributes");
    const char *kind = str(v, "kind"), *id = str(v, "id");
    if (!strcmp(kind, REC_NS "/inventory") && !strcmp(id, "inventory/current")) observed_at = str(a, "observed_at");
    else if (!strcmp(kind, REC_NS "/adapter") && lj_get(a, "name") && lj_get(a, "name")->type == LJ_STRING)
      bad |= ll_map_put(&adapters, str(a, "name"), strlen(str(a, "name")), (void *)v);
    else if (!strcmp(kind, REC_NS "/program")) bad |= ll_map_put(&programs, id, strlen(id), (void *)v);
    else if ((!strcmp(kind, REC_NS "/capability") || !strcmp(kind, REC_NS "/support")) && *str(a, "name"))
      bad |= ll_map_put(&names, id, strlen(id), (void *)str(a, "name"));
  }
  candidate *found = NULL;
  size_t n = 0;
  for (size_t i = 0; !bad && vertices && i < vertices->u.array.len; i++) {
    const lj_value *v = vertices->u.array.items[i], *a = lj_get(v, "attributes");
    if (strcmp(str(v, "kind"), REC_NS "/context") != 0) continue;
    const char *adapter = str(a, "adapter"), *selection = str(a, "selection"), *selection_digest = str(a, "selection_digest");
    const lj_value *ad = ll_map_get(&adapters, adapter, strlen(adapter)), *aa = lj_get(ad, "attributes");
    if (!ad || !*selection_digest || !boolean(aa, "trusted") || !boolean(aa, "listed")) continue;
    const char *runtime_value = str(aa, "runtime");
    if (*runtime && strcmp(runtime_value, runtime) != 0) continue;
    const char *adapter_id = str(ad, "id"), *context_vertex = str(v, "id");
    /* A context without its own capabilities or supports inherits its adapter's. */
    lj_value *capabilities = lj_new(LJ_ARRAY), *supports = lj_new(LJ_ARRAY), *program = NULL;
    const lj_value *own_caps = lj_get(a, "capabilities"), *own_supports = lj_get(a, "supports");
    bad |= !capabilities || !supports || append_strings(capabilities, own_caps) | append_strings(supports, own_supports);
    for (size_t e = 0; edges && e < edges->u.array.len; e++) {
      const lj_value *edge = edges->u.array.items[e];
      const char *type = str(edge, "type"), *from = str(edge, "from"), *to = str(edge, "to");
      const char *name = ll_map_get(&names, to, strlen(to));
      if (!own_caps && !strcmp(type, REC_NS "/supports") && !strcmp(from, adapter_id) && name) bad |= lj_array_push(capabilities, rec_str(name));
      if (!own_supports && !strcmp(type, REC_NS "/supports-kind") && !strcmp(from, adapter_id) && name) bad |= lj_array_push(supports, rec_str(name));
      if (!strcmp(type, REC_NS "/uses-program") && !strcmp(from, context_vertex)) {
        const lj_value *pv = ll_map_get(&programs, to, strlen(to)), *pa = lj_get(pv, "attributes");
        if (!pv) continue;
        lj_free(program);
        program = rec_obj();
        bad |= rec_set(program, "path", rec_str(str(pa, "path"))) | rec_set(program, "realpath", rec_str(str(pa, "realpath")));
        if (*str(pa, "version")) bad |= rec_set(program, "version", rec_str(str(pa, "version")));
        if (boolean(pa, "on_path")) bad |= rec_set(program, "on_path", rec_bool(1));
        lj_value *inst = strings_or_null(lj_get(pa, "installers"), 1), *sugg = strings_or_null(lj_get(pa, "suggested_installers"), 1);
        if (inst && inst->type == LJ_ARRAY) bad |= rec_set(program, "installers", inst);
        else lj_free(inst);
        if (sugg && sugg->type == LJ_ARRAY) bad |= rec_set(program, "suggested_installers", sugg);
        else lj_free(sugg);
      }
    }
    sort_strings(capabilities);
    sort_strings(supports);
    lj_value *c = rec_obj();
    bad |= rec_set(c, "adapter", rec_str(adapter)) | rec_set(c, "runtime", rec_str(runtime_value)) | rec_set(c, "selection", rec_str(selection)) |
           rec_set(c, "selection_digest", rec_str(selection_digest)) | rec_set(c, "observed_at", rec_str(rec_field(lj_get(v, "provenance"), "at")));
    if (!*selection) bad |= rec_set(c, "selection_redacted", rec_bool(1));
    /* Go writes nil capabilities as null; supports, engine and role are left out when empty. */
    if (capabilities && capabilities->u.array.len == 0 && !(own_caps && own_caps->type == LJ_ARRAY)) {
      lj_free(capabilities);
      capabilities = lj_new(LJ_NULL);
    }
    bad |= rec_set(c, "capabilities", capabilities);
    if (supports && supports->u.array.len) bad |= rec_set(c, "supports", supports);
    else lj_free(supports);
    lj_value *engine = strings_or_null(lj_get(aa, "engine"), 1), *role = strings_or_null(lj_get(aa, "role"), 1);
    if (engine && engine->type == LJ_ARRAY) bad |= rec_set(c, "engine", engine);
    else lj_free(engine);
    if (role && role->type == LJ_ARRAY) bad |= rec_set(c, "role", role);
    else lj_free(role);
    const lj_value *share = lj_get(a, "browser_share");
    if (share && share->type == LJ_OBJECT && share->u.object.len) {
      lj_value *s = rec_obj();
      for (size_t k = 0; k < share->u.object.len; k++)
        if (share->u.object.members[k].value->type == LJ_STRING)
          bad |= lj_object_set(s, share->u.object.members[k].key.ptr, share->u.object.members[k].key.len, lj_clone(share->u.object.members[k].value));
      if (s && s->u.object.len) bad |= rec_set(c, "browser_share", s);
      else lj_free(s);
    }
    if (program) bad |= rec_set(c, "program", program);
    candidate *grown = realloc(found, (n + 1) * sizeof *found);
    if (!grown || !c) {
      lj_free(c);
      bad = 1;
      break;
    }
    found = grown;
    found[n] = (candidate){adapter, selection, n, c};
    n++;
  }
  lj_value *result = rec_obj(), *contexts = n ? lj_new(LJ_ARRAY) : lj_new(LJ_NULL);
  if (n) qsort(found, n, sizeof *found, cmp_candidates);
  for (size_t i = 0; i < n; i++) {
    if (!bad && contexts) bad |= lj_array_push(contexts, found[i].value);
    else lj_free(found[i].value);
  }
  free(found);
  bad |= rec_set(result, "contexts", contexts) | rec_set(result, "observed_at", rec_str(*observed_at ? observed_at : "0001-01-01T00:00:00Z"));
  ll_map_free(&adapters);
  ll_map_free(&names);
  ll_map_free(&programs);
  if (!bad) {
    lj_buf b = {0};
    lj_write(&b, result);
    *out = lj_buf_take(&b, out_len);
    bad = !*out;
  }
  lj_free(result);
  lj_free(snap);
  return bad ? EXT_LEDGER_IO : 0;
}
