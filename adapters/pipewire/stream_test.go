package pipewire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/webong/ext/res/system/audio"
	"github.com/webong/ext/res/system/media"
)

const allOutput = `[{"id":0,"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Sink","node.name":"sink-one","node.state":"RUNNING","audio.rate":"48000","audio.channels":"2"}}},{"id":1,"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Sink","node.name":"sink-two","node.state":"SUSPENDED","audio.rate":"44100","audio.channels":"2"}}},{"id":2,"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Source","node.name":"microphone","node.state":"IDLE","audio.rate":"48000","audio.channels":"1"}}},{"id":3,"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Monitor","node.name":"sink-one.monitor","node.state":"IDLE","audio.rate":"48000","audio.channels":"2"}}}]` + "\n"

func TestObservePipewireDevices(t *testing.T) {
	observer := NewObserver(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "pw-cli" {
			return nil, errors.New("unexpected executable")
		}
		if len(args) != 1 || args[0] != "list-objects" {
			return nil, errors.New("unexpected arguments")
		}
		return []byte(allOutput), nil
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Backend != "pipewire" || snapshot.Status.State != "complete" || len(snapshot.Devices) != 4 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Devices[0].Kind != audio.KindOutput || snapshot.Devices[0].SampleRate != 48_000 ||
		snapshot.Devices[0].Channels != 2 || snapshot.Devices[0].SampleFormat != "s16le" {
		t.Fatalf("sink = %#v", snapshot.Devices[0])
	}
	if snapshot.Devices[2].Kind != audio.KindInput {
		t.Fatalf("microphone = %#v", snapshot.Devices[2])
	}
	if snapshot.Devices[3].Kind != audio.KindMonitor {
		t.Fatalf("monitor = %#v", snapshot.Devices[3])
	}
	if snapshot.Devices[1].State != "suspended" {
		t.Fatalf("state = %q", snapshot.Devices[1].State)
	}
}

func TestObserveReportsMalformedAndBoundedRecords(t *testing.T) {
	observer := NewObserver(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`[{"id":0,"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Sink","node.name":"one"}}},{"id":1,"type":"PipeWire:Interface:Node","info":{"props":{"media.class":"Audio/Sink"}}}]`), nil
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{MaxDevices: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Devices) != 1 || snapshot.Status.State != "partial" {
		t.Fatalf("bounded snapshot = %#v", snapshot)
	}
}

func TestObserveFailsExplicitlyWithoutPwCli(t *testing.T) {
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

func TestStreamArguments(t *testing.T) {
	format := Format{SampleRate: 8_000, Channels: 1, PacketMillis: 10}
	arguments := format.arguments("microphone")
	for _, expected := range []string{"--device=microphone", "--format=s16le", "--rate=8000", "--channels=1", "--raw"} {
		found := false
		for _, argument := range arguments {
			if argument == expected {
				found = true
			}
		}
		if !found {
			t.Errorf("arguments are missing %q: %#v", expected, arguments)
		}
	}
	if format.packetBytes() != 160 {
		t.Fatalf("packet bytes = %d", format.packetBytes())
	}
}

func TestCaptureReadsBoundedPacket(t *testing.T) {
	process := &fakeProcess{out: bytes.NewBuffer(bytes.Repeat([]byte{1}, 200))}
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
	if !process.killed {
		t.Fatal("process was not stopped")
	}
	if _, err = source.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("closed read = %v", err)
	}
}

func TestPlaybackWritesValidatedPackets(t *testing.T) {
	process := &fakeProcess{out: bytes.NewBuffer(nil), in: bytes.NewBuffer(nil)}
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

type fakeProcess struct {
	out    *bytes.Buffer
	in     *bytes.Buffer
	killed bool
}

func (f *fakeProcess) Stdout() io.ReadCloser { return io.NopCloser(f.out) }
func (f *fakeProcess) Stdin() io.WriteCloser { return nopWriteCloser{Writer: f.in} }
func (f *fakeProcess) Wait() error           { return nil }
func (f *fakeProcess) Kill() error           { f.killed = true; return nil }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
