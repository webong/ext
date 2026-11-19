/* Helpers the ledger tests share: reading files and making temporary directories. */
#ifndef LEDGER_TESTUTIL_H
#define LEDGER_TESTUTIL_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <unistd.h>
#endif

static inline char *read_file(const char *path, size_t *len) {
  FILE *f = fopen(path, "rb");
  if (!f) return NULL;
  fseek(f, 0, SEEK_END);
  long n = ftell(f);
  fseek(f, 0, SEEK_SET);
  char *data = malloc((size_t)n + 1);
  if (data && fread(data, 1, (size_t)n, f) != (size_t)n) {
    free(data);
    data = NULL;
  }
  fclose(f);
  if (data) {
    data[n] = 0;
    *len = (size_t)n;
  }
  return data;
}

/* A new, empty directory path under the system's temporary directory; the directory itself is not created. */
static inline char *temp_path(const char *tag) {
  static unsigned counter;
  char base[1024];
#ifdef _WIN32
  GetTempPathA(sizeof base, base);
  unsigned long pid = GetCurrentProcessId();
#else
  const char *t = getenv("TMPDIR");
  snprintf(base, sizeof base, "%s/", t && *t ? t : "/tmp");
  unsigned long pid = (unsigned long)getpid();
#endif
  char *p = malloc(1200);
  snprintf(p, 1200, "%sext-ledger-%s-%lu-%u", base, tag, pid, counter++);
  return p;
}

/* Removes a store directory the tests made: its files, then the directory. */
#include "ledger_internal.h"
static inline int remove_entry(void *dir, const char *name) {
  char path[2048];
  snprintf(path, sizeof path, "%s/%s", (const char *)dir, name);
  ll_remove(path);
  return 0;
}
static inline void remove_store(const char *dir) {
  ll_list_dir(dir, remove_entry, (void *)dir);
#ifdef _WIN32
  RemoveDirectoryA(dir);
#else
  rmdir(dir);
#endif
}

/* Match in storetest.go: the expectation names only what it checks. */
static inline int match(const lj_value *want, const lj_value *actual) {
  if (actual && actual->type == LJ_NULL) actual = NULL;
  switch (want->type) {
  case LJ_OBJECT:
    if (actual && actual->type != LJ_OBJECT) return 0;
    for (size_t i = 0; i < want->u.object.len; i++) {
      const lj_member *m = &want->u.object.members[i];
      if (!match(m->value, actual ? lj_get(actual, m->key.ptr) : NULL)) return 0;
    }
    return 1;
  case LJ_ARRAY:
    if (!actual) return want->u.array.len == 0;
    if (actual->type != LJ_ARRAY || actual->u.array.len != want->u.array.len) return 0;
    for (size_t i = 0; i < want->u.array.len; i++)
      if (!match(want->u.array.items[i], actual->u.array.items[i])) return 0;
    return 1;
  case LJ_STRING:
    if (!actual) return want->u.string.len == 0;
    if (actual->type == LJ_OBJECT) {
      const lj_value *id = lj_get(actual, "id");
      return id && lj_equal(id, want);
    }
    if (actual->type != LJ_STRING) return 0;
    if (lj_equal(want, actual)) return 1;
    {
      ll_time a, b;
      return ll_time_parse(want->u.string.ptr, want->u.string.len, &a) == 0 &&
             ll_time_parse(actual->u.string.ptr, actual->u.string.len, &b) == 0 && a.sec == b.sec && a.nsec == b.nsec;
    }
  case LJ_NULL:
    return actual == NULL;
  case LJ_BOOL:
    if (!actual) return !want->u.boolean;
    return actual->type == LJ_BOOL && actual->u.boolean == want->u.boolean;
  case LJ_NUMBER:
    if (!actual) return want->u.number == 0;
    return actual->type == LJ_NUMBER && actual->u.number == want->u.number;
  }
  return 0;
}

#endif
