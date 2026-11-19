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

static char *read_file(const char *path, size_t *len) {
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
static char *temp_path(const char *tag) {
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
static int remove_entry(void *dir, const char *name) {
  char path[2048];
  snprintf(path, sizeof path, "%s/%s", (const char *)dir, name);
  ll_remove(path);
  return 0;
}
static void remove_store(const char *dir) {
  ll_list_dir(dir, remove_entry, (void *)dir);
#ifdef _WIN32
  RemoveDirectoryA(dir);
#else
  rmdir(dir);
#endif
}

#endif
