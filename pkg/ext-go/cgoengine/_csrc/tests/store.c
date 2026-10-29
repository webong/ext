#include "ext_host.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <direct.h>
#define MAKE_DIR(path) _mkdir(path)
#define REMOVE_DIR(path) _rmdir(path)
#else
#include <sys/stat.h>
#include <unistd.h>
#define MAKE_DIR(path) mkdir(path, 0700)
#define REMOVE_DIR(path) rmdir(path)
#endif

static void write_file(const char *path, const char *body) {
  FILE *f = fopen(path, "wb");
  assert(f);
  size_t n = strlen(body);
  assert(fwrite(body, 1, n, f) == n);
  assert(!fclose(f));
}
static char *join(const char *base, const char *leaf) {
  size_t a = strlen(base), b = strlen(leaf);
  char *path = malloc(a + b + 2);
  assert(path);
  memcpy(path, base, a);
  path[a] = '/';
  memcpy(path + a + 1, leaf, b + 1);
  return path;
}
static void hex(const uint8_t digest[32], char out[65]) {
  const char digits[] = "0123456789abcdef";
  for (size_t i = 0; i < 32; i++) {
    out[2 * i] = digits[digest[i] >> 4];
    out[2 * i + 1] = digits[digest[i] & 15];
  }
  out[64] = 0;
}
int main(int argc, char **argv) {
  assert(argc == 2);
  const char *root = argv[1];
  assert(!MAKE_DIR(root));
  char *package = join(root, "example%2fa");
  char *src = join(package, "src");
  char *file = join(src, "main.py");
  char *manifest_path = join(package, "plugin.json");
  char *extra = join(package, "extra");
  assert(!MAKE_DIR(package));
  assert(!MAKE_DIR(src));
  write_file(file, "one");
  uint8_t raw[32], got[32], expected[32];
  char digest[65];
  assert(ext_engine_sha256((const uint8_t *)"one", 3, raw) == EXT_OK);
  hex(raw, digest);
  char manifest[2048];
  int n = snprintf(manifest, sizeof(manifest),
    "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":{\"apiVersion\":\"ext.plugin/v1\","
    "\"identity\":{\"id\":\"example/a\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"example.read\","
    "\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}]},"
    "\"artifacts\":[{\"name\":\"main\",\"path\":\"src/main.py\",\"sha256\":\"%s\"}]}",
    digest);
  assert(n > 0 && (size_t)n < sizeof(manifest));
  write_file(manifest_path, manifest);
  assert(ext_store_digest(root, "example/a", got) == EXT_OK);
  assert(ext_directory_digest(package, expected) == EXT_OK);
  assert(!memcmp(got, expected, 32));
  assert(ext_store_digest(root, "../bad", got) == EXT_INVALID);
  write_file(extra, "unexpected");
  assert(ext_store_digest(root, "example/a", got) == EXT_INVALID);
  assert(!remove(extra));
  write_file(file, "two");
  assert(ext_store_digest(root, "example/a", got) == EXT_MISMATCH);
  assert(!remove(file));
  assert(!remove(manifest_path));
  assert(!REMOVE_DIR(src));
  assert(!REMOVE_DIR(package));
  assert(!REMOVE_DIR(root));
  free(extra);
  free(manifest_path);
  free(file);
  free(src);
  free(package);
  return 0;
}
