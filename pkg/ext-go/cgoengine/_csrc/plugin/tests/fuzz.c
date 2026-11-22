/* Fuzz target for everything the engine parses or validates from a peer's
 * bytes: JSON frames, descriptors, requests, package manifests and the package
 * services (validate, select, resolve). Sanitizers find memory errors but not an
 * input that is merely slow, so run it with a per-input time limit:
 *
 *   cmake -S lib/plugin -B build/fuzz -DCMAKE_C_COMPILER=clang -DEXT_FUZZ=ON
 *   cmake --build build/fuzz --target ext_fuzz
 *   build/fuzz/ext_fuzz -max_len=262144 -timeout=5 -max_total_time=60 corpus/
 *
 * libFuzzer stops and reports the input as soon as one call runs past -timeout
 * seconds. Build with -DEXT_FUZZ_REPLAY (no fuzzer runtime needed) to replay
 * files given on the command line, for example a stored corpus. */
#include "../src/services.h"
#include "../src/wire.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define DESCRIPTOR                                                             \
  "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":\"x.y\","            \
  "\"revision\":\"r1\"},\"contracts\":[{\"name\":\"c\",\"version\":\"1\","     \
  "\"operations\":[{\"name\":\"op\",\"surface\":\"\"}]}]}"

static void exercise(const uint8_t *data, size_t size) {
  yyjson_doc *doc = ext_parse(data, size);
  if (!doc)
    return;
  yyjson_val *root = yyjson_doc_get_root(doc);
  yyjson_doc *descriptor =
      ext_parse((const uint8_t *)DESCRIPTOR, sizeof(DESCRIPTOR) - 1);
  int64_t deadline = 0;
  (void)ext_descriptor(root);
  (void)ext_manifest_valid(root);
  (void)ext_request(yyjson_doc_get_root(descriptor), root, &deadline);
  (void)ext_match(yyjson_doc_get_root(descriptor), root);
  const char *services[] = {"package.validate", "package.select",
                            "package.resolve"};
  for (size_t i = 0; i < sizeof(services) / sizeof(*services); i++) {
    ext_buffer out = {0};
    (void)ext_package_service(services[i], root, &out);
    ext_buffer_free(&out);
  }
  yyjson_doc_free(descriptor);
  yyjson_doc_free(doc);
}

int LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) {
  exercise(data, size);
  return 0;
}

#ifdef EXT_FUZZ_REPLAY
int main(int argc, char **argv) {
  for (int i = 1; i < argc; i++) {
    FILE *f = fopen(argv[i], "rb");
    if (!f)
      return 2;
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    uint8_t *buf = malloc(n > 0 ? (size_t)n : 1);
    if (!buf || (n > 0 && fread(buf, 1, (size_t)n, f) != (size_t)n))
      return 2;
    fclose(f);
    exercise(buf, (size_t)n);
    free(buf);
  }
  return 0;
}
#endif
