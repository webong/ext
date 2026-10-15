#define _POSIX_C_SOURCE 200809L
#include "ext_guest.h"
#include "wire.h"
#include <stdlib.h>
#include <string.h>
#include <time.h>
struct ext_guest {
  yyjson_doc *descriptor;
  ext_guest_options options;
};
static int64_t clock_ms(void) {
  struct timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return (int64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}
typedef struct {
  yyjson_doc *doc;
  uint32_t kind;
  int emitted;
  ext_status status;
} reply;
static ext_status emit(void *sink, uint32_t kind, const uint8_t *data,
                       size_t len) {
  reply *r = sink;
  if (r->emitted++ || kind > EXT_GUEST_PUBLIC_ERROR) {
    r->status = EXT_INVALID;
    return r->status;
  }
  r->doc = ext_parse(data, len);
  r->kind = kind;
  r->status = r->doc ? EXT_OK : EXT_INVALID;
  return r->status;
}
ext_status ext_guest_create(const ext_guest_options *o, ext_guest **out) {
  if (!out)
    return EXT_INVALID;
  *out = NULL;
  if (!o || o->abi_version != EXT_HOST_ABI_VERSION ||
      o->struct_size != sizeof(*o))
    return EXT_INVALID;
  if (!o->handle)
    return EXT_DENIED;
  yyjson_doc *d = ext_parse(o->descriptor, o->descriptor_len);
  if (!d || !ext_descriptor(yyjson_doc_get_root(d))) {
    yyjson_doc_free(d);
    return EXT_INVALID;
  }
  ext_guest *g = calloc(1, sizeof(*g));
  if (!g) {
    yyjson_doc_free(d);
    return EXT_NOMEM;
  }
  g->descriptor = d;
  g->options = *o;
  g->options.descriptor = NULL;
  g->options.descriptor_len = 0;
  if (!g->options.max_call_ms)
    g->options.max_call_ms = 30000;
  *out = g;
  return EXT_OK;
}
ext_status ext_guest_descriptor(ext_guest *g, ext_buffer *out) {
  if (!out)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  if (!g)
    return EXT_INVALID;
  out->data = (uint8_t *)yyjson_write(g->descriptor, 0, &out->len);
  return out->data ? EXT_OK : EXT_NOMEM;
}
void ext_guest_destroy(ext_guest *g) {
  if (g) {
    yyjson_doc_free(g->descriptor);
    free(g);
  }
}
ext_status ext_guest_invoke(ext_guest *g, const uint8_t *input, size_t len,
                            uint32_t timeout, void *call_user,
                            ext_buffer *out) {
  if (!out)
    return EXT_INVALID;
  *out = (ext_buffer){0};
  if (!g || !timeout)
    return EXT_INVALID;
  yyjson_doc *request = ext_parse(input, len);
  if (!request)
    return EXT_INVALID;
  yyjson_val *r = yyjson_doc_get_root(request), *id = yyjson_obj_get(r, "id");
  if (!ext_text(id, 1, 0)) {
    yyjson_doc_free(request);
    return EXT_INVALID;
  }
  int64_t wall_deadline;
  ext_status s =
      ext_request(yyjson_doc_get_root(g->descriptor), r, &wall_deadline);
  const char *code = NULL, *message = NULL;
  reply result = {0};
  if (s) {
    code = "invalid_request";
    message = "request does not match selected contract";
  } else {
    uint32_t limit =
        timeout < g->options.max_call_ms ? timeout : g->options.max_call_ms;
    int64_t remaining = wall_deadline - ext_wall_ms();
    if (remaining < limit)
      limit = remaining > 0 ? (uint32_t)remaining : 0;
    int64_t deadline = clock_ms() + limit;
    s = limit ? g->options.handle(g->options.user, call_user, input, len, limit,
                                  emit, &result)
              : EXT_TIMEOUT;
    if (s || clock_ms() >= deadline) {
      code = "operation_failed";
      message = "plugin operation failed";
    } else if (result.status) {
      s = result.status;
      goto done;
    }
  }
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  if (!doc) {
    s = EXT_NOMEM;
    goto done;
  }
  yyjson_mut_val *root = yyjson_mut_obj(doc);
  yyjson_mut_doc_set_root(doc, root);
  int ok = root &&
           yyjson_mut_obj_add_str(doc, root, "apiVersion", "ext.plugin/v1") &&
           yyjson_mut_obj_add_strn(doc, root, "id", yyjson_get_str(id),
                                   yyjson_get_len(id));
  if (code) {
    yyjson_mut_val *error = yyjson_mut_obj(doc);
    ok = ok && error && yyjson_mut_obj_add_str(doc, error, "code", code) &&
         yyjson_mut_obj_add_str(doc, error, "message", message) &&
         yyjson_mut_obj_add_val(doc, root, "error", error);
  } else {
    yyjson_mut_val *body =
        result.doc ? yyjson_val_mut_copy(doc, yyjson_doc_get_root(result.doc))
                   : yyjson_mut_null(doc);
    ok = ok && body &&
         yyjson_mut_obj_add_val(
             doc, root,
             result.kind == EXT_GUEST_PUBLIC_ERROR ? "error" : "payload", body);
  }
  if (ok)
    out->data = (uint8_t *)yyjson_mut_write(doc, 0, &out->len);
  yyjson_mut_doc_free(doc);
  s = out->data ? EXT_OK : EXT_NOMEM;
  if (!s) {
    yyjson_doc *validation = ext_parse(out->data, out->len);
    s = validation
            ? ext_response(yyjson_doc_get_root(validation), yyjson_get_str(id))
            : EXT_INVALID;
    yyjson_doc_free(validation);
  }
done:
  yyjson_doc_free(result.doc);
  yyjson_doc_free(request);
  if (s)
    ext_buffer_free(out);
  return s;
}
