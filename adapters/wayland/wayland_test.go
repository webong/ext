package wayland

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/webong/ext/res/system"
	"github.com/webong/ext/res/system/media"
)

func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type fakeRunner struct {
	calls  []string
	output []byte
	failed bool
}

func (f *fakeRunner) run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(arguments, " "))
	if f.failed {
		return nil, errors.New("tool failed")
	}
	return f.output, nil
}

func TestCaptureUsesGrimAndBounds(t *testing.T) {
	runner := &fakeRunner{output: pngFixture(t, 4, 3)}
	driver := New(Config{Run: runner.run})
	capture, err := driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 1024, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Width != 4 || capture.Height != 3 || capture.Format != "image/png" {
		t.Fatalf("capture = %#v", capture)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "grim -" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	if _, err = driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 4, MaxBytes: 1 << 20}); err == nil {
		t.Fatal("pixel bound was not enforced")
	}
	runner.failed = true
	if _, err = driver.Capture(context.Background(), system.CaptureOptions{}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("tool failure = %v", err)
	}
}

func TestCaptureSelectsOutput(t *testing.T) {
	runner := &fakeRunner{output: pngFixture(t, 4, 3)}
	driver := New(Config{Run: runner.run, Output: "HDMI-A-1"})
	capture, err := driver.Capture(context.Background(), system.CaptureOptions{MaxPixels: 1024, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Width != 4 || capture.Height != 3 {
		t.Fatalf("capture = %#v", capture)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "grim -o HDMI-A-1 -" {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestInputUsesWtypeAndRefusesPointer(t *testing.T) {
	runner := &fakeRunner{}
	driver := New(Config{Run: runner.run})
	if err := driver.Input(context.Background(), system.SurfaceInput{Kind: system.SurfaceInputText, Text: "hello"}, system.InputOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputKey, Key: "enter", Modifiers: []string{"command", "shift"},
	}, system.InputOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputKey, Key: "backspace",
	}, system.InputOptions{}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, fragment := range []string{
		"wtype -- hello",
		"wtype -M logo -M shift -k Return -m logo -m shift",
		"wtype -k BackSpace",
	} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("input calls are missing %q:\n%s", fragment, joined)
		}
	}
	for _, kind := range []system.SurfaceInputKind{
		system.SurfaceInputMove, system.SurfaceInputClick, system.SurfaceInputDrag, system.SurfaceInputScroll,
	} {
		if err := driver.Input(context.Background(), system.SurfaceInput{Kind: kind}, system.InputOptions{}); !errors.Is(err, system.ErrDesktopUnavailable) {
			t.Errorf("%s = %v", kind, err)
		}
	}
}

func TestRecordingIsExplicitlyUnavailable(t *testing.T) {
	driver := New(Config{})
	if _, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm"}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("wayland recording = %v", err)
	}
	if !strings.Contains(driver.Name(), "wayland") {
		t.Fatalf("name = %q", driver.Name())
	}
}

func TestRecordingRequiresExplicitRecorder(t *testing.T) {
	driver := New(Config{Run: (&fakeRunner{}).run})
	if _, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm"}); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("wayland recording without recorder = %v", err)
	}
}

func TestRecordingUsesConfiguredRecorder(t *testing.T) {
	runner := &fakeRunner{output: []byte("webm-frames")}
	process := &fakeProcess{out: bytes.NewBuffer(nil), in: bytes.NewBuffer(nil)}
	driver := New(Config{Run: runner.run, Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			runner.calls = append(runner.calls, name+" "+strings.Join(arguments, " "))
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, " ")
	for _, fragment := range []string{"wf-recorder --filter fps=30 --codec libvpx-vp9 --no-damage --codec-param deadline=realtime --codec-param lag-in-frames=0 --muxer webm --file pipe:3"} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("recording arguments are missing %q:\n%s", fragment, joined)
		}
	}
	if err := recording.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := recording.Read(make([]byte, 16)); err != nil && err != io.EOF {
		t.Fatalf("read = %v", err)
	}
}

func TestRecordingMapsFormatToCodecAndMuxer(t *testing.T) {
	for _, tc := range []struct {
		format, codec, muxer string
	}{
		{"webm", "libvpx-vp9", "webm"},
	} {
		var received []string
		process := &fakeProcess{out: bytes.NewBuffer(nil), in: bytes.NewBuffer(nil)}
		driver := New(Config{Recorder: "wf-recorder",
			Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
				received = append([]string(nil), arguments...)
				return process, nil
			}})
		_, err := driver.Record(context.Background(), media.RecordingOptions{Format: tc.format, FrameRate: 30})
		if err != nil {
			t.Fatalf("%s = %v", tc.format, err)
		}
		for _, fragment := range []string{"--filter", "fps=30", "--codec", tc.codec, "--muxer", tc.muxer, "--file", "pipe:3"} {
			found := false
			for _, arg := range received {
				if arg == fragment {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s recording args missing %q: %#v", tc.format, fragment, received)
			}
		}
	}
}

func TestRecordingRejectsMP4ForPipedOutput(t *testing.T) {
	driver := New(Config{Recorder: "wf-recorder"})
	if _, err := driver.Record(context.Background(), media.RecordingOptions{Format: "mp4", FrameRate: 30}); err == nil {
		t.Fatal("mp4 piped recording was accepted")
	}
}

func TestRecordingRejectsUnsupportedFormat(t *testing.T) {
	driver := New(Config{Recorder: "wf-recorder"})
	if _, err := driver.Record(context.Background(), media.RecordingOptions{Format: "avi"}); err == nil {
		t.Fatal("unsupported format was accepted")
	}
}

func TestRecordingEnforcesByteLimitActivelyStops(t *testing.T) {
	stopped := make(chan struct{})
	process := &fakeProcess{
		out:      bytes.NewBufferString(strings.Repeat("x", 500)),
		in:       bytes.NewBuffer(nil),
		stopChan: stopped,
	}
	driver := New(Config{Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 256})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	total := 0
	var limitErr error
	for {
		n, err := recording.Read(buf[total:])
		total += n
		if err != nil {
			limitErr = err
			break
		}
		if total >= len(buf) {
			break
		}
	}
	if !errors.Is(limitErr, media.ErrRecordingLimit) {
		t.Fatalf("byte limit error = %v", limitErr)
	}
	// Verify the process was actively stopped.
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("process was not actively stopped at byte limit")
	}
	// A subsequent read after the limit reports the limit error, not EOF.
	if _, err := recording.Read(buf); !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("read after limit = %v, want ErrRecordingLimit", err)
	}
	// Stop is idempotent and consistent after the limit.
	if err := recording.Stop(context.Background()); !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("stop after byte limit = %v, want ErrRecordingLimit", err)
	}
	if err := recording.Stop(context.Background()); !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("second stop = %v, want ErrRecordingLimit", err)
	}
	if err := recording.Close(); !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("close after stop = %v, want ErrRecordingLimit", err)
	}
}

func TestRecordingReadLimitedToRemainingBytes(t *testing.T) {
	process := &fakeProcess{out: bytes.NewBufferString(strings.Repeat("y", 1000)), in: bytes.NewBuffer(nil)}
	driver := New(Config{Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	// Ask for 1000 bytes but only 100 remain; read must not exceed the bound.
	buf := make([]byte, 1000)
	n, err := recording.Read(buf)
	if err == nil {
		t.Fatalf("read of 1000 bytes with 100 byte limit did not return limit error")
	}
	if !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("read error = %v, want ErrRecordingLimit", err)
	}
	if n != 100 {
		t.Fatalf("read %d bytes, want exactly 100 (remaining bound)", n)
	}
}

func TestRecordingEnforcesDurationLimitWithoutRead(t *testing.T) {
	// A blocked reader: duration enforcement must happen without a read call.
	stopped := make(chan struct{})
	process := &fakeProcess{
		out:      &blockingReader{closed: make(chan struct{})},
		in:       bytes.NewBuffer(nil),
		stopChan: stopped,
	}
	driver := New(Config{Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Second, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	// The watchdog fires based on wall time, not on a read call.
	if _, err := recording.Read(make([]byte, 16)); !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("duration limit error = %v, want ErrRecordingLimit", err)
	}
	// Verify the process was actively stopped by the duration watchdog.
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("process was not actively stopped at duration limit")
	}
	if err := recording.Stop(context.Background()); !errors.Is(err, media.ErrRecordingLimit) {
		t.Fatalf("stop after duration limit = %v, want ErrRecordingLimit", err)
	}
}

func TestRecordingHonorsContextCancellation(t *testing.T) {
	process := &fakeProcess{out: &blockingReader{closed: make(chan struct{})}, in: bytes.NewBuffer(nil)}
	driver := New(Config{Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recording.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stop = %v", err)
	}
}

func TestRecordingStopIsIdempotent(t *testing.T) {
	process := &fakeProcess{out: bytes.NewBuffer(nil), in: bytes.NewBuffer(nil)}
	driver := New(Config{Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := recording.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := recording.Stop(context.Background()); err != nil {
		t.Fatalf("second stop = %v", err)
	}
	if err := recording.Close(); err != nil {
		t.Fatalf("close after stop = %v", err)
	}
}

func TestRecordingReadAfterNormalStop(t *testing.T) {
	process := &fakeProcess{out: bytes.NewBuffer(nil), in: bytes.NewBuffer(nil)}
	driver := New(Config{Recorder: "wf-recorder",
		Start: func(ctx context.Context, name string, arguments ...string) (Process, error) {
			return process, nil
		}})
	recording, err := driver.Record(context.Background(), media.RecordingOptions{Format: "webm", FrameRate: 30, MaxDuration: time.Minute, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := recording.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := recording.Read(make([]byte, 16)); err != io.EOF {
		t.Fatalf("read after normal stop = %v, want io.EOF", err)
	}
	if err := recording.Stop(context.Background()); err != nil {
		t.Fatalf("second stop after read = %v", err)
	}
}

// Closing the output during shutdown must release an in-flight Read, and any
// bytes returned by that read must be discarded after the terminal transition.
func TestRecordingNoDataAfterTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader := &lateReader{blockingReader: blockingReader{closed: make(chan struct{})}}
		r := testRecording(&fakeProcess{out: reader})
		result := make(chan error, 1)
		go func() {
			n, err := r.Read(make([]byte, 16))
			if n != 0 || err != io.EOF {
				result <- fmt.Errorf("read = %d, %v; want 0, EOF", n, err)
				return
			}
			result <- nil
		}()
		synctest.Wait() // Read has entered the underlying reader and is blocked.
		if err := r.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRecordingStopRacesWatchdog(t *testing.T) {
	for _, stopFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("stopFirst=%t", stopFirst), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &fakeProcess{out: bytes.NewBuffer(nil), stopChan: make(chan struct{}), signalled: make(chan struct{})}
				r := testRecording(p)
				r.maxDuration = time.Second
				go r.watchDuration(context.Background())
				result := make(chan error, 1)
				var want error
				if stopFirst {
					go func() { result <- r.Stop(context.Background()) }()
					<-p.signalled // Stop is waiting for exit when the watchdog fires.
					time.Sleep(time.Second)
				} else {
					time.Sleep(time.Second)
					<-p.signalled // Watchdog selected the terminal state first.
					want = media.ErrRecordingLimit
					go func() { result <- r.Stop(context.Background()) }()
				}
				if err := <-result; !errors.Is(err, want) {
					t.Fatalf("stop = %v, want %v", err, want)
				}
				if err := r.Close(); !errors.Is(err, want) {
					t.Fatalf("close = %v, want %v", err, want)
				}
			})
		})
	}
}

func TestRecordingCancelRacesWatchdog(t *testing.T) {
	for _, cancelFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("cancelFirst=%t", cancelFirst), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &fakeProcess{out: bytes.NewBuffer(nil), stopChan: make(chan struct{}), signalled: make(chan struct{})}
				r := testRecording(p)
				r.maxDuration = time.Second
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go r.watchDuration(ctx)
				want := media.ErrRecordingLimit
				if cancelFirst {
					cancel()
					<-p.signalled
					want = context.Canceled
					time.Sleep(time.Second)
				} else {
					time.Sleep(time.Second)
					<-p.signalled
					cancel()
				}
				if err := r.Stop(context.Background()); !errors.Is(err, want) {
					t.Fatalf("stop = %v, want %v", err, want)
				}
				if _, err := r.Read(make([]byte, 1)); !errors.Is(err, want) {
					t.Fatalf("read = %v, want %v", err, want)
				}
			})
		})
	}
}

func TestRecordingCancelDuringStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProcess{out: &blockingReader{closed: make(chan struct{})}, stopChan: make(chan struct{}), signalled: make(chan struct{})}
		r := testRecording(p)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- r.Stop(ctx) }()
		<-p.signalled // Shutdown has started and the child has not exited.
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled wait = %v", err)
		}
		// Cancellation of this wait does not replace the normal terminal state.
		if err := r.Close(); err != nil {
			t.Fatalf("cleanup = %v", err)
		}
		select {
		case <-p.Done():
		default:
			t.Fatal("cleanup returned before the child was reaped")
		}
	})
}

func TestRecordingShutdownNeverExits(t *testing.T) {
	for _, killErr := range []error{nil, errors.New("kill denied")} {
		t.Run(fmt.Sprint(killErr), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &fakeProcess{out: &blockingReader{closed: make(chan struct{})}, stopChan: make(chan struct{}), noExit: true, killErr: killErr}
				r := testRecording(p)
				start := time.Now()
				err := r.Stop(context.Background())
				if !errors.Is(err, ErrRecordingShutdown) || (killErr != nil && !errors.Is(err, killErr)) {
					t.Fatalf("shutdown = %v", err)
				}
				if elapsed := time.Since(start); elapsed != 2*r.shutdownTimeout {
					t.Fatalf("shutdown took %v", elapsed)
				}
				if again := r.Close(); !errors.Is(again, ErrRecordingShutdown) {
					t.Fatalf("repeat shutdown = %v", again)
				}
				// Leave Done open: synctest rejects any abandoned goroutine
				// waiting on it when this bubble finishes.
			})
		})
	}
}

func TestRecordingSignalFailureForcesKill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProcess{
			out: bytes.NewBuffer(nil), stopChan: make(chan struct{}),
			signalErr: errors.New("interrupt unsupported"),
		}
		r := testRecording(p)
		start := time.Now()
		if err := r.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != 0 {
			t.Fatal("waited for grace after interrupt failed")
		}
		select {
		case <-p.Done():
		default:
			t.Fatal("child was not killed and reaped")
		}
	})
}

func TestRecordingConcurrentStopClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := testRecording(&fakeProcess{out: bytes.NewBuffer(nil)})
		results := make(chan error, 10)
		for i := 0; i < 5; i++ {
			go func() { results <- r.Stop(context.Background()) }()
			go func() { results <- r.Close() }()
		}
		for i := 0; i < 10; i++ {
			if err := <-results; err != nil {
				t.Fatalf("concurrent stop/close = %v", err)
			}
		}
	})
}

func testRecording(p Process) *recording {
	return &recording{
		process: p, output: p.Stdout(), maxBytes: 1024,
		maxDuration: time.Hour, started: time.Now(),
		done: make(chan struct{}), shutdownTimeout: 2 * time.Second,
	}
}

type lateReader struct{ blockingReader }

func (r *lateReader) Read(p []byte) (int, error) {
	<-r.closed
	return copy(p, "late frames"), nil
}

func TestNativeProcessCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wf-recorder uses a Unix inherited descriptor")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"output", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p, err := startProcess(ctx, executable, "-test.run=^TestWaylandProcessHelper$", "--", "wayland-helper", mode)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Stdout().Close()
			if mode == "cancel" {
				buf := make([]byte, len("ready"))
				if _, err := io.ReadFull(p.Stdout(), buf); err != nil {
					t.Fatal(err)
				}
				cancel()
			}
			if !waitProcess(p.Done(), 10*time.Second) {
				t.Fatal("child was not reaped")
			}
			if mode == "output" {
				output, err := io.ReadAll(p.Stdout())
				if err != nil || string(output) != "frames" {
					t.Fatalf("buffered output after exit = %q, %v", output, err)
				}
			}
		})
	}
}

func TestWaylandProcessHelper(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "wayland-helper" {
		return
	}
	if args[len(args)-1] == "output" {
		_, _ = os.Stdout.WriteString("native diagnostic must not enter video")
		_, _ = os.NewFile(3, "video").WriteString("frames")
		os.Exit(0)
	}
	_, _ = os.NewFile(3, "video").WriteString("ready")
	for {
		time.Sleep(time.Hour)
	}
}

func TestConfiguredYdotoolHandlesPointerInput(t *testing.T) {
	runner := &fakeRunner{}
	driver := New(Config{Run: runner.run, Pointer: "ydotool"})
	options := system.InputOptions{}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputMove, Point: system.SurfacePoint{X: 10, Y: 20},
	}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputClick, Point: system.SurfacePoint{X: 1, Y: 2}, Button: "right",
	}, options); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputDrag, Point: system.SurfacePoint{X: 3, Y: 4}, End: system.SurfacePoint{X: 5, Y: 6},
	}, options); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, fragment := range []string{
		"ydotool mousemove --absolute 10 20",
		"ydotool click 0xC1",
		"ydotool click 0x40",
		"ydotool mousemove --absolute 5 6",
		"ydotool click 0x80",
	} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("pointer calls are missing %q:\n%s", fragment, joined)
		}
	}
	if err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputScroll, Point: system.SurfacePoint{X: 0, Y: 0}, DeltaY: -120,
	}, options); !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("scroll = %v", err)
	}
}

func TestPointerRequiresExplicitDriver(t *testing.T) {
	driver := New(Config{})
	err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputMove, Point: system.SurfacePoint{X: 1, Y: 1},
	}, system.InputOptions{})
	if !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("unconfigured pointer = %v", err)
	}
}

func TestYdotoolDragReleasesTheButtonOnFailure(t *testing.T) {
	calls := []string{}
	driver := New(Config{
		Run: func(_ context.Context, name string, arguments ...string) ([]byte, error) {
			value := name + " " + strings.Join(arguments, " ")
			calls = append(calls, value)
			if strings.Contains(value, "mousemove --absolute 5 6") {
				return nil, errors.New("move failed")
			}
			return nil, nil
		},
		Pointer: "ydotool",
	})
	err := driver.Input(context.Background(), system.SurfaceInput{
		Kind: system.SurfaceInputDrag, Point: system.SurfacePoint{X: 3, Y: 4}, End: system.SurfacePoint{X: 5, Y: 6},
	}, system.InputOptions{})
	if !errors.Is(err, system.ErrDesktopUnavailable) {
		t.Fatalf("failed drag = %v", err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "ydotool click 0x80") {
		t.Fatalf("drag did not release the button:\n%s", joined)
	}
}

type fakeProcess struct {
	out       io.Reader
	in        *bytes.Buffer
	stopChan  chan struct{}
	signalled chan struct{}
	noExit    bool
	killErr   error
	signalErr error
}

func (f *fakeProcess) Stdout() io.ReadCloser {
	if rc, ok := f.out.(io.ReadCloser); ok {
		return rc
	}
	return io.NopCloser(f.out)
}
func (f *fakeProcess) Stdin() io.WriteCloser { return nopWriteCloser{Writer: f.in} }

var exitedProcess = func() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

func (f *fakeProcess) Done() <-chan struct{} {
	if f.stopChan != nil {
		return f.stopChan
	}
	return exitedProcess
}
func (f *fakeProcess) Kill() error {
	if f.stopChan != nil && !f.noExit {
		close(f.stopChan)
	}
	return f.killErr
}
func (f *fakeProcess) Signal(os.Signal) error {
	if f.signalled != nil {
		close(f.signalled)
	}
	return f.signalErr
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// blockingReader blocks until Close is called.
type blockingReader struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockingReader) Read(buffer []byte) (int, error) {
	<-r.closed
	return 0, io.EOF
}

func (r *blockingReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}
