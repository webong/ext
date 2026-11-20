/* The focused application's accessibility tree, as res/system's
 * DiscoverAccessibility reports it with its default bounds (1024 elements,
 * depth 16, no text): the Accessibility API on macOS, the foreground window and
 * its child windows on Windows. Each element is identified by its process
 * instance and its path in the tree. */
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
#if !TARGET_OS_IPHONE && defined(__BLOCKS__)
#include <ApplicationServices/ApplicationServices.h>
#define LL_AX 1
#endif
#endif

#define MAX_ELEMENTS 1024
#define MAX_DEPTH 16

static int set(lj_value *o, const char *key, lj_value *v) { return rec_set(o, key, v); }

static int cmp_id(const void *a, const void *b) {
  return strcmp(rec_field(*(lj_value *const *)a, "id"), rec_field(*(lj_value *const *)b, "id"));
}

#if defined(LL_AX) || defined(_WIN32)
/* accessibility:digest("accessibility" \0 pid \0 start \0 path). */
static void element_id(char out[64], int64_t pid, const char *start, const char *path) {
  lj_buf b = {0};
  char p[24];
  snprintf(p, sizeof p, "%lld", (long long)pid);
  lj_buf_puts(&b, "accessibility");
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, p);
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, start);
  lj_buf_putc(&b, 0);
  lj_buf_puts(&b, path);
  char d[25];
  rec_digest(b.ptr ? b.ptr : "", b.len, d);
  lj_buf_free(&b);
  snprintf(out, 64, "accessibility:%s", d);
}

static lj_value *element(const char *id, const char *parent, int64_t pid, const char *start, const char *role) {
  lj_value *e = rec_obj();
  if (set(e, "id", rec_str(id)) | (parent && *parent ? set(e, "parent_id", rec_str(parent)) : 0) | set(e, "pid", rec_num((double)pid)) |
      set(e, "process_start_id", rec_str(start)) | set(e, "role", rec_str(role))) {
    lj_free(e);
    return NULL;
  }
  return e;
}

static lj_value *rect(long x, long y, long w, long h) {
  lj_value *b = rec_obj();
  if (set(b, "x", rec_num((double)x)) | set(b, "y", rec_num((double)y)) | set(b, "width", rec_num((double)w)) | set(b, "height", rec_num((double)h))) {
    lj_free(b);
    return NULL;
  }
  return b;
}
#endif

#if defined(LL_AX)
static int ax_string(AXUIElementRef e, CFStringRef name, char *out, size_t n) {
  CFTypeRef v = NULL;
  out[0] = 0;
  if (AXUIElementCopyAttributeValue(e, name, &v) != kAXErrorSuccess || !v) return 0;
  int ok = CFGetTypeID(v) == CFStringGetTypeID() && CFStringGetCString(v, out, (CFIndex)n, kCFStringEncodingUTF8);
  CFRelease(v);
  if (!ok) out[0] = 0;
  return ok;
}

static int ax_bool(AXUIElementRef e, CFStringRef name, int *out) {
  CFTypeRef v = NULL;
  if (AXUIElementCopyAttributeValue(e, name, &v) != kAXErrorSuccess || !v) return 0;
  int ok = CFGetTypeID(v) == CFBooleanGetTypeID();
  if (ok) *out = CFBooleanGetValue(v) ? 1 : 0;
  CFRelease(v);
  return ok;
}

static CFArrayRef ax_children(AXUIElementRef e) {
  CFTypeRef v = NULL;
  if (AXUIElementCopyAttributeValue(e, kAXChildrenAttribute, &v) != kAXErrorSuccess || !v) return NULL;
  if (CFGetTypeID(v) != CFArrayGetTypeID()) {
    CFRelease(v);
    return NULL;
  }
  return v;
}

static int cmp_strings(const void *a, const void *b) {
  return strcmp((*(lj_value *const *)a)->u.string.ptr, (*(lj_value *const *)b)->u.string.ptr);
}

typedef struct {
  AXUIElementRef element;
  char parent[64];
  char *path;
  int depth;
} node;

static int platform(lj_value *elements, lj_value **status) {
  if (!AXIsProcessTrusted()) {
    *status = ll_status("permission-denied", "permission", "macOS Accessibility access is not granted");
    return 0;
  }
  AXUIElementRef system = AXUIElementCreateSystemWide();
  CFTypeRef focused = NULL;
  if (system) {
    AXError err = AXUIElementCopyAttributeValue(system, kAXFocusedApplicationAttribute, &focused);
    CFRelease(system);
    if (err != kAXErrorSuccess || !focused || CFGetTypeID(focused) != AXUIElementGetTypeID()) {
      if (focused) CFRelease(focused);
      focused = NULL;
    }
  }
  if (!focused) {
    *status = ll_status("unavailable", "no_device", "focused application is unavailable");
    return 0;
  }
  AXUIElementRef root = (AXUIElementRef)focused;
  pid_t pid = 0;
  char start[64];
  if (AXUIElementGetPid(root, &pid) != kAXErrorSuccess || pid <= 0) {
    CFRelease(root);
    *status = ll_status("unavailable", "no_device", "focused application identity is unavailable");
    return 0;
  }
  if (ll_process_start(pid, start, sizeof start)) {
    CFRelease(root);
    *status = ll_status("unavailable", "no_device", "focused application generation is unavailable");
    return 0;
  }
  size_t head = 0, tail = 0, cap = 64;
  node *queue = malloc(cap * sizeof *queue);
  int rc = queue ? 0 : EXT_LEDGER_IO, truncated = 0;
  if (!rc) {
    queue[tail].element = root;
    queue[tail].parent[0] = 0;
    queue[tail].path = strdup("0");
    queue[tail++].depth = 0;
  }
  while (!rc && head < tail) {
    node n = queue[head++];
    if ((int)elements->u.array.len >= MAX_ELEMENTS) {
      truncated = 1;
      if (n.element != root) CFRelease(n.element);
      free(n.path);
      continue;
    }
    char id[64], role[4096], identifier[4096];
    element_id(id, pid, start, n.path ? n.path : "");
    if (!ax_string(n.element, kAXRoleAttribute, role, sizeof role)) strcpy(role, "unknown");
    if (!*role) strcpy(role, "unknown");
    lj_value *e = element(id, n.parent, pid, start, role);
    if (!e) rc = EXT_LEDGER_IO;
    if (!rc && ax_string(n.element, kAXIdentifierAttribute, identifier, sizeof identifier) && *identifier) rc |= set(e, "identifier", rec_str(identifier));
    CFArrayRef actions = NULL;
    if (!rc && AXUIElementCopyActionNames(n.element, &actions) == kAXErrorSuccess && actions) {
      lj_value *list = lj_new(LJ_ARRAY);
      for (CFIndex i = 0; list && i < CFArrayGetCount(actions); i++) {
        CFStringRef a = CFArrayGetValueAtIndex(actions, i);
        char value[256];
        if (a && CFGetTypeID(a) == CFStringGetTypeID() && CFStringGetCString(a, value, sizeof value, kCFStringEncodingUTF8) && *value)
          rc |= lj_array_push(list, rec_str(value));
      }
      CFRelease(actions);
      if (list && list->u.array.len) {
        qsort(list->u.array.items, list->u.array.len, sizeof *list->u.array.items, cmp_strings);
        rc |= set(e, "actions", list);
      } else {
        lj_free(list);
      }
    }
    int flag;
    if (!rc && ax_bool(n.element, kAXEnabledAttribute, &flag)) rc |= set(e, "enabled", rec_bool(flag));
    if (!rc && ax_bool(n.element, kAXFocusedAttribute, &flag)) rc |= set(e, "focused", rec_bool(flag));
    CFTypeRef position = NULL, size = NULL;
    if (!rc && AXUIElementCopyAttributeValue(n.element, kAXPositionAttribute, &position) == kAXErrorSuccess && position &&
        AXUIElementCopyAttributeValue(n.element, kAXSizeAttribute, &size) == kAXErrorSuccess && size) {
      CGPoint p = CGPointZero;
      CGSize s = CGSizeZero;
      if (CFGetTypeID(position) == AXValueGetTypeID() && CFGetTypeID(size) == AXValueGetTypeID() && AXValueGetType(position) == kAXValueCGPointType &&
          AXValueGetType(size) == kAXValueCGSizeType && AXValueGetValue(position, kAXValueCGPointType, &p) &&
          AXValueGetValue(size, kAXValueCGSizeType, &s) && s.width > 0 && s.height > 0)
        rc |= set(e, "bounds", rect((long)p.x, (long)p.y, (long)s.width, (long)s.height));
    }
    if (position) CFRelease(position);
    if (size) CFRelease(size);
    if (!rc && lj_array_push(elements, e)) rc = EXT_LEDGER_IO;
    else if (rc) lj_free(e);
    CFArrayRef children = ax_children(n.element);
    if (children && n.depth < MAX_DEPTH) {
      for (CFIndex i = 0; !rc && i < CFArrayGetCount(children); i++) {
        CFTypeRef child = CFArrayGetValueAtIndex(children, i);
        if (!child || CFGetTypeID(child) != AXUIElementGetTypeID()) continue;
        if (tail == cap) {
          cap *= 2;
          node *g = realloc(queue, cap * sizeof *g);
          if (!g) {
            rc = EXT_LEDGER_IO;
            break;
          }
          queue = g;
        }
        CFRetain(child);
        size_t pl = strlen(n.path ? n.path : "") + 24;
        char *path = malloc(pl);
        if (path) snprintf(path, pl, "%s/%ld", n.path ? n.path : "", (long)i);
        queue[tail].element = (AXUIElementRef)child;
        snprintf(queue[tail].parent, sizeof queue[tail].parent, "%s", id);
        queue[tail].path = path;
        queue[tail++].depth = n.depth + 1;
      }
    } else if (children && CFArrayGetCount(children) > 0) {
      truncated = 1;
    }
    if (children) CFRelease(children);
    if (n.element != root) CFRelease(n.element);
    free(n.path);
  }
  for (size_t i = head; i < tail; i++) {
    if (queue[i].element != root) CFRelease(queue[i].element);
    free(queue[i].path);
  }
  free(queue);
  CFRelease(root);
  if (rc) return rc;
  *status = truncated ? ll_status("partial", NULL, "accessibility element or depth limit reached") : ll_status("complete", NULL, NULL);
  return 0;
}
#elif defined(_WIN32)
typedef struct {
  HWND root;
  HWND *handles;
  size_t n;
  int truncated, failed;
} walk;

static int depth_of(HWND window, HWND root) {
  int depth = 0;
  while (window && window != root && depth <= 65536) {
    window = GetParent(window);
    depth++;
  }
  return depth;
}

static BOOL CALLBACK each_child(HWND child, LPARAM arg) {
  walk *w = (walk *)arg;
  if (depth_of(child, w->root) > MAX_DEPTH) {
    w->truncated = 1;
    return TRUE;
  }
  if (w->n >= MAX_ELEMENTS) {
    w->truncated = 1;
    return FALSE;
  }
  w->handles[w->n++] = child;
  return TRUE;
}

static int platform(lj_value *elements, lj_value **status) {
  HWND window = GetForegroundWindow();
  if (!window) {
    *status = ll_status("unavailable", "no_device", "focused window is unavailable");
    return 0;
  }
  DWORD pid = 0;
  GetWindowThreadProcessId(window, &pid);
  if (!pid) {
    *status = ll_status("unavailable", "no_device", "focused application identity is unavailable");
    return 0;
  }
  char start[64];
  if (ll_process_start(pid, start, sizeof start)) {
    *status = ll_status("unavailable", "no_device", "focused application generation is unavailable");
    return 0;
  }
  walk w = {window, malloc((MAX_ELEMENTS + 1) * sizeof(HWND)), 0, 0, 0};
  if (!w.handles) return EXT_LEDGER_IO;
  w.handles[w.n++] = window;
  EnumChildWindows(window, each_child, (LPARAM)&w);
  char (*ids)[64] = malloc(w.n * sizeof *ids);
  if (!ids) {
    free(w.handles);
    return EXT_LEDGER_IO;
  }
  for (size_t i = 0; i < w.n; i++) {
    char native[32];
    snprintf(native, sizeof native, "%llu", (unsigned long long)(uintptr_t)w.handles[i]);
    element_id(ids[i], pid, start, native);
  }
  int rc = 0;
  for (size_t i = 0; !rc && i < w.n; i++) {
    HWND h = w.handles[i];
    wchar_t cls[256];
    int len = GetClassNameW(h, cls, 256);
    char role[1024] = "window";
    if (len > 0) WideCharToMultiByte(CP_UTF8, 0, cls, len, role, sizeof role - 1, NULL, NULL), role[sizeof role - 1] = 0;
    if (len > 0) {
      int n = WideCharToMultiByte(CP_UTF8, 0, cls, len, role, sizeof role - 1, NULL, NULL);
      role[n > 0 ? n : 0] = 0;
      if (!*role) strcpy(role, "window");
    }
    HWND parent = GetParent(h);
    const char *parent_id = "";
    for (size_t k = 0; k < w.n; k++)
      if (w.handles[k] == parent) parent_id = ids[k];
    lj_value *e = element(ids[i], parent_id, pid, start, role);
    if (!e) {
      rc = EXT_LEDGER_IO;
      break;
    }
    int control = GetDlgCtrlID(h);
    if (control != 0) {
      char c[24];
      snprintf(c, sizeof c, "%d", control);
      rc |= set(e, "identifier", rec_str(c));
    }
    RECT r;
    if (GetWindowRect(h, &r) && r.right > r.left && r.bottom > r.top) rc |= set(e, "bounds", rect(r.left, r.top, r.right - r.left, r.bottom - r.top));
    rc |= set(e, "enabled", rec_bool(IsWindowEnabled(h) != 0)) | set(e, "focused", rec_bool(h == window));
    if (rc || lj_array_push(elements, e)) rc = EXT_LEDGER_IO;
  }
  free(ids);
  free(w.handles);
  if (rc) return rc;
  *status = w.truncated ? ll_status("partial", NULL, "accessibility element limit reached") : ll_status("complete", NULL, NULL);
  return 0;
}
#else
static int platform(lj_value *elements, lj_value **status) {
  (void)elements;
  *status = ll_status("unsupported", "unsupported", "native accessibility tree observation is unavailable on this platform");
  return 0;
}
#endif

int ll_collect_accessibility(const ext_ledger_context *ctx, lj_value **out) {
  *out = NULL;
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  ll_time now = ll_time_now();
  lj_buf at = {0};
  ll_time_write(&at, &now);
  lj_value *elements = lj_new(LJ_ARRAY), *status = NULL;
  if (at.oom || !elements) rc = EXT_LEDGER_IO;
  if (!rc) rc = platform(elements, &status);
  if (!rc && !status) rc = EXT_LEDGER_IO;
  lj_value *a = NULL;
  if (!rc) {
    at.ptr[at.len - 1] = 0;
    if (elements->u.array.len > 1) qsort(elements->u.array.items, elements->u.array.len, sizeof *elements->u.array.items, cmp_id);
    a = rec_obj();
    if (set(status, "observed_at", rec_str(at.ptr + 1)) | set(a, "platform", rec_str(ll_goos())) | set(a, "observed_at", rec_str(at.ptr + 1)) |
        set(a, "status", status) | set(a, "elements", elements))
      rc = EXT_LEDGER_IO;
    status = elements = NULL;
  }
  lj_buf_free(&at);
  lj_free(status);
  lj_free(elements);
  if (rc) {
    lj_free(a);
    return rc;
  }
  *out = a;
  return 0;
}
