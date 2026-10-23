/* The language-neutral fixtures for progress events and reverse calls
 * (testdata/v1/frame-cases.json, progress-limiter-cases.json, model-cases.json),
 * which the Go contract computes and every SDK must reproduce. This is the C
 * engine reproducing them. Usage: ext_fixtures_test <testdata/v1 directory> */
#define _XOPEN_SOURCE 700
#include "progress.h"
#include "reverse.h"
#include "wire.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static char *read_file(const char *dir, const char *name, size_t *length) {
  char path[1024];
  snprintf(path, sizeof(path), "%s/%s", dir, name);
  FILE *f = fopen(path, "rb");
  if (!f) {
    fprintf(stderr, "cannot open %s\n", path);
    exit(2);
  }
  assert(!fseek(f, 0, SEEK_END));
  long n = ftell(f);
  assert(n > 0);
  rewind(f);
  char *data = malloc((size_t)n + 1);
  assert(data && fread(data, 1, (size_t)n, f) == (size_t)n);
  data[n] = 0;
  fclose(f);
  *length = (size_t)n;
  return data;
}
static yyjson_doc *load(const char *dir, const char *name) {
  size_t length;
  char *data = read_file(dir, name, &length);
  yyjson_doc *doc = ext_parse((const uint8_t *)data, length);
  assert(doc);
  free(data); /* ext_parse copies */
  return doc;
}
static const char *str(yyjson_val *obj, const char *key) {
  return yyjson_get_str(yyjson_obj_get(obj, key));
}
/* The parser keeps numbers as raw text, so read them through the engine's own
 * integer rule. */
static int64_t integer(yyjson_val *v) {
  int64_t out = 0;
  assert(v && ext_int64(v, &out));
  return out;
}
static long failures;
static void mismatch(const char *section, yyjson_val *c, const char *what) {
  failures++;
  if (failures <= 10)
    fprintf(stderr, "%s: %s: %s\n", section, str(c, "name"), what);
}

/* A frame is valid when it parses strictly and the frame rules accept it. */
static int frame_valid(const char *section, const char *text) {
  yyjson_doc *doc = ext_parse((const uint8_t *)text, strlen(text));
  if (!doc)
    return 0;
  yyjson_val *root = yyjson_doc_get_root(doc);
  int ok;
  if (!strcmp(section, "progress")) {
    ext_progress_event event;
    yyjson_val *parent;
    ok = ext_progress_frame(root, &event, &parent) == EXT_OK;
  } else {
    ext_reverse_request request;
    ok = ext_reverse_request_frame(root, &request) == EXT_OK;
  }
  yyjson_doc_free(doc);
  return ok;
}
static void test_frames(const char *dir) {
  yyjson_doc *doc = load(dir, "frame-cases.json");
  yyjson_val *root = yyjson_doc_get_root(doc);
  size_t checked = 0;
  /* Reverse responses are a guest's to receive; the host engine never reads one. */
  const char *sections[] = {"progress", "reverseRequest"};
  for (size_t s = 0; s < 2; s++) {
    yyjson_val *cases = yyjson_obj_get(root, sections[s]), *c;
    size_t i, n, valid = 0, invalid = 0;
    assert(yyjson_is_arr(cases) && yyjson_arr_size(cases) > 100);
    yyjson_arr_foreach(cases, i, n, c) {
      int want = yyjson_get_bool(yyjson_obj_get(c, "valid"));
      if (frame_valid(sections[s], str(c, "frame")) != want)
        mismatch(sections[s], c, want ? "should be accepted" : "should be refused");
      want ? valid++ : invalid++;
      checked++;
    }
    assert(valid >= 5 && invalid >= 5);
  }
  assert(!failures);
  yyjson_doc_free(doc);
  printf("%zu frame cases\n", checked);
}

static void test_limiter(const char *dir) {
  yyjson_doc *doc = load(dir, "progress-limiter-cases.json");
  yyjson_val *cases = yyjson_obj_get(yyjson_doc_get_root(doc), "cases"), *c;
  size_t i, n, steps_checked = 0;
  assert(yyjson_is_arr(cases) && yyjson_arr_size(cases) >= 10);
  yyjson_arr_foreach(cases, i, n, c) {
    ext_limiter limiter;
    ext_limiter_init(&limiter, integer(yyjson_obj_get(c, "eventsPerSecond")),
                     integer(yyjson_obj_get(c, "bytesPerSecond")), 0);
    yyjson_val *steps = yyjson_obj_get(c, "steps"), *step;
    size_t j, m;
    yyjson_arr_foreach(steps, j, m, step) {
      uint64_t dropped = 0;
      int admitted = ext_limiter_admit(
          &limiter, (size_t)integer(yyjson_obj_get(step, "textBytes")),
          integer(yyjson_obj_get(step, "atMs")) * 1000000LL, &dropped);
      int want = yyjson_get_bool(yyjson_obj_get(step, "admitted"));
      yyjson_val *recorded = yyjson_obj_get(step, "dropped");
      uint64_t want_dropped = recorded ? (uint64_t)integer(recorded) : 0;
      if (admitted != want || (want && dropped != want_dropped)) {
        char what[128];
        snprintf(what, sizeof(what), "step %zu: admitted %d dropped %llu, want %d %llu", j,
                 admitted, (unsigned long long)dropped, want, (unsigned long long)want_dropped);
        mismatch("limiter", c, what);
      }
      steps_checked++;
    }
    if (ext_limiter_pending(&limiter) != (uint64_t)integer(yyjson_obj_get(c, "pending")))
      mismatch("limiter", c, "pending count");
  }
  assert(!failures);
  yyjson_doc_free(doc);
  printf("%zu limiter steps\n", steps_checked);
}

static void test_model(const char *dir) {
  yyjson_doc *doc = load(dir, "model-cases.json");
  yyjson_val *root = yyjson_doc_get_root(doc), *c;
  yyjson_val *descriptors = yyjson_obj_get(root, "descriptors"),
             *requests = yyjson_obj_get(root, "requests");
  size_t i, n, checked = 0;
  assert(yyjson_is_arr(descriptors) && yyjson_arr_size(descriptors) > 100);
  yyjson_arr_foreach(descriptors, i, n, c) {
    const char *text = str(c, "descriptor");
    yyjson_doc *d = ext_parse((const uint8_t *)text, strlen(text));
    int ok = d && ext_descriptor(yyjson_doc_get_root(d));
    if (ok != yyjson_get_bool(yyjson_obj_get(c, "valid")))
      mismatch("descriptor", c, ok ? "should be refused" : "should be accepted");
    yyjson_doc_free(d);
    checked++;
  }
  /* The requests are judged against one descriptor, given as an object. */
  size_t selection_len;
  char *selection_text = yyjson_val_write(yyjson_obj_get(root, "requestDescriptor"), 0, &selection_len);
  assert(selection_text);
  yyjson_doc *selection = ext_parse((const uint8_t *)selection_text, selection_len);
  assert(selection && ext_descriptor(yyjson_doc_get_root(selection)));
  assert(yyjson_is_arr(requests) && yyjson_arr_size(requests) > 100);
  yyjson_arr_foreach(requests, i, n, c) {
    const char *text = str(c, "request");
    yyjson_doc *r = ext_parse((const uint8_t *)text, strlen(text));
    int64_t deadline;
    int ok = r && ext_request(yyjson_doc_get_root(selection), yyjson_doc_get_root(r), &deadline) == EXT_OK;
    int want = yyjson_get_bool(yyjson_obj_get(c, "valid"));
    if (ok != want) {
      mismatch("request", c, ok ? "should be refused" : "should be accepted");
    } else if (ok && yyjson_get_bool(yyjson_obj_get(c, "reporter"))) {
      /* The call gets a progress reporter at the rate with defaults filled in. */
      int declared;
      int64_t events, bytes;
      ext_request_progress(yyjson_doc_get_root(r), &declared, &events, &bytes);
      if (!declared || (events > 0 ? events : 50) != integer(yyjson_obj_get(c, "events")) ||
          (bytes > 0 ? bytes : 65536) != integer(yyjson_obj_get(c, "bytes")))
        mismatch("request", c, "progress rate");
    }
    yyjson_doc_free(r);
    checked++;
  }
  assert(!failures);
  free(selection_text);
  yyjson_doc_free(selection);
  yyjson_doc_free(doc);
  printf("%zu model cases\n", checked);
}

int main(int argc, char **argv) {
  assert(argc == 2);
  test_frames(argv[1]);
  test_limiter(argv[1]);
  test_model(argv[1]);
  puts("fixture checks passed");
  return 0;
}
