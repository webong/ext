/* Host resources: logical CPUs
 * and the capacity a cgroup quota leaves (Linux), physical memory and a cgroup
 * limit, container evidence (Linux), storage and power where visible, and uptime with
 * the boot time it implies. Local files and counters only; no command runs. */
#if defined(__linux__)
#define _GNU_SOURCE
#elif defined(__APPLE__)
#define _DARWIN_C_SOURCE
#endif
#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#endif

#include "collect.h"
#include "recorder.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(__linux__)
#include <dirent.h>
#include <sched.h>
#include <sys/stat.h>
#include <sys/statvfs.h>
#include <unistd.h>
#elif defined(__APPLE__)
#include <sys/sysctl.h>
#include <sys/statvfs.h>
#include <sys/time.h>
#include <unistd.h>
#include <TargetConditionals.h>
#if TARGET_OS_IPHONE
#include <objc/message.h>
#include <objc/runtime.h>
#endif
#elif !defined(_WIN32)
#include <unistd.h>
#endif

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

static int logical_cpus(void) {
#if defined(__linux__)
  cpu_set_t mask; /* Go's runtime.NumCPU counts the CPUs this process may run on */
  if (sched_getaffinity(0, sizeof mask, &mask) == 0) {
    int n = CPU_COUNT(&mask);
    if (n > 0) return n;
  }
  return (int)sysconf(_SC_NPROCESSORS_ONLN);
#elif defined(_WIN32)
  DWORD_PTR process, system;
  if (GetProcessAffinityMask(GetCurrentProcess(), &process, &system)) {
    int n = 0;
    for (; process; process >>= 1) n += (int)(process & 1);
    if (n > 0) return n;
  }
  SYSTEM_INFO info;
  GetSystemInfo(&info);
  return (int)info.dwNumberOfProcessors;
#else
  return (int)sysconf(_SC_NPROCESSORS_ONLN);
#endif
}

#if defined(__linux__)
static char *read_text(const char *path) {
  FILE *f = fopen(path, "r");
  if (!f) return NULL;
  size_t cap = 4096, n = 0;
  char *buf = malloc(cap + 1);
  size_t got;
  while (buf && (got = fread(buf + n, 1, cap - n, f)) > 0) {
    n += got;
    if (n == cap) {
      char *g = realloc(buf, cap * 2 + 1);
      if (!g) {
        free(buf);
        buf = NULL;
        break;
      }
      buf = g;
      cap *= 2;
    }
  }
  fclose(f);
  if (buf) buf[n] = 0;
  return buf;
}

/* A decimal unsigned integer, the whole of the trimmed text; 0 when it is not one or would overflow with the multiplier. */
static uint64_t parse_unsigned(const char *text, uint64_t multiplier) {
  while (*text == ' ' || *text == '\t' || *text == '\n') text++;
  char *end;
  if (*text < '0' || *text > '9') return 0;
  unsigned long long v = strtoull(text, &end, 10);
  while (*end == ' ' || *end == '\t' || *end == '\n') end++;
  if (*end || multiplier == 0 || v > UINT64_MAX / multiplier) return 0;
  return (uint64_t)v * multiplier;
}

static double bounded(double host, double quota, double period) {
  double capacity = quota / period;
  return capacity <= 0 || capacity > host ? host : capacity;
}

/* The CPU capacity cgroup v2 or v1 quotas leave; *error is set (static text) when a quota cannot be read. */
static double linux_cpu_capacity(double host, const char **source, const char **error) {
  *source = "logical CPU count";
  *error = NULL;
  char *v2 = read_text("/sys/fs/cgroup/cpu.max");
  if (v2) {
    char quota[64], period[64], extra[8];
    int n = sscanf(v2, "%63s %63s %7s", quota, period, extra);
    free(v2);
    if (n != 2) {
      *error = "cgroup v2 cpu.max is malformed";
      return host;
    }
    if (strcmp(quota, "max") == 0) return host;
    uint64_t q = parse_unsigned(quota, 1), p = parse_unsigned(period, 1);
    if (!q || !p) {
      *error = "cgroup v2 cpu.max is malformed";
      return host;
    }
    double c = bounded(host, (double)q, (double)p);
    if (c >= host) return host;
    *source = "cgroup-v2";
    return c;
  }
  struct stat st;
  char *quota = read_text("/sys/fs/cgroup/cpu/cpu.cfs_quota_us");
  if (!quota) {
    if (stat("/sys/fs/cgroup/cpu/cpu.cfs_quota_us", &st) != 0) return host;
    *error = "cgroup v1 quota is unreadable";
    return host;
  }
  char *period = read_text("/sys/fs/cgroup/cpu/cpu.cfs_period_us");
  if (!period) {
    free(quota);
    *error = "cgroup v1 period is unreadable";
    return host;
  }
  char *end;
  long long q = strtoll(quota, &end, 10);
  while (*end == ' ' || *end == '\n') end++;
  int bad_quota = *end != 0 || end == quota;
  uint64_t p = parse_unsigned(period, 1);
  free(quota);
  free(period);
  if (bad_quota) {
    *error = "cgroup v1 quota is malformed";
    return host;
  }
  if (q < 0) return host;
  if (!p) {
    *error = "cgroup v1 period is malformed";
    return host;
  }
  if (q == 0) {
    *source = "cgroup-v1";
    return 0;
  }
  double c = bounded(host, (double)q, (double)p);
  if (c >= host) return host;
  *source = "cgroup-v1";
  return c;
}

static int linux_memory(uint64_t *physical, uint64_t *effective, const char **source, char error[128]) {
  *physical = *effective = 0;
  *source = "";
  error[0] = 0;
  char *meminfo = read_text("/proc/meminfo");
  if (!meminfo) {
    *source = "physical memory unavailable";
    snprintf(error, 128, "proc meminfo is unreadable");
    return -1;
  }
  for (char *line = meminfo; line && *line;) {
    char *next = strchr(line, '\n');
    if (next) *next++ = 0;
    if (strncmp(line, "MemTotal:", 9) == 0) {
      char value[64];
      if (sscanf(line + 9, "%63s", value) == 1) *physical = parse_unsigned(value, 1024);
      break;
    }
    line = next;
  }
  free(meminfo);
  if (!*physical) {
    *source = "physical memory unavailable";
    snprintf(error, 128, "proc meminfo has no MemTotal");
    return -1;
  }
  *effective = *physical;
  *source = "physical memory capacity";
  static const char *const paths[] = {"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"};
  for (int i = 0; i < 2; i++) {
    char *v = read_text(paths[i]);
    if (!v) continue;
    char trimmed[64] = "";
    sscanf(v, "%63s", trimmed);
    if (strcmp(trimmed, "max") == 0) {
      free(v);
      *source = "cgroup unlimited";
      return 0;
    }
    uint64_t limit = parse_unsigned(v, 1);
    free(v);
    if (!limit) {
      snprintf(error, 128, "%s is malformed", paths[i]);
      return -1;
    }
    if (limit < (1ULL << 60) && limit < *physical) {
      *effective = limit;
      *source = "cgroup memory limit";
    }
    return 0;
  }
  return 0;
}

static int cmp_names(const void *a, const void *b) { return strcmp(*(const char *const *)a, *(const char *const *)b); }

static lj_value *linux_container(void) {
  const char *names[16];
  int n = 0;
  const char *env = getenv("container");
  if (env && strspn(env, " \t\n") != strlen(env)) names[n++] = "container-environment";
  env = getenv("KUBERNETES_SERVICE_HOST");
  if (env && strspn(env, " \t\n") != strlen(env)) names[n++] = "kubernetes-environment";
  struct stat st;
  if (stat("/.dockerenv", &st) == 0) names[n++] = "docker-marker";
  if (stat("/run/.containerenv", &st) == 0) names[n++] = "podman-marker";
  static const char *const cgroups[] = {"/proc/1/cgroup", "/proc/self/cgroup"};
  static const char *const signatures[][2] = {{"/docker/", "docker-cgroup"},   {"/docker-", "docker-cgroup"},         {"/containerd/", "containerd-cgroup"},
                                               {"/podman/", "podman-cgroup"}, {"/kubepods/", "kubernetes-cgroup"}, {"/lxc/", "lxc-cgroup"}};
  for (int c = 0; c < 2; c++) {
    char *text = read_text(cgroups[c]);
    if (!text) continue;
    for (char *p = text; *p; p++)
      if (*p >= 'A' && *p <= 'Z') *p = (char)(*p + 32);
    for (int s = 0; s < 6 && n < 16; s++)
      if (strstr(text, signatures[s][0])) names[n++] = signatures[s][1];
    free(text);
  }
  const char *unique[16];
  int u = 0;
  for (int i = 0; i < n; i++) {
    int seen = 0;
    for (int k = 0; k < u && !seen; k++) seen = strcmp(unique[k], names[i]) == 0;
    if (!seen) unique[u++] = names[i];
  }
  qsort(unique, (size_t)u, sizeof *unique, cmp_names);
  lj_value *evidence = lj_new(LJ_ARRAY);
  for (int i = 0; evidence && i < u; i++)
    if (lj_array_push(evidence, rec_str(unique[i]))) break;
  lj_value *c = rec_obj();
  int detected = evidence && evidence->u.array.len > 0;
  set(c, "detected", rec_bool(detected));
  set(c, "state", rec_str(detected ? "detected" : "none"));
  set(c, "evidence", evidence);
  return c;
}

static lj_value *linux_storage(void) {
#if defined(__ANDROID__)
  struct statvfs st;
  if (statvfs("/data", &st) != 0) return NULL;
  lj_value *list = lj_new(LJ_ARRAY), *s = rec_obj();
  if (!list || !s || set(s, "device", rec_str("local")) ||
      set(s, "mount_point", rec_str("/data")) ||
      set(s, "total_bytes", rec_num((double)((uint64_t)st.f_blocks * st.f_frsize))) ||
      set(s, "available_bytes", rec_num((double)((uint64_t)st.f_bavail * st.f_frsize))) ||
      lj_array_push(list, s)) {
    lj_free(s); lj_free(list); return NULL;
  }
  return list;
#else
  FILE *f = fopen("/proc/mounts", "r");
  if (!f) return NULL;
  lj_value *list = lj_new(LJ_ARRAY);
  char line[8192];
  while (list && fgets(line, sizeof line, f)) {
    char device[2048], mount[2048], type[256], options[2048] = "";
    if (sscanf(line, "%2047s %2047s %255s %2047s", device, mount, type, options) < 3) continue;
    struct statvfs st;
    if (statvfs(mount, &st) != 0) continue;
    int read_only = 0;
    for (char *o = strtok(options, ","); o; o = strtok(NULL, ","))
      if (strcmp(o, "ro") == 0) read_only = 1;
    lj_value *s = rec_obj();
    set(s, "device", rec_str(device));
    set(s, "mount_point", rec_str(mount));
    set(s, "total_bytes", rec_num((double)((uint64_t)st.f_blocks * (uint64_t)st.f_frsize)));
    set(s, "available_bytes", rec_num((double)((uint64_t)st.f_bavail * (uint64_t)st.f_frsize)));
    if (*type) set(s, "type", rec_str(type));
    if (read_only) set(s, "read_only", rec_bool(1));
    if (lj_array_push(list, s)) break;
  }
  fclose(f);
  if (list && !list->u.array.len) { /* omit an empty list */
    lj_free(list);
    list = NULL;
  }
  return list;
#endif
}

static lj_value *linux_power(void) {
  DIR *d = opendir("/sys/class/power_supply");
  if (!d) return NULL;
  int any = 0, percentage = 0;
  const char *state = "";
  struct dirent *e;
  while ((e = readdir(d)) != NULL) {
    if (e->d_name[0] == '.') continue;
    char path[512];
    snprintf(path, sizeof path, "/sys/class/power_supply/%s/capacity", e->d_name);
    char *capacity = read_text(path);
    if (!capacity) continue;
    char *end;
    long pc = strtol(capacity, &end, 10);
    while (*end == ' ' || *end == '\n') end++;
    int ok = *end == 0 && end != capacity && pc >= 0 && pc <= 100;
    free(capacity);
    if (!ok) continue;
    snprintf(path, sizeof path, "/sys/class/power_supply/%s/status", e->d_name);
    char *status = read_text(path);
    if (!status) continue;
    for (char *p = status; *p; p++)
      if (*p >= 'A' && *p <= 'Z') *p = (char)(*p + 32);
    char word[64] = "";
    sscanf(status, "%63s", word);
    free(status);
    any = 1;
    if (pc > percentage) percentage = (int)pc;
    if (!strcmp(word, "charging")) state = "charging";
    else if (!strcmp(word, "discharging")) state = "discharging";
    else if (!strcmp(word, "full") || !strcmp(word, "charged")) state = "charged";
    else if (!*state) state = "unknown";
  }
  closedir(d);
  if (!any) return NULL;
  lj_value *p = rec_obj();
  set(p, "state", rec_str(state));
  set(p, "percentage", rec_num(percentage));
  return p;
}
#endif

#if defined(__APPLE__)
static lj_value *apple_storage(void) {
#if TARGET_OS_IPHONE
  const char *path = getenv("HOME");
  if (!path || !*path) return NULL;
#else
  const char *path = "/";
#endif
  struct statvfs st;
  if (statvfs(path, &st) != 0) return NULL;
  lj_value *list = lj_new(LJ_ARRAY), *s = rec_obj();
  if (!list || !s || set(s, "device", rec_str("local")) ||
      set(s, "mount_point", rec_str(path)) ||
      set(s, "total_bytes", rec_num((double)((uint64_t)st.f_blocks * st.f_frsize))) ||
      set(s, "available_bytes", rec_num((double)((uint64_t)st.f_bavail * st.f_frsize))) ||
      set(s, "type", rec_str("apfs")) || lj_array_push(list, s)) {
    lj_free(s); lj_free(list); return NULL;
  }
  return list;
}

static lj_value *apple_power(void) {
#if TARGET_OS_IPHONE
  Class cls = objc_getClass("UIDevice");
  if (!cls) return NULL;
  id device = ((id (*)(Class, SEL))objc_msgSend)(cls, sel_registerName("currentDevice"));
  if (!device) return NULL;
  ((void (*)(id, SEL, BOOL))objc_msgSend)(device, sel_registerName("setBatteryMonitoringEnabled:"), YES);
  float level = ((float (*)(id, SEL))objc_msgSend)(device, sel_registerName("batteryLevel"));
  if (level < 0 || level > 1) return NULL;
  long state = ((long (*)(id, SEL))objc_msgSend)(device, sel_registerName("batteryState"));
  const char *name = state == 2 ? "charging" : state == 3 ? "charged" : state == 1 ? "discharging" : "unknown";
  lj_value *power = rec_obj();
  if (!power || set(power, "state", rec_str(name)) ||
      set(power, "percentage", rec_num(roundf(level * 100)))) {
    lj_free(power); return NULL;
  }
  return power;
#else
  return NULL;
#endif
}
#endif

#if defined(_WIN32)
static lj_value *windows_storage(void) {
  WCHAR system[MAX_PATH], root[MAX_PATH];
  if (!GetWindowsDirectoryW(system, MAX_PATH) ||
      !GetVolumePathNameW(system, root, MAX_PATH)) return NULL;
  ULARGE_INTEGER available, total, free_bytes;
  if (!GetDiskFreeSpaceExW(root, &available, &total, &free_bytes)) return NULL;
  char path[MAX_PATH * 4];
  if (!WideCharToMultiByte(CP_UTF8, 0, root, -1, path, sizeof path, NULL, NULL)) return NULL;
  lj_value *list = lj_new(LJ_ARRAY), *s = rec_obj();
  if (!list || !s || set(s, "device", rec_str("local")) ||
      set(s, "mount_point", rec_str(path)) ||
      set(s, "total_bytes", rec_num((double)total.QuadPart)) ||
      set(s, "available_bytes", rec_num((double)available.QuadPart)) ||
      lj_array_push(list, s)) {
    lj_free(s); lj_free(list); return NULL;
  }
  return list;
}

static lj_value *windows_power(void) {
  SYSTEM_POWER_STATUS status;
  if (!GetSystemPowerStatus(&status) || status.BatteryFlag == 128 ||
      status.BatteryLifePercent == 255 || status.BatteryLifePercent > 100) return NULL;
  const char *state = status.BatteryFlag & 8 ? "charging" :
                      status.BatteryLifePercent == 100 ? "charged" :
                      status.ACLineStatus == 0 ? "discharging" : "unknown";
  lj_value *power = rec_obj();
  if (!power || set(power, "state", rec_str(state)) ||
      set(power, "percentage", rec_num(status.BatteryLifePercent))) {
    lj_free(power); return NULL;
  }
  return power;
}
#endif

/* Uptime in nanoseconds, or -1 with a static reason. */
static int64_t uptime_ns(const ll_time *now, const char **reason) {
  *reason = "uptime is unavailable";
#if defined(__linux__)
  char *text = read_text("/proc/uptime");
  if (text) {
    char *end;
    double seconds = strtod(text, &end);
    int ok = end != text && seconds >= 0;
    free(text);
    if (ok) return (int64_t)(seconds * 1e9);
  }
  char *stat = read_text("/proc/stat");
  if (!stat) {
    *reason = "proc uptime is unreadable";
    return -1;
  }
  long long boot = 0;
  for (char *line = stat; line && *line;) {
    char *next = strchr(line, '\n');
    if (next) *next++ = 0;
    if (strncmp(line, "btime ", 6) == 0) boot = atoll(line + 6);
    line = next;
  }
  free(stat);
  if (boot <= 0) {
    *reason = "proc stat has no boot time";
    return -1;
  }
  if (boot > now->sec) {
    *reason = "native boot time is in the future";
    return -1;
  }
  return (now->sec - boot) * 1000000000LL + now->nsec;
#elif defined(__APPLE__)
  struct timeval boot;
  size_t size = sizeof boot;
  if (sysctlbyname("kern.boottime", &boot, &size, NULL, 0) != 0 || boot.tv_sec <= 0) {
    *reason = "sysctl kern.boottime is unavailable";
    return -1;
  }
  int64_t at = (int64_t)boot.tv_sec * 1000000000LL + (int64_t)boot.tv_usec * 1000LL, t = now->sec * 1000000000LL + now->nsec;
  if (at > t) {
    *reason = "native boot time is in the future";
    return -1;
  }
  return t - at;
#elif defined(_WIN32)
  (void)now;
  ULONGLONG unbiased = 0;
  if (!QueryUnbiasedInterruptTime(&unbiased) || unbiased == 0) {
    *reason = "QueryUnbiasedInterruptTime is unavailable";
    return -1;
  }
  return (int64_t)unbiased * 100;
#else
  (void)now;
  return -1;
#endif
}

#if !defined(__linux__)
static uint64_t physical_memory(void) {
#if defined(__APPLE__)
  uint64_t value = 0;
  size_t size = sizeof value;
  if (sysctlbyname("hw.memsize", &value, &size, NULL, 0) != 0) return 0;
  return value;
#elif defined(_WIN32)
  MEMORYSTATUSEX status;
  status.dwLength = sizeof status;
  return GlobalMemoryStatusEx(&status) ? status.ullTotalPhys : 0;
#else
  return 0;
#endif
}
#endif

static lj_value *time_value(int64_t sec, int32_t nsec) {
  ll_time t = {1, sec, nsec, 0};
  lj_buf b = {0};
  ll_time_write(&b, &t);
  lj_value *v = b.oom || b.len < 2 ? NULL : rec_strn(b.ptr + 1, b.len - 2);
  lj_buf_free(&b);
  return v;
}

int ll_collect_resources(const ext_ledger_context *ctx, lj_value **out) {
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  ll_time now = ll_time_now();
  int logical = logical_cpus();
  if (logical < 1) logical = 1;
  lj_value *r = rec_obj();
  int bad = set(r, "os", rec_str(ll_goos())) | set(r, "architecture", rec_str(ll_goarch())) | set(r, "observed_at", time_value(now.sec, now.nsec)) |
            set(r, "logical_cpus", rec_num(logical));
  double capacity = logical;
  lj_value *cpu_status = ll_status("complete", NULL, "logical CPU count");
  uint64_t memory = 0, memory_capacity = 0;
  lj_value *memory_status = NULL;
#if defined(__linux__)
  const char *source, *error;
  capacity = linux_cpu_capacity(logical, &source, &error);
  lj_free(cpu_status);
  cpu_status = error ? ll_status("partial", NULL, error) : ll_status("complete", NULL, source);
  char merror[128];
  const char *msource;
  linux_memory(&memory, &memory_capacity, &msource, merror);
  if (!memory && !memory_capacity) memory_status = ll_status("unsupported", "unsupported", *merror ? merror : "Linux memory capacity is unavailable");
  else memory_status = *merror ? ll_status("partial", NULL, merror) : ll_status("complete", NULL, msource);
  bad |= set(r, "container", linux_container());
  lj_value *storage = linux_storage(), *power = linux_power();
  if (storage) bad |= set(r, "storage", storage);
  if (power) bad |= set(r, "power", power);
#else
  memory = memory_capacity = physical_memory();
  memory_status = memory ? ll_status("complete", NULL, "physical memory capacity")
                         : ll_status("unsupported", "unsupported", "physical memory capacity is unavailable");
  lj_value *container = rec_obj();
  bad |= set(container, "detected", rec_bool(0)) | set(container, "state", rec_str("unsupported")) | set(container, "evidence", lj_new(LJ_ARRAY)) |
         set(r, "container", container);
#if defined(__APPLE__)
  lj_value *storage = apple_storage(), *power = apple_power();
  if (storage) bad |= set(r, "storage", storage);
  if (power) bad |= set(r, "power", power);
#elif defined(_WIN32)
  lj_value *storage = windows_storage(), *power = windows_power();
  if (storage) bad |= set(r, "storage", storage);
  if (power) bad |= set(r, "power", power);
#endif
#endif
  bad |= set(r, "cpu_capacity", rec_num(capacity)) | set(r, "cpu_status", cpu_status) | set(r, "memory_bytes", rec_num((double)memory)) |
         set(r, "memory_capacity_bytes", rec_num((double)memory_capacity)) | set(r, "memory_status", memory_status);
  const char *reason;
  int64_t up = uptime_ns(&now, &reason);
  if (up < 0) {
    bad |= set(r, "uptime_status", ll_status("unsupported", "unsupported", reason));
  } else {
    int64_t t = now.sec * 1000000000LL + now.nsec - up;
    int64_t sec = t >= 0 ? t / 1000000000LL : -((-t + 999999999LL) / 1000000000LL);
    bad |= set(r, "uptime_seconds", rec_num((double)up / 1e9)) | set(r, "boot_time", time_value(sec, (int32_t)(t - sec * 1000000000LL))) |
           set(r, "uptime_status", ll_status("complete", NULL, NULL));
  }
  if (bad) {
    lj_free(r);
    return EXT_LEDGER_IO;
  }
  *out = r;
  return 0;
}
