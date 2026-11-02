package ffmpeg

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/res/system/capture/media"
)

type fakeProcess struct {
	output  io.ReadCloser
	kills   int
	waits   int
	waitErr error
}

func (p *fakeProcess) Stdout() io.ReadCloser { return p.output }
func (p *fakeProcess) Wait() error           { p.waits++; return p.waitErr }
func (p *fakeProcess) Kill() error           { p.kills++; return nil }

// starter records the command line it was asked to run and returns a process that streams data.
func starter(name *string, arguments *[]string, process *fakeProcess) Starter {
	return func(_ context.Context, command string, args ...string) (Process, error) {
		*name, *arguments = command, args
		return process, nil
	}
}

func TestPlatformsAndSourcesAreChecked(t *testing.T) {
	if _, err := New(Config{OS: "darwin"}); err == nil {
		t.Fatal("macOS recording started without a caller-chosen source")
	}
	if _, err := New(Config{OS: "linux"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("linux: %v", err)
	}
	for _, source := range []string{"-f", "a\nb", strings.Repeat("x", 300)} {
		if _, err := New(Config{OS: "windows", Source: source}); err == nil {
			t.Fatalf("accepted source %q", source)
		}
	}
	if _, err := New(Config{OS: "windows"}); err != nil {
		t.Fatalf("windows defaults to the whole desktop: %v", err)
	}
}

func TestMacOSRecordingBuildsABoundedAVFoundationCommand(t *testing.T) {
	var name string
	var arguments []string
	process := &fakeProcess{output: io.NopCloser(strings.NewReader("mp4-bytes"))}
	recorder, err := New(Config{OS: "darwin", Source: "1:none", Start: starter(&name, &arguments, process)})
	if err != nil {
		t.Fatal(err)
	}
	recording, err := recorder.Record(context.Background(), media.RecordingOptions{Format: "mp4", FrameRate: 24, MaxDuration: 90 * time.Second, MaxBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-y", "-loglevel", "error", "-f", "avfoundation", "-framerate", "24", "-i", "1:none",
		"-t", "90", "-fs", "4194304",
		"-c:v", "libx264", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4", "pipe:1",
	}
	if name != "ffmpeg" || !reflect.DeepEqual(arguments, want) {
		t.Fatalf("ran %s %q\nwant ffmpeg %q", name, arguments, want)
	}
	data, err := io.ReadAll(recording)
	if err != nil || string(data) != "mp4-bytes" {
		t.Fatalf("stream = %q, %v", data, err)
	}
}

func TestWindowsWebMRecordingUsesGDIGrabAndDefaults(t *testing.T) {
	var name string
	var arguments []string
	recorder, _ := New(Config{OS: "windows", Binary: `C:\tools\ffmpeg.exe`, Start: starter(&name, &arguments, &fakeProcess{output: io.NopCloser(strings.NewReader(""))})})
	if _, err := recorder.Record(context.Background(), media.RecordingOptions{Format: "webm"}); err != nil {
		t.Fatal(err)
	}
	if name != `C:\tools\ffmpeg.exe` {
		t.Fatalf("binary = %q", name)
	}
	joined := strings.Join(arguments, " ")
	for _, part := range []string{"-f gdigrab", "-framerate 30", "-i desktop", "-t 60", "-fs 134217728", "-c:v libvpx-vp9 -deadline realtime -cpu-used 8 -f webm pipe:1"} {
		if !strings.Contains(joined, part) {
			t.Fatalf("missing %q in %q", part, joined)
		}
	}
}

func TestInvalidOptionsAndStartFailuresAreReported(t *testing.T) {
	failing := func(context.Context, string, ...string) (Process, error) { return nil, errors.New("not installed") }
	recorder, _ := New(Config{OS: "windows", Start: failing})
	if _, err := recorder.Record(context.Background(), media.RecordingOptions{Format: "gif"}); err == nil {
		t.Fatal("accepted an unsupported format")
	}
	if _, err := recorder.Record(context.Background(), media.RecordingOptions{Format: "mp4"}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("start failure: %v", err)
	}
}

func TestStopEndsTheProcessOnceAndCloseStopsToo(t *testing.T) {
	var name string
	var arguments []string
	process := &fakeProcess{output: io.NopCloser(strings.NewReader("x"))}
	recorder, _ := New(Config{OS: "windows", Start: starter(&name, &arguments, process)})
	recording, _ := recorder.Record(context.Background(), media.RecordingOptions{Format: "mp4"})
	if err := recording.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := recording.Close(); err != nil {
		t.Fatal(err)
	}
	if process.kills != 1 || process.waits != 1 {
		t.Fatalf("kills=%d waits=%d, want one each", process.kills, process.waits)
	}
}
