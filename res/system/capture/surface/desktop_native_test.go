package surface

import (
	"context"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/res/system/capture/media"
)

// fakeDesktop records what the driver asks of the operating system; the tests never post real
// events.
type fakeDesktop struct {
	mu       sync.Mutex
	bounds_  SurfaceBounds
	events   []nativePointer
	texts    []string
	keys     []string
	failNext error
	capture_ SurfaceCapture
}

func newFakeDesktop() *fakeDesktop {
	return &fakeDesktop{bounds_: SurfaceBounds{X: -100, Y: 50, Width: 300, Height: 200}}
}

func (f *fakeDesktop) name() string                                  { return "fake" }
func (f *fakeDesktop) bounds(context.Context) (SurfaceBounds, error) { return f.bounds_, nil }
func (f *fakeDesktop) capture(context.Context, CaptureOptions) (SurfaceCapture, error) {
	return f.capture_, nil
}
func (f *fakeDesktop) pointer(_ context.Context, event nativePointer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	f.events = append(f.events, event)
	return nil
}
func (f *fakeDesktop) text(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeDesktop) key(_ context.Context, key string, modifiers []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, strings.Join(append(modifiers, key), "+"))
	return nil
}
func (f *fakeDesktop) recorded() []nativePointer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]nativePointer(nil), f.events...)
}

func driverWith(backend *fakeDesktop, recorder media.Recorder) *NativeDesktop {
	return &NativeDesktop{backend: backend, recorder: recorder}
}

func TestNewNativeDesktopIsOnlyAvailableOnMacOSAndWindows(t *testing.T) {
	driver, err := NewNativeDesktop(NativeDesktopOptions{})
	switch runtime.GOOS {
	case "darwin", "windows":
		if err != nil {
			// macOS built without cgo has no driver.
			if !errors.Is(err, ErrDesktopUnavailable) {
				t.Fatal(err)
			}
			return
		}
		if want := map[string]string{"darwin": "native-macos", "windows": "native-windows"}[runtime.GOOS]; driver.Name() != want {
			t.Fatalf("name = %q, want %q", driver.Name(), want)
		}
	default:
		if !errors.Is(err, ErrDesktopUnavailable) || driver != nil {
			t.Fatalf("got %v, %v; want ErrDesktopUnavailable and no driver", driver, err)
		}
	}
}

func TestInputMapsDesktopPositionsToGlobalCoordinatesAndRefusesOutsideTheDisplays(t *testing.T) {
	fake := newFakeDesktop()
	driver := driverWith(fake, nil)
	ctx := context.Background()
	if err := driver.Input(ctx, SurfaceInput{Kind: SurfaceInputClick, Point: SurfacePoint{X: 10, Y: 20}}, InputOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(ctx, SurfaceInput{Kind: SurfaceInputDrag, Point: SurfacePoint{X: 0, Y: 0}, End: SurfacePoint{X: 299, Y: 199}}, InputOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []nativePointer{
		{op: pointerClick, button: "left", x: -90, y: 70},
		{op: pointerDrag, x: -100, y: 50, endX: 199, endY: 249},
	}
	if got := fake.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	for _, point := range []SurfacePoint{{X: 300, Y: 0}, {X: 0, Y: 200}, {X: -1, Y: 0}} {
		if err := driver.Input(ctx, SurfaceInput{Kind: SurfaceInputMove, Point: point}, InputOptions{}); err == nil {
			t.Fatalf("accepted %+v outside the displays", point)
		}
	}
	if got := len(fake.recorded()); got != len(want) {
		t.Fatalf("a refused position still produced an event (%d events)", got)
	}
}

func TestKeyboardInputNeedsNoPositionAndKeepsModifiers(t *testing.T) {
	fake := newFakeDesktop()
	driver := driverWith(fake, nil)
	ctx := context.Background()
	if err := driver.Input(ctx, SurfaceInput{Kind: SurfaceInputText, Text: "héllo"}, InputOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Input(ctx, SurfaceInput{Kind: SurfaceInputKey, Key: "Enter", Modifiers: []string{"command", "shift"}}, InputOptions{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.texts, []string{"héllo"}) || !reflect.DeepEqual(fake.keys, []string{"command+shift+enter"}) {
		t.Fatalf("texts=%q keys=%q", fake.texts, fake.keys)
	}
	if err := driver.Input(ctx, SurfaceInput{Kind: SurfaceInputText, Text: strings.Repeat("a", 5000)}, InputOptions{}); err == nil {
		t.Fatal("accepted text over the default limit")
	}
}

func TestCaptureEnforcesTheCallersLimits(t *testing.T) {
	fake := newFakeDesktop()
	driver := driverWith(fake, nil)
	ctx := context.Background()
	if _, err := driver.Capture(ctx, CaptureOptions{}); err == nil {
		t.Fatal("accepted an empty image")
	}
	fake.capture_ = SurfaceCapture{Format: "image/png", Width: 300, Height: 200, Data: make([]byte, 100)}
	got, err := driver.Capture(ctx, CaptureOptions{})
	if err != nil || got.CapturedAt.IsZero() {
		t.Fatalf("capture = %+v, %v", got, err)
	}
	if _, err := driver.Capture(ctx, CaptureOptions{MaxBytes: 10}); err == nil {
		t.Fatal("accepted an image over the byte limit")
	}
	if _, err := driver.Capture(ctx, CaptureOptions{MaxPixels: 1000}); err == nil {
		t.Fatal("accepted an image over the pixel limit")
	}
}

type fakeRecorder struct{ called bool }

func (r *fakeRecorder) Record(context.Context, media.RecordingOptions) (media.Recording, error) {
	r.called = true
	return emptyRecording{}, nil
}

type emptyRecording struct{}

func (emptyRecording) Read([]byte) (int, error)   { return 0, io.EOF }
func (emptyRecording) Close() error               { return nil }
func (emptyRecording) Stop(context.Context) error { return nil }

func TestRecordingNeedsTheCallersRecorder(t *testing.T) {
	options := media.RecordingOptions{Format: "mp4"}
	if _, err := RecordDesktop(context.Background(), driverWith(newFakeDesktop(), nil), options); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("without a recorder: %v", err)
	}
	recorder := &fakeRecorder{}
	if _, err := RecordDesktop(context.Background(), driverWith(newFakeDesktop(), recorder), options); err != nil || !recorder.called {
		t.Fatalf("with a recorder: %v called=%v", err, recorder.called)
	}
}

func TestPointerSessionHoldsDragsAndReleases(t *testing.T) {
	fake := newFakeDesktop()
	session, err := driverWith(fake, nil).OpenPointerSession(context.Background(), PointerSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	steps := []error{
		session.Move(ctx, SurfacePoint{X: 5, Y: 5}),
		session.Down(ctx, SurfacePoint{X: 5, Y: 5}, ""),
		session.Move(ctx, SurfacePoint{X: 50, Y: 60}),
		session.Up(ctx, SurfacePoint{X: 50, Y: 60}, "left"),
	}
	for index, err := range steps {
		if err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
	}
	want := []nativePointer{
		{op: pointerMove, x: -95, y: 55},
		{op: pointerDown, button: "left", x: -95, y: 55},
		{op: pointerHeldMove, button: "left", x: -50, y: 110},
		{op: pointerUp, button: "left", x: -50, y: 110},
	}
	if got := fake.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.recorded()); got != len(want) {
		t.Fatalf("closing with nothing held produced %d extra events", got-len(want))
	}
}

func TestPointerSessionRefusesBadSequencesAndKeepsStateOnFailure(t *testing.T) {
	fake := newFakeDesktop()
	session, _ := driverWith(fake, nil).OpenPointerSession(context.Background(), PointerSessionOptions{})
	defer session.Close()
	ctx := context.Background()
	if err := session.Up(ctx, SurfacePoint{}, "left"); err == nil {
		t.Fatal("released a button that was not held")
	}
	if err := session.Down(ctx, SurfacePoint{X: 400, Y: 0}, "left"); err == nil {
		t.Fatal("pressed outside the displays")
	}
	if err := session.Down(ctx, SurfacePoint{}, "back"); err == nil {
		t.Fatal("accepted an unknown button")
	}
	fake.failNext = errors.New("native failure")
	if err := session.Down(ctx, SurfacePoint{}, "left"); err == nil {
		t.Fatal("hid a native failure")
	}
	if err := session.Down(ctx, SurfacePoint{}, "left"); err != nil {
		t.Fatalf("a failed press must not count as held: %v", err)
	}
	if err := session.Down(ctx, SurfacePoint{}, "left"); err == nil {
		t.Fatal("pressed a held button twice")
	}
}

func TestClosingASessionReleasesEveryHeldButtonAtTheLastPosition(t *testing.T) {
	fake := newFakeDesktop()
	session, _ := driverWith(fake, nil).OpenPointerSession(context.Background(), PointerSessionOptions{})
	ctx := context.Background()
	for _, button := range []string{"left", "right"} {
		if err := session.Down(ctx, SurfacePoint{X: 1, Y: 2}, button); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Move(ctx, SurfacePoint{X: 9, Y: 8}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	events := fake.recorded()
	released := events[len(events)-2:]
	want := []nativePointer{{op: pointerUp, button: "right", x: -91, y: 58}, {op: pointerUp, button: "left", x: -91, y: 58}}
	if !reflect.DeepEqual(released, want) {
		t.Fatalf("released %+v, want %+v", released, want)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := session.Move(ctx, SurfacePoint{}); !errors.Is(err, ErrPointerSessionClosed) {
		t.Fatalf("use after Close: %v", err)
	}
}

func TestASessionReleasesWhenItsTimeRunsOutOrItsContextEnds(t *testing.T) {
	for name, open := range map[string]func(*NativeDesktop) (*PointerSession, func()){
		"time": func(d *NativeDesktop) (*PointerSession, func()) {
			session, err := d.OpenPointerSession(context.Background(), PointerSessionOptions{MaxDuration: 30 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			return session, func() {}
		},
		"context": func(d *NativeDesktop) (*PointerSession, func()) {
			ctx, cancel := context.WithCancel(context.Background())
			session, err := d.OpenPointerSession(ctx, PointerSessionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return session, cancel
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeDesktop()
			session, end := open(driverWith(fake, nil))
			if err := session.Down(context.Background(), SurfacePoint{X: 3, Y: 4}, "left"); err != nil {
				t.Fatal(err)
			}
			end()
			deadline := time.Now().Add(2 * time.Second)
			for {
				events := fake.recorded()
				if len(events) == 2 && events[1].op == pointerUp {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the held button was not released: %+v", events)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := session.Move(context.Background(), SurfacePoint{}); !errors.Is(err, ErrPointerSessionClosed) {
				t.Fatalf("use after the session ended: %v", err)
			}
		})
	}
}

func TestPointerSessionOptionsAreBounded(t *testing.T) {
	driver := driverWith(newFakeDesktop(), nil)
	if _, err := driver.OpenPointerSession(context.Background(), PointerSessionOptions{MaxDuration: 2 * time.Hour}); err == nil {
		t.Fatal("accepted a session over an hour")
	}
	if _, err := driver.OpenPointerSession(context.Background(), PointerSessionOptions{MaxDuration: -time.Second}); err == nil {
		t.Fatal("accepted a negative duration")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := driver.OpenPointerSession(ctx, PointerSessionOptions{}); err == nil {
		t.Fatal("opened a session on a finished context")
	}
}
