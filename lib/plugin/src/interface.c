/* The library interface: the plugin engine's objects behind four functions, opened from JSON,
 * called by operation name with JSON in and out, closed by handle, as lib/ledger's are. A binding
 * in any language binds these and nothing else; the typed API stays for C callers. Handle 0 is the
 * library itself: the pure services of ext_plugin_services_call and the digests and trust records.
 * Handles are never reused; an object is freed once it is closed and no call holds it. */
#include "ext_plugin_host.h"
#include "interface_host.h"
#include "interface_graph.h"
#include "interface_scope.h"
#include "fs.h"
#include "sync.h"
#include "wire.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

enum { K_STORE = 1, K_LAYERED, K_HOST, K_WRITER, K_GRAPH, K_SCOPE };

typedef struct entry {
  uint64_t id;
  int kind;
  void *ptr;
  int inflight, closing;
} entry;

static pthread_mutex_t table_lock = PTHREAD_MUTEX_INITIALIZER;
static entry **table;
static size_t table_len, table_cap;
static uint64_t next_id = 1;

static void destroy(entry *e) {
  if (e->kind == K_STORE)
    ext_plugin_store_close(e->ptr);
  else if (e->kind == K_LAYERED)
    ext_plugin_layered_close(e->ptr);
  else if (e->kind == K_WRITER)
    ext_plugin_store_writer_free(e->ptr);
  else if (e->kind == K_SCOPE)
    ext_interface_scope_free(e->ptr);
  else if (e->kind == K_GRAPH)
    ext_interface_graph_free(e->ptr);
  else
    ext_interface_host_free(e->ptr);
  free(e);
}

static ext_plugin_status add(int kind, void *ptr, uint64_t *handle) {
  entry *e = calloc(1, sizeof *e);
  if (!e)
    return EXT_PLUGIN_NOMEM;
  e->kind = kind, e->ptr = ptr;
  pthread_mutex_lock(&table_lock);
  if (table_len == table_cap) {
    size_t cap = table_cap ? table_cap * 2 : 16;
    entry **grown = realloc(table, cap * sizeof *grown);
    if (!grown) {
      pthread_mutex_unlock(&table_lock);
      free(e);
      return EXT_PLUGIN_NOMEM;
    }
    table = grown, table_cap = cap;
  }
  e->id = next_id++;
  table[table_len++] = e;
  pthread_mutex_unlock(&table_lock);
  *handle = e->id;
  return EXT_PLUGIN_OK;
}

static entry *acquire(uint64_t handle) {
  entry *found = NULL;
  pthread_mutex_lock(&table_lock);
  for (size_t i = 0; i < table_len; i++)
    if (table[i]->id == handle) {
      if (!table[i]->closing) {
        found = table[i];
        found->inflight++;
      }
      break;
    }
  pthread_mutex_unlock(&table_lock);
  return found;
}

static void remove_locked(entry *e) {
  for (size_t i = 0; i < table_len; i++)
    if (table[i] == e) {
      table[i] = table[--table_len];
      break;
    }
}

static void release(entry *e) {
  int free_it = 0;
  pthread_mutex_lock(&table_lock);
  if (--e->inflight == 0 && e->closing) {
    remove_locked(e);
    free_it = 1;
  }
  pthread_mutex_unlock(&table_lock);
  if (free_it)
    destroy(e);
}

/* ---- JSON helpers ---- */

static const char *str(yyjson_val *o, const char *key) {
  return yyjson_get_str(yyjson_obj_get(o, key));
}

static int flag(yyjson_val *o, const char *key) {
  return yyjson_get_bool(yyjson_obj_get(o, key));
}

/* The engine reads numbers as raw text (to keep their digits), so integers are read from it. */
static int raw_uint(yyjson_val *v, uint64_t *out) {
  if (!yyjson_is_raw(v))
    return 0;
  const char *p = yyjson_get_raw(v);
  size_t n = yyjson_get_len(v);
  uint64_t x = 0;
  if (!p || !n)
    return 0;
  for (size_t i = 0; i < n; i++) {
    if (p[i] < '0' || p[i] > '9' || x > (UINT64_MAX - (uint64_t)(p[i] - '0')) / 10)
      return 0;
    x = x * 10 + (uint64_t)(p[i] - '0');
  }
  *out = x;
  return 1;
}

static ext_plugin_status uint_of(yyjson_val *o, const char *key, uint64_t *out) {
  yyjson_val *v = yyjson_obj_get(o, key);
  if (!v)
    return EXT_PLUGIN_OK;
  return raw_uint(v, out) ? EXT_PLUGIN_OK : EXT_PLUGIN_INVALID;
}

ext_plugin_status ext_interface_host_borrow(yyjson_val *value, ext_interface_host **out, void **token) {
  uint64_t handle = 0;
  if (!raw_uint(value, &handle) || !handle) return EXT_PLUGIN_INVALID;
  entry *e = acquire(handle);
  if (!e) return EXT_PLUGIN_NOT_FOUND;
  if (e->kind != K_HOST) { release(e); return EXT_PLUGIN_INVALID; }
  *out = e->ptr;
  *token = e;
  return EXT_PLUGIN_OK;
}

void ext_interface_host_release(void *token) { release(token); }
ext_plugin_status ext_interface_host_route_events(uint64_t handle, ext_interface_host *sink,
                                                  yyjson_val *consumer, yyjson_val *routes) {
  entry *e = acquire(handle);
  if (!e) return EXT_PLUGIN_NOT_FOUND;
  ext_plugin_status s = e->kind == K_HOST ? EXT_PLUGIN_OK : EXT_PLUGIN_INVALID;
  if (!s) ext_interface_host_event_sink(e->ptr, sink, consumer, routes);
  release(e); return s;
}


static ext_plugin_status take(ext_plugin_buffer *b, char **out, size_t *out_len) {
  char *copy = malloc(b->len + 1);
  if (!copy) {
    ext_plugin_buffer_free(b);
    return EXT_PLUGIN_NOMEM;
  }
  if (b->len)
    memcpy(copy, b->data, b->len);
  copy[b->len] = 0;
  *out = copy, *out_len = b->len;
  ext_plugin_buffer_free(b);
  return EXT_PLUGIN_OK;
}

static ext_plugin_status text(const char *s, char **out, size_t *out_len) {
  ext_plugin_buffer b = {0};
  size_t n = strlen(s);
  if (!(b.data = malloc(n + 1)))
    return EXT_PLUGIN_NOMEM;
  memcpy(b.data, s, n);
  b.len = n;
  return take(&b, out, out_len);
}

static ext_plugin_status null_out(char **out, size_t *out_len) { return text("null", out, out_len); }

/* buffered runs a call that fills an ext_plugin_buffer and hands its JSON out. */
#define BUFFERED(call)                                                                             \
  do {                                                                                             \
    ext_plugin_buffer b_ = {0};                                                                    \
    ext_plugin_status s_ = (call);                                                                 \
    if (s_) {                                                                                      \
      ext_plugin_buffer_free(&b_);                                                                 \
      return s_;                                                                                   \
    }                                                                                              \
    return take(&b_, out, out_len);                                                                \
  } while (0)

static void hex_of(const uint8_t d[32], char out[65]) {
  static const char digits[] = "0123456789abcdef";
  for (int i = 0; i < 32; i++)
    out[2 * i] = digits[d[i] >> 4], out[2 * i + 1] = digits[d[i] & 15];
  out[64] = 0;
}

static int digest_of(const char *hex, uint8_t out[32]) {
  if (!hex || strlen(hex) != 64)
    return 0;
  for (int i = 0; i < 32; i++) {
    unsigned v = 0;
    for (int j = 0; j < 2; j++) {
      char c = hex[2 * i + j];
      v <<= 4;
      if (c >= '0' && c <= '9')
        v |= (unsigned)(c - '0');
      else if (c >= 'a' && c <= 'f')
        v |= (unsigned)(c - 'a' + 10);
      else if (c >= 'A' && c <= 'F')
        v |= (unsigned)(c - 'A' + 10);
      else
        return 0;
    }
    out[i] = (uint8_t)v;
  }
  return 1;
}

static ext_plugin_status digest_out(ext_plugin_status s, const uint8_t d[32], char **out,
                                    size_t *out_len) {
  if (s)
    return s;
  char hex[65], json[68];
  hex_of(d, hex);
  snprintf(json, sizeof json, "\"%s\"", hex);
  return text(json, out, out_len);
}

static ext_plugin_status limits_of(yyjson_val *config, ext_plugin_store_limits *l) {
  memset(l, 0, sizeof *l);
  yyjson_val *v = yyjson_obj_get(config, "limits");
  if (!v)
    return EXT_PLUGIN_OK;
  if (!yyjson_is_obj(v))
    return EXT_PLUGIN_INVALID;
  uint64_t files = 0;
  ext_plugin_status s = uint_of(v, "max_artifact_bytes", &l->max_artifact_bytes);
  if (!s)
    s = uint_of(v, "max_tree_bytes", &l->max_tree_bytes);
  if (!s)
    s = uint_of(v, "max_tree_files", &files);
  if (s || files > UINT32_MAX)
    return EXT_PLUGIN_INVALID;
  l->max_tree_files = (uint32_t)files;
  return EXT_PLUGIN_OK;
}

/* ---- opening ---- */

uint32_t ext_plugin_abi_version(void) { return EXT_PLUGIN_ABI_VERSION; }

ext_plugin_status ext_plugin_open(const char *config, size_t len, uint64_t *handle) {
  if (!handle)
    return EXT_PLUGIN_INVALID;
  *handle = 0;
  yyjson_doc *doc = ext_parse((const uint8_t *)(config ? config : "{}"), config ? len : 2);
  if (!doc)
    return EXT_PLUGIN_INVALID;
  yyjson_val *c = yyjson_doc_get_root(doc);
  const char *object = str(c, "object");
  ext_plugin_status s = EXT_PLUGIN_INVALID;
  ext_plugin_store_limits limits;
  if (object && !strcmp(object, "command_scope")) {
    ext_interface_scope *scope = NULL;
    if (!(s = ext_interface_scope_open(c, &scope)) && (s = add(K_SCOPE, scope, handle))) ext_interface_scope_free(scope);
  } else if (object && !strcmp(object, "execution_graph")) {
    ext_interface_graph *graph = NULL;
    if (!(s = ext_interface_graph_open(c, &graph)) && (s = add(K_GRAPH, graph, handle)))
      ext_interface_graph_free(graph);
  } else if (object && !strcmp(object, "host")) {
    ext_interface_host *host = NULL;
    if (!(s = ext_interface_host_open(c, &host)) && (s = add(K_HOST, host, handle))) {
      ext_interface_host_shutdown(host);
      ext_interface_host_free(host);
    }
  } else if (object && !strcmp(object, "package_writer")) {
    const char *root = str(c, "root");
    /* Filesystem APIs consume C strings: never lock a truncated JSON path. */
    if (root && strlen(root) != yyjson_get_len(yyjson_obj_get(c, "root")))
      root = NULL;
    ext_plugin_store_writer *writer = NULL;
    if (root && !(s = ext_plugin_store_writer_acquire(root, &writer)) &&
        (s = add(K_WRITER, writer, handle)))
      ext_plugin_store_writer_free(writer);
  } else if (object && !strcmp(object, "store")) {
    const char *root = str(c, "root");
    ext_plugin_store *store = NULL;
    if (root && !(s = limits_of(c, &limits)) && !(s = ext_plugin_store_open(root, &limits, &store)) &&
        (s = add(K_STORE, store, handle)))
      ext_plugin_store_close(store);
  } else if (object && !strcmp(object, "layered")) {
    yyjson_val *roots = yyjson_obj_get(c, "roots");
    size_t n = yyjson_arr_size(roots);
    const char **list = n ? calloc(n, sizeof *list) : NULL;
    if (n && !list) {
      s = EXT_PLUGIN_NOMEM;
    } else if (n && !(s = limits_of(c, &limits))) {
      for (size_t i = 0; i < n && !s; i++)
        if (!(list[i] = yyjson_get_str(yyjson_arr_get(roots, i))))
          s = EXT_PLUGIN_INVALID;
      ext_plugin_layered *layered = NULL;
      if (!s && !(s = ext_plugin_layered_open(list, n, &limits, &layered)) &&
          (s = add(K_LAYERED, layered, handle)))
        ext_plugin_layered_close(layered);
    }
    free(list);
  }
  yyjson_doc_free(doc);
  return s;
}

ext_plugin_status ext_plugin_close(uint64_t handle) {
  entry *e = NULL;
  ext_plugin_status s = EXT_PLUGIN_NOT_FOUND;
  int free_it = 0;
  pthread_mutex_lock(&table_lock);
  for (size_t i = 0; i < table_len; i++)
    if (table[i]->id == handle && !table[i]->closing) {
      e = table[i];
      e->closing = 1;
      e->inflight++; /* held while a host shuts down, outside the table lock */
      s = EXT_PLUGIN_OK;
      break;
    }
  pthread_mutex_unlock(&table_lock);
  if (!e)
    return s;
  if (e->kind == K_HOST)
    ext_interface_host_shutdown(e->ptr);
  else if (e->kind == K_SCOPE)
    ext_interface_scope_shutdown(e->ptr);
  else if (e->kind == K_GRAPH)
    ext_interface_graph_shutdown(e->ptr);
  else if (e->kind == K_WRITER)
    s = ext_plugin_store_writer_release(e->ptr);
  pthread_mutex_lock(&table_lock);
  if (--e->inflight == 0) {
    remove_locked(e);
    free_it = 1;
  }
  pthread_mutex_unlock(&table_lock);
  if (free_it)
    destroy(e);
  return s;
}

void ext_plugin_free(void *p) { free(p); }

/* The interface's code names are identifiers, as every library of lib/ names its codes (lib/ledger's
 * not_found is the same word), so a binding can go by name. */
const char *ext_plugin_code_name(int32_t code) {
  static const char *const names[] = {"ok",       "invalid",   "denied",   "mismatch", "unsupported",
                                      "closed",   "timeout",   "io",       "out_of_memory", "draining",
                                      "not_found", "ambiguous", "canceled", "updating", "capacity",
                                      "sequence", "protocol",  "expired",  "exists",   "missing"};
  return code >= 0 && (size_t)code < sizeof names / sizeof names[0] ? names[code] : "unknown";
}

/* ---- calling ---- */

/* interface.json, compiled in by CMakeLists.txt. */
extern const char ext_interface_description[];
extern const size_t ext_interface_description_len;

static ext_plugin_status call_library(const char *op, yyjson_val *in, const char *input, size_t len,
                                      char **out, size_t *out_len) {
  uint8_t d[32];
  if (!strcmp(op, "describe")) {
    if (!(*out = malloc(ext_interface_description_len + 1)))
      return EXT_PLUGIN_NOMEM;
    memcpy(*out, ext_interface_description, ext_interface_description_len + 1);
    *out_len = ext_interface_description_len;
    return EXT_PLUGIN_OK;
  }
  if (!strcmp(op, "directory_digest")) {
    const char *root = str(in, "root");
    return root ? digest_out(ext_plugin_directory_digest(root, d), d, out, out_len) : EXT_PLUGIN_INVALID;
  }
  if (!strcmp(op, "artifact_digest")) {
    const char *root = str(in, "root"), *path = str(in, "path");
    if (!root || !path)
      return EXT_PLUGIN_INVALID;
    return digest_out(ext_plugin_artifact_digest(root, path, flag(in, "tree"), d), d, out, out_len);
  }
  if (!strcmp(op, "store_digest")) {
    const char *root = str(in, "root"), *id = str(in, "id");
    return root && id ? digest_out(ext_plugin_store_digest(root, id, d), d, out, out_len) : EXT_PLUGIN_INVALID;
  }
  if (!strcmp(op, "package_verify")) {
    const char *root = str(in, "root");
    yyjson_val *manifest = yyjson_obj_get(in, "manifest");
    if (!root || !manifest)
      return EXT_PLUGIN_INVALID;
    size_t n = 0;
    char *m = yyjson_val_write(manifest, 0, &n);
    if (!m)
      return EXT_PLUGIN_NOMEM;
    ext_plugin_status s = ext_plugin_package_verify((const uint8_t *)m, n, root);
    free(m);
    return s ? s : null_out(out, out_len);
  }
  if (!strcmp(op, "trust_state") || !strcmp(op, "trust_record") || !strcmp(op, "trust_revoke")) {
    const char *path = str(in, "path"), *scope = str(in, "scope"), *id = str(in, "id");
    if (!path || !scope || !id)
      return EXT_PLUGIN_INVALID;
    if (!strcmp(op, "trust_revoke")) {
      ext_plugin_status s = ext_plugin_trust_revoke(path, scope, id);
      return s ? s : null_out(out, out_len);
    }
    if (!digest_of(str(in, "digest"), d))
      return EXT_PLUGIN_INVALID;
    if (!strcmp(op, "trust_record")) {
      ext_plugin_status s = ext_plugin_trust_record(path, scope, id, d);
      return s ? s : null_out(out, out_len);
    }
    ext_plugin_trust_state state;
    ext_plugin_status s = ext_plugin_trust_state_of(path, scope, id, d, &state);
    if (s)
      return s;
    return text(state == EXT_PLUGIN_TRUST_TRUSTED ? "\"trusted\""
                : state == EXT_PLUGIN_TRUST_CHANGED ? "\"changed\""
                                                    : "\"untrusted\"",
                out, out_len);
  }
  if (!strcmp(op, "code_name")) {
    uint64_t code = 0;
    if (!raw_uint(yyjson_obj_get(in, "code"), &code) || code > INT32_MAX)
      return EXT_PLUGIN_INVALID;
    char json[64];
    snprintf(json, sizeof json, "\"%s\"", ext_plugin_code_name((int32_t)code));
    return text(json, out, out_len);
  }
  /* Everything else is one of the pure services, with the same input. */
  BUFFERED(ext_plugin_services_call(op, (const uint8_t *)input, len, &b_));
}

static ext_plugin_status install(ext_plugin_store *s, const char *op, yyjson_val *in, char **out,
                                 size_t *out_len) {
  uint32_t flags = flag(in, "preserve_executable") ? EXT_PLUGIN_STORE_PRESERVE_EXECUTABLE : 0;
  if (!strcmp(op, "install_archive")) {
    const char *archive = str(in, "archive"), *sha = str(in, "sha256");
    uint64_t max = 0;
    if (!archive || !sha || uint_of(in, "max_archive_bytes", &max))
      return EXT_PLUGIN_INVALID;
    BUFFERED(ext_plugin_store_install_archive(s, archive, sha, str(in, "id"), flags, max, &b_));
  }
  const char *source = str(in, "source");
  if (!source)
    return EXT_PLUGIN_INVALID;
  if (!strcmp(op, "install"))
    BUFFERED(ext_plugin_store_install(s, source, flags, &b_));
  if (!strcmp(op, "upgrade"))
    BUFFERED(ext_plugin_store_upgrade(s, source, flags, &b_));
  BUFFERED(ext_plugin_store_replace(s, source, flags, &b_));
}

static ext_plugin_status call_store(ext_plugin_store *s, const char *op, yyjson_val *in, char **out,
                                    size_t *out_len) {
  const char *id = str(in, "id");
  if (!strcmp(op, "install") || !strcmp(op, "upgrade") || !strcmp(op, "replace") ||
      !strcmp(op, "install_archive"))
    return install(s, op, in, out, out_len);
  if (!strcmp(op, "snapshot")) {
    const char *source = str(in, "source"), *digest = str(in, "reviewed_digest");
    if (!source || !id || !digest) return EXT_PLUGIN_INVALID;
    BUFFERED(ext_plugin_store_snapshot(s, source, id, digest, &b_));
  }
  if (!strcmp(op, "ids"))
    BUFFERED(ext_plugin_store_ids(s, &b_));
  if (!strcmp(op, "list"))
    BUFFERED(ext_plugin_store_list(s, &b_));
  if (!strcmp(op, "verify"))
    BUFFERED(ext_plugin_store_verify(s, &b_));
  if (!strcmp(op, "list_unverified"))
    BUFFERED(ext_plugin_store_list_unverified(s, &b_));
  if (!strcmp(op, "resolve_entrypoint")) {
    const char *contract = str(in, "contract"), *version = str(in, "version"),
               *protocol = str(in, "protocol"), *digest = str(in, "reviewed_digest");
    if (!id || !contract || !version || !protocol)
      return EXT_PLUGIN_INVALID;
    BUFFERED(ext_plugin_store_resolve_entrypoint(s, id, contract, version, protocol, digest ? digest : "",
                                                 str(in, "os"), str(in, "arch"), &b_));
  }
  if (!id)
    return strcmp(op, "remove") && strcmp(op, "load") && strcmp(op, "receipt") ? EXT_PLUGIN_UNSUPPORTED
                                                                                 : EXT_PLUGIN_INVALID;
  if (!strcmp(op, "remove")) {
    ext_plugin_status st = ext_plugin_store_remove(s, id);
    return st ? st : null_out(out, out_len);
  }
  if (!strcmp(op, "load"))
    BUFFERED(ext_plugin_store_load(s, id, &b_));
  if (!strcmp(op, "receipt"))
    BUFFERED(ext_plugin_store_receipt(s, id, &b_));
  return EXT_PLUGIN_UNSUPPORTED;
}

static ext_plugin_status call_layered(ext_plugin_layered *l, const char *op, yyjson_val *in, char **out,
                                      size_t *out_len) {
  if (!strcmp(op, "list"))
    BUFFERED(ext_plugin_layered_list(l, &b_));
  if (!strcmp(op, "verify"))
    BUFFERED(ext_plugin_layered_verify(l, &b_));
  if (!strcmp(op, "list_unverified"))
    BUFFERED(ext_plugin_layered_list_unverified(l, &b_));
  if (!strcmp(op, "load")) {
    const char *id = str(in, "id");
    if (!id)
      return EXT_PLUGIN_INVALID;
    ext_plugin_buffer b = {0};
    size_t layer = 0;
    ext_plugin_status s = ext_plugin_layered_load(l, id, &b, &layer);
    if (s) {
      ext_plugin_buffer_free(&b);
      return s;
    }
    size_t n = b.len + 48;
    char *json = malloc(n);
    if (!json) {
      ext_plugin_buffer_free(&b);
      return EXT_PLUGIN_NOMEM;
    }
    int w = snprintf(json, n, "{\"layer\":%zu,\"manifest\":%.*s}", layer, (int)b.len, (const char *)b.data);
    ext_plugin_buffer_free(&b);
    *out = json, *out_len = (size_t)w;
    return EXT_PLUGIN_OK;
  }
  return EXT_PLUGIN_UNSUPPORTED;
}

ext_plugin_status ext_plugin_call(uint64_t handle, const char *op, const char *input, size_t len,
                                  char **out, size_t *out_len) {
  if (!op || !out || !out_len)
    return EXT_PLUGIN_INVALID;
  *out = NULL, *out_len = 0;
  if (!input || !len)
    input = "{}", len = 2;
  yyjson_doc *doc = ext_parse((const uint8_t *)input, len);
  if (!doc)
    return EXT_PLUGIN_INVALID;
  yyjson_val *in = yyjson_doc_get_root(doc);
  ext_plugin_status s;
  /* package.resolve is described as an array of manifests. All object-bound
   * operations still require object input. */
  if (!yyjson_is_obj(in) &&
      !(handle == 0 && !strcmp(op, "package.resolve") && yyjson_is_arr(in))) {
    s = EXT_PLUGIN_INVALID;
  } else if (handle == 0) {
    s = call_library(op, in, input, len, out, out_len);
  } else {
    entry *e = acquire(handle);
    if (!e) {
      s = EXT_PLUGIN_NOT_FOUND;
    } else {
      s = e->kind == K_STORE     ? call_store(e->ptr, op, in, out, out_len)
          : e->kind == K_LAYERED ? call_layered(e->ptr, op, in, out, out_len)
          : e->kind == K_SCOPE ? ext_interface_scope_call(e->ptr, op, in, out, out_len)
          : e->kind == K_GRAPH ? ext_interface_graph_call(e->ptr, op, in, out, out_len)
          : e->kind == K_WRITER ? EXT_PLUGIN_UNSUPPORTED
                                 : ext_interface_host_call(e->ptr, op, in, out, out_len);
      release(e);
    }
  }
  yyjson_doc_free(doc);
  if (s && *out) {
    free(*out);
    *out = NULL, *out_len = 0;
  }
  return s;
}
