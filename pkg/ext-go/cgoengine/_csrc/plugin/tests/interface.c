/* The library interface: the pure services and the stores behind open, call and close. */
#include "ext_plugin_host.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <direct.h>
#define make_dir(p) _mkdir(p)
#else
#include <sys/stat.h>
#define make_dir(p) mkdir(p, 0700)
#endif

static int failures;
#define CHECK(cond, ...)                                                                           \
  do {                                                                                             \
    if (!(cond)) {                                                                                 \
      fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);                                         \
      fprintf(stderr, __VA_ARGS__);                                                                \
      fprintf(stderr, "\n");                                                                       \
      failures++;                                                                                  \
    }                                                                                              \
  } while (0)

static uint64_t open_json(const char *config, ext_plugin_status want) {
  uint64_t h = 0;
  ext_plugin_status s = ext_plugin_open(config, strlen(config), &h);
  CHECK(s == want, "open %s: %s, want %s", config, ext_plugin_code_name(s), ext_plugin_code_name(want));
  return h;
}

static char *call(uint64_t h, const char *op, const char *in, ext_plugin_status want) {
  char *out = NULL;
  size_t n = 0;
  ext_plugin_status s = ext_plugin_call(h, op, in, in ? strlen(in) : 0, &out, &n);
  CHECK(s == want, "%s %s: %s, want %s", op, in ? in : "", ext_plugin_code_name(s), ext_plugin_code_name(want));
  CHECK(s != EXT_PLUGIN_OK || (out && strlen(out) == n), "%s: OK without output", op);
  CHECK(s == EXT_PLUGIN_OK || !out, "%s: output with an error", op);
  return out;
}

static void expect(uint64_t h, const char *op, const char *in, const char *contains) {
  char *out = call(h, op, in, EXT_PLUGIN_OK);
  CHECK(out && strstr(out, contains), "%s: %s lacks %s", op, out ? out : "(null)", contains);
  ext_plugin_free(out);
}

int main(int argc, char **argv) {
  if (argc < 2) {
    fprintf(stderr, "usage: %s <work-dir>\n", argv[0]);
    return 2;
  }
  const char *work = argv[1];
  make_dir(work);
  char root[1024], user[1024], system_root[1024], trust[1024], config[4096], in[4096];
  snprintf(root, sizeof root, "%s/store", work);
  snprintf(user, sizeof user, "%s/user", work);
  snprintf(system_root, sizeof system_root, "%s/system", work);
  snprintf(trust, sizeof trust, "%s/trust.json", work);
  remove(trust);

  CHECK(ext_plugin_abi_version() == EXT_PLUGIN_ABI_VERSION, "abi version");

  /* Handle 0: a pure service, the digests and the trust records. */
  expect(0, "protocol.negotiate", "{\"preferred\":[\"a\",\"b\"],\"offered\":[\"b\"]}", "\"b\"");
  ext_plugin_free(call(0, "descriptor.match", "{}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(0, "no.such.service", "{}", EXT_PLUGIN_UNSUPPORTED));
  expect(0, "code_name", "{\"code\":10}", "\"not found\"");
  make_dir(root);
  snprintf(in, sizeof in, "{\"root\":\"%s\"}", root);
  char *digest = call(0, "directory_digest", in, EXT_PLUGIN_OK);
  CHECK(digest && strlen(digest) == 66, "a directory digest: %s", digest ? digest : "(null)");
  if (digest) {
    snprintf(in, sizeof in, "{\"path\":\"%s\",\"scope\":\"demo\",\"id\":\"example/demo\",\"digest\":%s}", trust, digest);
    expect(0, "trust_state", in, "\"untrusted\"");
    expect(0, "trust_record", in, "null");
    expect(0, "trust_state", in, "\"trusted\"");
    snprintf(in, sizeof in, "{\"path\":\"%s\",\"scope\":\"demo\",\"id\":\"example/demo\"}", trust);
    expect(0, "trust_revoke", in, "null");
  }
  ext_plugin_free(digest);
  ext_plugin_free(call(0, "trust_state", "{\"path\":\"x\",\"scope\":\"s\",\"id\":\"i\",\"digest\":\"zz\"}", EXT_PLUGIN_INVALID));

  /* A store and a layered view over an empty root. */
  snprintf(config, sizeof config, "{\"object\":\"store\",\"root\":\"%s\"}", root);
  uint64_t store = open_json(config, EXT_PLUGIN_OK);
  expect(store, "ids", NULL, "[]");
  expect(store, "list", NULL, "[]");
  expect(store, "verify", NULL, "[]");
  expect(store, "list_unverified", NULL, "\"packages\"");
  ext_plugin_free(call(store, "load", "{\"id\":\"example/missing\"}", EXT_PLUGIN_NOT_FOUND));
  ext_plugin_free(call(store, "install", "{}", EXT_PLUGIN_INVALID));
  ext_plugin_free(call(store, "nothing", "{}", EXT_PLUGIN_UNSUPPORTED));
  ext_plugin_free(call(store, "ids", "[1]", EXT_PLUGIN_INVALID));
  make_dir(user);
  make_dir(system_root);
  snprintf(config, sizeof config, "{\"object\":\"layered\",\"roots\":[\"%s\",\"%s\"]}", user, system_root);
  uint64_t layered = open_json(config, EXT_PLUGIN_OK);
  expect(layered, "list", NULL, "[]");
  ext_plugin_free(call(layered, "load", "{\"id\":\"example/missing\"}", EXT_PLUGIN_NOT_FOUND));
  CHECK(ext_plugin_close(layered) == EXT_PLUGIN_OK, "close the layered view");
  CHECK(ext_plugin_close(store) == EXT_PLUGIN_OK, "close the store");
  CHECK(ext_plugin_close(store) == EXT_PLUGIN_NOT_FOUND, "close a closed handle");
  ext_plugin_free(call(store, "ids", NULL, EXT_PLUGIN_NOT_FOUND));
  open_json("{\"object\":\"store\"}", EXT_PLUGIN_INVALID);
  open_json("{\"object\":\"layered\",\"roots\":[]}", EXT_PLUGIN_INVALID);
  open_json("{\"object\":\"nothing\"}", EXT_PLUGIN_INVALID);
  open_json("not json", EXT_PLUGIN_INVALID);

  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("library interface checks passed\n");
  return 0;
}
