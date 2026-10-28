package pipewire

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/webong/ext/res/system/audio"
)

// TestNativePipeWireRequiresOptIn exercises the real pw-dump and pw-record
// tools. It is skipped unless EXT_PIPEWIRE_NATIVE_TESTS=1, the host is Linux,
// the tools are installed, and a PipeWire server is running.
func TestNativePipeWireRequiresOptIn(t *testing.T) {
	if os.Getenv("EXT_PIPEWIRE_NATIVE_TESTS") != "1" {
		t.Skip("set EXT_PIPEWIRE_NATIVE_TESTS=1 to run PipeWire integration tests")
	}
	if runtime.GOOS != "linux" {
		t.Skip("PipeWire integration tests need Linux")
	}
	for _, tool := range []string{"pw-dump", "pw-record"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "pw-dump").Run(); err != nil {
		t.Skipf("no running PipeWire server: %v", err)
	}

	observer := NewObserver(nil)
	snapshot, err := observer.Observe(ctx, audio.Limits{})
	if err != nil {
		if errors.Is(err, audio.ErrUnavailable) {
			t.Skipf("PipeWire is not running: %v", err)
		}
		t.Fatal(err)
	}
	if snapshot.Backend != "pipewire" || snapshot.Status.State == "unsupported" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, device := range snapshot.Devices {
		if device.ID == "" || device.Name == "" {
			t.Fatalf("device = %#v", device)
		}
		if device.Kind != audio.KindInput && device.Kind != audio.KindOutput && device.Kind != audio.KindMonitor {
			t.Fatalf("device kind = %#v", device)
		}
		if device.SampleFormat != "s16" {
			t.Fatalf("device format = %#v", device)
		}
	}

	for _, device := range snapshot.Devices {
		if device.Kind != audio.KindInput {
			continue
		}
		backend := NewBackend(nil)
		if device.SampleRate >= 8_000 && device.SampleRate <= 192_000 &&
			device.Channels >= 1 && device.Channels <= 8 {
			backend.Format = Format{SampleRate: device.SampleRate, Channels: device.Channels, PacketMillis: 20}
		} else {
			backend.Format = Format{SampleRate: 48_000, Channels: 2, PacketMillis: 20}
		}
		limits, err := (audio.Limits{MaxPacketBytes: 1 << 20}).Normalize()
		if err != nil {
			t.Fatal(err)
		}
		source, err := backend.OpenCapture(ctx, device.ID, limits)
		if err != nil {
			t.Skipf("cannot capture from %s: %v", device.ID, err)
		}
		packet, readErr := source.Read(ctx)
		closeErr := source.Close()
		if readErr != nil {
			t.Fatalf("read from %s: %v", device.ID, readErr)
		}
		if len(packet.Data) == 0 || packet.SampleRate != backend.Format.SampleRate || packet.Channels != backend.Format.Channels {
			t.Fatalf("packet = %d bytes, %d Hz, %d channels", len(packet.Data), packet.SampleRate, packet.Channels)
		}
		if closeErr != nil {
			t.Fatalf("close %s: %v", device.ID, closeErr)
		}
		return
	}
	t.Log("no input device was available for capture")
}
