/* Entrypoint resolution in the package store (ext_store_resolve_entrypoint,
 * store.c). The Go store's ResolveEntrypoint is the baseline; the differential
 * tests in pkg/ext-go/cgoengine hold the two together. */
#include "ext_host.h"
#include "fs.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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
static void sha(const char *body, char out[65]) {
  uint8_t raw[32];
  assert(ext_engine_sha256((const uint8_t *)body, strlen(body), raw) == EXT_OK);
  hex(raw, out);
}

/* The path the engine reports for an artifact, as it appears inside a JSON
 * string: the root and the rest joined and cleaned. On Windows the engine uses
 * backslashes (any run of separators becomes one) and JSON doubles each. */
static void json_path(char *out, size_t size, const char *root, const char *id,
                      const char *artifact) {
  char joined[1024];
  int n = snprintf(joined, sizeof(joined), "%s/%s/bin/%s", root, id, artifact);
  assert(n > 0 && (size_t)n < sizeof(joined) && (size_t)n * 2 + 1 <= size);
#ifdef _WIN32
  size_t w = 0;
  for (size_t i = 0; joined[i]; i++) {
    if (joined[i] == '/' || joined[i] == '\\') {
      if (w >= 2 && out[w - 1] == '\\' && out[w - 2] == '\\')
        continue;
      out[w++] = '\\';
      out[w++] = '\\';
    } else
      out[w++] = joined[i];
  }
  out[w] = 0;
#else
  memcpy(out, joined, (size_t)n + 1);
#endif
}

typedef struct {
  const char *name, *os, *arch;
} art;
typedef struct {
  const char *name, *runtime, *artifact, *protocols; /* protocols: JSON array */
} ent;

/* A source package at dir: one file artifact bin/<name> (holding its name) per
 * art, and the entrypoints. Contract example/v1 is what resolution asks for. */
static void package(const char *dir, const char *id, const char *revision,
                    const art *arts, size_t na, const ent *ents, size_t ne) {
  assert(ext_fs_make_root(dir) == EXT_OK);
  ext_fs *fs = NULL;
  assert(ext_fs_open(dir, &fs) == EXT_OK);
  {
    ext_fs_entry *old = NULL;
    size_t count = 0;
    assert(ext_fs_list(fs, "", &old, &count) == EXT_OK);
    for (size_t i = 0; i < count; i++)
      assert(ext_fs_remove_all(fs, old[i].name) == EXT_OK);
    ext_fs_list_free(old, count);
  }
  char artifacts[4096] = "", entries[4096] = "";
  size_t used = 0;
  for (size_t i = 0; i < na; i++) {
    char path[128], sum[65], extra[160] = "";
    snprintf(path, sizeof(path), "bin/%s", arts[i].name);
    assert(ext_fs_write_file(fs, path, (const uint8_t *)arts[i].name,
                             strlen(arts[i].name), 1) == EXT_OK);
    sha(arts[i].name, sum);
    if (arts[i].os)
      snprintf(extra + strlen(extra), sizeof(extra) - strlen(extra),
               ",\"os\":\"%s\"", arts[i].os);
    if (arts[i].arch)
      snprintf(extra + strlen(extra), sizeof(extra) - strlen(extra),
               ",\"arch\":\"%s\"", arts[i].arch);
    int n = snprintf(artifacts + used, sizeof(artifacts) - used,
                     "%s{\"name\":\"%s\",\"path\":\"%s\",\"sha256\":\"%s\"%s}",
                     i ? "," : "", arts[i].name, path, sum, extra);
    assert(n > 0 && used + (size_t)n < sizeof(artifacts));
    used += (size_t)n;
  }
  used = 0;
  for (size_t i = 0; i < ne; i++) {
    int n = snprintf(entries + used, sizeof(entries) - used,
                     "%s{\"name\":\"%s\",\"runtime\":\"%s\",\"artifact\":\"%s\","
                     "\"protocols\":%s}",
                     i ? "," : "", ents[i].name, ents[i].runtime,
                     ents[i].artifact, ents[i].protocols);
    assert(n > 0 && used + (size_t)n < sizeof(entries));
    used += (size_t)n;
  }
  char manifest[12288];
  int n = snprintf(manifest, sizeof(manifest),
                   "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":{"
                   "\"apiVersion\":\"ext.plugin/v1\","
                   "\"identity\":{\"id\":\"%s\",\"revision\":\"%s\"},"
                   "\"contracts\":[{\"name\":\"example\",\"version\":\"v1\","
                   "\"operations\":[{\"name\":\"run\"}]}]},"
                   "\"artifacts\":[%s],\"entrypoints\":[%s]}",
                   id, revision, artifacts, entries);
  assert(n > 0 && (size_t)n < sizeof(manifest));
  assert(ext_fs_write_file(fs, "plugin.json", (const uint8_t *)manifest,
                           (size_t)n, 0) == EXT_OK);
  ext_fs_close(fs);
}

typedef struct {
  ext_store *store;
  const char *root;
  char digest[65];
} fixture;

/* Installs the package and records the digest a reviewer would. */
static void install(fixture *f, const char *source, const char *id) {
  ext_buffer manifest = {0};
  assert(ext_store_install(f->store, source, 0, &manifest) == EXT_OK);
  ext_buffer_free(&manifest);
  uint8_t raw[32];
  assert(ext_store_digest(f->root, id, raw) == EXT_OK);
  hex(raw, f->digest);
}
static ext_status resolve(const fixture *f, const char *id, const char *contract,
                          const char *version, const char *protocol,
                          const char *digest, const char *os, const char *arch,
                          ext_buffer *out) {
  return ext_store_resolve_entrypoint(f->store, id, contract, version, protocol,
                                      digest, os, arch, out);
}
/* The selection is exactly name and artifact, with the path under the store. */
static void expect(const fixture *f, const char *id, const char *protocol,
                   const char *os, const char *arch, const char *name,
                   const char *artifact) {
  ext_buffer out = {0};
  assert(resolve(f, id, "example", "v1", protocol, f->digest, os, arch, &out) ==
         EXT_OK);
  char want[2048], path[2048];
  json_path(path, sizeof(path), f->root, id, artifact);
  int n = snprintf(want, sizeof(want),
                   "{\"name\":\"%s\",\"artifact\":\"%s\",\"path\":\"%s\","
                   "\"digest\":\"%s\"}",
                   name, artifact, path, f->digest);
  assert(n > 0 && (size_t)n < sizeof(want));
  if (out.len != (size_t)n || memcmp(out.data, want, (size_t)n)) {
    fprintf(stderr, "os=%s arch=%s\n got:  %.*s\n want: %s\n", os, arch,
            (int)out.len, (const char *)out.data, want);
    abort();
  }
  ext_buffer_free(&out);
}
static void expect_status(const fixture *f, const char *id, const char *contract,
                          const char *version, const char *protocol,
                          const char *digest, const char *os, const char *arch,
                          ext_status want) {
  ext_buffer out = {(uint8_t *)"x", 1}; /* must be cleared on any failure */
  ext_status got = resolve(f, id, contract, version, protocol, digest, os, arch, &out);
  if (got != want) {
    fprintf(stderr, "%s: got %d (%s), want %d (%s)\n", id, got,
            ext_host_status_string(got), want, ext_host_status_string(want));
    abort();
  }
  assert(!out.data && !out.len);
}

static void platform_names(const char *scratch, fixture *f) {
  char *source = join(scratch, "source-names");
  const art arts[] = {{"generic", NULL, NULL},
                      {"on-linux", NULL, NULL},
                      {"on-darwin", NULL, NULL},
                      {"on-windows", NULL, NULL}};
  const ent ents[] = {{"main", "argv", "generic", "[\"example/v1\"]"},
                      {"main.linux", "argv", "on-linux", "[\"example/v1\"]"},
                      {"main.darwin", "argv", "on-darwin", "[\"example/v1\"]"},
                      {"main.windows", "argv", "on-windows", "[\"example/v1\"]"}};
  package(source, "names", "r1", arts, 4, ents, 4);
  install(f, source, "names");
  /* an entrypoint named for another platform is not for this one */
  expect(f, "names", "example/v1", "linux", "amd64", "main.linux", "on-linux");
  expect(f, "names", "example/v1", "darwin", "arm64", "main.darwin", "on-darwin");
  expect(f, "names", "example/v1", "windows", "amd64", "main.windows",
         "on-windows");
  expect(f, "names", "example/v1", "plan9", "amd64", "main", "generic");
  /* nothing is named for freebsd, so only the plain entrypoint is left */
  expect(f, "names", "example/v1", "freebsd", "amd64", "main", "generic");

  /* the trust and contract gates, before anything is selected */
  expect_status(f, "names", "example", "v1", "example/v1", NULL, "linux",
                "amd64", EXT_DENIED);
  expect_status(f, "names", "example", "v1", "example/v1", "", "linux", "amd64",
                EXT_DENIED);
  expect_status(f, "names", "example", "v1", "example/v1", "wrong", "linux",
                "amd64", EXT_DENIED);
  char upper[65];
  for (size_t i = 0; i < 65; i++)
    upper[i] = f->digest[i] >= 'a' && f->digest[i] <= 'f'
                   ? (char)(f->digest[i] - 32)
                   : f->digest[i];
  expect_status(f, "names", "example", "v1", "example/v1", upper, "linux",
                "amd64", EXT_DENIED); /* the digest is compared as it was recorded */
  expect_status(f, "names", "different", "v1", "example/v1", f->digest, "linux",
                "amd64", EXT_UNSUPPORTED);
  expect_status(f, "names", "example", "v2", "example/v1", f->digest, "linux",
                "amd64", EXT_UNSUPPORTED);
  expect_status(f, "names", "example", "v1", "other/v1", f->digest, "linux",
                "amd64", EXT_UNSUPPORTED);
  /* a missing digest is refused before the id is even read, as Go does */
  expect_status(f, "../bad", "example", "v1", "example/v1", "", "linux", "amd64",
                EXT_DENIED);
  expect_status(f, "../bad", "example", "v1", "example/v1", f->digest, "linux",
                "amd64", EXT_INVALID);
  expect_status(f, "absent", "example", "v1", "example/v1", f->digest, "linux",
                "amd64", EXT_NOT_FOUND);

  /* the build's own platform when none is named */
  ext_buffer out = {0};
  assert(resolve(f, "names", "example", "v1", "example/v1", f->digest, NULL, NULL,
                 &out) == EXT_OK);
  ext_buffer_free(&out);
  free(source);
}

static void artifact_platform(const char *scratch, fixture *f) {
  char *source = join(scratch, "source-score");
  const art arts[] = {{"any", NULL, NULL},
                      {"linux", "linux", NULL},
                      {"linux-amd64", "linux", "amd64"},
                      {"arm64", NULL, "arm64"},
                      {"linux-again", "linux", NULL}};
  const char *p = "[\"example/v1\"]";
  const ent ents[] = {{"e0", "argv", "any", p},
                      {"e1", "argv", "linux", p},
                      {"e2", "argv", "linux-amd64", p},
                      {"e3", "argv", "arm64", p},
                      {"e4", "argv", "linux-again", p}};
  package(source, "score", "r1", arts, 5, ents, 5);
  install(f, source, "score");
  /* 3 for os and arch, 2 for os alone, 1 for arch alone, 0 for neither */
  expect(f, "score", "example/v1", "linux", "amd64", "e2", "linux-amd64");
  /* two at 2: the first in manifest order wins */
  expect(f, "score", "example/v1", "linux", "arm64", "e1", "linux");
  expect(f, "score", "example/v1", "darwin", "arm64", "e3", "arm64");
  expect(f, "score", "example/v1", "darwin", "amd64", "e0", "any");
  free(source);
}

static void runtime_and_protocol(const char *scratch, fixture *f) {
  char *source = join(scratch, "source-filter");
  const art arts[] = {{"first", NULL, NULL}, {"second", NULL, NULL}};
  const ent ents[] = {
      {"x", "wasm", "first", "[\"p/v1\"]"},          /* not argv */
      {"y", "argv", "first", "[\"q/v1\",\"r/v1\"]"}, /* other protocols */
      {"z", "argv", "second", "[\"p/v1\"]"}};
  package(source, "filter", "r1", arts, 2, ents, 3);
  install(f, source, "filter");
  expect(f, "filter", "p/v1", "linux", "amd64", "z", "second");
  expect(f, "filter", "r/v1", "linux", "amd64", "y", "first");
  expect_status(f, "filter", "example", "v1", "none/v1", f->digest, "linux",
                "amd64", EXT_UNSUPPORTED);
  /* an artifact for another platform leaves nothing to run */
  char *other = join(scratch, "source-elsewhere");
  const art foreign[] = {{"only-windows", "windows", NULL}};
  const ent entry[] = {{"main", "argv", "only-windows", "[\"p/v1\"]"}};
  package(other, "elsewhere", "r1", foreign, 1, entry, 1);
  install(f, other, "elsewhere");
  expect_status(f, "elsewhere", "example", "v1", "p/v1", f->digest, "linux",
                "amd64", EXT_UNSUPPORTED);
  expect(f, "elsewhere", "p/v1", "windows", "amd64", "main", "only-windows");
  free(other);
  free(source);
}

/* The package is read and verified first, so a changed one is never selected. */
static void damaged(fixture *f) {
  ext_fs *fs = NULL;
  char *dir = join(f->root, "names");
  assert(ext_fs_open(dir, &fs) == EXT_OK);
  assert(ext_fs_remove_all(fs, "bin/generic") == EXT_OK);
  assert(ext_fs_write_file(fs, "bin/generic", (const uint8_t *)"tampered", 8,
                           1) == EXT_OK);
  ext_fs_close(fs);
  /* the package no longer verifies, whatever digest the caller reviewed */
  uint8_t raw[32];
  assert(ext_store_digest(f->root, "names", raw) == EXT_MISMATCH);
  expect_status(f, "names", "example", "v1", "example/v1", f->digest, "linux",
                "amd64", EXT_MISMATCH);
  free(dir);
}

static void arguments(const fixture *f) {
  ext_buffer out = {0};
  assert(ext_store_resolve_entrypoint(NULL, "filter", "example", "v1",
                                      "p/v1", f->digest, "linux", "amd64",
                                      &out) == EXT_INVALID);
  assert(ext_store_resolve_entrypoint(f->store, "filter", NULL, "v1", "p/v1",
                                      f->digest, "linux", "amd64", &out) ==
         EXT_INVALID);
  assert(ext_store_resolve_entrypoint(f->store, "filter", "example", NULL,
                                      "p/v1", f->digest, "linux", "amd64",
                                      &out) == EXT_INVALID);
  assert(ext_store_resolve_entrypoint(f->store, "filter", "example", "v1", NULL,
                                      f->digest, "linux", "amd64", &out) ==
         EXT_INVALID);
  assert(ext_store_resolve_entrypoint(f->store, "filter", "example", "v1",
                                      "p/v1", f->digest, "linux", "amd64",
                                      NULL) == EXT_INVALID);
}

int main(int argc, char **argv) {
  if (argc != 2) {
    fprintf(stderr, "usage: %s scratch-directory\n", argv[0]);
    return 2;
  }
  char *root = join(argv[1], "store");
  fixture f = {NULL, root, ""};
  assert(ext_store_open(root, NULL, &f.store) == EXT_OK);
  platform_names(argv[1], &f);
  artifact_platform(argv[1], &f);
  runtime_and_protocol(argv[1], &f);
  arguments(&f);
  damaged(&f);
  ext_store_close(f.store);
  free(root);
  puts("store resolve ok");
  return 0;
}
