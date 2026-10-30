package pipewire

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/webong/ext/res/system/audio"
	"github.com/webong/ext/res/system/media"
)

// Opt-in means prerequisites and both streams are required, never silently
// skipped. The caller supplies isolated device names; no hardware is selected.
func TestNativePipeWireRequiresOptIn(t *testing.T) {
	if os.Getenv("EXT_PIPEWIRE_NATIVE_TESTS") != "1" {
		t.Skip("set EXT_PIPEWIRE_NATIVE_TESTS=1")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("native PipeWire verification requires Linux")
	}
	for _, tool := range []string{"pw-dump", "pw-record", "pw-play"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	input, output := os.Getenv("EXT_PIPEWIRE_TEST_INPUT"), os.Getenv("EXT_PIPEWIRE_TEST_OUTPUT")
	if input == "" || output == "" {
		t.Fatal("supply EXT_PIPEWIRE_TEST_INPUT and EXT_PIPEWIRE_TEST_OUTPUT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	snapshot, err := NewObserver(nil).Observe(ctx, audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	foundInput, foundOutput := false, false
	for _, d := range snapshot.Devices {
		foundInput = foundInput || (d.ID == input && d.Kind == audio.KindInput)
		foundOutput = foundOutput || (d.ID == output && d.Kind == audio.KindOutput)
	}
	if !foundInput || !foundOutput {
		t.Fatalf("test endpoints missing: %#v", snapshot)
	}
	backend := NewBackend(nil)
	source, err := backend.OpenCapture(ctx, input, audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	packet, err := source.Read(ctx)
	if err != nil || len(packet.Data) != 3840 || packet.SampleRate != 48000 || packet.Channels != 2 {
		t.Fatalf("capture: %d bytes %d Hz %d channels, %v", len(packet.Data), packet.SampleRate, packet.Channels, err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	sink, err := backend.OpenPlayback(ctx, output, audio.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	for i := 0; i < 20; i++ {
		if err := sink.Write(ctx, media.PCM16{Data: make([]byte, 3840), SampleRate: 48000, Channels: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}
