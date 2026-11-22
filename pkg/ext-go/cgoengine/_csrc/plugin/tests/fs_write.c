/* The write side of the filesystem layer (fs_write_*.c): what the package store
 * builds installs, upgrades and removals from. */
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
static ext_fs_kind kind(ext_fs *fs, const char *path) {
  ext_fs_kind k = EXT_FS_OTHER;
  assert(ext_fs_kind_of(fs, path, &k) == EXT_OK);
  return k;
}
static void put(ext_fs *fs, const char *path, const char *body) {
  assert(ext_fs_write_file(fs, path, (const uint8_t *)body, strlen(body), 0) ==
         EXT_OK);
}
static void expect_content(ext_fs *fs, const char *path, const char *want) {
  ext_buffer got = {0};
  assert(ext_fs_read_file(fs, path, 1 << 20, &got) == EXT_OK);
  assert(got.len == strlen(want) && !memcmp(got.data, want, got.len));
  ext_buffer_free(&got);
}
static int has(ext_fs_entry *entries, size_t n, const char *name,
               ext_fs_kind k) {
  for (size_t i = 0; i < n; i++)
    if (!strcmp(entries[i].name, name))
      return entries[i].kind == k;
  return 0;
}

int main(int argc, char **argv) {
  assert(argc == 2);
  const char *base = argv[1];
  char *root = join(base, "store");
  /* make_root creates the missing parents, and again is fine */
  assert(ext_fs_make_root(root) == EXT_OK);
  assert(ext_fs_make_root(root) == EXT_OK);
  assert(ext_fs_make_root("") == EXT_INVALID);
  ext_fs *fs = NULL;
  assert(ext_fs_open(root, &fs) == EXT_OK);

  /* paths: relative, no empty, "." or ".." components */
  const char *bad[] = {"", "/abs", "a//b", "a/./b", "../x", "a/..", "a/", "./a"};
  for (size_t i = 0; i < sizeof(bad) / sizeof(*bad); i++) {
    ext_fs_kind k;
    uint8_t sum[32];
    uint64_t size;
    assert(ext_fs_kind_of(fs, bad[i], &k) == EXT_INVALID);
    assert(ext_fs_mkdirs(fs, bad[i]) == EXT_INVALID);
    assert(ext_fs_write_file(fs, bad[i], (const uint8_t *)"x", 1, 0) ==
           EXT_INVALID);
    assert(ext_fs_remove_all(fs, bad[i]) == EXT_INVALID);
    assert(ext_fs_rename(fs, bad[i], "ok") == EXT_INVALID);
    assert(ext_fs_copy_file(fs, bad[i], fs, "ok", 0, 10, sum, &size) ==
           EXT_INVALID);
  }

  /* kinds, mkdirs and write_file */
  assert(kind(fs, "a") == EXT_FS_NONE);
  assert(kind(fs, "a/b/c") == EXT_FS_NONE); /* a missing parent is none */
  assert(ext_fs_mkdirs(fs, "a/b/c") == EXT_OK);
  assert(ext_fs_mkdirs(fs, "a/b/c") == EXT_OK);
  assert(kind(fs, "a") == EXT_FS_DIR && kind(fs, "a/b/c") == EXT_FS_DIR);
  put(fs, "a/b/f.txt", "hello");
  assert(kind(fs, "a/b/f.txt") == EXT_FS_FILE);
  assert(ext_fs_write_file(fs, "a/b/f.txt", (const uint8_t *)"x", 1, 0) ==
         EXT_INVALID); /* never replaces */
  assert(ext_fs_mkdirs(fs, "a/b/f.txt") == EXT_INVALID);
  assert(ext_fs_mkdirs(fs, "a/b/f.txt/deeper") == EXT_INVALID);
  assert(ext_fs_kind_of(fs, "a/b/f.txt/deeper", &(ext_fs_kind){0}) ==
         EXT_INVALID); /* a parent that is a file */
  put(fs, "n/new/dirs/g.txt", "made parents"); /* write creates its parents */
  expect_content(fs, "n/new/dirs/g.txt", "made parents");

  /* copy_file: bytes, digest, size, limit, never over an existing file */
  uint8_t sum[32], want[32];
  uint64_t size = 0;
  char digest[65], expected[65];
  assert(ext_engine_sha256((const uint8_t *)"hello", 5, want) == EXT_OK);
  hex(want, expected);
  assert(ext_fs_copy_file(fs, "a/b/f.txt", fs, "copy/dir/h.txt", 0, 5, sum,
                          &size) == EXT_OK);
  hex(sum, digest);
  assert(size == 5 && !strcmp(digest, expected));
  expect_content(fs, "copy/dir/h.txt", "hello");
  assert(ext_fs_copy_file(fs, "a/b/f.txt", fs, "copy/dir/h.txt", 0, 5, sum,
                          &size) == EXT_INVALID); /* exists */
  assert(ext_fs_copy_file(fs, "a/b/f.txt", fs, "copy/dir/short.txt", 0, 4, sum,
                          &size) == EXT_INVALID); /* longer than the limit */
  assert(ext_fs_copy_file(fs, "a/b/f.txt", fs, "copy/dir/zero.txt", 0, 0, sum,
                          &size) == EXT_INVALID);
  put(fs, "empty", "");
  assert(ext_fs_copy_file(fs, "empty", fs, "copy/dir/empty.txt", 0, 0, sum,
                          &size) == EXT_OK && size == 0);
  assert(ext_fs_copy_file(fs, "a/b", fs, "copy/dir/d", 0, 10, sum, &size) ==
         EXT_INVALID); /* a directory */
  assert(ext_fs_copy_file(fs, "a/b/missing", fs, "copy/dir/m", 0, 10, sum,
                          &size) == EXT_NOT_FOUND);
  assert(ext_fs_copy_file(fs, "nope/x", fs, "copy/dir/m", 0, 10, sum, &size) ==
         EXT_NOT_FOUND);
  /* a large file crosses the copy buffer */
  {
    size_t big = 200000;
    uint8_t *data = malloc(big);
    assert(data);
    for (size_t i = 0; i < big; i++)
      data[i] = (uint8_t)(i * 7 + 3);
    assert(ext_fs_write_file(fs, "big.bin", data, big, 0) == EXT_OK);
    assert(ext_engine_sha256(data, big, want) == EXT_OK);
    assert(ext_fs_copy_file(fs, "big.bin", fs, "copy/big.bin", 0, big, sum,
                            &size) == EXT_OK);
    assert(size == big && !memcmp(sum, want, 32));
    assert(ext_fs_copy_file(fs, "big.bin", fs, "copy/big2.bin", 0, big - 1, sum,
                            &size) == EXT_INVALID);
    free(data);
  }

#ifndef _WIN32
  /* owner-only modes, and the executable bit survives any umask */
  {
    umask(0777);
    assert(ext_fs_copy_file(fs, "a/b/f.txt", fs, "copy/x.bin", 1, 5, sum,
                            &size) == EXT_OK);
    assert(ext_fs_copy_file(fs, "a/b/f.txt", fs, "copy/y.txt", 0, 5, sum,
                            &size) == EXT_OK);
    assert(ext_fs_mkdirs(fs, "restricted/dir") == EXT_OK);
    umask(022);
    struct stat st;
    char *p = join(root, "copy/x.bin");
    assert(!stat(p, &st) && (st.st_mode & 0777) == 0700);
    free(p);
    p = join(root, "copy/y.txt");
    assert(!stat(p, &st) && (st.st_mode & 0777) == 0600);
    free(p);
    p = join(root, "restricted/dir");
    assert(!stat(p, &st) && (st.st_mode & 0777) == 0700);
    free(p);
  }
  /* links are refused at every component, and never followed */
  {
    char *outside = join(base, "outside");
    char *secret = join(outside, "secret.txt");
    assert(!mkdir(outside, 0700));
    FILE *f = fopen(secret, "wb");
    assert(f && fwrite("keep", 1, 4, f) == 4 && !fclose(f));
    char *link = join(root, "lnk");
    assert(!symlink(outside, link));
    char *filelink = join(root, "flnk");
    assert(!symlink(secret, filelink));
    assert(kind(fs, "lnk") == EXT_FS_OTHER);
    assert(kind(fs, "flnk") == EXT_FS_OTHER);
    assert(ext_fs_kind_of(fs, "lnk/secret.txt", &(ext_fs_kind){0}) ==
           EXT_INVALID);
    assert(ext_fs_copy_file(fs, "lnk/secret.txt", fs, "stolen", 0, 10, sum,
                            &size) == EXT_INVALID);
    assert(ext_fs_copy_file(fs, "flnk", fs, "stolen", 0, 10, sum, &size) ==
           EXT_INVALID);
    assert(ext_fs_write_file(fs, "lnk/new.txt", (const uint8_t *)"x", 1, 0) ==
           EXT_INVALID);
    assert(ext_fs_mkdirs(fs, "lnk/dir") == EXT_INVALID);
    assert(ext_fs_remove_all(fs, "lnk/secret.txt") == EXT_INVALID);
    assert(kind(fs, "stolen") == EXT_FS_NONE);
    /* removing a link removes the link, not what it points at */
    assert(ext_fs_remove_all(fs, "lnk") == EXT_OK);
    assert(ext_fs_remove_all(fs, "flnk") == EXT_OK);
    assert(kind(fs, "lnk") == EXT_FS_NONE && kind(fs, "flnk") == EXT_FS_NONE);
    struct stat st;
    assert(!stat(secret, &st));
    /* a link inside a tree that is removed is unlinked, not followed */
    assert(ext_fs_mkdirs(fs, "tree/inner") == EXT_OK);
    put(fs, "tree/inner/file", "x");
    char *inner = join(root, "tree/inner/out");
    assert(!symlink(outside, inner));
    assert(ext_fs_remove_all(fs, "tree") == EXT_OK);
    assert(kind(fs, "tree") == EXT_FS_NONE);
    assert(!stat(secret, &st));
    free(inner);
    assert(!remove(secret));
    assert(!REMOVE_DIR(outside));
    free(filelink);
    free(link);
    free(secret);
    free(outside);
  }
#endif

  /* list: names and kinds, the root and a subdirectory */
  {
    ext_fs_entry *entries = NULL;
    size_t n = 0;
    assert(ext_fs_list(fs, "a/b", &entries, &n) == EXT_OK);
    assert(n == 2 && has(entries, n, "c", EXT_FS_DIR) &&
           has(entries, n, "f.txt", EXT_FS_FILE));
    ext_fs_list_free(entries, n);
    assert(ext_fs_list(fs, "", &entries, &n) == EXT_OK);
    assert(has(entries, n, "a", EXT_FS_DIR) &&
           has(entries, n, "big.bin", EXT_FS_FILE) &&
           has(entries, n, "empty", EXT_FS_FILE));
    ext_fs_list_free(entries, n);
    assert(ext_fs_list(fs, "a/b/c", &entries, &n) == EXT_OK && n == 0 &&
           !entries);
    assert(ext_fs_list(fs, "a/b/f.txt", &entries, &n) == EXT_INVALID);
    assert(ext_fs_list(fs, "missing", &entries, &n) == EXT_NOT_FOUND);
  }

  /* mkdtemp: unique, under the prefix, created empty */
  {
    char *one = NULL, *two = NULL, *nested = NULL;
    assert(ext_fs_mkdtemp(fs, "", ".install-", &one) == EXT_OK);
    assert(ext_fs_mkdtemp(fs, "", ".install-", &two) == EXT_OK);
    assert(!strncmp(one, ".install-", 9) && strcmp(one, two));
    assert(kind(fs, one) == EXT_FS_DIR && kind(fs, two) == EXT_FS_DIR);
    ext_fs_entry *entries = NULL;
    size_t n = 0;
    assert(ext_fs_list(fs, one, &entries, &n) == EXT_OK && n == 0);
    assert(ext_fs_mkdtemp(fs, "tmp/work", ".backup-", &nested) == EXT_OK);
    assert(kind(fs, "tmp/work") == EXT_FS_DIR);
    assert(ext_fs_mkdtemp(fs, "", "bad/prefix", &(char *){0}) == EXT_INVALID);
    assert(ext_fs_remove_all(fs, one) == EXT_OK);
    assert(ext_fs_remove_all(fs, two) == EXT_OK);
    assert(kind(fs, one) == EXT_FS_NONE);
    free(one);
    free(two);
    free(nested);
  }

  /* rename: a directory moves whole; an occupied target is refused */
  assert(ext_fs_rename(fs, "copy", "moved") == EXT_OK);
  assert(kind(fs, "copy") == EXT_FS_NONE && kind(fs, "moved") == EXT_FS_DIR);
  expect_content(fs, "moved/dir/h.txt", "hello");
  assert(ext_fs_rename(fs, "a", "moved") == EXT_IO); /* not empty */
  assert(kind(fs, "a") == EXT_FS_DIR);
  assert(ext_fs_rename(fs, "no-such", "elsewhere") == EXT_IO);
  assert(ext_fs_mkdirs(fs, "dest/sub") == EXT_OK);
  assert(ext_fs_rename(fs, "moved/dir/h.txt", "dest/sub/h.txt") == EXT_OK);
  assert(ext_fs_rename(fs, "moved/dir/h.txt", "dest/sub/zz") == EXT_IO);

  /* remove_all: trees, a single file, something already gone */
  assert(ext_fs_remove_all(fs, "moved") == EXT_OK);
  assert(ext_fs_remove_all(fs, "moved") == EXT_OK);
  assert(ext_fs_remove_all(fs, "never/was/here") == EXT_OK);
  assert(ext_fs_remove_all(fs, "big.bin") == EXT_OK);
  assert(kind(fs, "moved") == EXT_FS_NONE && kind(fs, "big.bin") == EXT_FS_NONE);
  assert(ext_fs_remove_all(fs, "a") == EXT_OK);
  assert(ext_fs_remove_all(fs, "n") == EXT_OK);
  assert(ext_fs_remove_all(fs, "dest") == EXT_OK);
  assert(ext_fs_remove_all(fs, "empty") == EXT_OK);
  assert(ext_fs_remove_all(fs, "restricted") == EXT_OK);
  assert(ext_fs_remove_all(fs, "tmp") == EXT_OK);
  {
    ext_fs_entry *entries = NULL;
    size_t n = 7;
    assert(ext_fs_list(fs, "", &entries, &n) == EXT_OK);
    assert(n == 0);
  }

  ext_fs_close(fs);
  assert(!REMOVE_DIR(root));
  REMOVE_DIR(base); /* made by ext_fs_make_root */
  free(root);
  return 0;
}
