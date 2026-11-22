/* The machine on POSIX systems: Linux, Android, macOS, iOS and the BSDs. */
#ifndef _WIN32
#define _POSIX_C_SOURCE 200809L
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE
#elif defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__) || defined(__DragonFly__)
#define __BSD_VISIBLE 1
#else
#define _DEFAULT_SOURCE
#endif

#include "collect.h"

#include <limits.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <TargetConditionals.h>
#endif

const char *ll_goos(void) {
#if defined(__ANDROID__)
  return "android";
#elif defined(__linux__)
  return "linux";
#elif defined(__APPLE__) && TARGET_OS_IPHONE
  return "ios";
#elif defined(__APPLE__)
  return "darwin";
#elif defined(__FreeBSD__)
  return "freebsd";
#elif defined(__NetBSD__)
  return "netbsd";
#elif defined(__OpenBSD__)
  return "openbsd";
#elif defined(__DragonFly__)
  return "dragonfly";
#else
  return "unknown";
#endif
}

const char *ll_goarch(void) {
#if defined(__x86_64__)
  return "amd64";
#elif defined(__aarch64__) || defined(__arm64__)
  return "arm64";
#elif defined(__i386__)
  return "386";
#elif defined(__arm__)
  return "arm";
#elif defined(__riscv) && __riscv_xlen == 64
  return "riscv64";
#elif defined(__powerpc64__) && defined(__LITTLE_ENDIAN__)
  return "ppc64le";
#elif defined(__s390x__)
  return "s390x";
#else
  return "unknown";
#endif
}

static char *copy_string(const char *s) {
  if (!s) return NULL;
  size_t n = strlen(s);
  char *p = malloc(n + 1);
  if (p) memcpy(p, s, n + 1);
  return p;
}

char *ll_os_hostname(void) {
  char buf[256];
  if (gethostname(buf, sizeof buf) != 0) return copy_string("");
  buf[sizeof buf - 1] = 0;
  return copy_string(buf);
}

int64_t ll_os_ppid(void) { return (int64_t)getppid(); }

char *ll_os_getcwd(void) {
  char buf[PATH_MAX];
  return getcwd(buf, sizeof buf) ? copy_string(buf) : NULL;
}

int64_t ll_os_uid(void) { return (int64_t)getuid(); }

/* As Go's os.UserHomeDir: $HOME, with the defaults Go gives Android and iOS. */
char *ll_os_home(void) {
  const char *home = getenv("HOME");
  if (home && *home) return copy_string(home);
#if defined(__ANDROID__)
  return copy_string("/sdcard");
#elif defined(__APPLE__) && TARGET_OS_IPHONE
  return copy_string("/");
#else
  return NULL;
#endif
}

char *ll_os_getenv(const char *name) { return copy_string(getenv(name)); }

int ll_os_exists(const char *path) {
  struct stat st;
  return stat(path, &st) == 0;
}

static char *link_or(const char *path, const char *fallback) {
  char buf[512];
  ssize_t n = readlink(path, buf, sizeof buf - 1);
  if (n < 0) return copy_string(fallback);
  buf[n] = 0;
  return copy_string(buf);
}

char *ll_os_namespaces(size_t *len) {
  *len = 0;
#if defined(__linux__) && !defined(__ANDROID__)
  char *mnt = link_or("/proc/self/ns/mnt", "namespace-unavailable"), *user = link_or("/proc/self/ns/user", "namespace-unavailable");
  char *out = NULL;
  if (mnt && user) {
    size_t a = strlen(mnt), b = strlen(user);
    out = malloc(a + b + 3);
    if (out) {
      out[0] = 0;
      memcpy(out + 1, mnt, a);
      out[a + 1] = 0;
      memcpy(out + a + 2, user, b + 1);
      *len = a + b + 2;
    }
  }
  free(mnt);
  free(user);
  return out;
#else
  (void)link_or;
  return NULL;
#endif
}
#endif
