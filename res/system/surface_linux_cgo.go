//go:build linux && cgo

package system

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct _XDisplay Display;
typedef unsigned long XID;
typedef XID Window;
typedef unsigned long Atom;
typedef int Bool;

typedef struct {
    int x, y;
    int width, height;
    int border_width;
    int depth;
    void *visual;
    Window root;
    int class;
    int bit_gravity;
    int win_gravity;
    int backing_store;
    unsigned long backing_planes;
    unsigned long backing_pixel;
    Bool save_under;
    long colormap;
    Bool map_installed;
    int map_state;
    long all_event_masks;
    long your_event_mask;
    long do_not_propagate_mask;
    Bool override_redirect;
    void *screen;
} ext_xwindow_attributes;

typedef struct {
    unsigned long identifier;
    int32_t pid;
    int x;
    int y;
    int width;
    int height;
} ext_linux_window;

typedef Display *(*ext_XOpenDisplay)(const char *);
typedef int (*ext_XCloseDisplay)(Display *);
typedef int (*ext_XDefaultScreen)(Display *);
typedef Window (*ext_XRootWindow)(Display *, int);
typedef int (*ext_XDisplayWidth)(Display *, int);
typedef int (*ext_XDisplayHeight)(Display *, int);
typedef Atom (*ext_XInternAtom)(Display *, const char *, Bool);
typedef int (*ext_XGetWindowProperty)(Display *, Window, Atom, long, long, Bool, Atom, Atom *, int *, unsigned long *, unsigned long *, unsigned char **);
typedef int (*ext_XGetWindowAttributes)(Display *, Window, ext_xwindow_attributes *);
typedef Bool (*ext_XTranslateCoordinates)(Display *, Window, Window, int, int, int *, int *, Window *);
typedef int (*ext_XQueryTree)(Display *, Window, Window *, Window *, Window **, unsigned int *);
typedef int (*ext_XFree)(void *);

typedef struct {
    void *library;
    ext_XOpenDisplay open_display;
    ext_XCloseDisplay close_display;
    ext_XDefaultScreen default_screen;
    ext_XRootWindow root_window;
    ext_XDisplayWidth display_width;
    ext_XDisplayHeight display_height;
    ext_XInternAtom intern_atom;
    ext_XGetWindowProperty get_property;
    ext_XGetWindowAttributes get_attributes;
    ext_XTranslateCoordinates translate;
    ext_XQueryTree query_tree;
    ext_XFree free_value;
} ext_x11;

static int ext_x11_load(ext_x11 *x11) {
    x11->library = dlopen("libX11.so.6", RTLD_LAZY | RTLD_LOCAL);
    if (x11->library == NULL) return 0;
    #define LOAD(name, field) x11->field = (ext_##name)dlsym(x11->library, #name); if (x11->field == NULL) return 0
    LOAD(XOpenDisplay, open_display);
    LOAD(XCloseDisplay, close_display);
    LOAD(XDefaultScreen, default_screen);
    LOAD(XRootWindow, root_window);
    LOAD(XDisplayWidth, display_width);
    LOAD(XDisplayHeight, display_height);
    LOAD(XInternAtom, intern_atom);
    LOAD(XGetWindowProperty, get_property);
    LOAD(XGetWindowAttributes, get_attributes);
    LOAD(XTranslateCoordinates, translate);
    LOAD(XQueryTree, query_tree);
    LOAD(XFree, free_value);
    #undef LOAD
    return 1;
}

static void ext_x11_unload(ext_x11 *x11) { if (x11->library != NULL) dlclose(x11->library); }

static int ext_x11_property(ext_x11 *x11, Display *display, Window window, Atom atom, unsigned char **data, unsigned long *count) {
    Atom actual = 0;
    int format = 0;
    unsigned long remaining = 0;
    *data = NULL; *count = 0;
    if (atom == 0 || x11->get_property(display, window, atom, 0, 65536, 0, 0, &actual, &format, count, &remaining, data) != 0) return 0;
    if (*data == NULL || format != 32) {
        if (*data != NULL) x11->free_value(*data);
        *data = NULL; *count = 0;
        return 0;
    }
    return 1;
}

static int ext_system_x11(int display_limit, int window_limit, int *width, int *height, int *screen_id,
                          ext_linux_window **output, int *written, int *total, int *library_state) {
    ext_x11 x11 = {0};
    *output = NULL; *written = 0; *total = 0; *library_state = 0;
    if (!ext_x11_load(&x11)) { ext_x11_unload(&x11); return -1; }
    *library_state = 1;
    Display *display = x11.open_display(NULL);
    if (display == NULL) { ext_x11_unload(&x11); return -2; }
    int screen = x11.default_screen(display);
    Window root = x11.root_window(display, screen);
    *width = x11.display_width(display, screen);
    *height = x11.display_height(display, screen);
    *screen_id = screen;
    if (display_limit == 0 || window_limit == 0) { x11.close_display(display); ext_x11_unload(&x11); return 0; }

    Atom clients_atom = x11.intern_atom(display, "_NET_CLIENT_LIST", 1);
    unsigned char *property = NULL;
    unsigned long count = 0;
    Window *windows = NULL;
    unsigned int child_count = 0;
    Window ignored_root = 0, ignored_parent = 0;
    int property_owned = ext_x11_property(&x11, display, root, clients_atom, &property, &count);
    if (property_owned) {
        windows = (Window *)property;
        child_count = (unsigned int)count;
    } else if (!x11.query_tree(display, root, &ignored_root, &ignored_parent, &windows, &child_count)) {
        windows = NULL; child_count = 0;
    }
    ext_linux_window *values = calloc((size_t)window_limit, sizeof(ext_linux_window));
    if (values == NULL) {
        if (windows != NULL) x11.free_value(windows);
        x11.close_display(display); ext_x11_unload(&x11); return -3;
    }
    Atom pid_atom = x11.intern_atom(display, "_NET_WM_PID", 1);
    int stored = 0;
    for (unsigned int index = 0; index < child_count; index++) {
        ext_xwindow_attributes attributes;
        if (!x11.get_attributes(display, windows[index], &attributes) || attributes.map_state != 2 || attributes.width <= 0 || attributes.height <= 0) continue;
        unsigned char *pid_property = NULL;
        unsigned long pid_count = 0;
        if (!ext_x11_property(&x11, display, windows[index], pid_atom, &pid_property, &pid_count) || pid_count < 1) {
            if (pid_property != NULL) x11.free_value(pid_property);
            continue;
        }
        int32_t pid = (int32_t)((unsigned long *)pid_property)[0];
        x11.free_value(pid_property);
        if (pid <= 0) continue;
        (*total)++;
        if (stored >= window_limit) continue;
        int x = attributes.x, y = attributes.y;
        Window child = 0;
        x11.translate(display, windows[index], root, 0, 0, &x, &y, &child);
        values[stored].identifier = windows[index];
        values[stored].pid = pid;
        values[stored].x = x;
        values[stored].y = y;
        values[stored].width = attributes.width;
        values[stored].height = attributes.height;
        stored++;
    }
    if (windows != NULL) x11.free_value(windows);
    x11.close_display(display); ext_x11_unload(&x11);
    *output = values; *written = stored;
    return 0;
}
*/
import "C"

import (
	"context"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

func platformSurfaces(ctx context.Context, maxDisplays, maxWindows int) ([]DisplayInfo, CollectionStatus, []WindowInfo, CollectionStatus, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, nil, CollectionStatus{}, "", err
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		detail := "DISPLAY is not set"
		if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" {
			detail = "the Wayland compositor does not expose global display or window geometry"
		}
		return []DisplayInfo{}, surfaceStatus("unsupported", detail), []WindowInfo{}, surfaceStatus("unsupported", detail), "desktop-global-pixels", nil
	}
	var values *C.ext_linux_window
	var written, total, width, height, screen, libraryState C.int
	code := C.ext_system_x11(C.int(maxDisplays), C.int(maxWindows), &width, &height, &screen, &values, &written, &total, &libraryState)
	if values != nil {
		defer C.free(unsafe.Pointer(values))
	}
	if code != 0 {
		detail := "libX11 is unavailable"
		if libraryState != 0 {
			detail = "the X11 display could not be opened"
		}
		return []DisplayInfo{}, surfaceStatus("unavailable", detail), []WindowInfo{}, surfaceStatus("unavailable", detail), "desktop-global-pixels", nil
	}
	displays := []DisplayInfo{}
	if width > 0 && height > 0 && maxDisplays > 0 {
		displays = append(displays, DisplayInfo{ID: surfaceID("display", "x11:"+strconv.Itoa(int(screen))), Bounds: SurfaceBounds{Width: int(width), Height: int(height)}, Primary: true})
	}
	displayStatus := surfaceStatus("partial", "X11 root geometry does not expose monitor partitions")
	if maxDisplays < 1 {
		displayStatus = surfaceStatus("partial", "display limit reached")
	}
	windows := make([]WindowInfo, 0, int(written))
	identityUnavailable := false
	starts := map[int]string{}
	for _, value := range unsafe.Slice(values, int(written)) {
		if err := ctx.Err(); err != nil {
			return nil, CollectionStatus{}, nil, CollectionStatus{}, "", err
		}
		pid := int(value.pid)
		startID, exists := starts[pid]
		if !exists {
			process, err := platformProcess(ctx, pid)
			if err == nil {
				startID = process.StartID
			}
			starts[pid] = startID
		}
		if startID == "" {
			identityUnavailable = true
			continue
		}
		bounds := SurfaceBounds{X: int(value.x), Y: int(value.y), Width: int(value.width), Height: int(value.height)}
		windows = append(windows, WindowInfo{ID: surfaceWindowID(pid, startID, strconv.FormatUint(uint64(value.identifier), 10)), PID: pid, ProcessStartID: startID, Bounds: bounds, OnScreen: true})
	}
	windowStatus := surfaceStatus("complete", "")
	if total > written {
		windowStatus = surfaceStatus("partial", "window limit reached")
	} else if identityUnavailable {
		windowStatus = surfaceStatus("partial", "some window process generations were unavailable")
	}
	return displays, displayStatus, windows, windowStatus, "desktop-global-pixels", nil
}
