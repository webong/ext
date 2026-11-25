/* The library interface: objects opened from JSON, called by operation name, closed by handle. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "ext_ledger.h"

static int failures;

#define CHECK(cond, ...)                                     \
  do {                                                       \
    if (!(cond)) {                                           \
      fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);   \
      fprintf(stderr, __VA_ARGS__);                          \
      fprintf(stderr, "\n");                                 \
      failures++;                                            \
    }                                                        \
  } while (0)

static uint64_t open_json(const char *config, int want) {
  uint64_t h = 0;
  int rc = ext_ledger_open(config, strlen(config), &h);
  CHECK(rc == want, "open %s: %s, want %s", config, ext_ledger_code_name(rc), ext_ledger_code_name(want));
  return h;
}

/* call runs op and returns its output (caller frees), checking the code. */
static char *call(uint64_t h, const char *op, const char *in, int want) {
  char *out = NULL;
  size_t n = 0;
  int rc = ext_ledger_call(h, op, in, in ? strlen(in) : 0, &out, &n);
  CHECK(rc == want, "%s %s: %s, want %s", op, in ? in : "", ext_ledger_code_name(rc), ext_ledger_code_name(want));
  CHECK(rc != 0 || (out && strlen(out) == n), "%s: OK without output", op);
  CHECK(rc == 0 || !out, "%s: output with an error", op);
  return out;
}

static void expect(uint64_t h, const char *op, const char *in, const char *contains) {
  char *out = call(h, op, in, EXT_LEDGER_OK);
  CHECK(out && strstr(out, contains), "%s: %s lacks %s", op, out ? out : "(null)", contains);
  ext_ledger_free(out);
}

int main(void) {
  CHECK(ext_ledger_abi_version() == EXT_LEDGER_ABI_VERSION, "abi version");

  uint64_t store = open_json("{\"object\":\"store\",\"backend\":\"memory\"}", EXT_LEDGER_OK);
  CHECK(store != 0, "a store handle");
  ext_ledger_free(call(store, "register", "{\"schema\":{\"namespace\":\"demo\",\"version\":\"1\"}}", EXT_LEDGER_OK));
  uint64_t watch = open_json("{\"object\":\"watch\",\"store\":1,\"namespace\":\"demo\",\"buffer\":8}", EXT_LEDGER_OK);
  char *out = call(watch, "next", "{}", EXT_LEDGER_TIMEOUT);
  free(out);
  expect(store, "apply", "{\"transaction\":{\"namespace\":\"demo\",\"vertices\":[{\"id\":\"a\",\"kind\":\"demo/node\"}]}}", "\"cursor\"");
  expect(store, "get_vertex", "{\"namespace\":\"demo\",\"id\":\"a\"}", "\"demo/node\"");
  expect(store, "query_vertices", "{\"query\":{\"namespace\":\"demo\",\"limit\":10}}", "\"a\"");
  expect(store, "snapshot", "{\"namespace\":\"demo\"}", "\"a\"");
  expect(store, "changes", "{\"namespace\":\"demo\",\"limit\":10}", "\"a\"");
  expect(watch, "next", "{\"timeout_ms\":1000}", "\"a\"");
  ext_ledger_free(call(store, "get_vertex", "{\"namespace\":\"demo\",\"id\":\"missing\"}", EXT_LEDGER_NOT_FOUND));
  ext_ledger_free(call(store, "apply", "{}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(store, "no_such_operation", "{}", EXT_LEDGER_UNSUPPORTED));
  ext_ledger_free(call(store, "snapshot", "[1]", EXT_LEDGER_INVALID));

  /* The library itself, at handle 0. */
  expect(0, "vocabulary", NULL, "ext.system");
  expect(0, "code_name", "{\"code\":2}", "\"not_found\"");
  expect(0, "code_name", "{\"code\":0}", "\"ok\"");
  {
    /* describe serves interface.json as it is. */
    FILE *f = fopen(EXT_LEDGER_INTERFACE_JSON, "rb");
    CHECK(f != NULL, "open %s", EXT_LEDGER_INTERFACE_JSON);
    if (f) {
      static char file[1 << 16];
      size_t fn = fread(file, 1, sizeof file, f);
      fclose(f);
      char *described = NULL;
      size_t dn = 0;
      CHECK(ext_ledger_call(0, "describe", NULL, 0, &described, &dn) == EXT_LEDGER_OK, "describe");
      CHECK(described && dn == fn && !memcmp(described, file, fn), "describe is not interface.json (%zu and %zu bytes)", dn, fn);
      ext_ledger_free(described);
    }
  }
  expect(0, "canonical_transaction", "{\"transaction\":{\"vertices\":[],\"namespace\":\"demo\"}}", "\"fingerprint\"");
  expect(0, "collect_environment", NULL, "\"hostname\"");
  ext_ledger_free(call(0, "collect", "{\"what\":\"processes\",\"limit\":0}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(0, "collect", "{\"what\":\"processes\",\"limit\":65537}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(0, "collect", "{\"what\":\"processes\",\"limit\":1.5}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(0, "collect", "{\"what\":\"host\",\"limit\":1}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(0, "collect", "{\"what\":\"process\"}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(0, "collect", "{\"what\":\"process\",\"pid\":0}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(0, "collect", "{\"what\":\"process\",\"pid\":1,\"resource_limit\":65537}", EXT_LEDGER_INVALID));
  ext_ledger_free(call(0, "collect", "{\"what\":\"processes\",\"pid\":1}", EXT_LEDGER_INVALID));
  {
    const char *input = "{\"what\":\"processes\",\"limit\":1}";
    char *snapshot = NULL;
    size_t length = 0;
    int rc = ext_ledger_call(0, "collect", input, strlen(input), &snapshot, &length);
    CHECK(rc == EXT_LEDGER_OK || rc == EXT_LEDGER_UNAVAILABLE || rc == EXT_LEDGER_DENIED || rc == EXT_LEDGER_UNSUPPORTED,
          "bounded process collection: %s", ext_ledger_code_name(rc));
    if (rc == EXT_LEDGER_OK) CHECK(snapshot && strstr(snapshot, "\"processes\"") && strstr(snapshot, "\"enumeration\""),
                                   "bounded process collection has an inventory");
    ext_ledger_free(snapshot);
  }

  /* A recorder over the store, and a store that will not close under it. */
  uint64_t recorder = open_json("{\"object\":\"recorder\",\"store\":1,\"machine\":true}", EXT_LEDGER_OK);
  CHECK(ext_ledger_close(store) == EXT_LEDGER_BUSY, "a store closed under its watch and recorder");
  expect(recorder, "record", "{\"operation\":\"read_host\",\"input\":{}}", "{");
  CHECK(ext_ledger_close(recorder) == EXT_LEDGER_OK, "close the recorder");
  CHECK(ext_ledger_close(recorder) == EXT_LEDGER_NOT_FOUND, "close a closed handle");
  ext_ledger_free(call(recorder, "scan", "{\"what\":\"processes\"}", EXT_LEDGER_NOT_FOUND));

  /* A supervisor. */
  uint64_t sup = open_json("{\"object\":\"supervisor\",\"store\":1,\"options\":{\"runtime_id\":\"interface-test\"}}", EXT_LEDGER_OK);
  expect(sup, "instances", NULL, "[");
  ext_ledger_free(call(sup, "instance", "{\"id\":\"nothing\"}", EXT_LEDGER_NOT_FOUND));
  ext_ledger_free(call(sup, "await", "{\"id\":\"x\",\"states\":\"running\"}", EXT_LEDGER_INVALID));
  CHECK(ext_ledger_close(sup) == EXT_LEDGER_OK, "close the supervisor");

  CHECK(ext_ledger_close(watch) == EXT_LEDGER_OK, "close the watch");
  CHECK(ext_ledger_close(store) == EXT_LEDGER_OK, "close the store once nothing is open on it");
  open_json("{\"object\":\"watch\",\"store\":1}", EXT_LEDGER_NOT_FOUND);
  open_json("{\"object\":\"store\",\"backend\":\"file\"}", EXT_LEDGER_INVALID);
  open_json("{\"object\":\"nothing\"}", EXT_LEDGER_INVALID);
  open_json("not json", EXT_LEDGER_INVALID);

  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("ledger interface checks passed\n");
  return 0;
}
