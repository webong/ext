package wayland

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/webong/ext/res/system"
	"github.com/webong/ext/res/system/media"
)

func TestNativeWaylandCaptureAndRecording(t *testing.T) {
	if os.Getenv("EXT_WAYLAND_NATIVE_TESTS") != "1" {
		t.Skip("set EXT_WAYLAND_NATIVE_TESTS=1")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("native Wayland verification requires Linux")
	}
	output := os.Getenv("EXT_WAYLAND_TEST_OUTPUT")
	if output == "" {
		t.Fatal("supply EXT_WAYLAND_TEST_OUTPUT for an isolated compositor")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	diagnostics, err := os.Create(filepath.Join(t.TempDir(), "recorder.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = diagnostics.Close()
		if t.Failed() {
			data, _ := os.ReadFile(diagnostics.Name())
			t.Logf("recorder: %s", data)
		}
	})
	driver := New(Config{Output: output, Recorder: "wf-recorder", Start: func(ctx context.Context, name string, args ...string) (Process, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stderr = diagnostics
		return startCommand(cmd)
	}})
	capture, err := driver.Capture(ctx, system.CaptureOptions{MaxPixels: 1024 * 1024, MaxBytes: 4 << 20})
	if err != nil || capture.Width == 0 || capture.Height == 0 {
		t.Fatalf("capture = %#v, %v", capture, err)
	}
	recording, err := driver.Record(ctx, media.RecordingOptions{Format: "webm", FrameRate: 10, MaxDuration: 5 * time.Second, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer recording.Close()
	data, err := io.ReadAll(recording)
	if !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("recording ended without duration limit: %v", err)
	}
	if len(data) < 4 || !bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		t.Fatalf("recording did not produce WebM: %d bytes, prefix %x", len(data), data[:min(16, len(data))])
	}
	if err := recording.Stop(context.Background()); !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatal(err)
	}
}
