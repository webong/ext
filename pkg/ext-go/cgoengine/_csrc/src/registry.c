/* The guest registry (ext_host.h): a reverse handler that serves a guest's
 * reverse call with a forward call to another guest, along an edge the host
 * declared. It is host-side composition over the public host API; the engine's
 * reverse path, the frames and the descriptor are unchanged. The reference for
 * its behavior is Registry in pkg/ext-go/engine/host (registry.go). */
#include "registry.h"
#include "reverse.h"
#include "sync.h"
#include <stdlib.h>
#include <string.h>

typedef struct target {
  struct target *next;
  char *name, *version;
  ext_host *host;
  size_t busy; /* forward calls routed to host and not yet returned */
} target;
typedef struct edge {
  struct edge *next;
  char *from[3], *to[3]; /* name, version, operation */
} edge;
/* One host on a call chain; the chain is the list up to its source. */
typedef struct step {
  ext_host *host;
  const struct step *up;
} step;
/* A forward call in flight, by the host it went to and the id that host gave
 * it, so a reverse call the target makes inside it continues its chain. */
typedef struct forward {
  struct forward *next;
  ext_host *host;
  char id[32];
  const step *chain;
  int linked;
} forward;
typedef struct port {
  struct port *next;
  ext_registry *registry;
  ext_host *source;
  ext_context_policy authorize;
  void *user;
} port;
struct ext_registry {
  pthread_mutex_t mu;
  pthread_cond_t idle;
  target *targets;
  edge *edges;
  forward *forwards;
  port *ports;
};

/* ---- names ---- */

static char *text_copy(const char *s) {
  size_t n = strlen(s) + 1;
  char *c = malloc(n);
  if (c)
    memcpy(c, s, n);
  return c;
}

/* Whether the strings are a contract ref (name, version) and, when operation
 * is not NULL, an operation name, by the rules a descriptor follows. */
static int valid(const char *name, const char *version,
                 const char *operation) {
  if (!name || !version)
    return 0;
  yyjson_mut_doc *m = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *root = m ? yyjson_mut_obj(m) : NULL,
                 *ref = m ? yyjson_mut_obj(m) : NULL;
  int ok = root && ref && yyjson_mut_obj_add_str(m, ref, "name", name) &&
           yyjson_mut_obj_add_str(m, ref, "version", version) &&
           yyjson_mut_obj_add_val(m, root, "ref", ref) &&
           (!operation ||
            yyjson_mut_obj_add_str(m, root, "operation", operation));
  yyjson_doc *d = NULL;
  if (ok) {
    yyjson_mut_doc_set_root(m, root);
    d = yyjson_mut_doc_imut_copy(m, NULL);
  }
  yyjson_mut_doc_free(m);
  yyjson_val *r = d ? yyjson_doc_get_root(d) : NULL;
  ok = r && ext_ref(yyjson_obj_get(r, "ref")) &&
       (!operation || ext_text(yyjson_obj_get(r, "operation"), 0, 0));
  yyjson_doc_free(d);
  return ok;
}
static int same(const char *a, yyjson_val *b) {
  return b && yyjson_is_str(b) && yyjson_equals_str(b, a);
}
static target *find_target(ext_registry *r, const char *name,
                           const char *version) {
  for (target *t = r->targets; t; t = t->next)
    if (!strcmp(t->name, name) && !strcmp(t->version, version))
      return t;
  return NULL;
}

/* ---- configuration ---- */

ext_status ext_registry_create(ext_registry **out) {
  if (!out)
    return EXT_INVALID;
  *out = NULL;
  ext_registry *r = calloc(1, sizeof(*r));
  if (!r)
    return EXT_NOMEM;
  pthread_mutex_init(&r->mu, NULL);
  pthread_cond_init(&r->idle, NULL);
  *out = r;
  return EXT_OK;
}

ext_status ext_registry_register(ext_registry *r, const char *name,
                                 const char *version, ext_host *host) {
  if (!r || !host || !valid(name, version, NULL))
    return EXT_INVALID;
  /* The host must serve the contract: an edge to it could never be answered. */
  yyjson_mut_doc *m = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *ref = m ? yyjson_mut_obj(m) : NULL;
  yyjson_doc *d = NULL;
  if (ref && yyjson_mut_obj_add_str(m, ref, "name", name) &&
      yyjson_mut_obj_add_str(m, ref, "version", version)) {
    yyjson_mut_doc_set_root(m, ref);
    d = yyjson_mut_doc_imut_copy(m, NULL);
  }
  yyjson_mut_doc_free(m);
  if (!d)
    return EXT_NOMEM;
  int serves = ext_host_serves(host, yyjson_doc_get_root(d));
  yyjson_doc_free(d);
  if (!serves)
    return EXT_UNSUPPORTED;
  target *t = calloc(1, sizeof(*t));
  if (!t || !(t->name = text_copy(name)) || !(t->version = text_copy(version))) {
    if (t) {
      free(t->name);
      free(t);
    }
    return EXT_NOMEM;
  }
  t->host = host;
  pthread_mutex_lock(&r->mu);
  ext_status s = find_target(r, name, version) ? EXT_EXISTS : EXT_OK;
  if (!s) {
    t->next = r->targets;
    r->targets = t;
  }
  pthread_mutex_unlock(&r->mu);
  if (s) {
    free(t->name);
    free(t->version);
    free(t);
  }
  return s;
}

ext_status ext_registry_unregister(ext_registry *r, const char *name,
                                   const char *version) {
  if (!r || !name || !version)
    return EXT_INVALID;
  pthread_mutex_lock(&r->mu);
  target **at = &r->targets;
  while (*at && (strcmp((*at)->name, name) || strcmp((*at)->version, version)))
    at = &(*at)->next;
  target *t = *at;
  if (t) {
    *at = t->next;
    /* No new call can reach it now; wait for the ones that did. */
    while (t->busy)
      pthread_cond_wait(&r->idle, &r->mu);
  }
  pthread_mutex_unlock(&r->mu);
  if (!t)
    return EXT_NOT_FOUND;
  free(t->name);
  free(t->version);
  free(t);
  return EXT_OK;
}

static void free_edge(edge *e) {
  for (int i = 0; i < 3; i++) {
    free(e->from[i]);
    free(e->to[i]);
  }
  free(e);
}
ext_status ext_registry_route(ext_registry *r, const ext_registry_edge *spec) {
  if (!r || !spec || spec->struct_size != sizeof(*spec) ||
      !valid(spec->from_name, spec->from_version, spec->from_operation) ||
      !valid(spec->to_name, spec->to_version, spec->to_operation))
    return EXT_INVALID;
  edge *e = calloc(1, sizeof(*e));
  if (!e)
    return EXT_NOMEM;
  const char *from[3] = {spec->from_name, spec->from_version,
                         spec->from_operation},
             *to[3] = {spec->to_name, spec->to_version, spec->to_operation};
  for (int i = 0; i < 3; i++)
    if (!(e->from[i] = text_copy(from[i])) || !(e->to[i] = text_copy(to[i]))) {
      free_edge(e);
      return EXT_NOMEM;
    }
  pthread_mutex_lock(&r->mu);
  ext_status s = EXT_OK;
  for (edge *x = r->edges; x && !s; x = x->next)
    if (!strcmp(x->from[0], from[0]) && !strcmp(x->from[1], from[1]) &&
        !strcmp(x->from[2], from[2]))
      s = EXT_EXISTS;
  if (!s) {
    e->next = r->edges;
    r->edges = e;
  }
  pthread_mutex_unlock(&r->mu);
  if (s)
    free_edge(e);
  return s;
}

/* ---- serving ---- */

static int32_t port_authorize(void *user, const ext_call_options *o,
                              const uint8_t *json, size_t len) {
  port *p = user;
  return p->authorize(p->user, o, json, len);
}

static ext_status refuse(ext_reverse_emit emit, void *sink, const char *code) {
  yyjson_mut_doc *m = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *e = m ? yyjson_mut_obj(m) : NULL;
  size_t n = 0;
  char *json = NULL;
  if (e && yyjson_mut_obj_add_str(m, e, "code", code) &&
      yyjson_mut_obj_add_str(m, e, "message", "reverse call refused")) {
    yyjson_mut_doc_set_root(m, e);
    json = yyjson_mut_write(m, 0, &n);
  }
  yyjson_mut_doc_free(m);
  if (!json)
    return EXT_NOMEM;
  ext_status s = emit(sink, EXT_HOST_REVERSE_PUBLIC_ERROR, (uint8_t *)json, n);
  free(json);
  return s;
}

typedef struct {
  ext_registry *registry;
  forward *call;
} begun_context;
static int begun(void *context, const char *id) {
  begun_context *b = context;
  size_t n = strlen(id);
  if (n >= sizeof(b->call->id))
    return 1;
  memcpy(b->call->id, id, n + 1);
  pthread_mutex_lock(&b->registry->mu);
  b->call->next = b->registry->forwards;
  b->registry->forwards = b->call;
  b->call->linked = 1;
  pthread_mutex_unlock(&b->registry->mu);
  return 0;
}

/* The forward call's request: the edge's target and the reverse payload. */
static char *forward_input(const edge *e, yyjson_val *payload, size_t *len) {
  yyjson_mut_doc *m = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *root = m ? yyjson_mut_obj(m) : NULL,
                 *ref = m ? yyjson_mut_obj(m) : NULL;
  char *json = NULL;
  if (root && ref && yyjson_mut_obj_add_str(m, ref, "name", e->to[0]) &&
      yyjson_mut_obj_add_str(m, ref, "version", e->to[1]) &&
      yyjson_mut_obj_add_val(m, root, "contract", ref) &&
      yyjson_mut_obj_add_str(m, root, "operation", e->to[2])) {
    yyjson_mut_val *copy = payload ? yyjson_val_mut_copy(m, payload) : NULL;
    if (!payload || (copy && yyjson_mut_obj_add_val(m, root, "payload", copy))) {
      yyjson_mut_doc_set_root(m, root);
      json = yyjson_mut_write(m, 0, len);
    }
  }
  yyjson_mut_doc_free(m);
  return json;
}

/* The target's response, as the reverse call's answer: its payload, or its
 * error unchanged. */
static ext_status answer(const ext_buffer *out, ext_reverse_emit emit,
                         void *sink) {
  yyjson_doc *d = ext_parse(out->data, out->len);
  yyjson_val *root = d ? yyjson_doc_get_root(d) : NULL;
  if (!root || !yyjson_is_obj(root)) {
    yyjson_doc_free(d);
    return EXT_IO;
  }
  yyjson_val *error = yyjson_obj_get(root, "error"),
             *payload = yyjson_obj_get(root, "payload");
  uint32_t kind = EXT_HOST_REVERSE_PAYLOAD;
  yyjson_val *value = payload;
  if (error && !yyjson_is_null(error)) {
    kind = EXT_HOST_REVERSE_PUBLIC_ERROR;
    value = error;
  }
  size_t n = 0;
  char *json = value ? yyjson_val_write(value, 0, &n) : NULL;
  ext_status s = value && !json ? EXT_NOMEM
                                : emit(sink, kind, (uint8_t *)json, n);
  free(json);
  yyjson_doc_free(d);
  return s;
}

static ext_status serve(void *user, const ext_call_options *o,
                        const uint8_t *request, size_t len,
                        ext_reverse_emit emit, void *sink) {
  port *p = user;
  ext_registry *r = p->registry;
  yyjson_doc *d = ext_parse(request, len);
  yyjson_val *root = d ? yyjson_doc_get_root(d) : NULL;
  if (!root) {
    yyjson_doc_free(d);
    return EXT_INVALID;
  }
  yyjson_val *ref = yyjson_obj_get(root, "contract"),
             *operation = yyjson_obj_get(root, "operation"),
             *parent = yyjson_obj_get(root, "parent");
  yyjson_val *name = yyjson_obj_get(ref, "name"),
             *version = yyjson_obj_get(ref, "version");
  const char *code = NULL;
  const edge *e = NULL;
  target *t = NULL;
  step source = {p->source, NULL};
  const step *chain = &source;
  pthread_mutex_lock(&r->mu);
  for (edge *x = r->edges; x && !e; x = x->next)
    if (same(x->from[0], name) && same(x->from[1], version) &&
        same(x->from[2], operation))
      e = x;
  if (!e)
    code = EXT_REVERSE_UNDECLARED;
  else if (!(t = find_target(r, e->to[0], e->to[1])))
    code = EXT_REVERSE_UNAVAILABLE;
  else {
    /* A call-scoped request inside a forward call this registry made carries
     * on that call's chain. */
    if (parent && yyjson_is_str(parent))
      for (forward *f = r->forwards; f; f = f->next)
        if (f->host == p->source && yyjson_equals_str(parent, f->id)) {
          chain = f->chain;
          break;
        }
    for (const step *s = chain; s && !code; s = s->up)
      if (s->host == t->host)
        code = EXT_REVERSE_DENIED;
    if (!code)
      t->busy++;
  }
  pthread_mutex_unlock(&r->mu);
  if (code) {
    yyjson_doc_free(d);
    return refuse(emit, sink, code);
  }
  /* Edges are never removed and t is kept while busy, so both stay valid. */
  size_t n = 0;
  char *input = forward_input(e, yyjson_obj_get(root, "payload"), &n);
  ext_status s = EXT_NOMEM;
  ext_buffer out = {0};
  step next = {t->host, chain};
  forward call = {0};
  call.host = t->host;
  call.chain = &next;
  if (input) {
    /* The value is the source binding's own context and means nothing to the
     * target's, which may be another binding: it is not passed on. */
    ext_call_options options = {sizeof(options),
                                o->timeout_ms ? o->timeout_ms : 1, o->cancel,
                                NULL};
    begun_context b = {r, &call};
    s = ext_host_call_begun(t->host, (const uint8_t *)input, n, &options,
                            begun, &b, &out);
  }
  free(input);
  yyjson_doc_free(d);
  pthread_mutex_lock(&r->mu);
  if (call.linked) {
    forward **at = &r->forwards;
    while (*at != &call)
      at = &(*at)->next;
    *at = call.next;
  }
  if (!--t->busy)
    pthread_cond_broadcast(&r->idle);
  pthread_mutex_unlock(&r->mu);
  switch (s) {
  case EXT_OK:
    s = answer(&out, emit, sink);
    break;
  case EXT_DENIED:
    s = refuse(emit, sink, EXT_REVERSE_DENIED);
    break;
  case EXT_CLOSED:
  case EXT_DRAINING:
  case EXT_UNSUPPORTED:
    s = refuse(emit, sink, EXT_REVERSE_UNAVAILABLE);
    break;
  case EXT_TIMEOUT:
  case EXT_EXPIRED:
    s = refuse(emit, sink, EXT_REVERSE_EXPIRED);
    break;
  default:
    break; /* the engine answers operation_failed */
  }
  ext_buffer_free(&out);
  return s;
}

ext_status ext_registry_attach(ext_registry *r, ext_host *source,
                               const ext_host_services *services) {
  if (!r || !source || !services ||
      services->struct_size != sizeof(*services) || services->handle)
    return EXT_INVALID;
  if (!services->authorize)
    return EXT_DENIED;
  port *p = calloc(1, sizeof(*p));
  if (!p)
    return EXT_NOMEM;
  p->registry = r;
  p->source = source;
  p->authorize = services->authorize;
  p->user = services->user;
  ext_host_services wrapped = *services;
  wrapped.user = p;
  wrapped.authorize = port_authorize;
  wrapped.handle = serve;
  ext_status s = ext_host_set_services(source, &wrapped);
  if (s) {
    free(p);
    return s;
  }
  pthread_mutex_lock(&r->mu);
  p->next = r->ports;
  r->ports = p;
  pthread_mutex_unlock(&r->mu);
  return EXT_OK;
}

void ext_registry_destroy(ext_registry *r) {
  if (!r)
    return;
  while (r->targets) {
    target *t = r->targets;
    r->targets = t->next;
    free(t->name);
    free(t->version);
    free(t);
  }
  while (r->edges) {
    edge *e = r->edges;
    r->edges = e->next;
    free_edge(e);
  }
  while (r->ports) {
    port *p = r->ports;
    r->ports = p->next;
    free(p);
  }
  pthread_cond_destroy(&r->idle);
  pthread_mutex_destroy(&r->mu);
  free(r);
}
