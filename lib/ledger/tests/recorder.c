/* Runs the recorder contract (lib/ledger/testdata/recorder/v1) against the C
 * recorder, as TestRecorderFixtures does against the Go one. A case that uses
 * an operation this build does not have yet is reported and skipped. */
#include "ext_ledger.h"
#include "ledger_internal.h"
#include "testutil.h"

static int failures, skipped, passed;

static lj_value *load(const char *dir, const char *name) {
  char path[2048];
  snprintf(path, sizeof path, "%s/%s", dir, name);
  size_t len;
  char *text = read_file(path, &len);
  if (!text) {
    fprintf(stderr, "cannot read %s\n", path);
    exit(2);
  }
  lj_value *v = lj_parse(text, len, NULL);
  free(text);
  if (!v) {
    fprintf(stderr, "%s is not JSON\n", path);
    exit(2);
  }
  return v;
}

static char *text_of(const lj_value *v, size_t *len) {
  lj_buf b = {0};
  if (v) lj_write(&b, v);
  return lj_buf_take(&b, len);
}

static const char *field(const lj_value *o, const char *key) {
  const lj_value *v = lj_get(o, key);
  return v && v->type == LJ_STRING ? v->u.string.ptr : "";
}

static void check_vocabulary(const char *dir) {
  lj_value *file = load(dir, "vocabulary.json");
  size_t a, b;
  char *want = text_of(lj_get(file, "schema"), &a);
  const char *have = ext_ledger_recorder_vocabulary();
  lj_value *parsed = lj_parse(have, strlen(have), NULL);
  char *got = text_of(parsed, &b);
  if (!want || !got || strcmp(want, got) != 0) {
    fprintf(stderr, "FAIL the vocabulary is not vocabulary.json:\n  got  %s\n  want %s\n", got ? got : "?", want ? want : "?");
    failures++;
  }
  free(want);
  free(got);
  lj_free(parsed);
  lj_free(file);
}

static void run_case(const lj_value *c) {
  const char *name = field(c, "name");
  ext_ledger *store;
  ext_ledger_open_memory(NULL, &store);
  size_t len;
  char *env = text_of(lj_get(c, "environment"), &len);
  ext_ledger_recorder *r;
  int rc = ext_ledger_recorder_open(store, NULL, env, len, &r);
  free(env);
  if (rc) {
    fprintf(stderr, "FAIL %s: open: %s\n", name, ext_ledger_code_name(rc));
    failures++;
    ext_ledger_close(store);
    return;
  }
  const lj_value *steps = lj_get(c, "steps");
  int ok = 1;
  for (size_t i = 0; ok && i < steps->u.array.len; i++) {
    const lj_value *st = steps->u.array.items[i];
    const char *op = field(st, "op");
    char *out = NULL;
    size_t out_len = 0;
    if (strcmp(op, "snapshot") == 0) {
      rc = ext_ledger_snapshot(store, NULL, "ext.system", &out, &out_len);
    } else {
      size_t in_len = 0;
      char *in = lj_get(st, "input") ? text_of(lj_get(st, "input"), &in_len) : NULL;
      rc = ext_ledger_record(r, NULL, op, in, in_len, &out, &out_len);
      free(in);
    }
    if (rc == EXT_LEDGER_UNSUPPORTED) {
      printf("skip %s: %s is not built yet\n", name, op);
      skipped++;
      ok = 0;
      break;
    }
    const char *want = field(st, "error");
    if (strcmp(ext_ledger_code_name(rc), want) != 0) {
      fprintf(stderr, "FAIL %s: step %zu (%s): code \"%s\", want \"%s\"\n", name, i, op, ext_ledger_code_name(rc), want);
      failures++;
      ok = 0;
    }
    const lj_value *expect = lj_get(st, "expect");
    if (ok && !rc && expect) {
      lj_value *actual = out ? lj_parse(out, out_len, NULL) : NULL;
      if (!match(expect, actual)) {
        size_t wl;
        char *w = text_of(expect, &wl);
        fprintf(stderr, "FAIL %s: step %zu (%s):\n  got  %s\n  want %s\n", name, i, op, out ? out : "null", w);
        free(w);
        failures++;
        ok = 0;
      }
      lj_free(actual);
    }
    ext_ledger_free(out);
  }
  if (ok) passed++;
  ext_ledger_recorder_close(r);
  ext_ledger_close(store);
}

int main(int argc, char **argv) {
  if (argc != 2) {
    fprintf(stderr, "usage: ledger_recorder <lib/ledger/testdata/recorder/v1>\n");
    return 2;
  }
  check_vocabulary(argv[1]);
  lj_value *file = load(argv[1], "cases.json");
  const lj_value *cases = lj_get(file, "cases");
  for (size_t i = 0; i < cases->u.array.len; i++) run_case(cases->u.array.items[i]);
  lj_free(file);
  if (failures) {
    fprintf(stderr, "%d failures\n", failures);
    return 1;
  }
  printf("ok: %d recorder cases, %d skipped (operations not built yet); the vocabulary\n", passed, skipped);
  return 0;
}
