/* Displays, windows and the permissions observing them needs: CoreGraphics and the
 * Accessibility API on macOS, user32 on Windows, and X11 (loaded when it is
 * there, never linked) on Linux. A window is identified by its process
 * instance, so a reused PID never inherits a window. */
#if defined(__linux__)
#define _GNU_SOURCE
#endif
#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#endif

#include "collect.h"
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(__APPLE__)
#include <TargetConditionals.h>
#if !TARGET_OS_IPHONE && defined(__BLOCKS__) /* Apple's headers need blocks: Clang, not GCC */
#include <ApplicationServices/ApplicationServices.h>
#define LL_MACOS 1
#endif
#elif defined(__linux__) && !defined(__ANDROID__)
#include <dlfcn.h>
#define LL_X11 1
#endif

#if defined(LL_MACOS) || defined(_WIN32) || defined(LL_X11)
#define LL_WINDOWING 1
#endif

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

static int cmp_id(const void *a, const void *b) {
  return strcmp(rec_field(*(lj_value *const *)a, "id"), rec_field(*(lj_value *const *)b, "id"));
}

static lj_value *now_text(void) {
  ll_time t = ll_time_now();
  lj_buf b = {0};
  ll_time_write(&b, &t);
  lj_value *v = b.oom || b.len < 2 ? NULL : rec_strn(b.ptr + 1, b.len - 2);
  lj_buf_free(&b);
  return v;
}

#if defined(LL_WINDOWING)
/* kind:digest(kind \0 native), the stable surface identity. */
static void surface_id(char out[64], const char *kind, const char *native) {
  lj_buf b = {0};
  lj_buf_puts(&b, kind);
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, native);
  char d[25];
  rec_digest(b.ptr ? b.ptr : "", b.len, d);
  lj_buf_free(&b);
  snprintf(out, 64, "%s:%s", kind, d);
}

static lj_value *bounds(long x, long y, long w, long h) {
  lj_value *b = rec_obj();
  if (set(b, "x", rec_num((double)x)) | set(b, "y", rec_num((double)y)) | set(b, "width", rec_num((double)w)) | set(b, "height", rec_num((double)h))) {
    lj_free(b);
    return NULL;
  }
  return b;
}

/* ---- windows and their process instances ---- */

typedef struct {
  int64_t pid;
  char start[64];
} start_cache;

typedef struct {
  start_cache *items;
  size_t n, cap;
} starts;

static const char *start_of(starts *s, int64_t pid) {
  for (size_t i = 0; i < s->n; i++)
    if (s->items[i].pid == pid) return s->items[i].start;
  if (s->n == s->cap) {
    s->cap = s->cap ? s->cap * 2 : 32;
    start_cache *g = realloc(s->items, s->cap * sizeof *g);
    if (!g) return "";
    s->items = g;
  }
  start_cache *c = &s->items[s->n++];
  c->pid = pid;
  if (ll_process_start(pid, c->start, sizeof c->start)) c->start[0] = 0;
  return c->start;
}

/* Appends a window, or returns 1 when its process instance is unknown (it is then left out). */
static int add_window(lj_value *windows, starts *s, int64_t pid, unsigned long long native, long x, long y, long w, long h) {
  const char *start = start_of(s, pid);
  if (!*start) return 1;
  char id_native[160], id[64];
  snprintf(id_native, sizeof id_native, "%lld", (long long)pid);
  size_t at = strlen(id_native);
  id_native[at] = 0; /* pid \0 start \0 native: built in a buffer, since it holds NULs */
  lj_buf b = {0};
  lj_buf_puts(&b, id_native);
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, start);
  lj_buf_putc(&b, 0);
  char nat[32];
  snprintf(nat, sizeof nat, "%llu", native);
  lj_buf_puts(&b, nat);
  lj_buf k = {0};
  lj_buf_puts(&k, "window");
  lj_buf_putc(&k, 0);
  lj_buf_put(&k, b.ptr ? b.ptr : "", b.len);
  char d[25];
  rec_digest(k.ptr ? k.ptr : "", k.len, d);
  lj_buf_free(&b);
  lj_buf_free(&k);
  snprintf(id, sizeof id, "window:%s", d);
  lj_value *win = rec_obj();
  if (set(win, "id", rec_str(id)) | set(win, "pid", rec_num((double)pid)) | set(win, "process_start_id", rec_str(start)) |
      set(win, "bounds", bounds(x, y, w, h)) | set(win, "on_screen", rec_bool(1)) | lj_array_push(windows, win))
    return -1;
  return 0;
}


#endif

#define MAX_DISPLAYS 64
#define MAX_WINDOWS 4096

/* ---- macOS ---- */
#if defined(LL_MACOS)
static int platform(lj_value *displays, lj_value **dstatus, lj_value *windows, lj_value **wstatus, const char **space) {
  *space = "desktop-global-points";
  uint32_t count = 0;
  CGError err = CGGetActiveDisplayList(0, NULL, &count);
  char detail[96];
  if (err == kCGErrorSuccess) {
    CGDirectDisplayID *ids = calloc(count ? count : 1, sizeof *ids);
    if (ids) err = CGGetActiveDisplayList(count, ids, &count);
    if (ids && err == kCGErrorSuccess) {
      uint32_t capacity = count < MAX_DISPLAYS ? count : MAX_DISPLAYS;
      for (uint32_t i = 0; i < capacity; i++) {
        CGRect b = CGDisplayBounds(ids[i]);
        long w = (long)b.size.width, h = (long)b.size.height;
        if (w <= 0 || h <= 0) continue;
        char native[24], id[64];
        snprintf(native, sizeof native, "%u", ids[i]);
        surface_id(id, "display", native);
        lj_value *d = rec_obj();
        double scale = b.size.width > 0 ? (double)CGDisplayPixelsWide(ids[i]) / b.size.width : 0;
        int bad = set(d, "id", rec_str(id)) | set(d, "bounds", bounds((long)b.origin.x, (long)b.origin.y, w, h)) |
                  set(d, "primary", rec_bool(ids[i] == CGMainDisplayID()));
        if (scale != 0) bad |= set(d, "scale", rec_num(scale));
        if (bad || lj_array_push(displays, d)) {
          free(ids);
          return EXT_LEDGER_IO;
        }
      }
      *dstatus = count > capacity ? ll_status("partial", NULL, "display limit reached") : ll_status("complete", NULL, NULL);
    }
    free(ids);
  }
  if (!*dstatus) {
    snprintf(detail, sizeof detail, "CoreGraphics display enumeration failed (%d)", (int)err);
    *dstatus = ll_status("unavailable", "no_device", detail);
  }
  CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
  if (!list) {
    *wstatus = ll_status("unavailable", "no_device", "CoreGraphics window enumeration failed (-2)");
    return 0;
  }
  starts cache = {0};
  int found = 0, stored = 0, missing = 0, rc = 0;
  for (CFIndex i = 0; !rc && i < CFArrayGetCount(list); i++) {
    CFDictionaryRef item = CFArrayGetValueAtIndex(list, i);
    int32_t pid = 0;
    uint32_t number = 0;
    CGRect b = CGRectZero;
    CFNumberRef pv = CFDictionaryGetValue(item, kCGWindowOwnerPID), nv = CFDictionaryGetValue(item, kCGWindowNumber);
    CFDictionaryRef bv = CFDictionaryGetValue(item, kCGWindowBounds);
    if (!pv || !nv || !bv || !CFNumberGetValue(pv, kCFNumberSInt32Type, &pid) || !CFNumberGetValue(nv, kCFNumberSInt32Type, &number) ||
        !CGRectMakeWithDictionaryRepresentation(bv, &b) || pid <= 0 || b.size.width <= 0 || b.size.height <= 0)
      continue;
    found++;
    if (stored >= MAX_WINDOWS) continue;
    stored++;
    long w = (long)b.size.width, h = (long)b.size.height;
    if (w <= 0 || h <= 0) continue;
    int r = add_window(windows, &cache, pid, number, (long)b.origin.x, (long)b.origin.y, w, h);
    if (r < 0) rc = EXT_LEDGER_IO;
    missing |= r == 1;
  }
  CFRelease(list);
  free(cache.items);
  if (rc) return rc;
  if (found > stored) *wstatus = ll_status("partial", NULL, "window limit reached");
  else if (missing) *wstatus = ll_status("partial", NULL, "some window process generations were unavailable");
  else *wstatus = ll_status("complete", NULL, NULL);
  return 0;
}

static int permissions(lj_value **accessibility, lj_value **recording) {
  *accessibility = AXIsProcessTrusted() ? ll_status("ready", NULL, NULL) : ll_status("denied", NULL, "Accessibility access is not granted");
  *recording = CGPreflightScreenCaptureAccess() ? ll_status("ready", NULL, NULL) : ll_status("denied", NULL, "Screen Recording access is not granted");
  return set(*accessibility, "scope", rec_str("macos.accessibility")) | set(*recording, "scope", rec_str("macos.screen-recording"));
}

/* ---- Windows ---- */
#elif defined(_WIN32)
typedef struct {
  lj_value *list;
  int truncated, failed;
} display_walk;

static BOOL CALLBACK each_monitor(HMONITOR monitor, HDC dc, LPRECT rect, LPARAM arg) {
  (void)dc;
  (void)rect;
  display_walk *w = (display_walk *)arg;
  if (w->list->u.array.len >= MAX_DISPLAYS) {
    w->truncated = 1;
    return TRUE;
  }
  MONITORINFO info;
  info.cbSize = sizeof info;
  if (!GetMonitorInfoW(monitor, &info)) {
    w->failed = 1;
    return FALSE;
  }
  long width = info.rcMonitor.right - info.rcMonitor.left, height = info.rcMonitor.bottom - info.rcMonitor.top;
  if (width <= 0 || height <= 0) return TRUE;
  char native[32], id[64];
  snprintf(native, sizeof native, "%llu", (unsigned long long)(uintptr_t)monitor);
  surface_id(id, "display", native);
  lj_value *d = rec_obj();
  if (set(d, "id", rec_str(id)) | set(d, "bounds", bounds(info.rcMonitor.left, info.rcMonitor.top, width, height)) |
      set(d, "primary", rec_bool((info.dwFlags & MONITORINFOF_PRIMARY) != 0)) | lj_array_push(w->list, d)) {
    w->failed = 1;
    return FALSE;
  }
  return TRUE;
}

typedef struct {
  lj_value *list;
  starts cache;
  int truncated, failed;
} window_walk;

static BOOL CALLBACK each_window(HWND window, LPARAM arg) {
  window_walk *w = (window_walk *)arg;
  if (!IsWindowVisible(window)) return TRUE;
  RECT r;
  if (!GetWindowRect(window, &r) || r.right <= r.left || r.bottom <= r.top) return TRUE;
  DWORD pid = 0;
  GetWindowThreadProcessId(window, &pid);
  if (!pid) return TRUE;
  if (w->list->u.array.len >= MAX_WINDOWS) {
    w->truncated = 1;
    return TRUE;
  }
  if (add_window(w->list, &w->cache, pid, (unsigned long long)(uintptr_t)window, r.left, r.top, r.right - r.left, r.bottom - r.top) < 0) {
    w->failed = 1;
    return FALSE;
  }
  return TRUE;
}

static int platform(lj_value *displays, lj_value **dstatus, lj_value *windows, lj_value **wstatus, const char **space) {
  *space = "virtual-desktop-pixels";
  display_walk dw = {displays, 0, 0};
  if (!EnumDisplayMonitors(NULL, NULL, each_monitor, (LPARAM)&dw) && !dw.failed) {
    *dstatus = ll_status("unavailable", "no_device", "EnumDisplayMonitors failed");
  } else if (dw.failed) {
    return EXT_LEDGER_IO;
  } else {
    *dstatus = dw.truncated ? ll_status("partial", NULL, "display limit reached") : ll_status("complete", NULL, NULL);
  }
  window_walk ww = {windows, {0}, 0, 0};
  BOOL ok = EnumWindows(each_window, (LPARAM)&ww);
  free(ww.cache.items);
  if (ww.failed) return EXT_LEDGER_IO;
  if (!ok) *wstatus = ll_status("unavailable", "no_device", "EnumWindows failed");
  else *wstatus = ww.truncated ? ll_status("partial", NULL, "window limit reached") : ll_status("complete", NULL, NULL);
  return 0;
}

static int permissions(lj_value **accessibility, lj_value **recording) {
  *accessibility = ll_status("not-required", NULL, NULL);
  *recording = ll_status("not-required", NULL, NULL);
  return set(*accessibility, "scope", rec_str("windows.ui-observation")) | set(*recording, "scope", rec_str("windows.graphics-capture"));
}

/* ---- Linux (X11) ---- */
#elif defined(LL_X11)
typedef struct _XDisplay Display;
typedef unsigned long XID;
typedef XID Window;
typedef unsigned long Atom;
typedef int Bool;
typedef struct {
  int x, y, width, height, border_width, depth;
  void *visual;
  Window root;
  int class, bit_gravity, win_gravity, backing_store;
  unsigned long backing_planes, backing_pixel;
  Bool save_under;
  long colormap;
  Bool map_installed;
  int map_state;
  long all_event_masks, your_event_mask, do_not_propagate_mask;
  Bool override_redirect;
  void *screen;
} xattrs;

typedef struct {
  void *lib;
  Display *(*open_display)(const char *);
  int (*close_display)(Display *);
  int (*default_screen)(Display *);
  Window (*root_window)(Display *, int);
  int (*display_width)(Display *, int);
  int (*display_height)(Display *, int);
  Atom (*intern_atom)(Display *, const char *, Bool);
  int (*get_property)(Display *, Window, Atom, long, long, Bool, Atom, Atom *, int *, unsigned long *, unsigned long *, unsigned char **);
  int (*get_attributes)(Display *, Window, xattrs *);
  Bool (*translate)(Display *, Window, Window, int, int, int *, int *, Window *);
  int (*query_tree)(Display *, Window, Window *, Window *, Window **, unsigned int *);
  int (*free_value)(void *);
} x11;

static int x11_load(x11 *x) {
  x->lib = dlopen("libX11.so.6", RTLD_LAZY | RTLD_LOCAL);
  if (!x->lib) return 0;
#define LOAD(field, name)                         \
  *(void **)(&x->field) = dlsym(x->lib, #name); \
  if (!x->field) return 0
  LOAD(open_display, XOpenDisplay);
  LOAD(close_display, XCloseDisplay);
  LOAD(default_screen, XDefaultScreen);
  LOAD(root_window, XRootWindow);
  LOAD(display_width, XDisplayWidth);
  LOAD(display_height, XDisplayHeight);
  LOAD(intern_atom, XInternAtom);
  LOAD(get_property, XGetWindowProperty);
  LOAD(get_attributes, XGetWindowAttributes);
  LOAD(translate, XTranslateCoordinates);
  LOAD(query_tree, XQueryTree);
  LOAD(free_value, XFree);
#undef LOAD
  return 1;
}

static int x11_property(x11 *x, Display *d, Window w, Atom atom, unsigned char **data, unsigned long *count) {
  Atom actual = 0;
  int format = 0;
  unsigned long remaining = 0;
  *data = NULL;
  *count = 0;
  if (!atom || x->get_property(d, w, atom, 0, 65536, 0, 0, &actual, &format, count, &remaining, data) != 0) return 0;
  if (!*data || format != 32) {
    if (*data) x->free_value(*data);
    *data = NULL;
    *count = 0;
    return 0;
  }
  return 1;
}

static int platform(lj_value *displays, lj_value **dstatus, lj_value *windows, lj_value **wstatus, const char **space) {
  *space = "desktop-global-pixels";
  char *display_env = ll_os_getenv("DISPLAY"), *wayland = ll_os_getenv("WAYLAND_DISPLAY");
  int has_display = display_env && strspn(display_env, " \t") != strlen(display_env);
  int has_wayland = wayland && strspn(wayland, " \t") != strlen(wayland);
  free(display_env);
  free(wayland);
  if (!has_display) {
    const char *detail = has_wayland ? "the Wayland compositor does not expose global display or window geometry" : "DISPLAY is not set";
    *dstatus = ll_status("unsupported", "unsupported", detail);
    *wstatus = ll_status("unsupported", "unsupported", detail);
    return 0;
  }
  x11 x = {0};
  if (!x11_load(&x)) {
    if (x.lib) dlclose(x.lib);
    *dstatus = ll_status("unavailable", "tool_missing", "libX11 is unavailable");
    *wstatus = ll_status("unavailable", "tool_missing", "libX11 is unavailable");
    return 0;
  }
  Display *d = x.open_display(NULL);
  if (!d) {
    dlclose(x.lib);
    *dstatus = ll_status("unavailable", "no_device", "the X11 display could not be opened");
    *wstatus = ll_status("unavailable", "no_device", "the X11 display could not be opened");
    return 0;
  }
  int screen = x.default_screen(d), width = x.display_width(d, screen), height = x.display_height(d, screen);
  Window root = x.root_window(d, screen);
  int rc = 0;
  if (width > 0 && height > 0) {
    char native[32], id[64];
    snprintf(native, sizeof native, "x11:%d", screen);
    surface_id(id, "display", native);
    lj_value *disp = rec_obj();
    if (set(disp, "id", rec_str(id)) | set(disp, "bounds", bounds(0, 0, width, height)) | set(disp, "primary", rec_bool(1)) | lj_array_push(displays, disp))
      rc = EXT_LEDGER_IO;
  }
  *dstatus = ll_status("partial", NULL, "X11 root geometry does not expose monitor partitions");
  Atom clients = x.intern_atom(d, "_NET_CLIENT_LIST", 1), pid_atom = x.intern_atom(d, "_NET_WM_PID", 1);
  unsigned char *property = NULL;
  unsigned long count = 0;
  Window *list = NULL, ignored_root = 0, ignored_parent = 0;
  unsigned int children = 0;
  if (x11_property(&x, d, root, clients, &property, &count)) {
    list = (Window *)property;
    children = (unsigned int)count;
  } else if (!x.query_tree(d, root, &ignored_root, &ignored_parent, &list, &children)) {
    list = NULL;
    children = 0;
  }
  starts cache = {0};
  int total = 0, stored = 0, missing = 0;
  for (unsigned int i = 0; !rc && i < children; i++) {
    xattrs a;
    if (!x.get_attributes(d, list[i], &a) || a.map_state != 2 || a.width <= 0 || a.height <= 0) continue;
    unsigned char *pp = NULL;
    unsigned long pc = 0;
    if (!x11_property(&x, d, list[i], pid_atom, &pp, &pc) || pc < 1) {
      if (pp) x.free_value(pp);
      continue;
    }
    int32_t pid = (int32_t)((unsigned long *)pp)[0];
    x.free_value(pp);
    if (pid <= 0) continue;
    total++;
    if (stored >= MAX_WINDOWS) continue;
    stored++;
    int wx = a.x, wy = a.y;
    Window child = 0;
    x.translate(d, list[i], root, 0, 0, &wx, &wy, &child);
    int r = add_window(windows, &cache, pid, list[i], wx, wy, a.width, a.height);
    if (r < 0) rc = EXT_LEDGER_IO;
    missing |= r == 1;
  }
  if (list) x.free_value(list);
  free(cache.items);
  x.close_display(d);
  dlclose(x.lib);
  if (rc) return rc;
  if (total > stored) *wstatus = ll_status("partial", NULL, "window limit reached");
  else if (missing) *wstatus = ll_status("partial", NULL, "some window process generations were unavailable");
  else *wstatus = ll_status("complete", NULL, NULL);
  return 0;
}
#else
static int platform(lj_value *displays, lj_value **dstatus, lj_value *windows, lj_value **wstatus, const char **space) {
  (void)displays;
  (void)windows;
  *space = "desktop-global";
  *dstatus = ll_status("unsupported", "unsupported", "native display enumeration is unavailable on this platform");
  *wstatus = ll_status("unsupported", "unsupported", "native window enumeration is unavailable on this platform");
  return 0;
}
#endif

#if !defined(LL_MACOS) && !defined(_WIN32)
static int permissions(lj_value **accessibility, lj_value **recording) {
#if defined(__APPLE__)
  *accessibility = ll_status("unsupported", "unsupported", "system-wide accessibility observation is unavailable on iOS");
  *recording = ll_status("unsupported", "unsupported", "screen recording of other apps is unavailable on iOS");
  return set(*accessibility, "scope", rec_str("ios.accessibility")) | set(*recording, "scope", rec_str("ios.screen-recording"));
#else
  *accessibility = ll_status("unsupported", "unsupported", "AT-SPI observation is not available in this build");
  int bad = set(*accessibility, "scope", rec_str("linux.at-spi"));
  char *display = ll_os_getenv("DISPLAY"), *wayland = ll_os_getenv("WAYLAND_DISPLAY");
  if (display && strspn(display, " \t") != strlen(display)) {
    *recording = ll_status("not-required", NULL, NULL);
    bad |= set(*recording, "scope", rec_str("linux.x11-capture"));
  } else if (wayland && strspn(wayland, " \t") != strlen(wayland)) {
    *recording = ll_status("not-determined", NULL, "portal consent is determined when capture is requested");
    bad |= set(*recording, "scope", rec_str("linux.pipewire-portal"));
  } else {
    *recording = ll_status("unavailable", "no_device", "no local graphical session is visible");
  }
  free(display);
  free(wayland);
  return bad;
#endif
}
#endif

int ll_collect_surfaces(const ext_ledger_context *ctx, lj_value **out) {
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  lj_value *at = now_text(), *displays = lj_new(LJ_ARRAY), *windows = lj_new(LJ_ARRAY), *dstatus = NULL, *wstatus = NULL;
  const char *space = "";
  if (!at || !displays || !windows) rc = EXT_LEDGER_IO;
  if (!rc) rc = platform(displays, &dstatus, windows, &wstatus, &space);
  if (!rc && (!dstatus || !wstatus)) rc = EXT_LEDGER_IO;
  lj_value *s = rc ? NULL : rec_obj();
  if (!rc) {
    if (displays->u.array.len > 1) qsort(displays->u.array.items, displays->u.array.len, sizeof *displays->u.array.items, cmp_id);
    if (windows->u.array.len > 1) qsort(windows->u.array.items, windows->u.array.len, sizeof *windows->u.array.items, cmp_id);
    int bad = set(dstatus, "observed_at", lj_clone(at)) | set(wstatus, "observed_at", lj_clone(at)) | set(s, "platform", rec_str(ll_goos())) |
              set(s, "coordinate_space", rec_str(space)) | set(s, "observed_at", at) | set(s, "displays", displays) | set(s, "windows", windows) |
              set(s, "display_status", dstatus) | set(s, "window_status", wstatus);
    at = displays = windows = dstatus = wstatus = NULL;
    if (bad) rc = EXT_LEDGER_IO;
  }
  lj_free(at);
  lj_free(displays);
  lj_free(windows);
  lj_free(dstatus);
  lj_free(wstatus);
  if (rc) {
    lj_free(s);
    return rc;
  }
  *out = s;
  return 0;
}

int ll_collect_permissions(const ext_ledger_context *ctx, lj_value **out) {
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  lj_value *accessibility = NULL, *recording = NULL, *p = rec_obj();
  int bad = permissions(&accessibility, &recording) | set(p, "platform", rec_str(ll_goos())) | set(p, "observed_at", now_text()) |
            set(p, "accessibility", accessibility) | set(p, "screen_recording", recording);
  if (bad) {
    lj_free(p);
    return EXT_LEDGER_IO;
  }
  *out = p;
  return 0;
}
