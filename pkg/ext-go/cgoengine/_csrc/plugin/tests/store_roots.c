/* The package store's whole-root reports and the layered view (store.c): list,
 * verify, list-unverified, and a read-only view over several roots in priority
 * order. The Go store is the baseline; the differential tests in
 * pkg/ext-go/cgoengine hold the two together. */
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
static void put(ext_fs *fs, const char *path, const char *body) {
  assert(ext_fs_write_file(fs, path, (const uint8_t *)body, strlen(body), 0) ==
         EXT_OK);
}
/* A package directory: src/main.py holding body and a manifest for id. */
static void make_package(const char *dir, const char *id, const char *revision,
                         const char *body) {
  assert(ext_fs_make_root(dir) == EXT_OK);
  ext_fs *fs = NULL;
  assert(ext_fs_open(dir, &fs) == EXT_OK);
  ext_fs_entry *old = NULL;
  size_t count = 0;
  assert(ext_fs_list(fs, "", &old, &count) == EXT_OK);
  for (size_t i = 0; i < count; i++)
    assert(ext_fs_remove_all(fs, old[i].name) == EXT_OK);
  ext_fs_list_free(old, count);
  put(fs, "src/main.py", body);
  uint8_t raw[32];
  char sum[65];
  assert(ext_engine_sha256((const uint8_t *)body, strlen(body), raw) == EXT_OK);
  hex(raw, sum);
  char manifest[2048];
  int n = snprintf(manifest, sizeof(manifest),
                   "{\"apiVersion\":\"ext.package/v1\",\"descriptor\":{"
                   "\"apiVersion\":\"ext.plugin/v1\","
                   "\"identity\":{\"id\":\"%s\",\"revision\":\"%s\"},"
                   "\"contracts\":[{\"name\":\"c\",\"version\":\"v1\","
                   "\"operations\":[{\"name\":\"read\"}]}]},"
                   "\"artifacts\":[{\"name\":\"main\",\"path\":\"src/main.py\","
                   "\"sha256\":\"%s\"}]}",
                   id, revision, sum);
  assert(n > 0 && (size_t)n < sizeof(manifest));
  put(fs, "plugin.json", manifest);
  ext_fs_close(fs);
}
static void install(ext_store *store, const char *scratch, const char *id,
                    const char *revision, const char *body) {
  char *src = join(scratch, "source");
  make_package(src, id, revision, body);
  ext_buffer m = {0};
  assert(ext_store_replace(store, src, 0, &m) == EXT_OK);
  ext_buffer_free(&m);
  free(src);
}
static void write_in(const char *root, const char *path, const char *body) {
  ext_fs *fs = NULL;
  assert(ext_fs_open(root, &fs) == EXT_OK);
  (void)ext_fs_remove_all(fs, path);
  put(fs, path, body);
  ext_fs_close(fs);
}
static void mkdir_in(const char *root, const char *path) {
  ext_fs *fs = NULL;
  assert(ext_fs_open(root, &fs) == EXT_OK);
  assert(ext_fs_mkdirs(fs, path) == EXT_OK);
  ext_fs_close(fs);
}

/* A report with each "directory" value cut out, because a directory is spelled
 * with the platform's separator. The values go to dirs, in order. */
#define MAX_DIRS 16
typedef struct {
  char text[8192];
  char dirs[MAX_DIRS][512];
  size_t count;
} flat;
static void flatten(const ext_buffer *report, flat *out) {
  static const char key[] = "\"directory\":\"";
  size_t w = 0;
  out->count = 0;
  for (size_t i = 0; i < report->len;) {
    if (i + sizeof(key) - 1 <= report->len &&
        !memcmp(report->data + i, key, sizeof(key) - 1)) {
      memcpy(out->text + w, key, sizeof(key) - 1);
      w += sizeof(key) - 1;
      i += sizeof(key) - 1;
      size_t d = 0;
      while (i < report->len && report->data[i] != '"') {
        if (report->data[i] == '\\')
          i++;
        assert(d + 1 < sizeof(out->dirs[0]) && out->count < MAX_DIRS);
        out->dirs[out->count][d++] = (char)report->data[i++];
      }
      out->dirs[out->count++][d] = 0;
      continue;
    }
    assert(w + 1 < sizeof(out->text));
    out->text[w++] = (char)report->data[i++];
  }
  out->text[w] = 0;
}
static int ends_with(const char *text, const char *suffix) {
  size_t a = strlen(text), b = strlen(suffix);
  return a >= b && !strcmp(text + a - b, suffix);
}
static void expect_report(const ext_buffer *report, const char *shape,
                          const char *const *names) {
  flat f;
  flatten(report, &f);
  if (strcmp(f.text, shape)) {
    fprintf(stderr, "report differs:\n got  %s\n want %s\n", f.text, shape);
    abort();
  }
  size_t n = 0;
  while (names && names[n])
    n++;
  assert(f.count == n);
  for (size_t i = 0; i < n; i++)
    if (!ends_with(f.dirs[i], names[i])) {
      fprintf(stderr, "directory %s does not end in %s\n", f.dirs[i], names[i]);
      abort();
    }
}
static void expect_buffer(ext_buffer *report, const char *shape,
                          const char *const *names) {
  expect_report(report, shape, names);
  ext_buffer_free(report);
}

#define MANIFEST_A1 "{\"apiVersion\":\"ext.package/v1\""
static int has(const ext_buffer *b, const char *text) {
  size_t n = strlen(text);
  for (size_t i = 0; i + n <= b->len; i++)
    if (!memcmp(b->data + i, text, n))
      return 1;
  return 0;
}

static void single_store(const char *scratch) {
  char *root = join(scratch, "single");
  ext_store *store = NULL;
  assert(ext_store_open(root, NULL, &store) == EXT_OK);
  ext_buffer out = {0};

  /* A root that is not there is an empty store. */
  assert(ext_store_list(store, &out) == EXT_OK);
  expect_buffer(&out, "[]", NULL);
  assert(ext_store_verify(store, &out) == EXT_OK);
  expect_buffer(&out, "[]", NULL);
  assert(ext_store_list_unverified(store, &out) == EXT_OK);
  expect_buffer(&out, "{\"packages\":[],\"problems\":[]}", NULL);

  install(store, scratch, "beta", "r1", "print('b')\n");
  install(store, scratch, "alpha/a", "r1", "print('a')\n");

  assert(ext_store_list(store, &out) == EXT_OK);
  assert(has(&out, "\"id\":\"alpha/a\"") && has(&out, "\"id\":\"beta\""));
  assert(has(&out, MANIFEST_A1));
  assert(out.data[0] == '[' && out.data[out.len - 1] == ']');
  {
    const char *dirs[] = {"alpha%2fa", "beta", NULL};
    flat f;
    flatten(&out, &f);
    assert(f.count == 2 && ends_with(f.dirs[0], dirs[0]) &&
           ends_with(f.dirs[1], dirs[1]));
    /* alpha/a sorts before beta. */
    assert(strstr(f.text, "alpha/a") < strstr(f.text, "\"id\":\"beta\""));
  }
  ext_buffer_free(&out);
  {
    const char *dirs[] = {"alpha%2fa", "beta", NULL};
    assert(ext_store_verify(store, &out) == EXT_OK);
    expect_buffer(&out,
                  "[{\"id\":\"alpha/a\",\"directory\":\"\",\"layer\":0,\"status\":0},"
                  "{\"id\":\"beta\",\"directory\":\"\",\"layer\":0,\"status\":0}]",
                  dirs);
  }

  /* Damage: a changed artifact fails verify and list, not the manifest read. */
  char *beta = join(root, "beta");
  write_in(beta, "src/main.py", "print('tampered')\n");
  char want[512];
  {
    const char *dirs[] = {"alpha%2fa", "beta", NULL};
    assert(ext_store_verify(store, &out) == EXT_OK);
    snprintf(want, sizeof(want),
             "[{\"id\":\"alpha/a\",\"directory\":\"\",\"layer\":0,\"status\":0},"
             "{\"id\":\"beta\",\"directory\":\"\",\"layer\":0,\"status\":%d}]",
             (int)EXT_MISMATCH);
    expect_buffer(&out, want, dirs);
  }
  assert(ext_store_list(store, &out) == EXT_MISMATCH && !out.data);
  assert(ext_store_list_unverified(store, &out) == EXT_OK);
  assert(has(&out, "\"packages\":[{") && has(&out, "\"id\":\"beta\"") &&
         has(&out, "\"problems\":[]"));
  ext_buffer_free(&out);

  /* Entries that are not packages are reported where they sit, and a staging
   * directory is skipped. */
  write_in(root, "stray.txt", "x");
  mkdir_in(root, "Not A Package");
  mkdir_in(root, ".install-leftover");
  mkdir_in(root, "gamma"); /* a directory whose manifest names another id */
  {
    char *src = join(scratch, "source");
    make_package(src, "delta", "r1", "print('d')\n");
    char *gamma = join(root, "gamma");
    ext_fs *fs = NULL;
    assert(ext_fs_open(src, &fs) == EXT_OK);
    ext_buffer manifest = {0}, body = {0};
    assert(ext_fs_read_file(fs, "plugin.json", 1 << 20, &manifest) == EXT_OK);
    assert(ext_fs_read_file(fs, "src/main.py", 1 << 20, &body) == EXT_OK);
    ext_fs_close(fs);
    char *m = malloc(manifest.len + 1), *b = malloc(body.len + 1);
    assert(m && b);
    memcpy(m, manifest.data, manifest.len);
    m[manifest.len] = 0;
    memcpy(b, body.data, body.len);
    b[body.len] = 0;
    write_in(gamma, "plugin.json", m);
    write_in(gamma, "src/main.py", b);
    free(m);
    free(b);
    ext_buffer_free(&manifest);
    ext_buffer_free(&body);
    free(gamma);
    free(src);
  }
  assert(ext_store_ids(store, &out) == EXT_INVALID);
  assert(ext_store_list(store, &out) == EXT_INVALID && !out.data);
  assert(ext_store_verify(store, &out) == EXT_OK);
  assert(has(&out, "\"id\":\"Not A Package\"") && has(&out, "\"id\":\"stray.txt\"") &&
         has(&out, "\"id\":\"gamma\""));
  assert(!has(&out, "install-leftover"));
  {
    flat f;
    flatten(&out, &f);
    char pair[128];
    snprintf(pair, sizeof(pair), "\"id\":\"gamma\",\"directory\":\"\",\"layer\":0,\"status\":%d",
             (int)EXT_MISMATCH);
    assert(strstr(f.text, pair));
    snprintf(pair, sizeof(pair), "\"id\":\"stray.txt\",\"directory\":\"\",\"layer\":0,\"status\":%d",
             (int)EXT_INVALID);
    assert(strstr(f.text, pair));
  }
  ext_buffer_free(&out);
  assert(ext_store_list_unverified(store, &out) == EXT_OK);
  assert(has(&out, "\"problems\":[{") && has(&out, "\"id\":\"gamma\""));
  /* alpha/a and beta still read: the content is never looked at. */
  assert(has(&out, "\"id\":\"alpha/a\"") && has(&out, "\"id\":\"beta\""));
  ext_buffer_free(&out);

  /* A closed argument list. */
  assert(ext_store_list(NULL, &out) == EXT_INVALID);
  assert(ext_store_verify(store, NULL) == EXT_INVALID);
  assert(ext_store_list_unverified(NULL, &out) == EXT_INVALID);

  free(beta);
  ext_store_close(store);
  free(root);
}

static void layered(const char *scratch) {
  char *user = join(scratch, "user"), *system = join(scratch, "system");
  char *absent = join(scratch, "absent");
  ext_store *u = NULL, *s = NULL;
  assert(ext_store_open(user, NULL, &u) == EXT_OK);
  assert(ext_store_open(system, NULL, &s) == EXT_OK);
  install(s, scratch, "a", "sys", "print('a')\n");
  install(s, scratch, "b", "sys", "print('b-system')\n");
  install(u, scratch, "b", "usr", "print('b-user')\n");
  install(u, scratch, "c", "usr", "print('c')\n");

  const char *roots[] = {user, system};
  ext_layered *view = NULL;
  assert(ext_layered_open(roots, 2, NULL, &view) == EXT_OK);

  ext_buffer m = {0};
  size_t layer = 99;
  assert(ext_layered_load(view, "b", &m, &layer) == EXT_OK && layer == 0);
  assert(has(&m, "\"revision\":\"usr\""));
  ext_buffer_free(&m);
  assert(ext_layered_load(view, "a", &m, &layer) == EXT_OK && layer == 1);
  ext_buffer_free(&m);
  assert(ext_layered_load(view, "a", &m, NULL) == EXT_OK);
  ext_buffer_free(&m);
  assert(ext_layered_load(view, "zzz", &m, &layer) == EXT_NOT_FOUND);
  assert(ext_layered_load(view, "Not Valid", &m, &layer) == EXT_INVALID);

  ext_buffer out = {0};
  assert(ext_layered_list(view, &out) == EXT_OK);
  {
    flat f;
    flatten(&out, &f);
    assert(f.count == 3);
    assert(ends_with(f.dirs[0], "system/a") || ends_with(f.dirs[0], "system\\a"));
    assert(ends_with(f.dirs[1], "user/b") || ends_with(f.dirs[1], "user\\b"));
    assert(ends_with(f.dirs[2], "user/c") || ends_with(f.dirs[2], "user\\c"));
    assert(strstr(f.text, "\"id\":\"a\",\"directory\":\"\",\"layer\":1"));
    assert(strstr(f.text, "\"id\":\"b\",\"directory\":\"\",\"layer\":0"));
    assert(!has(&out, "b-system"));
  }
  ext_buffer_free(&out);
  assert(ext_layered_verify(view, &out) == EXT_OK);
  {
    const char *dirs[] = {"a", "b", "c", NULL};
    flat f;
    flatten(&out, &f);
    assert(f.count == 3); /* the shadowed system copy of b is not checked */
    (void)dirs;
    assert(strstr(f.text, "\"id\":\"a\",\"directory\":\"\",\"layer\":1,\"status\":0"));
    assert(strstr(f.text, "\"id\":\"b\",\"directory\":\"\",\"layer\":0,\"status\":0"));
  }
  ext_buffer_free(&out);

  /* The winning copy is damaged: it fails closed and is not replaced by the
   * system copy; the shadowed copy is never read. */
  char *user_b = join(user, "b");
  write_in(user_b, "src/main.py", "print('tampered')\n");
  assert(ext_layered_load(view, "b", &m, &layer) == EXT_MISMATCH && !m.data);
  assert(ext_layered_list(view, &out) == EXT_MISMATCH && !out.data);
  assert(ext_layered_verify(view, &out) == EXT_OK);
  {
    flat f;
    flatten(&out, &f);
    char pair[128];
    snprintf(pair, sizeof(pair), "\"id\":\"b\",\"directory\":\"\",\"layer\":0,\"status\":%d",
             (int)EXT_MISMATCH);
    assert(strstr(f.text, pair) && f.count == 3);
  }
  ext_buffer_free(&out);
  /* Damage the shadowed copy too: nothing changes for verify. */
  char *system_b = join(system, "b");
  write_in(system_b, "src/main.py", "print('also tampered')\n");
  assert(ext_layered_verify(view, &out) == EXT_OK);
  {
    flat f;
    flatten(&out, &f);
    assert(f.count == 3);
  }
  ext_buffer_free(&out);
  /* An unreadable manifest in the winner is a problem, not a fallback. */
  write_in(user_b, "plugin.json", "{");
  assert(ext_layered_list_unverified(view, &out) == EXT_OK);
  assert(has(&out, "\"problems\":[{\"id\":\"b\"") &&
         has(&out, "\"id\":\"a\"") && has(&out, "\"id\":\"c\""));
  assert(!has(&out, "also tampered") && !has(&out, "b-system"));
  ext_buffer_free(&out);

  /* An entry that is not a package in a lower root fails the list there. */
  write_in(system, "junk", "x");
  assert(ext_layered_list(view, &out) == EXT_INVALID && !out.data);
  assert(ext_layered_verify(view, &out) == EXT_OK);
  assert(has(&out, "\"id\":\"junk\",") && has(&out, "\"layer\":1"));
  ext_buffer_free(&out);
  ext_layered_close(view);

  /* A root that is not there is empty, and a view over only such roots is
   * empty. */
  const char *with_absent[] = {absent, system};
  assert(ext_layered_open(with_absent, 2, NULL, &view) == EXT_OK);
  assert(ext_layered_load(view, "a", &m, &layer) == EXT_OK && layer == 1);
  ext_buffer_free(&m);
  ext_layered_close(view);
  const char *only_absent[] = {absent};
  assert(ext_layered_open(only_absent, 1, NULL, &view) == EXT_OK);
  assert(ext_layered_list(view, &out) == EXT_OK);
  expect_buffer(&out, "[]", NULL);
  assert(ext_layered_load(view, "a", &m, &layer) == EXT_NOT_FOUND);
  ext_layered_close(view);

  /* What a view refuses to open over. */
  char *dotted = join(user, "..");
  char *same_user = join(dotted, "user");
  const char *repeated[] = {user, same_user};
  view = NULL;
  assert(ext_layered_open(repeated, 2, NULL, &view) == EXT_INVALID && !view);
  const char *twice[] = {user, user};
  assert(ext_layered_open(twice, 2, NULL, &view) == EXT_INVALID);
  const char *empty[] = {user, ""};
  assert(ext_layered_open(empty, 2, NULL, &view) == EXT_INVALID);
  const char *missing[] = {user, NULL};
  assert(ext_layered_open(missing, 2, NULL, &view) == EXT_INVALID);
  assert(ext_layered_open(roots, 0, NULL, &view) == EXT_INVALID);
  assert(ext_layered_open(NULL, 1, NULL, &view) == EXT_INVALID);
  assert(ext_layered_open(roots, 2, NULL, NULL) == EXT_INVALID);
  assert(ext_layered_list(NULL, &out) == EXT_INVALID);
  assert(ext_layered_load(NULL, "a", &m, NULL) == EXT_INVALID);
  ext_layered_close(NULL);

  free(same_user);
  free(dotted);
  free(system_b);
  free(user_b);
  free(absent);
  free(system);
  free(user);
  ext_store_close(s);
  ext_store_close(u);
}

int main(int argc, char **argv) {
  if (argc != 2) {
    fprintf(stderr, "usage: %s scratch-directory\n", argv[0]);
    return 2;
  }
  char *single = join(argv[1], "one"), *many = join(argv[1], "two");
  assert(ext_fs_make_root(single) == EXT_OK);
  assert(ext_fs_make_root(many) == EXT_OK);
  single_store(single);
  layered(many);
  free(single);
  free(many);
  puts("store roots ok");
  return 0;
}
