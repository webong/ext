/* The native desktop visible to this process: whether it is interactive,
 * its local X11 and Wayland endpoints,
 * whether it runs in a container, and what recording would need. Environment
 * variables, socket paths and a PATH search for ffmpeg (never run) only. */
#include "collect.h"
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

/* An environment value that is set and not blank, or NULL. */
static char *value(const char *name) {
  char *v = ll_os_getenv(name);
  if (v && strspn(v, " \t\r\n") == strlen(v)) {
    free(v);
    return NULL;
  }
  return v;
}

static int is_set(const char *name) {
  char *v = ll_os_getenv(name);
  int set_ = v != NULL;
  free(v);
  return set_;
}

typedef struct {
  int available;
  const char *reason;
} endpoint;

static endpoint x11(void) {
  endpoint e = {0, "DISPLAY is not set"};
  char *display = value("DISPLAY");
  if (!display) return e;
  e.reason = "local X11 socket is unavailable";
  char *start = display;
  while (*start == ' ' || *start == '\t') start++;
  char *colon = strchr(start, ':');
  if (colon) {
    size_t hl = (size_t)(colon - start);
    int local = hl == 0 || (hl == 9 && strncmp(start, "localhost", 9) == 0) || (hl == 4 && strncmp(start, "unix", 4) == 0);
    char number[32] = "";
    size_t k = 0;
    for (const char *p = colon + 1; *p && *p != '.' && *p != ' ' && k < sizeof number - 1; p++) number[k++] = *p;
    if (!k) strcpy(number, "0");
    char *end;
    long n = strtol(number, &end, 10);
    if (local && !*end && n >= 0) {
      char socket[64];
      snprintf(socket, sizeof socket, "/tmp/.X11-unix/X%ld", n);
      if (ll_os_exists(socket)) e = (endpoint){1, NULL};
    }
  }
  free(display);
  return e;
}

static endpoint wayland(void) {
  endpoint e = {0, "WAYLAND_DISPLAY is not set to a local endpoint"};
  char *display = value("WAYLAND_DISPLAY");
  if (!display || strchr(display, '/') || strchr(display, '\\')) {
    free(display);
    return e;
  }
  char *dir = ll_os_getenv("XDG_RUNTIME_DIR");
  if (!dir || dir[0] != '/') e.reason = "XDG_RUNTIME_DIR is unavailable";
  else {
    char *path = ll_path_join(dir, display);
    e = path && ll_os_exists(path) ? (endpoint){1, NULL} : (endpoint){0, "local Wayland socket is unavailable"};
    free(path);
  }
  free(dir);
  free(display);
  return e;
}

static int native_desktop(const char **reason) {
  const char *os = ll_goos();
  *reason = "";
  if (strcmp(os, "darwin") == 0) {
    char *ci = value("CI");
    if (ci) {
      free(ci);
      *reason = "CI does not expose an interactive macOS desktop";
      return 0;
    }
    if (!is_set("SSH_CONNECTION") && !is_set("SSH_CLIENT") && !is_set("SSH_TTY")) return 1;
    char *display = value("DISPLAY");
    if (display) {
      free(display);
      return 1;
    }
    *reason = "SSH session does not expose a local macOS desktop";
    return 0;
  }
  if (strcmp(os, "windows") == 0) {
    char *session = value("SESSIONNAME");
    if (!session) {
      *reason = "SESSIONNAME is not set";
      return 0;
    }
    char *s = session;
    while (*s == ' ') s++;
    int services = strncmp(s, "Services", 8) == 0 || strncmp(s, "services", 8) == 0 || strncmp(s, "SERVICES", 8) == 0;
    free(session);
    if (services) {
      *reason = "service session is not interactive";
      return 0;
    }
    return 1;
  }
  *reason = "native desktop observation is unavailable";
  return 0;
}

static int containerized(void) {
  char *c = value("container");
  if (c) {
    free(c);
    return 1;
  }
  if (ll_os_exists("/.dockerenv")) return 1;
  FILE *f = fopen("/proc/1/cgroup", "r");
  if (!f) return 0;
  char buf[8192];
  size_t n = fread(buf, 1, sizeof buf - 1, f);
  fclose(f);
  buf[n] = 0;
  for (char *p = buf; *p; p++)
    if (*p >= 'A' && *p <= 'Z') *p = (char)(*p + 32);
  return strstr(buf, "/docker/") || strstr(buf, "/containerd/") || strstr(buf, "/podman/") || strstr(buf, "/kubepods/");
}

/* Whether a program is on PATH, as exec.LookPath finds it (absolute directories only). */
static int on_path(const char *name) {
  char *path = ll_os_getenv("PATH");
  if (!path) return 0;
  char sep = ll_path_list_separator(), file[256];
  snprintf(file, sizeof file, "%s%s", name, ll_executable_suffix());
  int found = 0;
  for (char *p = path; !found && p;) {
    char *end = strchr(p, sep);
    if (end) *end = 0;
    int abs = p[0] == '/' || (p[0] && p[1] == ':');
    if (abs) {
      char *candidate = ll_path_join(p, file);
      found = candidate && ll_is_executable(candidate);
      free(candidate);
    }
    p = end ? end + 1 : NULL;
  }
  free(path);
  return found;
}

static lj_value *endpoint_value(endpoint e) {
  lj_value *o = rec_obj();
  if (set(o, "available", rec_bool(e.available)) | (e.reason ? set(o, "reason", rec_str(e.reason)) : 0)) {
    lj_free(o);
    return NULL;
  }
  return o;
}

static int cmp_str(const void *a, const void *b) { return strcmp(*(const char *const *)a, *(const char *const *)b); }

int ll_collect_desktop(const ext_ledger_context *ctx, lj_value **out) {
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  ll_time now = ll_time_now();
  lj_buf at = {0};
  ll_time_write(&at, &now);
  if (at.oom) return EXT_LEDGER_IO;
  at.ptr[at.len - 1] = 0;
  const char *os = ll_goos(), *when = at.ptr + 1;
  endpoint ex = x11(), ew = wayland();
  int contained = containerized(), interactive;
  const char *reason = "";
  if (strcmp(os, "linux") == 0) {
    interactive = ex.available || ew.available;
    if (!interactive) reason = ex.reason && *ex.reason ? ex.reason : ew.reason ? ew.reason : "";
  } else {
    interactive = native_desktop(&reason);
  }
  /* What recording would need. */
  int encoder = on_path("ffmpeg"), capture = 0;
  const char *permission = "not-required", *status = "unavailable", *message = "";
  if (strcmp(os, "darwin") == 0) {
    const char *ignored;
    capture = native_desktop(&ignored);
    permission = "macos.screen-recording";
  } else if (strcmp(os, "windows") == 0) {
    const char *ignored;
    capture = native_desktop(&ignored);
  } else if (strcmp(os, "linux") == 0) {
    capture = ex.available || ew.available;
    if (ew.available && !ex.available) permission = "linux.pipewire-portal";
  }
  if (!capture) message = "native display capture is unavailable";
  else if (!encoder) message = "ffmpeg is unavailable";
  else if (strcmp(permission, "not-required") != 0) {
    status = "permission-required";
    message = "the embedding application must obtain capture permission";
  } else status = "ready";
  const char *caps[5];
  size_t ncaps = 0;
  if (interactive) caps[ncaps++] = "desktop.native";
  if (ex.available) caps[ncaps++] = "desktop.x11";
  if (ew.available) caps[ncaps++] = "desktop.wayland";
  if (contained) caps[ncaps++] = "desktop.container";
  if (strcmp(status, "ready") == 0) caps[ncaps++] = "desktop.recording";
  qsort(caps, ncaps, sizeof *caps, cmp_str);
  lj_value *recording = rec_obj(), *d = rec_obj();
  int bad = set(recording, "status", rec_str(status)) | set(recording, "encoder_available", rec_bool(encoder)) |
            set(recording, "capture_available", rec_bool(capture)) | set(recording, "permission", rec_str(permission)) |
            set(recording, "checked_at", rec_str(when));
  if (encoder) {
    lj_value *formats = lj_new(LJ_ARRAY);
    bad |= !formats || lj_array_push(formats, rec_str("mp4")) | lj_array_push(formats, rec_str("webm")) | set(recording, "formats", formats);
  }
  if (*message) bad |= set(recording, "message", rec_str(message));
  bad |= set(d, "platform", rec_str(os)) | set(d, "interactive", rec_bool(interactive)) | set(d, "containerized", rec_bool(contained)) |
         set(d, "x11", endpoint_value(ex)) | set(d, "wayland", endpoint_value(ew)) | set(d, "recording", recording) | set(d, "observed_at", rec_str(when));
  if (ncaps) {
    lj_value *c = lj_new(LJ_ARRAY);
    for (size_t i = 0; c && i < ncaps; i++) bad |= lj_array_push(c, rec_str(caps[i]));
    bad |= set(d, "capabilities", c);
  }
  if (reason && *reason) bad |= set(d, "reason", rec_str(reason));
  lj_buf_free(&at);
  if (bad) {
    lj_free(d);
    return EXT_LEDGER_IO;
  }
  *out = d;
  return 0;
}
