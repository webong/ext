/* The package store (store.c): install, upgrade, replace, remove, load and ids,
 * with the failures each has. The Go store is the baseline; the differential
 * tests in pkg/ext-go/cgoengine hold the two together. */
#include "ext_host.h"
#include "fs.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <direct.h>
#define REMOVE_DIR(path) _rmdir(path)
#else
#include <sys/stat.h>
#include <unistd.h>
#define REMOVE_DIR(path) rmdir(path)
#endif

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
static void put(ext_fs *fs, const char *path, const char *body, int executable) {
  assert(ext_fs_write_file(fs, path, (const uint8_t *)body, strlen(body),
                           executable) == EXT_OK);
}
static int same(const ext_buffer *b, const ext_buffer *c) {
  return b->len == c->len && !memcmp(b->data, c->data, b->len);
}
static int equals(const ext_buffer *b, const char *text) {
  return b->len == strlen(text) && !memcmp(b->data, text, b->len);
}

/* A package directory: src/main.py holding main_body, and with_tree adds the
 * tree artifact "data" (data/x.txt, data/sub/y.txt and the executable
 * data/run.sh). The contract name sets what upgrade may change. */
static void make_package(const char *dir, const char *id, const char *revision,
                         const char *contract, const char *main_body,
                         int with_tree) {
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
  put(fs, "src/main.py", main_body, 0);
  char main_sum[65], tree_sum[65] = "";
  sha(main_body, main_sum);
  char artifacts[1024];
  int n = snprintf(artifacts, sizeof(artifacts),
                   "{\"name\":\"main\",\"path\":\"src/main.py\",\"sha256\":\"%s\"}",
                   main_sum);
  assert(n > 0 && (size_t)n < sizeof(artifacts));
  if (with_tree) {
    put(fs, "data/x.txt", "ex", 0);
    put(fs, "data/sub/y.txt", "why", 0);
    put(fs, "data/run.sh", "#!/bin/sh\n", 1);
    uint8_t raw[32];
    assert(ext_artifact_digest(dir, "data", 1, raw) == EXT_OK);
    hex(raw, tree_sum);
    size_t used = (size_t)n;
    n = snprintf(artifacts + used, sizeof(artifacts) - used,
                 ",{\"name\":\"assets\",\"path\":\"data\",\"sha256\":\"%s\","
                 "\"kind\":\"tree\"}",
                 tree_sum);
    assert(n > 0 && used + (size_t)n < sizeof(artifacts));
  }
  char manifest[4096];
  n = snprintf(manifest, sizeof(manifest),
               "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":{"
               "\"apiVersion\":\"ext.plugin/v1\","
               "\"identity\":{\"id\":\"%s\",\"revision\":\"%s\"},"
               "\"contracts\":[{\"name\":\"%s\",\"version\":\"v1\","
               "\"operations\":[{\"name\":\"read\"}]}]},"
               "\"artifacts\":[%s]}",
               id, revision, contract, artifacts);
  assert(n > 0 && (size_t)n < sizeof(manifest));
  put(fs, "plugin.json", manifest, 0);
  ext_fs_close(fs);
}
static void read_manifest(const char *dir, ext_buffer *out) {
  ext_fs *fs = NULL;
  assert(ext_fs_open(dir, &fs) == EXT_OK);
  assert(ext_fs_read_file(fs, "plugin.json", 1 << 20, out) == EXT_OK);
  ext_fs_close(fs);
}
static ext_fs_kind kind(ext_fs *fs, const char *path) {
  ext_fs_kind k = EXT_FS_OTHER;
  assert(ext_fs_kind_of(fs, path, &k) == EXT_OK);
  return k;
}
static void expect_content(ext_fs *fs, const char *path, const char *want) {
  ext_buffer got = {0};
  assert(ext_fs_read_file(fs, path, 1 << 20, &got) == EXT_OK);
  assert(equals(&got, want));
  ext_buffer_free(&got);
}
#ifndef _WIN32
static unsigned mode_of(const char *root, const char *path) {
  char *full = join(root, path);
  struct stat st;
  assert(!stat(full, &st));
  free(full);
  return (unsigned)(st.st_mode & 0777);
}
#endif
/* Only the installed packages are below the root: no staging or backup. */
static void expect_entries(ext_fs *fs, size_t want) {
  ext_fs_entry *entries = NULL;
  size_t n = 0;
  assert(ext_fs_list(fs, "", &entries, &n) == EXT_OK);
  if (n != want) {
    fprintf(stderr, "expected %zu entries below the root, found %zu:", want, n);
    for (size_t i = 0; i < n; i++)
      fprintf(stderr, " %s", entries[i].name);
    fprintf(stderr, "\n");
  }
  assert(n == want);
  ext_fs_list_free(entries, n);
}

int main(int argc, char **argv) {
  assert(argc == 2);
  const char *base = argv[1];
  char *root = join(base, "store");
  char *s1 = join(base, "s1"), *s2 = join(base, "s2"), *s3 = join(base, "s3");
  ext_buffer manifest = {0}, loaded = {0}, source = {0}, ids = {0};

  /* nothing there yet: an empty store, and nothing to find */
  ext_store *store = NULL;
  assert(ext_store_open(root, NULL, &store) == EXT_OK);
  assert(ext_store_open("", NULL, &(ext_store *){0}) == EXT_INVALID);
  assert(ext_store_ids(store, &ids) == EXT_OK && equals(&ids, "[]"));
  ext_buffer_free(&ids);
  assert(ext_store_load(store, "example/a", &loaded) == EXT_NOT_FOUND);
  assert(ext_store_remove(store, "example/a") == EXT_NOT_FOUND);
  make_package(s1, "example/a", "r2", "example.read", "two", 0);
  assert(ext_store_upgrade(store, s1, 0, &manifest) == EXT_NOT_FOUND);
  assert(ext_store_load(store, "../bad", &loaded) == EXT_INVALID);
  assert(ext_store_remove(store, "Bad") == EXT_INVALID);
  assert(ext_store_install(store, "", 0, &manifest) == EXT_INVALID);
  assert(ext_store_install(store, s1, 2u, &manifest) == EXT_INVALID);
  ext_fs_kind none;
  assert(ext_fs_probe(root, &none) == EXT_OK && none == EXT_FS_NONE);

  /* install: the first one makes the root, and returns plugin.json verbatim */
  make_package(s1, "example/a", "r1", "example.read", "one", 1);
  read_manifest(s1, &source);
  assert(ext_store_install(store, s1, 0, &manifest) == EXT_OK);
  assert(same(&manifest, &source));
  ext_buffer_free(&manifest);
  ext_fs *fs = NULL;
  assert(ext_fs_open(root, &fs) == EXT_OK);
  assert(kind(fs, "example%2fa") == EXT_FS_DIR);
  expect_entries(fs, 1);
  expect_content(fs, "example%2fa/src/main.py", "one");
  expect_content(fs, "example%2fa/data/sub/y.txt", "why");
  {
    ext_buffer stored = {0};
    assert(ext_fs_read_file(fs, "example%2fa/plugin.json", 1 << 20, &stored) ==
           EXT_OK);
    assert(same(&stored, &source)); /* the manifest is copied verbatim */
    ext_buffer_free(&stored);
  }
  ext_buffer_free(&source);
  assert(ext_store_ids(store, &ids) == EXT_OK && equals(&ids, "[\"example/a\"]"));
  ext_buffer_free(&ids);
  assert(ext_store_load(store, "example/a", &loaded) == EXT_OK);
  ext_buffer_free(&loaded);
  assert(ext_store_install(store, s1, 0, &manifest) == EXT_EXISTS);
  uint8_t digest[32], expected[32];
  char *dir = join(root, "example%2fa");
  assert(ext_store_digest(root, "example/a", digest) == EXT_OK);
  assert(ext_directory_digest(dir, expected) == EXT_OK && !memcmp(digest, expected, 32));
  free(dir);
#ifndef _WIN32
  /* owner-only; a tree's executable file stays executable */
  assert(mode_of(root, "example%2fa/src/main.py") == 0600);
  assert(mode_of(root, "example%2fa/data/x.txt") == 0600);
  assert(mode_of(root, "example%2fa/data/run.sh") == 0700);
  assert(mode_of(root, "example%2fa/plugin.json") == 0600);
  assert(mode_of(root, "example%2fa/data/sub") == 0700);
  assert(mode_of(root, "example%2fa") == 0700);
#endif

  /* upgrade: a new revision of the same id and contracts, in place */
  make_package(s2, "example/a", "r2", "example.read", "two", 1);
  uint8_t before[32], after[32];
  assert(ext_store_digest(root, "example/a", before) == EXT_OK);
  read_manifest(s2, &source);
  assert(ext_store_upgrade(store, s2, 0, &manifest) == EXT_OK);
  assert(same(&manifest, &source));
  ext_buffer_free(&manifest);
  ext_buffer_free(&source);
  expect_content(fs, "example%2fa/src/main.py", "two");
  assert(ext_store_digest(root, "example/a", after) == EXT_OK);
  assert(memcmp(before, after, 32));
  expect_entries(fs, 1); /* no staging or backup left behind */
  assert(ext_store_upgrade(store, s2, 0, &manifest) == EXT_INVALID); /* same revision */
  make_package(s3, "example/a", "r3", "example.write", "three", 0);
  assert(ext_store_upgrade(store, s3, 0, &manifest) == EXT_MISMATCH); /* contracts */
  expect_content(fs, "example%2fa/src/main.py", "two");
  expect_entries(fs, 1);
  /* replace may change the contracts, and installs what is not there */
  assert(ext_store_replace(store, s3, 0, &manifest) == EXT_OK);
  ext_buffer_free(&manifest);
  expect_content(fs, "example%2fa/src/main.py", "three");
  assert(kind(fs, "example%2fa/data") == EXT_FS_NONE); /* the tree is gone */
  make_package(s3, "example/b", "r1", "example.read", "bee", 0);
  assert(ext_store_replace(store, s3, 0, &manifest) == EXT_OK);
  ext_buffer_free(&manifest);
  expect_entries(fs, 2);
  assert(ext_store_ids(store, &ids) == EXT_OK &&
         equals(&ids, "[\"example/a\",\"example/b\"]"));
  ext_buffer_free(&ids);

  /* a source that is not what its manifest says is not installed, and leaves nothing */
  make_package(s2, "example/c", "r1", "example.read", "cee", 1);
  {
    ext_fs *src = NULL;
    assert(ext_fs_open(s2, &src) == EXT_OK);
    put(src, "extra.txt", "unlisted", 0);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_INVALID);
    assert(ext_fs_remove_all(src, "extra.txt") == EXT_OK);
    assert(ext_fs_remove_all(src, "src/main.py") == EXT_OK);
    put(src, "src/main.py", "changed", 0);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_MISMATCH);
    assert(ext_fs_remove_all(src, "src/main.py") == EXT_OK);
    assert(ext_store_install(store, s2, 0, &manifest) != EXT_OK); /* missing */
    put(src, "src/main.py", "cee", 0);
    assert(ext_fs_remove_all(src, "data/sub/y.txt") == EXT_OK);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_MISMATCH); /* tree */
    expect_entries(fs, 2);
    ext_fs_close(src);
  }
  {
    /* the manifest's own name is not an artifact path */
    ext_fs *src = NULL;
    assert(ext_fs_make_root(s3) == EXT_OK && ext_fs_open(s3, &src) == EXT_OK);
    assert(ext_fs_remove_all(src, "plugin.json") == EXT_OK);
    char sum[65];
    sha("{}", sum);
    char text[1024];
    int n = snprintf(text, sizeof(text),
                     "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":{"
                     "\"apiVersion\":\"ext.plugin/v1\","
                     "\"identity\":{\"id\":\"example/d\",\"revision\":\"r1\"},"
                     "\"contracts\":[{\"name\":\"example.read\",\"version\":\"v1\","
                     "\"operations\":[{\"name\":\"read\"}]}]},"
                     "\"artifacts\":[{\"name\":\"m\",\"path\":\"Plugin.JSON\","
                     "\"sha256\":\"%s\"}]}",
                     sum);
    assert(n > 0 && (size_t)n < sizeof(text));
    put(src, "plugin.json", text, 0);
    assert(ext_store_install(store, s3, 0, &manifest) == EXT_INVALID);
    ext_fs_close(src);
  }

  /* remove, and the directories that are not what their name says */
  assert(ext_store_remove(store, "example/b") == EXT_OK);
  assert(ext_store_remove(store, "example/b") == EXT_NOT_FOUND);
  assert(ext_store_load(store, "example/b", &loaded) == EXT_NOT_FOUND);
  assert(ext_fs_rename(fs, "example%2fa", "example%2fz") == EXT_OK);
  assert(ext_store_load(store, "example/z", &loaded) == EXT_MISMATCH);
  assert(ext_store_remove(store, "example/z") == EXT_MISMATCH);
  assert(ext_store_load(store, "example/a", &loaded) == EXT_NOT_FOUND);
  assert(ext_fs_rename(fs, "example%2fz", "example%2fa") == EXT_OK);
  /* a package changed behind the store's back does not load */
  {
    ext_fs *pkg = NULL;
    char *p = join(root, "example%2fa");
    assert(ext_fs_open(p, &pkg) == EXT_OK);
    assert(ext_fs_remove_all(pkg, "src/main.py") == EXT_OK);
    put(pkg, "src/main.py", "tampered", 0);
    assert(ext_store_load(store, "example/a", &loaded) == EXT_MISMATCH);
    put(pkg, "stray", "x", 0);
    assert(ext_store_load(store, "example/a", &loaded) != EXT_OK);
    ext_fs_close(pkg);
    free(p);
  }
  assert(ext_store_remove(store, "example/a") == EXT_OK); /* removes it anyway */
  expect_entries(fs, 0);

  /* ids: a staging directory is skipped, anything else that is not a package is not */
  assert(ext_fs_mkdirs(fs, ".install-leftover") == EXT_OK);
  assert(ext_store_ids(store, &ids) == EXT_OK && equals(&ids, "[]"));
  ext_buffer_free(&ids);
  put(fs, "stray", "x", 0);
  assert(ext_store_ids(store, &ids) == EXT_INVALID);
  assert(ext_fs_remove_all(fs, "stray") == EXT_OK);
  assert(ext_fs_mkdirs(fs, "Not-A-Package") == EXT_OK);
  assert(ext_store_ids(store, &ids) == EXT_INVALID);
  assert(ext_fs_remove_all(fs, "Not-A-Package") == EXT_OK);
  assert(ext_fs_remove_all(fs, ".install-leftover") == EXT_OK);
  ext_store_close(store);

#ifndef _WIN32
  /* PRESERVE_EXECUTABLE keeps the execute bit of a file artifact; the default does not */
  make_package(s2, "example/e", "r1", "example.read", "run me", 0);
  {
    ext_fs *src = NULL;
    assert(ext_fs_open(s2, &src) == EXT_OK);
    assert(ext_fs_remove_all(src, "src/main.py") == EXT_OK);
    put(src, "src/main.py", "run me", 1);
    ext_fs_close(src);
  }
  assert(ext_store_open(root, NULL, &store) == EXT_OK);
  assert(ext_store_install(store, s2, 0, &manifest) == EXT_OK);
  ext_buffer_free(&manifest);
  assert(mode_of(root, "example%2fe/src/main.py") == 0600);
  assert(ext_store_replace(store, s2, EXT_STORE_PRESERVE_EXECUTABLE, &manifest) == EXT_OK);
  ext_buffer_free(&manifest);
  assert(mode_of(root, "example%2fe/src/main.py") == 0700);
  assert(ext_store_remove(store, "example/e") == EXT_OK);
  ext_store_close(store);
#endif

  /* limits: an artifact, a tree's files and a tree's bytes are bounded */
  {
    ext_store_limits small = {2, 0, 0};
    assert(ext_store_open(root, &small, &store) == EXT_OK);
    make_package(s2, "example/f", "r1", "example.read", "three bytes", 0);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_INVALID);
    ext_store_close(store);
    ext_store_limits few = {0, 2, 0};
    assert(ext_store_open(root, &few, &store) == EXT_OK);
    make_package(s2, "example/f", "r1", "example.read", "ok", 1);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_INVALID); /* 3 files */
    ext_store_close(store);
    ext_store_limits light = {0, 0, 4};
    assert(ext_store_open(root, &light, &store) == EXT_OK);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_INVALID); /* bytes */
    ext_store_close(store);
    expect_entries(fs, 0);
    assert(ext_store_open(root, NULL, &store) == EXT_OK);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_OK);
    ext_buffer_free(&manifest);
    assert(ext_store_remove(store, "example/f") == EXT_OK);
    ext_store_close(store);
  }

  /* the root is something other than a directory */
  ext_fs_close(fs);
  assert(ext_fs_open(base, &fs) == EXT_OK);
  put(fs, "file-root", "x", 0);
  {
    char *r = join(base, "file-root");
    assert(ext_store_open(r, NULL, &store) == EXT_OK);
    assert(ext_store_ids(store, &ids) == EXT_INVALID);
    assert(ext_store_install(store, s2, 0, &manifest) == EXT_INVALID);
    assert(ext_store_load(store, "example/a", &loaded) == EXT_INVALID);
    ext_store_close(store);
    free(r);
  }
  assert(ext_fs_remove_all(fs, "file-root") == EXT_OK);
  assert(ext_fs_remove_all(fs, "store") == EXT_OK);
  assert(ext_fs_remove_all(fs, "s1") == EXT_OK);
  assert(ext_fs_remove_all(fs, "s2") == EXT_OK);
  assert(ext_fs_remove_all(fs, "s3") == EXT_OK);
  ext_fs_close(fs);
  REMOVE_DIR(base);
  free(root);
  free(s1);
  free(s2);
  free(s3);
  return 0;
}
