package pipewire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/webong/ext/res/system/audio"
	"github.com/webong/ext/res/system/media"
)

// pwDumpFixture follows the real pw-dump shape: state is a sibling of props and
// stream parameters carry the negotiated rate and channel count.
const pwDumpFixture = `[
  {"id":0,"type":"PipeWire:Interface:Metadata","version":3,"info":null},
  {
    "id":41,"type":"PipeWire:Interface:Node","version":3,
    "permissions":["r","w","x","m"],
    "info":{
      "max-input-ports":65,"max-output-ports":0,"state":"running",
      "props":{
        "media.class":"Audio/Sink",
        "node.name":"alsa_output.pci-0000_00_1f.3.analog-stereo",
        "audio.channels":2,"audio.rate":48000
      },
      "params":{"Format":[{"mediaType":"audio","mediaSubtype":"raw","format":"S16LE","rate":48000,"channels":2}]}
    }
  },
  {
    "id":42,"type":"PipeWire:Interface:Node","version":3,
    "info":{
      "state":"suspended",
      "props":{"media.class":"Audio/Sink","node.name":"alsa_output.usb-headset"},
      "params":{"EnumFormat":[{"mediaType":"audio","mediaSubtype":"raw","format":"S16LE","rate":44100,"channels":2}]}
    }
  },
  {
    "id":43,"type":"PipeWire:Interface:Node","version":3,
    "info":{
      "state":"idle",
      "props":{"media.class":"Audio/Source","node.name":"alsa_input.usb-microphone"},
      "params":{"Format":[{"mediaType":"audio","mediaSubtype":"raw","format":"S16LE","rate":48000,"channels":1}]}
    }
  },
  {
    "id":44,"type":"PipeWire:Interface:Node","version":3,
    "info":{
      "state":"idle",
      "props":{"media.class":"Audio/Source","node.name":"alsa_output.pci-0000_00_1f.3.analog-stereo.monitor"},
      "params":{"Format":[{"mediaType":"audio","mediaSubtype":"raw","format":"S16LE","rate":48000,"channels":2}]}
    }
  },
  {"id":45,"type":"PipeWire:Interface:Node","version":3,
   "info":{"state":"idle","props":{"media.class":"Stream/Output/Audio","node.name":"player"}}}
]`

func TestObservePipewireDevices(t *testing.T) {
	observer := NewObserver(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "pw-dump" {
			return nil, errors.New("unexpected executable")
		}
		if len(args) != 0 {
			return nil, errors.New("unexpected arguments")
		}
		return []byte(pwDumpFixture), nil
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Backend != "pipewire" || snapshot.Status.State != "complete" || len(snapshot.Devices) != 4 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	sink := snapshot.Devices[0]
	if sink.Kind != audio.KindOutput || sink.SampleRate != 48_000 || sink.Channels != 2 ||
		sink.SampleFormat != "s16" || sink.State != "running" ||
		sink.ID != "alsa_output.pci-0000_00_1f.3.analog-stereo" {
		t.Fatalf("sink = %#v", sink)
	}
	if snapshot.Devices[1].SampleRate != 44_100 || snapshot.Devices[1].State != "suspended" {
		t.Fatalf("second sink = %#v", snapshot.Devices[1])
	}
	microphone := snapshot.Devices[2]
	if microphone.Kind != audio.KindInput || microphone.Channels != 1 ||
		microphone.SampleRate != 48_000 {
		t.Fatalf("microphone = %#v", microphone)
	}
	if snapshot.Devices[3].Kind != audio.KindMonitor {
		t.Fatalf("monitor = %#v", snapshot.Devices[3])
	}
}

func TestObserveReportsMalformedAndBoundedRecords(t *testing.T) {
	observer := NewObserver(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`[
          {"id":0,"type":"PipeWire:Interface:Node","info":{"state":"idle","props":{"media.class":"Audio/Sink","node.name":"one"}}},
          {"id":1,"type":"PipeWire:Interface:Node","info":{"state":"idle","props":{"media.class":"Audio/Sink"}}}
        ]`), nil
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{MaxDevices: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Devices) != 1 || snapshot.Status.State != "partial" {
		t.Fatalf("bounded snapshot = %#v", snapshot)
	}
}

func TestObserveRejectsOversizedOutputBeforeDecoding(t *testing.T) {
	observer := NewObserver(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(strings.Repeat(" ", 4096)), nil
	})
	observer.MaxOutputBytes = 32
	snapshot, err := observer.Observe(context.Background(), audio.Limits{})
	if !errors.Is(err, audio.ErrLimit) {
		t.Fatalf("error = %v", err)
	}
	if snapshot.Status.State != "partial" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestObserveReportsMalformedAndEmptyPayloads(t *testing.T) {
	for name, payload := range map[string]string{
		"malformed": `[{"id":0,"type":`,
		"empty":     "",
		"trailing":  `[] []`,
		"notarray":  `{"id":0}`,
	} {
		observer := NewObserver(func(context.Context, string, ...string) ([]byte, error) {
			return []byte(payload), nil
		})
		snapshot, err := observer.Observe(context.Background(), audio.Limits{})
		if !errors.Is(err, audio.ErrUnavailable) {
			t.Errorf("%s error = %v", name, err)
		}
		if snapshot.Status.State != "partial" {
			t.Errorf("%s status = %#v", name, snapshot.Status)
		}
	}
}

func TestObserveFailsExplicitlyWithoutPwDump(t *testing.T) {
	observer := NewObserver(func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not found")
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{})
	if !errors.Is(err, audio.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if snapshot.Status.State != "unsupported" || len(snapshot.Devices) != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestDeviceValidation(t *testing.T) {
	backend := NewBackend(nil)
	for _, id := range []string{"", "-bad", "../escape", "bad\x00id", strings.Repeat("x", 257)} {
		if _, err := backend.OpenCapture(context.Background(), id, audio.Limits{}); !errors.Is(err, ErrInvalidDevice) {
			t.Errorf("%q = %v", id, err)
		}
	}
}

func TestStreamArgumentsUseDocumentedInterface(t *testing.T) {
	format := Format{SampleRate: 8_000, Channels: 1, PacketMillis: 10}
	want := []string{"--target=microphone", "--format=s16", "--rate=8000", "--channels=1", "--raw", "-"}
	arguments := format.arguments("microphone")
	if strings.Join(arguments, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v", arguments)
	}
	if format.packetBytes() != 160 {
		t.Fatalf("packet bytes = %d", format.packetBytes())
	}

	for _, playback := range []bool{false, true} {
		var executable string
		var received []string
		backend := NewBackend(func(_ context.Context, name string, arguments ...string) (Process, error) {
			executable = name
			received = append([]string(nil), arguments...)
			return &fakeProcess{out: &blockingReader{closed: make(chan struct{})}, in: bytes.NewBuffer(nil)}, nil
		})
		backend.Format = format
		var err error
		if playback {
			_, err = backend.OpenPlayback(context.Background(), "microphone", audio.Limits{})
		} else {
			_, err = backend.OpenCapture(context.Background(), "microphone", audio.Limits{})
		}
		if err != nil {
			t.Fatal(err)
		}
		expectedExecutable := "pw-record"
		if playback {
			expectedExecutable = "pw-play"
		}
		if executable != expectedExecutable {
			t.Errorf("executable = %q", executable)
		}
		if strings.Join(received, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("playback=%t arguments = %#v", playback, received)
		}
	}
}

func TestCaptureReadsBoundedPacket(t *testing.T) {
	process := &fakeProcess{out: &blockingReader{closed: make(chan struct{}), data: bytes.Repeat([]byte{1}, 200)}, in: bytes.NewBuffer(nil)}
	source, err := NewBackend(func(context.Context, string, ...string) (Process, error) {
		return process, nil
	}).OpenCapture(context.Background(), "microphone", audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	source.(*stream).format = Format{SampleRate: 8_000, Channels: 1, PacketMillis: 10}
	packet, err := source.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Data) != 160 || packet.SampleRate != 8_000 || packet.Channels != 1 {
		t.Fatalf("packet = %d bytes, %d Hz, %d channels", len(packet.Data), packet.SampleRate, packet.Channels)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	process.mu.Lock()
	stopped := process.stopped
	process.mu.Unlock()
	if !stopped {
		t.Fatal("process was not stopped")
	}
	if _, err = source.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("closed read = %v", err)
	}
}

func TestPlaybackWritesValidatedPackets(t *testing.T) {
	process := &fakeProcess{out: &blockingReader{closed: make(chan struct{})}, in: bytes.NewBuffer(nil)}
	backend := NewBackend(func(context.Context, string, ...string) (Process, error) {
		return process, nil
	})
	backend.Format = Format{SampleRate: 8_000, Channels: 1, PacketMillis: 10}
	sink, err := backend.OpenPlayback(context.Background(), "sink-one", audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	packet := media.PCM16{SampleRate: 8_000, Channels: 1, Data: make([]byte, 160)}
	if err = sink.Write(context.Background(), packet); err != nil {
		t.Fatal(err)
	}
	if process.in.Len() != 160 {
		t.Fatalf("written bytes = %d", process.in.Len())
	}
	if err = sink.Write(context.Background(), media.PCM16{SampleRate: 44_100, Channels: 1, Data: make([]byte, 160)}); !errors.Is(err, audio.ErrInvalid) {
		t.Fatalf("format mismatch = %v", err)
	}
	if err = sink.Write(context.Background(), media.PCM16{SampleRate: 8_000, Channels: 1, Data: []byte{1}}); !errors.Is(err, audio.ErrLimit) {
		t.Fatalf("misaligned packet = %v", err)
	}
	if err = sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureHonorsContextCancellation(t *testing.T) {
	reader := &blockingReader{closed: make(chan struct{})}
	process := &fakeProcess{out: reader, in: bytes.NewBuffer(nil)}
	source, err := NewBackend(func(context.Context, string, ...string) (Process, error) {
		return process, nil
	}).OpenCapture(context.Background(), "microphone", audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = source.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseIgnoresIntentionalKill(t *testing.T) {
	process := &stubbornProcess{closed: make(chan struct{})}
	source, err := NewBackend(func(context.Context, string, ...string) (Process, error) {
		return process, nil
	}).OpenCapture(context.Background(), "microphone", audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatalf("close = %v", err)
	}
	if !process.killed {
		t.Fatal("process was not killed")
	}
	if err = source.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
}

type fakeProcess struct {
	out     io.ReadCloser
	in      *bytes.Buffer
	killed  bool
	stopped bool
	mu      sync.Mutex
}

func (f *fakeProcess) Stdout() io.ReadCloser { return f.out }
func (f *fakeProcess) Stdin() io.WriteCloser {
	return nopWriteCloser{Writer: f.in}
}
func (f *fakeProcess) Wait() error {
	// A real child exits when its pipe is closed; model that so Close can stop
	// it deliberately without reporting a failure.
	if closer, ok := f.out.(io.Closer); ok {
		_ = closer.Close()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	return nil
}
func (f *fakeProcess) Kill() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = true
	if f.out != nil {
		_ = f.out.Close()
	}
	return nil
}

// stubbornProcess waits for Kill and reports the signal-style wait error a
// stopped child returns.
type stubbornProcess struct {
	closed chan struct{}
	once   sync.Once
	killed bool
	mu     sync.Mutex
}

func (p *stubbornProcess) Stdout() io.ReadCloser { return &blockingReader{closed: make(chan struct{})} }
func (p *stubbornProcess) Stdin() io.WriteCloser { return nopWriteCloser{Writer: bytes.NewBuffer(nil)} }
func (p *stubbornProcess) Wait() error {
	<-p.closed
	return errors.New("signal: killed")
}
func (p *stubbornProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.once.Do(func() { close(p.closed) })
	return nil
}

// blockingReader blocks until Close or the optional data is consumed.
type blockingReader struct {
	closed chan struct{}
	data   []byte
	once   sync.Once
}

func (r *blockingReader) Read(buffer []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(buffer, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	<-r.closed
	return 0, io.EOF
}

func (r *blockingReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
