/* lib/ledger as an ext plugin: the four ABI exports driven as a host drives
 * them, frame by frame. */
#include "ext_ledger.h"
#include "json.h"
#include "ledger_internal.h"
#include "testutil.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

uint32_t ext_plugin_abi_version(void);
uint64_t ext_plugin_open(void);
uint32_t ext_plugin_call(uint64_t handle, uint32_t operation, const uint8_t *request, uint32_t request_len, uint8_t *response,
                         uint32_t response_capacity, uint32_t *response_len);
void ext_plugin_close(uint64_t handle);

static int failures;
#define CHECK(cond, ...)                                   \
  do {                                                     \
    if (!(cond)) {                                         \
      fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); \
      fprintf(stderr, __VA_ARGS__);                        \
      fprintf(stderr, "\n");                               \
      failures++;                                          \
    }                                                      \
  } while (0)

#define CAP (24u * 1024u * 1024u)
static uint8_t *response;

static uint32_t raw(uint64_t h, uint32_t op, const char *req, char **out) {
  uint32_t len = 0;
  uint32_t status = ext_plugin_call(h, op, (const uint8_t *)req, (uint32_t)strlen(req), response, CAP, &len);
  *out = NULL;
  if (status == 0) {
    *out = malloc(len + 1);
    memcpy(*out, response, len);
    (*out)[len] = 0;
  }
  return status;
}

/* Invokes op with payload (JSON text); the Response, parsed. */
static lj_value *invoke(uint64_t h, const char *op, const char *payload) {
  static int n;
  size_t cap = strlen(payload) + 512;
  char *req = malloc(cap), *out;
  snprintf(req, cap,
          "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"call-%d\",\"plugin\":{\"id\":\"ext/ledger\",\"revision\":\"dev\"},"
          "\"contract\":{\"name\":\"ext.graph\",\"version\":\"v1\"},\"operation\":\"%s\",\"deadline\":\"2999-01-01T00:00:00Z\",\"payload\":%s}",
          ++n, op, payload);
  uint32_t status = raw(h, 2, req, &out);
  free(req);
  CHECK(status == 0, "%s: ABI status %u", op, status);
  lj_value *v = out ? lj_parse(out, strlen(out), NULL) : NULL;
  free(out);
  CHECK(v && lj_get(v, "id"), "%s: no response", op);
  return v;
}

static const lj_value *payload_of(const lj_value *r) { return lj_get(r, "payload"); }

static const char *error_of(const lj_value *r) {
  const lj_value *e = lj_get(lj_get(r, "error"), "code");
  return e && e->type == LJ_STRING ? e->u.string.ptr : "";
}

static const char *str(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : "";
}

/* Invokes and expects success; the payload's string member key, copied, or "". */
static char *ok(uint64_t h, const char *op, const char *payload, const char *key) {
  lj_value *r = invoke(h, op, payload);
  CHECK(r && payload_of(r) && !lj_get(r, "error"), "%s failed: %s", op, error_of(r));
  char *value = strdup(key ? str(payload_of(r), key) : "");
  lj_free(r);
  return value;
}

static void expect_error(uint64_t h, const char *op, const char *payload, const char *code) {
  lj_value *r = invoke(h, op, payload);
  CHECK(r && strcmp(error_of(r), code) == 0 && !payload_of(r), "%s: expected %s, got %s", op, code, error_of(r));
  lj_free(r);
}

int main(int argc, char **argv) {
  response = malloc(CAP);
  CHECK(ext_plugin_abi_version() == 1, "ABI version");
  uint64_t h = ext_plugin_open();
  CHECK(h != 0, "open");
  char *out;
  CHECK(raw(h, 2, "{}", &out) == 1, "an invoke before the handshake was taken");
  CHECK(raw(h, 1, "{}", &out) == 1, "a handshake without a deadline was taken");
  CHECK(raw(h, 1, "{\"deadline\":\"2999-01-01T00:00:00Z\"}", &out) == 0, "handshake");
  lj_value *d = out ? lj_parse(out, strlen(out), NULL) : NULL;
  free(out);
  const lj_value *contracts = lj_get(d, "contracts");
  CHECK(contracts && contracts->type == LJ_ARRAY && contracts->u.array.len == 1 && strcmp(str(contracts->u.array.items[0], "name"), "ext.graph") == 0,
        "descriptor");
  const lj_value *ops = contracts ? lj_get(contracts->u.array.items[0], "operations") : NULL;
  for (size_t i = 0; ops && i < ops->u.array.len; i++) {
    const char *name = str(ops->u.array.items[i], "name");
    for (const char *p = name; *p; p++) CHECK((*p >= 'a' && *p <= 'z') || (*p >= '0' && *p <= '9') || strchr("._/-", *p), "operation %s is not an identifier", name);
  }
  /* The package declares what the library serves: package/descriptor.json, but for the revision a package seals. */
  size_t tlen = 0;
  char *template_text = argc > 1 ? read_file(argv[1], &tlen) : NULL;
  lj_value *declared = template_text ? lj_parse(template_text, tlen, NULL) : NULL;
  free(template_text);
  CHECK(declared, "read %s", argc > 1 ? argv[1] : "(no descriptor path)");
  if (declared && d) {
    lj_value *identity = lj_get(declared, "identity"), *served = lj_get(d, "identity");
    CHECK(identity && served && strcmp(str(served, "id"), str(identity, "id")) == 0, "identity");
    lj_object_set(identity, "revision", 8, lj_clone(lj_get(served, "revision")));
    CHECK(lj_equal(declared, d), "package/descriptor.json differs from the descriptor the library serves");
  }
  lj_free(declared);
  lj_free(d);

  /* The store. */
  char *store = ok(h, "store.open", "{\"memory\":true}", "store");
  CHECK(store[0] == 's', "store handle %s", store);
  char p[4096];
  snprintf(p, sizeof p, "{\"store\":\"%s\",\"schema\":{\"namespace\":\"test.example\",\"version\":\"1\"}}", store);
  free(ok(h, "store.register", p, NULL));
  snprintf(p, sizeof p, "{\"store\":\"%s\",\"transaction\":{\"namespace\":\"test.example\",\"vertices\":[{\"id\":\"a\",\"kind\":\"test.example/node\"}]}}", store);
  free(ok(h, "store.apply", p, NULL));
  snprintf(p, sizeof p, "{\"store\":\"%s\",\"namespace\":\"test.example\",\"id\":\"a\"}", store);
  char *id = ok(h, "store.get-vertex", p, "id");
  CHECK(strcmp(id, "a") == 0, "get-vertex: %s", id);
  free(id);
  snprintf(p, sizeof p, "{\"store\":\"%s\",\"namespace\":\"test.example\",\"id\":\"absent\"}", store);
  expect_error(h, "store.get-vertex", p, "not-found");
  snprintf(p, sizeof p, "{\"store\":\"%s\",\"namespace\":\"test.example\",\"after\":0}", store);
  lj_value *changes = invoke(h, "store.changes", p);
  CHECK(payload_of(changes), "changes: %s", error_of(changes));
  lj_free(changes);
  free(ok(h, "store.canonical-transaction", "{\"transaction\":{\"namespace\":\"test.example\"}}", NULL));
  expect_error(h, "store.snapshot", "{\"store\":\"s999\",\"namespace\":\"test.example\"}", "not-found");
  expect_error(h, "store.apply", "{\"store\":\"s1\"}", "invalid");
  expect_error(h, "store.nothing", "{}", "unsupported");

  /* The recorder and a collector. */
  snprintf(p, sizeof p, "{\"store\":\"%s\"}", store);
  char *recorder = ok(h, "recorder.open", p, "recorder");
  CHECK(recorder[0] == 'r', "recorder handle %s", recorder);
  snprintf(p, sizeof p, "{\"store\":\"%s\"}", store);
  expect_error(h, "store.close", p, "busy");
  free(ok(h, "machine-environment", "{}", NULL));
  lj_value *collected = invoke(h, "collect", "{\"what\":\"shells\"}");
  CHECK(collected && (payload_of(collected) || strcmp(error_of(collected), "unsupported") == 0), "collect: %s", error_of(collected));
  lj_free(collected);

  /* The supervisor. */
  snprintf(p, sizeof p, "{\"store\":\"%s\",\"options\":{\"runtime_id\":\"plugin-test\"}}", store);
  char *sup = ok(h, "supervisor.open", p, "supervisor");
  snprintf(p, sizeof p, "{\"supervisor\":\"%s\"}", sup);
  lj_value *list = invoke(h, "supervisor.instances", p);
  const lj_value *instances = lj_get(payload_of(list), "instances");
  CHECK(instances && instances->type == LJ_ARRAY && instances->u.array.len == 0, "instances");
  lj_free(list);
  snprintf(p, sizeof p, "{\"supervisor\":\"%s\",\"spec\":{\"artifact\":{\"id\":\"a\"},\"command\":\"x\"}}", sup);
  expect_error(h, "supervisor.start", p, "invalid");
  snprintf(p, sizeof p, "{\"supervisor\":\"%s\",\"id\":\"none\",\"check\":true}", sup);
  expect_error(h, "supervisor.health", p, "not-found");
  snprintf(p, sizeof p, "{\"supervisor\":\"%s\"}", sup);
  free(ok(h, "supervisor.close", p, NULL));
  snprintf(p, sizeof p, "{\"recorder\":\"%s\"}", recorder);
  free(ok(h, "recorder.close", p, NULL));
  snprintf(p, sizeof p, "{\"store\":\"%s\"}", store);
  free(ok(h, "store.close", p, NULL));

  /* Handles belong to their session; closing it closes what is left. */
  uint64_t other = ext_plugin_open();
  CHECK(raw(other, 1, "{\"deadline\":\"2999-01-01T00:00:00Z\"}", &out) == 0, "second handshake");
  free(out);
  char *kept = ok(other, "store.open", "{\"memory\":true}", "store");
  snprintf(p, sizeof p, "{\"store\":\"%s\",\"namespace\":\"x\"}", kept);
  expect_error(h, "store.snapshot", p, "not-found");
  ext_plugin_close(other);
  CHECK(raw(other, 1, "{\"deadline\":\"2999-01-01T00:00:00Z\"}", &out) == 2, "a closed session answered");
  CHECK(raw(h, 2, "not json", &out) == 1, "a frame that is not JSON was taken");
  ext_plugin_close(h);
  free(kept);
  free(store);
  free(recorder);
  free(sup);
  free(response);
  if (failures) {
    fprintf(stderr, "%d failure(s)\n", failures);
    return 1;
  }
  printf("ok: plugin\n");
  return 0;
}
