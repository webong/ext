/* The read side of the filesystem layer (fs_posix.c, fs_win32.c): hashing and
 * reading a file and walking a tree below a root without following a link, and
 * seeing what a directory held when the walk reached it, not what a path leads
 * to later. The write side has its own test (fs_write.c). */
#include "ext_host.h"
#include "fs.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifndef _WIN32
#include <unistd.h>
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
static char *copy_of(const char *s) {
  size_t n = strlen(s) + 1;
  char *copy = malloc(n);
  assert(copy);
  memcpy(copy, s, n);
  return copy;
}
static void put(ext_fs *fs, const char *path, const char *body) {
  assert(ext_fs_write_file(fs, path, (const uint8_t *)body, strlen(body), 0) ==
         EXT_OK);
}
static void sha(const char *body, uint8_t out[32]) {
  assert(ext_engine_sha256((const uint8_t *)body, strlen(body), out) == EXT_OK);
}

/* What a walk visited, in order. */
typedef struct {
  char **paths;
  uint8_t *sums;
  size_t count, cap;
} seen;
static ext_status record(void *user, const char *path, const uint8_t sum[32]) {
  seen *s = user;
  if (s->count == s->cap) {
    s->cap = s->cap ? s->cap * 2 : 64;
    s->paths = realloc(s->paths, s->cap * sizeof(*s->paths));
    s->sums = realloc(s->sums, s->cap * 32);
    assert(s->paths && s->sums);
  }
  s->paths[s->count] = copy_of(path);
  memcpy(s->sums + 32 * s->count, sum, 32);
  s->count++;
  return EXT_OK;
}
static void forget(seen *s) {
  for (size_t i = 0; i < s->count; i++)
    free(s->paths[i]);
  free(s->paths);
  free(s->sums);
  memset(s, 0, sizeof(*s));
}
static long find(const seen *s, const char *path) {
  for (size_t i = 0; i < s->count; i++)
    if (!strcmp(s->paths[i], path))
      return (long)i;
  return -1;
}
static void expect_visited(const seen *s, const char *path, const char *body) {
  long i = find(s, path);
  assert(i >= 0);
  uint8_t want[32];
  sha(body, want);
  assert(!memcmp(s->sums + 32 * (size_t)i, want, 32));
}
static void expect_content(ext_fs *fs, const char *path, const char *want) {
  ext_buffer got = {0};
  assert(ext_fs_read_file(fs, path, 1 << 20, &got) == EXT_OK);
  assert(got.len == strlen(want) && !memcmp(got.data, want, got.len));
  ext_buffer_free(&got);
}
static ext_status refuse(void *user, const char *path, const uint8_t sum[32]) {
  (void)user, (void)path, (void)sum;
  return EXT_DENIED;
}

/* A directory link: a symbolic link, or on Windows a junction, which needs no
 * privilege to make. */
#ifdef _WIN32
static void backslashes(char *s) {
  for (; *s; s++)
    if (*s == '/')
      *s = '\\';
}
#endif
static void make_dir_link(const char *link, const char *target) {
#ifdef _WIN32
  char *l = copy_of(link), *t = copy_of(target);
  backslashes(l);
  backslashes(t);
  char command[2048];
  assert(snprintf(command, sizeof(command), "mklink /J \"%s\" \"%s\" >NUL", l, t) <
         (int)sizeof(command));
  assert(system(command) == 0);
  free(l);
  free(t);
#else
  assert(!symlink(target, link));
#endif
}

/* The walk of a tree being swapped while it is under way. */
typedef struct {
  seen found;
  const char *moved_from, *moved_to, *link, *target;
  int swapped;
} swap;
static ext_status swap_on_first_file(void *user, const char *path,
                                     const uint8_t sum[32]) {
  swap *w = user;
  ext_status s = record(&w->found, path, sum);
  if (!w->swapped && !strcmp(path, "a.txt")) {
    /* the directory "d" is already listed; make it a link to somewhere else */
    assert(!rename(w->moved_from, w->moved_to));
    make_dir_link(w->link, w->target);
    w->swapped = 1;
  }
  return s;
}

int main(int argc, char **argv) {
  assert(argc == 2);
  const char *base = argv[1];
  char *root = join(base, "root");
  assert(ext_fs_make_root(root) == EXT_OK);
  ext_fs *fs = NULL;
  assert(ext_fs_open(root, &fs) == EXT_OK);
  put(fs, "a.txt", "alpha");
  put(fs, "d/b.txt", "beta");
  put(fs, "d/e/c.txt", "gamma");
  put(fs, "empty", "");
  assert(ext_fs_mkdirs(fs, "z") == EXT_OK);

  /* hash_file */
  uint8_t sum[32], want[32];
  assert(ext_fs_hash_file(fs, "a.txt", sum) == EXT_OK);
  sha("alpha", want);
  assert(!memcmp(sum, want, 32));
  assert(ext_fs_hash_file(fs, "d/e/c.txt", sum) == EXT_OK);
  sha("gamma", want);
  assert(!memcmp(sum, want, 32));
  assert(ext_fs_hash_file(fs, "empty", sum) == EXT_OK);
  sha("", want);
  assert(!memcmp(sum, want, 32));
  assert(ext_fs_hash_file(fs, "nope.txt", sum) == EXT_MISSING);
  assert(ext_fs_hash_file(fs, "nope/x.txt", sum) == EXT_MISSING);
  assert(ext_fs_hash_file(fs, "d/nope.txt", sum) == EXT_MISSING);
  assert(ext_fs_hash_file(fs, "d", sum) == EXT_INVALID);       /* a directory */
  assert(ext_fs_hash_file(fs, "a.txt/x", sum) == EXT_INVALID); /* below a file */
  assert(ext_fs_hash_file(fs, "d/b.txt/x", sum) == EXT_INVALID);

  /* read_file: the bytes, the limit, and what is not a file */
  expect_content(fs, "a.txt", "alpha");
  expect_content(fs, "d/e/c.txt", "gamma");
  expect_content(fs, "empty", "");
  {
    ext_buffer got = {0};
    assert(ext_fs_read_file(fs, "a.txt", 5, &got) == EXT_OK && got.len == 5);
    ext_buffer_free(&got);
    assert(ext_fs_read_file(fs, "a.txt", 4, &got) == EXT_INVALID);
    assert(ext_fs_read_file(fs, "nope.txt", 100, &got) == EXT_MISSING);
    assert(ext_fs_read_file(fs, "d", 100, &got) == EXT_INVALID);
    assert(ext_fs_read_file(fs, "a.txt/x", 100, &got) == EXT_INVALID);
  }

  /* walk: every file, with its digest and its slash-separated path */
  {
    seen all = {0};
    assert(ext_fs_walk(fs, "", record, &all) == EXT_OK);
    assert(all.count == 4); /* the empty directory holds none */
    expect_visited(&all, "a.txt", "alpha");
    expect_visited(&all, "d/b.txt", "beta");
    expect_visited(&all, "d/e/c.txt", "gamma");
    expect_visited(&all, "empty", "");
    forget(&all);
    seen below = {0};
    assert(ext_fs_walk(fs, "d", record, &below) == EXT_OK);
    assert(below.count == 2); /* paths are relative to the directory walked */
    expect_visited(&below, "b.txt", "beta");
    expect_visited(&below, "e/c.txt", "gamma");
    forget(&below);
    seen none = {0};
    assert(ext_fs_walk(fs, "z", record, &none) == EXT_OK && none.count == 0);
    assert(ext_fs_walk(fs, "a.txt", record, &none) == EXT_INVALID);
    assert(ext_fs_walk(fs, "a.txt/x", record, &none) == EXT_INVALID);
    assert(ext_fs_walk(fs, "nope", record, &none) == EXT_MISSING);
    assert(none.count == 0);
    /* a visitor's status stops the walk and comes back as it was */
    assert(ext_fs_walk(fs, "", refuse, NULL) == EXT_DENIED);
  }

#ifdef _WIN32
  /* Windows names are not case sensitive, and a colon names a stream and a
   * backslash another directory: neither is a name below the root. */
  assert(ext_fs_hash_file(fs, "A.TXT", sum) == EXT_OK);
  sha("alpha", want);
  assert(!memcmp(sum, want, 32));
  assert(ext_fs_hash_file(fs, "D/E/C.TXT", sum) == EXT_OK);
  assert(ext_fs_hash_file(fs, "a.txt:stream", sum) == EXT_INVALID);
  assert(ext_fs_hash_file(fs, "d\\b.txt", sum) == EXT_INVALID);
  assert(ext_fs_hash_file(fs, "a.txt::$DATA", sum) == EXT_INVALID);
#endif

  /* a directory larger than one read of the directory */
  {
    char *bulk = join(base, "bulk");
    assert(ext_fs_make_root(bulk) == EXT_OK);
    ext_fs *big = NULL;
    assert(ext_fs_open(bulk, &big) == EXT_OK);
    enum { FILES = 1500 };
    for (int i = 0; i < FILES; i++) {
      char name[64];
      assert(snprintf(name, sizeof(name), "many/file-with-a-longer-name-%04d.txt",
                      i) > 0);
      put(big, name, "x");
    }
    seen walked = {0};
    assert(ext_fs_walk(big, "many", record, &walked) == EXT_OK);
    assert(walked.count == FILES);
    for (int i = 0; i < FILES; i++) {
      char name[64];
      assert(snprintf(name, sizeof(name), "file-with-a-longer-name-%04d.txt", i) > 0);
      assert(find(&walked, name) >= 0);
    }
    forget(&walked);
    ext_fs_close(big);
    free(bulk);
  }

  /* links are refused wherever they are met, and never followed */
  {
    char *outside = join(base, "outside");
    assert(ext_fs_make_root(outside) == EXT_OK);
    ext_fs *out = NULL;
    assert(ext_fs_open(outside, &out) == EXT_OK);
    put(out, "secret.txt", "keep");
    put(out, "deeper/more.txt", "more");
    char *link = join(root, "lnk");
    make_dir_link(link, outside);
    seen none = {0};
    assert(ext_fs_hash_file(fs, "lnk/secret.txt", sum) == EXT_INVALID);
    assert(ext_fs_hash_file(fs, "lnk", sum) == EXT_INVALID);
    ext_buffer got = {0};
    assert(ext_fs_read_file(fs, "lnk/secret.txt", 100, &got) == EXT_INVALID);
    assert(ext_fs_walk(fs, "lnk", record, &none) == EXT_INVALID);
    assert(ext_fs_walk(fs, "lnk/deeper", record, &none) == EXT_INVALID);
    assert(ext_fs_walk(fs, "", record, &none) == EXT_INVALID); /* inside the tree */
    forget(&none);
    {
      /* the link as the root */
      char *as_root = join(base, "root-link");
      make_dir_link(as_root, root);
      ext_fs *refused = NULL;
      assert(ext_fs_open(as_root, &refused) == EXT_INVALID);
      free(as_root);
    }
#ifndef _WIN32
    char *file_link = join(root, "flnk");
    char *secret = join(outside, "secret.txt");
    assert(!symlink(secret, file_link));
    assert(ext_fs_hash_file(fs, "flnk", sum) == EXT_INVALID);
    assert(ext_fs_read_file(fs, "flnk", 100, &got) == EXT_INVALID);
    assert(ext_fs_remove_all(fs, "flnk") == EXT_OK);
    free(file_link);
    free(secret);
#endif
    /* once the link is gone the tree reads again */
    assert(ext_fs_remove_all(fs, "lnk") == EXT_OK);
    seen all = {0};
    assert(ext_fs_walk(fs, "", record, &all) == EXT_OK && all.count == 4);
    forget(&all);
    expect_content(out, "secret.txt", "keep"); /* what it pointed at is intact */
    free(link);
    ext_fs_close(out);
    free(outside);
  }

  /* A directory listed and then swapped for a link before the walk reaches it
   * is not entered: the walk opens each entry from the directory it listed, and
   * what is behind the link is never read. */
  {
    char *outside = join(base, "elsewhere");
    assert(ext_fs_make_root(outside) == EXT_OK);
    ext_fs *out = NULL;
    assert(ext_fs_open(outside, &out) == EXT_OK);
    put(out, "evil.txt", "not yours");
    put(fs, "swap/a.txt", "first");
    put(fs, "swap/d/inner.txt", "inner");
    char *from = join(root, "swap/d"), *to = join(root, "swap/d.moved");
    char *at = join(root, "swap/d");
    swap w = {{0}, from, to, at, outside, 0};
    ext_status s = ext_fs_walk(fs, "swap", swap_on_first_file, &w);
    assert(find(&w.found, "evil.txt") < 0 && find(&w.found, "d/evil.txt") < 0);
    if (find(&w.found, "d/inner.txt") >= 0) {
      assert(s == EXT_OK); /* "d" came first and was read before anything changed */
    } else {
      assert(w.swapped); /* otherwise "d" was reached after the swap */
#ifdef _WIN32
      assert(s == EXT_INVALID); /* it was listed already, so it is opened and refused */
#else
      assert(s == EXT_INVALID || s == EXT_OK); /* the listing may skip a changed name */
#endif
    }
    forget(&w.found);
    free(from);
    free(to);
    free(at);
    ext_fs_close(out);
    free(outside);
  }

  ext_fs_close(fs);
  free(root);
  puts("fs read ok");
  return 0;
}
