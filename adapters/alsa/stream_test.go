package alsa

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

const playbackList = `default
    Default ALSA Output (currently PulseAudio Sound Server)
sysdefault:CARD=PCH
    HDA Intel PCH, ALC295 Analog
    Default Audio Device
null
    Discard all samples
`

const captureList = `default
    Default ALSA Input (currently PulseAudio Sound Server)
sysdefault:CARD=PCH
    HDA Intel PCH, ALC295 Analog
    Default Audio Device
`

func TestObserveALSADevices(t *testing.T) {
	observer := NewObserver(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "-L" {
			return nil, errors.New("unexpected arguments")
		}
		switch name {
		case "aplay":
			return []byte(playbackList), nil
		case "arecord":
			return []byte(captureList), nil
		}
		return nil, errors.New("unexpected executable")
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Backend != "alsa" || snapshot.Status.State != "complete" || len(snapshot.Devices) != 4 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Devices[0].ID != "output:default" || snapshot.Devices[0].Kind != audio.KindOutput {
		t.Fatalf("first device = %#v", snapshot.Devices[0])
	}
	if snapshot.Devices[0].Name != "Default ALSA Output (currently PulseAudio Sound Server)" {
		t.Fatalf("name = %q", snapshot.Devices[0].Name)
	}
	if snapshot.Devices[2].ID != "input:default" || snapshot.Devices[2].Kind != audio.KindInput {
		t.Fatalf("input device = %#v", snapshot.Devices[2])
	}
	for _, device := range snapshot.Devices {
		if strings.HasPrefix(device.ID, "output:null") || strings.HasPrefix(device.ID, "input:null") {
			t.Fatalf("null PCM was reported: %#v", device)
		}
	}
}

func TestObserveALSABoundsAndPartialState(t *testing.T) {
	observer := NewObserver(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("   orphan description\ndefault\n    Output\nsysdefault:CARD=PCH\n    Card\n"), nil
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{MaxDevices: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Devices) != 1 || snapshot.Status.State != "partial" {
		t.Fatalf("bounded snapshot = %#v", snapshot)
	}
	if !strings.Contains(snapshot.Status.Detail, "unusable") {
		t.Fatalf("detail = %q", snapshot.Status.Detail)
	}
}

func TestObserveALSAFailsExplicitly(t *testing.T) {
	observer := NewObserver(func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not found")
	})
	snapshot, err := observer.Observe(context.Background(), audio.Limits{})
	if !errors.Is(err, audio.ErrUnavailable) || snapshot.Status.State != "unsupported" {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}
	observer = NewObserver(func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "arecord" {
			return nil, errors.New("capture unavailable")
		}
		return []byte("default\n    Output\n"), nil
	})
	snapshot, err = observer.Observe(context.Background(), audio.Limits{})
	if err != nil || snapshot.Status.State != "partial" {
		t.Fatalf("one-sided snapshot = %#v, %v", snapshot, err)
	}
}

func TestDeviceIDsRequireDirection(t *testing.T) {
	backend := NewBackend(nil)
	if _, err := backend.OpenCapture(context.Background(), "output:default", audio.Limits{}); !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("wrong capture direction = %v", err)
	}
	if _, err := backend.OpenPlayback(context.Background(), "input:default", audio.Limits{}); !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("wrong playback direction = %v", err)
	}
	for _, id := range []string{"input:", "input:-bad", "input:bad\x00name"} {
		if _, err := backend.OpenCapture(context.Background(), id, audio.Limits{}); !errors.Is(err, ErrInvalidDevice) {
			t.Errorf("%q = %v", id, err)
		}
	}
}

func TestStreamArgumentsAndPackets(t *testing.T) {
	format := Format{SampleRate: 8_000, Channels: 1, PacketMillis: 10}
	arguments := strings.Join(format.arguments("plughw:CARD=PCH,DEV=0"), " ")
	for _, fragment := range []string{"-D plughw:CARD=PCH,DEV=0", "-f S16_LE", "-r 8000", "-c 1", "-t raw"} {
		if !strings.Contains(arguments, fragment) {
			t.Errorf("arguments are missing %q: %s", fragment, arguments)
		}
	}
	if format.packetBytes() != 160 {
		t.Fatalf("packet bytes = %d", format.packetBytes())
	}

	captureProcess := &fakeProcess{out: bytes.NewBuffer(bytes.Repeat([]byte{1}, 200))}
	source, err := NewBackend(func(context.Context, string, ...string) (Process, error) {
		return captureProcess, nil
	}).OpenCapture(context.Background(), "input:default", audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	source.(*stream).format = format
	packet, err := source.Read(context.Background())
	if err != nil || len(packet.Data) != 160 {
		t.Fatalf("packet = %d bytes, %v", len(packet.Data), err)
	}
	if err = source.Close(); err != nil || !captureProcess.killed {
		t.Fatalf("close = %v, killed = %v", err, captureProcess.killed)
	}

	playProcess := &fakeProcess{out: bytes.NewBuffer(nil), in: bytes.NewBuffer(nil)}
	backend := NewBackend(func(context.Context, string, ...string) (Process, error) {
		return playProcess, nil
	})
	backend.Format = format
	sink, err := backend.OpenPlayback(context.Background(), "output:default", audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err = sink.Write(context.Background(), media.PCM16{SampleRate: 8_000, Channels: 1, Data: make([]byte, 160)}); err != nil {
		t.Fatal(err)
	}
	if playProcess.in.Len() != 160 {
		t.Fatalf("written bytes = %d", playProcess.in.Len())
	}
	if err = sink.Write(context.Background(), media.PCM16{SampleRate: 44_100, Channels: 1, Data: make([]byte, 160)}); !errors.Is(err, audio.ErrInvalid) {
		t.Fatalf("format mismatch = %v", err)
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
