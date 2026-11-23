/* Install from a pinned archive (ext_plugin_store_install_archive, archive.c) and the
 * receipts it leaves, held to the fixtures every store runs:
 * testdata/v1/archive-cases.json and the archives beside it. The Go store runs
 * the same file (TestArchiveCases).
 * Usage: ext_store_archive_test <testdata/v1 directory> <work directory> */
#include "ext_plugin_host.h"
#include "fs.h"
#include "wire.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int failures;
#define FAIL(...)                                                              \
  do {                                                                         \
    fprintf(stderr, __VA_ARGS__);                                              \
    fputc('\n', stderr);                                                       \
    failures++;                                                                \
  } while (0)

static char *join(const char *base, const char *leaf) {
  size_t a = strlen(base), b = strlen(leaf);
  char *path = malloc(a + b + 2);
  if (!path)
    abort();
  memcpy(path, base, a);
  path[a] = '/';
  memcpy(path + a + 1, leaf, b + 1);
  return path;
}
static const char *status_name(ext_plugin_status s) {
  switch (s) {
  case EXT_PLUGIN_OK:
    return "ok";
  case EXT_PLUGIN_INVALID:
    return "invalid";
  case EXT_PLUGIN_MISMATCH:
    return "mismatch";
  case EXT_PLUGIN_UNSUPPORTED:
    return "unsupported";
  case EXT_PLUGIN_NOT_FOUND:
    return "not_found";
  case EXT_PLUGIN_EXISTS:
    return "exists";
  }
  return "failed";
}
static yyjson_doc *read_json(const char *path) {
  FILE *f = fopen(path, "rb");
  if (!f)
    return NULL;
  static uint8_t data[1 << 20];
  size_t n = fread(data, 1, sizeof(data), f);
  fclose(f);
  return ext_parse(data, n);
}
/* The package directory name: the id with '/' written "%2f". */
static void directory_name(const char *id, char *out, size_t size) {
  size_t n = 0;
  for (; *id && n + 4 < size; id++) {
    if (*id == '/') {
      memcpy(out + n, "%2f", 3);
      n += 3;
    } else
      out[n++] = *id;
  }
  out[n] = 0;
}
static const char *text(yyjson_val *step, const char *key) {
  return yyjson_get_str(yyjson_obj_get(step, key));
}
/* The engine's parser keeps a number as its text. */
static uint64_t limit(yyjson_val *limits, const char *key) {
  yyjson_val *v = yyjson_obj_get(limits, key);
  const char *raw = yyjson_get_raw(v);
  return raw ? strtoull(raw, NULL, 10) : yyjson_get_uint(v);
}

static void run_case(const char *fixtures, ext_fs *work, const char *work_path,
                     size_t index, yyjson_val *c) {
  const char *name = text(c, "name");
  char slot[32];
  snprintf(slot, sizeof(slot), "case-%zu", index);
  if (ext_fs_remove_all(work, slot)) {
    FAIL("%s: cannot clear the work directory", name);
    return;
  }
  char *base = join(work_path, slot), *root = join(base, "plugins");
  yyjson_val *l = yyjson_obj_get(c, "limits");
  ext_plugin_store_limits limits = {limit(l, "maxArtifactBytes"),
                             (uint32_t)limit(l, "maxTreeFiles"),
                             limit(l, "maxTreeBytes")};
  ext_plugin_store *store = NULL;
  if (ext_plugin_store_open(root, &limits, &store)) {
    FAIL("%s: cannot open the store", name);
    free(base);
    free(root);
    return;
  }
  size_t i, n;
  yyjson_val *step;
  yyjson_arr_foreach(yyjson_obj_get(c, "steps"), i, n, step) {
    const char *want = text(step, "status"), *want_revision = text(step, "revision");
    const char *sha = text(step, "sha256");
    if (!want)
      want = "ok";
    ext_plugin_status s = EXT_PLUGIN_IO;
    ext_plugin_buffer out = {0};
    const char *revision = NULL;
    yyjson_doc *doc = NULL;
    if (text(step, "install")) {
      char *archive = join(fixtures, text(step, "install"));
      s = ext_plugin_store_install_archive(store, archive, sha ? sha : "", text(step, "id"),
                                    0, limit(l, "maxArchiveBytes"), &out);
      free(archive);
      if (!s) {
        doc = ext_parse(out.data, out.len);
        revision = yyjson_get_str(yyjson_obj_get(
            yyjson_obj_get(yyjson_obj_get(yyjson_doc_get_root(doc), "descriptor"),
                           "identity"),
            "revision"));
      }
    } else if (text(step, "receipt")) {
      s = ext_plugin_store_receipt(store, text(step, "receipt"), &out);
      if (!s) {
        doc = ext_parse(out.data, out.len);
        yyjson_val *r = yyjson_doc_get_root(doc);
        revision = yyjson_get_str(yyjson_obj_get(r, "revision"));
        const char *digest = yyjson_get_str(yyjson_obj_get(r, "artifact_sha256"));
        const char *owner = yyjson_get_str(yyjson_obj_get(r, "id"));
        if (!digest || !sha || strcmp(digest, sha) || !owner ||
            strcmp(owner, text(step, "receipt")))
          FAIL("%s: step %zu: the receipt names archive %s", name, i,
               digest ? digest : "(none)");
      }
    } else if (text(step, "remove")) {
      s = ext_plugin_store_remove(store, text(step, "remove"));
    } else if (text(step, "damage")) {
      char directory[1024];
      directory_name(text(step, "damage"), directory, sizeof(directory));
      char *package = join(root, directory), *file = join(package, text(step, "path"));
      FILE *f = fopen(file, "wb");
      s = f && fwrite("damaged", 1, 7, f) == 7 ? EXT_PLUGIN_OK : EXT_PLUGIN_IO;
      if (f)
        fclose(f);
      free(package);
      free(file);
    } else
      FAIL("%s: step %zu: nothing to do", name, i);
    if (strcmp(status_name(s), want) ||
        (want_revision ? !revision || strcmp(revision, want_revision)
                       : revision != NULL && text(step, "install") != NULL))
      FAIL("%s: step %zu: %s (%d) revision %s, want %s revision %s", name, i,
           status_name(s), (int)s, revision ? revision : "-", want,
           want_revision ? want_revision : "-");
    yyjson_doc_free(doc);
    ext_plugin_buffer_free(&out);
  }
  ext_plugin_store_close(store);
  /* Nothing is left under the root but packages and receipts. */
  ext_fs *fs = NULL;
  if (!ext_fs_open(root, &fs)) {
    ext_fs_entry *entries = NULL;
    size_t count = 0;
    if (!ext_fs_list(fs, "", &entries, &count)) {
      for (size_t k = 0; k < count; k++)
        if (entries[k].name[0] == '.' && strcmp(entries[k].name, ".receipts"))
          FAIL("%s: left %s in the store", name, entries[k].name);
      ext_fs_list_free(entries, count);
    }
    ext_fs_close(fs);
  }
  ext_fs_kind escaped = EXT_FS_NONE;
  if (!ext_fs_probe("/tmp/ext-archive-escape", &escaped) && escaped != EXT_FS_NONE)
    FAIL("%s: an archive wrote outside the store", name);
  ext_fs_remove_all(work, slot);
  free(base);
  free(root);
}

/* What the fixtures leave out: arguments the store refuses before it looks at
 * an archive, and an archive that is not there. */
static void arguments(const char *fixtures, const char *work_path) {
  char *root = join(work_path, "arguments");
  char *archive = join(fixtures, "archives/tar-plain.bin");
  char *absent = join(fixtures, "archives/absent.bin");
  const char *zeros =
      "0000000000000000000000000000000000000000000000000000000000000000";
  ext_plugin_store *store = NULL;
  ext_plugin_buffer out = {0};
  if (ext_plugin_store_open(root, NULL, &store))
    FAIL("arguments: cannot open the store");
  if (ext_plugin_store_install_archive(NULL, archive, zeros, NULL, 0, 0, &out) != EXT_PLUGIN_INVALID ||
      ext_plugin_store_install_archive(store, NULL, zeros, NULL, 0, 0, &out) != EXT_PLUGIN_INVALID ||
      ext_plugin_store_install_archive(store, "", zeros, NULL, 0, 0, &out) != EXT_PLUGIN_INVALID ||
      ext_plugin_store_install_archive(store, archive, NULL, NULL, 0, 0, &out) != EXT_PLUGIN_INVALID ||
      ext_plugin_store_install_archive(store, archive, zeros, NULL, 2, 0, &out) != EXT_PLUGIN_INVALID ||
      ext_plugin_store_install_archive(store, archive, zeros, NULL, 0, 0, NULL) != EXT_PLUGIN_INVALID)
    FAIL("arguments: an invalid argument was accepted");
  if (ext_plugin_store_install_archive(store, absent, zeros, NULL, 0, 0, &out) != EXT_PLUGIN_MISSING)
    FAIL("arguments: a missing archive is not EXT_PLUGIN_MISSING");
  if (ext_plugin_store_install_archive(store, fixtures, zeros, NULL, 0, 0, &out) != EXT_PLUGIN_INVALID)
    FAIL("arguments: a directory was read as an archive");
  if (ext_plugin_store_receipt(store, "example/archive", &out) != EXT_PLUGIN_NOT_FOUND ||
      ext_plugin_store_receipt(store, "Not An Id", &out) != EXT_PLUGIN_INVALID ||
      ext_plugin_store_receipt(store, "example/archive", NULL) != EXT_PLUGIN_INVALID)
    FAIL("arguments: receipt of an empty store");
  ext_plugin_store_close(store);
  free(root);
  free(archive);
  free(absent);
}

int main(int argc, char **argv) {
  if (argc != 3) {
    fprintf(stderr, "usage: %s <testdata/v1 directory> <work directory>\n", argv[0]);
    return 2;
  }
  char *cases = join(argv[1], "archive-cases.json");
  yyjson_doc *doc = read_json(cases);
  free(cases);
  ext_fs *work = NULL;
  if (!doc || ext_fs_make_root(argv[2]) || ext_fs_open(argv[2], &work)) {
    fprintf(stderr, "cannot read the fixtures or make the work directory\n");
    return 2;
  }
  size_t i, n, ran = 0;
  yyjson_val *c;
  yyjson_arr_foreach(yyjson_obj_get(yyjson_doc_get_root(doc), "cases"), i, n, c) {
    run_case(argv[1], work, argv[2], i, c);
    ran++;
  }
  if (!ran)
    FAIL("no cases");
  ext_fs_remove_all(work, "arguments");
  arguments(argv[1], argv[2]);
  ext_fs_remove_all(work, "arguments");
  ext_fs_close(work);
  yyjson_doc_free(doc);
  if (failures) {
    fprintf(stderr, "%d failure(s)\n", failures);
    return 1;
  }
  printf("store archive: %zu cases\n", ran);
  return 0;
}
