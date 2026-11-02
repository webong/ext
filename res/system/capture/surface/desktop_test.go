package surface

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testDesktopProbe(platform string, environment map[string]string, paths map[string]bool) desktopProbe {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	return desktopProbe{
		os: platform,
		lookupEnv: func(key string) (string, bool) {
			value, ok := environment[key]
			return value, ok
		},
		stat: func(path string) error {
			if paths[path] {
				return nil
			}
			return errors.New("missing")
		},
		lookPath: func(name string) (string, error) {
			if paths[name] {
				return "/bin/" + name, nil
			}
			return "", errors.New("missing")
		},
		readFile: func(string) ([]byte, error) { return nil, errors.New("missing") },
		now:      func() time.Time { return now },
	}
}

func TestDesktopProbeObservesLocalLinuxEndpoints(t *testing.T) {
	runtimeDirectory := t.TempDir()
	probe := testDesktopProbe("linux", map[string]string{
		"DISPLAY": ":2.0", "WAYLAND_DISPLAY": "wayland-1", "XDG_RUNTIME_DIR": runtimeDirectory,
	}, map[string]bool{
		filepath.Join("/tmp/.X11-unix", "X2"):        true,
		filepath.Join(runtimeDirectory, "wayland-1"): true,
		"ffmpeg": true,
	})
	got, err := probe.observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Interactive || !got.X11.Available || !got.Wayland.Available || got.Containerized || got.Recording.Status != "ready" {
		t.Fatalf("unexpected desktop observation: %+v", got)
	}
	want := []string{DesktopNative, DesktopRecording, DesktopWayland, DesktopX11}
	if !reflect.DeepEqual(got.Capabilities, want) {
		t.Fatalf("capabilities=%v want=%v", got.Capabilities, want)
	}
}

func TestDesktopProbeReportsPermissionWithoutRequestingIt(t *testing.T) {
	probe := testDesktopProbe("darwin", map[string]string{}, map[string]bool{"ffmpeg": true})
	got, err := probe.observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Interactive || got.Recording.Status != "permission-required" || got.Recording.Permission != "macos.screen-recording" {
		t.Fatalf("unexpected macOS observation: %+v", got)
	}
}

func TestDesktopProbeRejectsRemoteX11(t *testing.T) {
	probe := testDesktopProbe("linux", map[string]string{"DISPLAY": "remote.example:0"}, map[string]bool{"ffmpeg": true})
	got, err := probe.observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.X11.Available || got.Interactive || got.Recording.CaptureAvailable {
		t.Fatalf("remote display was treated as local: %+v", got)
	}
}
