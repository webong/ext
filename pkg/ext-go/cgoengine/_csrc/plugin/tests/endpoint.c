/* Loopback data endpoints: the engine services endpoint.check and
 * endpoint.validate, run against the shared conformance fixture
 * (testdata/v1/endpoint-cases.json) and against inputs the fixture does not hold.
 * The Go differential tests in pkg/ext-go/cgoengine run the same fixture through
 * the Go package as well. Usage: ext_endpoint_test <path to the fixture> */
#define _XOPEN_SOURCE 700
#include "wire.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static ext_status call(const char *operation, const char *json) {
  ext_buffer out = {0};
  ext_status s = ext_engine_call(operation, (const uint8_t *)json, strlen(json), &out);
  ext_buffer_free(&out);
  return s;
}
static ext_status outcome(const char *name) {
  if (!strcmp(name, "ok"))
    return EXT_OK;
  if (!strcmp(name, "invalid"))
    return EXT_INVALID;
  if (!strcmp(name, "denied"))
    return EXT_DENIED;
  if (!strcmp(name, "expired"))
    return EXT_EXPIRED;
  fprintf(stderr, "unknown outcome %s\n", name);
  abort();
}
static char *read_file(const char *path, size_t *length) {
  FILE *f = fopen(path, "rb");
  if (!f) {
    fprintf(stderr, "cannot open %s\n", path);
    exit(2);
  }
  assert(!fseek(f, 0, SEEK_END));
  long n = ftell(f);
  assert(n > 0);
  rewind(f);
  char *data = malloc((size_t)n + 1);
  assert(data && fread(data, 1, (size_t)n, f) == (size_t)n);
  data[n] = 0;
  fclose(f);
  *length = (size_t)n;
  return data;
}

/* Runs every case of the fixture through both services. */
static void test_fixture(const char *path) {
  size_t length;
  char *data = read_file(path, &length);
  yyjson_doc *doc = ext_parse((const uint8_t *)data, length);
  assert(doc);
  yyjson_val *cases = yyjson_obj_get(yyjson_doc_get_root(doc), "cases"), *c;
  size_t i, n, failures = 0, counts[4] = {0};
  assert(yyjson_is_arr(cases) && yyjson_arr_size(cases) > 100);
  yyjson_arr_foreach(cases, i, n, c) {
    const char *name = yyjson_get_str(yyjson_obj_get(c, "name"));
    ext_status check = outcome(yyjson_get_str(yyjson_obj_get(c, "check"))),
               validate = outcome(yyjson_get_str(yyjson_obj_get(c, "validate")));
    yyjson_val *endpoint = yyjson_obj_get(c, "endpoint");
    /* endpoint.check takes the endpoint itself. */
    size_t endpoint_len;
    char *endpoint_json = yyjson_val_write(endpoint, 0, &endpoint_len);
    assert(endpoint_json);
    ext_status got_check = call("endpoint.check", endpoint_json);
    /* endpoint.validate takes the endpoint and the policy. */
    yyjson_mut_doc *input = yyjson_mut_doc_new(NULL);
    yyjson_mut_val *root = yyjson_mut_obj(input);
    yyjson_mut_doc_set_root(input, root);
    assert(yyjson_mut_obj_add_val(input, root, "endpoint",
                                  yyjson_val_mut_copy(input, endpoint)));
    assert(yyjson_mut_obj_add_val(input, root, "policy",
                                  yyjson_val_mut_copy(input, yyjson_obj_get(c, "policy"))));
    char *input_json = yyjson_mut_write(input, 0, NULL);
    assert(input_json);
    ext_status got_validate = call("endpoint.validate", input_json);
    if (got_check != check || got_validate != validate) {
      failures++;
      fprintf(stderr, "%s: check %s (want %s), validate %s (want %s)\n", name,
              ext_host_status_string(got_check), ext_host_status_string(check),
              ext_host_status_string(got_validate),
              ext_host_status_string(validate));
    }
    counts[validate == EXT_OK ? 0 : validate == EXT_INVALID ? 1 : validate == EXT_DENIED ? 2 : 3]++;
    free(endpoint_json);
    free(input_json);
    yyjson_mut_doc_free(input);
  }
  assert(!failures);
  /* A fixture that states only one kind of outcome proves little. */
  for (int k = 0; k < 4; k++)
    assert(counts[k] >= 5);
  yyjson_doc_free(doc);
  free(data);
}

/* What the services refuse as input, before any endpoint rule applies. */
static void test_service_input(void) {
  const char *good =
      "{\"scheme\":\"ws\",\"address\":\"localhost:80\",\"expiresAt\":"
      "\"2030-01-01T00:30:00Z\"}";
  char input[1024];
#define VALIDATE(policy)                                                       \
  (snprintf(input, sizeof(input), "{\"endpoint\":%s,\"policy\":%s}", good,    \
            policy),                                                           \
   call("endpoint.validate", input))
  const char *now = "\"now\":\"2030-01-01T00:00:00Z\"";
  char policy[256];
  snprintf(policy, sizeof(policy), "{\"schemes\":[\"ws\"],%s}", now);
  assert(VALIDATE(policy) == EXT_OK);
  /* The policy is a structure of the service's own: unknown fields, wrong types. */
  assert(VALIDATE("{\"schemes\":[\"ws\"],\"extra\":1}") == EXT_INVALID);
  assert(VALIDATE("{\"schemes\":\"ws\"}") == EXT_INVALID);
  assert(VALIDATE("{\"schemes\":[\"ws\",7]}") == EXT_INVALID);
  assert(VALIDATE("{\"schemes\":[\"ws\"],\"maxLifetimeMilliseconds\":1.5}") == EXT_INVALID);
  assert(VALIDATE("{\"schemes\":[\"ws\"],\"maxLifetimeMilliseconds\":\"1\"}") == EXT_INVALID);
  assert(VALIDATE("{\"schemes\":[\"ws\"],\"now\":\"soon\"}") == EXT_INVALID);
  assert(VALIDATE("{\"schemes\":[\"ws\"],\"now\":7}") == EXT_INVALID);
  assert(VALIDATE("[]") == EXT_INVALID);
  assert(VALIDATE("null") == EXT_INVALID);
  /* A policy may leave everything out: nothing is then admitted. */
  assert(VALIDATE("{}") == EXT_DENIED);
  /* The input itself: both parts are required, nothing else is allowed. */
  assert(call("endpoint.validate", "{\"endpoint\":{}}") == EXT_INVALID);
  assert(call("endpoint.validate", "{\"policy\":{}}") == EXT_INVALID);
  assert(call("endpoint.validate", "{\"endpoint\":{},\"policy\":{},\"x\":1}") == EXT_INVALID);
  assert(call("endpoint.validate", "[]") == EXT_INVALID);
  assert(call("endpoint.validate", "{\"endpoint\":{},\"endpoint\":{}}") == EXT_INVALID);
  /* endpoint.check takes the endpoint alone, and refuses what is not one. */
  assert(call("endpoint.check", good) == EXT_OK);
  assert(call("endpoint.check", "[]") == EXT_INVALID);
  assert(call("endpoint.check", "7") == EXT_INVALID);
  assert(call("endpoint.check", "null") == EXT_INVALID);
  assert(call("endpoint.check", "{\"scheme\":\"ws\",\"scheme\":\"ws\"}") == EXT_INVALID);
  assert(call("endpoint.check", "") == EXT_INVALID);
}

/* A policy without a clock uses the wall clock. */
static void test_wall_clock(void) {
  time_t base = time(NULL);
  char endpoint[256], input[512];
  struct {
    long offset;
    ext_status want;
  } cases[] = {{600, EXT_OK}, {3 * 3600, EXT_INVALID}, {-60, EXT_EXPIRED}};
  for (size_t i = 0; i < sizeof(cases) / sizeof(*cases); i++) {
    time_t at = base + cases[i].offset;
    struct tm t;
#ifdef _WIN32
    gmtime_s(&t, &at);
#else
    gmtime_r(&at, &t);
#endif
    char stamp[40];
    strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%SZ", &t);
    snprintf(endpoint, sizeof(endpoint),
             "{\"scheme\":\"tcp\",\"address\":\"[::1]:9\",\"expiresAt\":\"%s\"}", stamp);
    snprintf(input, sizeof(input),
             "{\"endpoint\":%s,\"policy\":{\"schemes\":[\"tcp\"]}}", endpoint);
    assert(call("endpoint.validate", input) == cases[i].want);
  }
}

/* The time parser does not lean on the platform: years the C library cannot
 * represent, and the zero time in any zone, are decided the same everywhere. */
static void test_times(void) {
  yyjson_doc *doc;
  int64_t seconds;
  int32_t nanoseconds;
#define PARSE(text)                                                            \
  (doc = ext_parse((const uint8_t *)"\"" text "\"", strlen(text) + 2),        \
   assert(doc), ext_instant(yyjson_doc_get_root(doc), &seconds, &nanoseconds))
  assert(PARSE("1970-01-01T00:00:00Z") && seconds == 0 && nanoseconds == 0);
  yyjson_doc_free(doc);
  assert(PARSE("0001-01-01T00:00:00Z") && ext_instant_is_zero(seconds, nanoseconds));
  yyjson_doc_free(doc);
  assert(PARSE("0001-01-01T01:00:00+01:00") && ext_instant_is_zero(seconds, nanoseconds));
  yyjson_doc_free(doc);
  assert(PARSE("0000-12-31T23:00:00-01:00") == 0); /* year 0 is not accepted */
  yyjson_doc_free(doc);
  assert(PARSE("2000-02-29T12:00:00Z") && seconds == 951825600);
  yyjson_doc_free(doc);
  assert(PARSE("1900-02-29T12:00:00Z") == 0); /* 1900 was not a leap year */
  yyjson_doc_free(doc);
  assert(PARSE("9999-12-31T23:59:59Z") && seconds == 253402300799LL);
  yyjson_doc_free(doc);
  assert(PARSE("2030-01-01T00:00:00.123456789Z") && nanoseconds == 123456789);
  yyjson_doc_free(doc);
  assert(PARSE("2030-01-01T00:00:00.5Z") && nanoseconds == 500000000);
  yyjson_doc_free(doc);
  assert(PARSE("2030-01-01T00:00:00.1234567891Z") && nanoseconds == 123456789);
  yyjson_doc_free(doc);
  assert(PARSE("2030-01-01T05:30:00+05:30") && seconds == 1893456000LL);
  yyjson_doc_free(doc);
}

int main(int argc, char **argv) {
  assert(argc == 2);
  test_fixture(argv[1]);
  test_service_input();
  test_wall_clock();
  test_times();
  puts("endpoint checks passed");
  return 0;
}
