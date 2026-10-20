#include "reverse.h"
#include <stdlib.h>
#include <string.h>

ext_status ext_reverse_request_frame(yyjson_val *frame,
                                     ext_reverse_request *request) {
  *request = (ext_reverse_request){0};
  if (!EXT_FIELDS(frame, "frame", "apiVersion", "id", "parent", "contract",
                  "operation", "deadline", "payload") ||
      !yyjson_equals_str(yyjson_obj_get(frame, "frame"), "reverse_request") ||
      !yyjson_equals_str(yyjson_obj_get(frame, "apiVersion"), "ext.plugin/v1"))
    return EXT_PROTOCOL;
  yyjson_val *id = yyjson_obj_get(frame, "id"),
             *parent = yyjson_obj_get(frame, "parent"),
             *contract = yyjson_obj_get(frame, "contract"),
             *operation = yyjson_obj_get(frame, "operation");
  /* A parent that is absent, null or empty is no parent: a session-scoped call.
   * One that is named must be an identifier (a string, as Go decodes it). */
  if (parent && yyjson_is_null(parent))
    parent = NULL;
  if (parent && (!yyjson_is_str(parent) ||
                 (yyjson_get_len(parent) && !ext_text(parent, 1, 0))))
    return EXT_PROTOCOL;
  if (parent && !yyjson_get_len(parent))
    parent = NULL;
  int64_t deadline = ext_deadline_ms(yyjson_obj_get(frame, "deadline"));
  if (!ext_text(id, 1, 0) || !EXT_FIELDS(contract, "name", "version") ||
      !ext_ref(contract) || !ext_text(operation, 0, 0) || deadline < 0)
    return EXT_PROTOCOL;
  request->id = id;
  request->parent = parent;
  request->contract = contract;
  request->operation = operation;
  request->payload = yyjson_obj_get(frame, "payload");
  request->deadline = yyjson_obj_get(frame, "deadline");
  request->deadline_ms = deadline;
  return EXT_OK;
}

static yyjson_mut_val *begin(yyjson_mut_doc *doc, const char *id,
                             size_t id_len) {
  yyjson_mut_val *root = yyjson_mut_obj(doc);
  yyjson_mut_doc_set_root(doc, root);
  if (!root ||
      !yyjson_mut_obj_add_str(doc, root, "frame", "reverse_response") ||
      !yyjson_mut_obj_add_str(doc, root, "apiVersion", "ext.plugin/v1") ||
      !yyjson_mut_obj_add_strncpy(doc, root, "id", id, id_len))
    return NULL;
  return root;
}
static char *finish(yyjson_mut_doc *doc, size_t *len) {
  char *line = yyjson_mut_write(doc, 0, len);
  yyjson_mut_doc_free(doc);
  return line;
}
/* An error frame; retry is omitted when zero, as RemoteError's omitempty does. */
static char *error_frame(const char *id, size_t id_len, const char *code,
                         size_t code_len, const char *message,
                         size_t message_len, int64_t retry, size_t *len) {
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  if (!doc)
    return NULL;
  yyjson_mut_val *root = begin(doc, id, id_len),
                 *error = yyjson_mut_obj(doc);
  if (!root || !error ||
      !yyjson_mut_obj_add_strncpy(doc, error, "code", code, code_len) ||
      !yyjson_mut_obj_add_strncpy(doc, error, "message", message,
                                  message_len) ||
      (retry && !yyjson_mut_obj_add_sint(doc, error, "retryAfterMilliseconds",
                                         retry)) ||
      !yyjson_mut_obj_add_val(doc, root, "error", error)) {
    yyjson_mut_doc_free(doc);
    return NULL;
  }
  return finish(doc, len);
}
char *ext_reverse_refusal(const char *id, size_t id_len, const char *code,
                          size_t *len) {
  static const char message[] = "reverse call refused";
  return error_frame(id, id_len, code, strlen(code), message,
                     sizeof(message) - 1, 0, len);
}
char *ext_reverse_failure(const char *id, size_t id_len, size_t *len) {
  static const char code[] = "operation_failed",
                    message[] = "host service failed";
  return error_frame(id, id_len, code, sizeof(code) - 1, message,
                     sizeof(message) - 1, 0, len);
}

char *ext_reverse_answer(const char *id, size_t id_len, uint32_t kind,
                         const uint8_t *data, size_t n, size_t max_payload,
                         size_t *len) {
  char *line = NULL;
  if (kind == EXT_HOST_REVERSE_PUBLIC_ERROR) {
    /* A handler's own error reaches the plugin unchanged when a peer would accept
     * it; one it would reject (an invalid code, an over-long message, a retry
     * hint out of range) is a failure, so one handler bug cannot end the
     * connection. */
    yyjson_doc *doc = ext_parse(data, n);
    yyjson_val *error = doc ? yyjson_doc_get_root(doc) : NULL;
    if (!error || !yyjson_is_obj(error) || !ext_error_valid(error)) {
      yyjson_doc_free(doc);
      return ext_reverse_failure(id, id_len, len);
    }
    yyjson_val *message = yyjson_obj_get(error, "message"),
               *retry = yyjson_obj_get(error, "retryAfterMilliseconds"),
               *code = yyjson_obj_get(error, "code");
    int64_t milliseconds = 0;
    if (retry && !yyjson_is_null(retry))
      (void)ext_int64(retry, &milliseconds);
    line = error_frame(id, id_len, yyjson_get_str(code), yyjson_get_len(code),
                       message && yyjson_is_str(message)
                           ? yyjson_get_str(message)
                           : "",
                       message && yyjson_is_str(message)
                           ? yyjson_get_len(message)
                           : 0,
                       milliseconds, len);
    yyjson_doc_free(doc);
    return line;
  }
  if (kind != EXT_HOST_REVERSE_PAYLOAD)
    return ext_reverse_failure(id, id_len, len);
  static const uint8_t null_payload[] = {'n', 'u', 'l', 'l'};
  if (!n) {
    data = null_payload;
    n = sizeof(null_payload);
  }
  if (n > max_payload)
    return ext_reverse_refusal(id, id_len, EXT_REVERSE_TOO_LARGE, len);
  yyjson_doc *payload = ext_parse(data, n);
  if (!payload)
    return ext_reverse_failure(id, id_len, len);
  yyjson_mut_doc *doc = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *root = doc ? begin(doc, id, id_len) : NULL;
  yyjson_mut_val *copy =
      root ? yyjson_val_mut_copy(doc, yyjson_doc_get_root(payload)) : NULL;
  if (copy && yyjson_mut_obj_add_val(doc, root, "payload", copy))
    line = finish(doc, len);
  else
    yyjson_mut_doc_free(doc);
  yyjson_doc_free(payload);
  /* An answer too large to send becomes a refusal, never a truncation. */
  if (line && *len > EXT_HOST_MAX_FRAME) {
    free(line);
    return ext_reverse_refusal(id, id_len, EXT_REVERSE_TOO_LARGE, len);
  }
  return line;
}
