package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DesktopNative    = "desktop.native"
	DesktopX11       = "desktop.x11"
	DesktopWayland   = "desktop.wayland"
	DesktopContainer = "desktop.container"
	DesktopRecording = "desktop.recording"
)

// DesktopEndpoint is evidence that a local desktop display endpoint is visible
// to this process. It never contains a socket path, credential, or remote host.
type DesktopEndpoint struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// RecordingInfo describes local recording prerequisites. Permission is an
// identifier for the embedding application to interpret; discovery never opens
// settings, requests permission, or starts a capture.
type RecordingInfo struct {
	Status           string    `json:"status"`
	EncoderAvailable bool      `json:"encoder_available"`
	CaptureAvailable bool      `json:"capture_available"`
	Permission       string    `json:"permission"`
	Formats          []string  `json:"formats,omitempty"`
	Message          string    `json:"message,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
}

// DesktopInfo is a point-in-time observation of the native desktop visible to
// the current process. It does not describe a remote display or authorize use.
type DesktopInfo struct {
	Platform      string          `json:"platform"`
	Interactive   bool            `json:"interactive"`
	Containerized bool            `json:"containerized"`
	Capabilities  []string        `json:"capabilities,omitempty"`
	X11           DesktopEndpoint `json:"x11"`
	Wayland       DesktopEndpoint `json:"wayland"`
	Recording     RecordingInfo   `json:"recording"`
	Reason        string          `json:"reason,omitempty"`
	ObservedAt    time.Time       `json:"observed_at"`
}

type desktopProbe struct {
	os        string
	lookupEnv func(string) (string, bool)
	stat      func(string) error
	lookPath  func(string) (string, error)
	readFile  func(string) ([]byte, error)
	now       func() time.Time
}

// DiscoverDesktop observes only the local native desktop and its recording
// prerequisites. It performs no network access and launches no process.
func DiscoverDesktop(ctx context.Context) (DesktopInfo, error) {
	if err := ctx.Err(); err != nil {
		return DesktopInfo{}, err
	}
	probe := desktopProbe{
		os: runtime.GOOS, lookupEnv: os.LookupEnv,
		stat:     func(path string) error { _, err := os.Stat(path); return err },
		lookPath: exec.LookPath, readFile: os.ReadFile, now: time.Now,
	}
	return probe.observe(ctx)
}

func (p desktopProbe) observe(ctx context.Context) (DesktopInfo, error) {
	if err := ctx.Err(); err != nil {
		return DesktopInfo{}, err
	}
	now := p.now().UTC()
	info := DesktopInfo{Platform: p.os, ObservedAt: now, X11: p.x11(), Wayland: p.wayland()}
	info.Containerized = p.containerized()
	if p.os == "linux" {
		info.Interactive = info.X11.Available || info.Wayland.Available
		if !info.Interactive {
			info.Reason = firstReason(info.X11.Reason, info.Wayland.Reason)
		}
	} else {
		info.Interactive, info.Reason = p.nativeDesktop()
	}
	if info.Interactive {
		info.Capabilities = append(info.Capabilities, DesktopNative)
	}
	if info.X11.Available {
		info.Capabilities = append(info.Capabilities, DesktopX11)
	}
	if info.Wayland.Available {
		info.Capabilities = append(info.Capabilities, DesktopWayland)
	}
	if info.Containerized {
		info.Capabilities = append(info.Capabilities, DesktopContainer)
	}
	info.Recording = p.recording(now)
	if info.Recording.Status == "ready" {
		info.Capabilities = append(info.Capabilities, DesktopRecording)
	}
	sort.Strings(info.Capabilities)
	return info, ctx.Err()
}

func (p desktopProbe) x11() DesktopEndpoint {
	display, ok := p.lookupEnv("DISPLAY")
	if !ok || strings.TrimSpace(display) == "" {
		return DesktopEndpoint{Reason: "DISPLAY is not set"}
	}
	socket, err := localX11Socket(display)
	if err != nil || p.stat(socket) != nil {
		return DesktopEndpoint{Reason: "local X11 socket is unavailable"}
	}
	return DesktopEndpoint{Available: true}
}

func (p desktopProbe) wayland() DesktopEndpoint {
	display, ok := p.lookupEnv("WAYLAND_DISPLAY")
	if !ok || strings.TrimSpace(display) == "" || filepath.Base(display) != display {
		return DesktopEndpoint{Reason: "WAYLAND_DISPLAY is not set to a local endpoint"}
	}
	runtimeDirectory, ok := p.lookupEnv("XDG_RUNTIME_DIR")
	if !ok || !filepath.IsAbs(runtimeDirectory) {
		return DesktopEndpoint{Reason: "XDG_RUNTIME_DIR is unavailable"}
	}
	if p.stat(filepath.Join(runtimeDirectory, display)) != nil {
		return DesktopEndpoint{Reason: "local Wayland socket is unavailable"}
	}
	return DesktopEndpoint{Available: true}
}

func localX11Socket(display string) (string, error) {
	host, value, ok := strings.Cut(strings.TrimSpace(display), ":")
	if !ok || (host != "" && host != "localhost" && host != "unix") {
		return "", fmt.Errorf("display is not local")
	}
	value, _, _ = strings.Cut(value, ".")
	if value == "" {
		value = "0"
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return "", fmt.Errorf("invalid display number")
	}
	return filepath.Join("/tmp/.X11-unix", "X"+strconv.Itoa(number)), nil
}

func (p desktopProbe) nativeDesktop() (bool, string) {
	switch p.os {
	case "darwin":
		if value, ok := p.lookupEnv("CI"); ok && strings.TrimSpace(value) != "" {
			return false, "CI does not expose an interactive macOS desktop"
		}
		_, connection := p.lookupEnv("SSH_CONNECTION")
		_, client := p.lookupEnv("SSH_CLIENT")
		_, terminal := p.lookupEnv("SSH_TTY")
		if !connection && !client && !terminal {
			return true, ""
		}
		if display, ok := p.lookupEnv("DISPLAY"); ok && strings.TrimSpace(display) != "" {
			return true, ""
		}
		return false, "SSH session does not expose a local macOS desktop"
	case "windows":
		session, ok := p.lookupEnv("SESSIONNAME")
		if !ok || strings.TrimSpace(session) == "" {
			return false, "SESSIONNAME is not set"
		}
		if strings.EqualFold(strings.TrimSpace(session), "Services") {
			return false, "service session is not interactive"
		}
		return true, ""
	default:
		return false, "native desktop observation is unavailable"
	}
}

func (p desktopProbe) recording(now time.Time) RecordingInfo {
	info := RecordingInfo{Status: "unavailable", Permission: "not-required", CheckedAt: now}
	if _, err := p.lookPath("ffmpeg"); err == nil {
		info.EncoderAvailable = true
		info.Formats = []string{"mp4", "webm"}
	}
	switch p.os {
	case "darwin":
		info.CaptureAvailable, _ = p.nativeDesktop()
		info.Permission = "macos.screen-recording"
	case "windows":
		info.CaptureAvailable, _ = p.nativeDesktop()
	case "linux":
		x11, wayland := p.x11(), p.wayland()
		info.CaptureAvailable = x11.Available || wayland.Available
		if wayland.Available && !x11.Available {
			info.Permission = "linux.pipewire-portal"
		}
	}
	switch {
	case !info.CaptureAvailable:
		info.Message = "native display capture is unavailable"
	case !info.EncoderAvailable:
		info.Message = "ffmpeg is unavailable"
	case info.Permission != "not-required":
		info.Status = "permission-required"
		info.Message = "the embedding application must obtain capture permission"
	default:
		info.Status = "ready"
	}
	return info
}

func (p desktopProbe) containerized() bool {
	if value, ok := p.lookupEnv("container"); ok && strings.TrimSpace(value) != "" {
		return true
	}
	if p.stat("/.dockerenv") == nil {
		return true
	}
	data, err := p.readFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	value := strings.ToLower(string(data))
	for _, marker := range []string{"/docker/", "/containerd/", "/podman/", "/kubepods/"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func firstReason(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
