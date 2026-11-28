package audio

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/webong/ext/src/ctx/system/capture/media"
)

func validSnapshot() Snapshot {
	return Snapshot{
		Backend: "fixture", ObservedAt: time.Now().UTC(), Status: Status{State: "complete"},
		Devices: []Device{
			{ID: "sink-1", Name: "Output", Kind: KindOutput, State: "running", SampleFormat: "s16le", SampleRate: 48_000, Channels: 2},
			{ID: "source-1", Name: "Microphone", Kind: KindInput},
			{ID: "sink-1.monitor", Name: "Monitor", Kind: KindMonitor},
		},
	}
}

func TestValidateSnapshot(t *testing.T) {
	if err := ValidateSnapshot(validSnapshot(), DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Snapshot){
		"backend":  func(s *Snapshot) { s.Backend = "" },
		"time":     func(s *Snapshot) { s.ObservedAt = time.Time{} },
		"status":   func(s *Snapshot) { s.Status.State = "ready" },
		"kind":     func(s *Snapshot) { s.Devices[0].Kind = "speaker" },
		"id":       func(s *Snapshot) { s.Devices[0].ID = "" },
		"name":     func(s *Snapshot) { s.Devices[0].Name = "" },
		"control":  func(s *Snapshot) { s.Devices[0].ID = "bad\x00id" },
		"rate":     func(s *Snapshot) { s.Devices[0].SampleRate = 1 },
		"channels": func(s *Snapshot) { s.Devices[0].Channels = 9 },
		"duplicate": func(s *Snapshot) {
			s.Devices[1].ID = s.Devices[0].ID
		},
	} {
		snapshot := validSnapshot()
		mutate(&snapshot)
		if err := ValidateSnapshot(snapshot, DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s snapshot = %v", name, err)
		}
	}
	snapshot := validSnapshot()
	if err := ValidateSnapshot(snapshot, over(Limits{MaxDevices: 2})); !errors.Is(err, ErrLimit) {
		t.Fatalf("device bound = %v", err)
	}
	snapshot = validSnapshot()
	snapshot.Status.State = "unsupported"
	snapshot.Status.Reason = "unsupported"
	snapshot.Devices = nil
	if err := ValidateSnapshot(snapshot, DefaultLimits()); err != nil {
		t.Fatalf("explicit unsupported snapshot = %v", err)
	}
}

func TestLimitsValidateCeilings(t *testing.T) {
	for name, limits := range map[string]Limits{
		"devices": {MaxDevices: MaxDevices + 1},
		"packets": {MaxPacketBytes: MaxPacketBytes + 1},
		"zero":    {MaxDevices: -1},
	} {
		if err := over(limits).Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if err := (Limits{}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("zero limits = %v", err)
	}
	defaults := DefaultLimits()
	if err := defaults.Validate(); err != nil || defaults.MaxDevices != DefaultMaxDevices || defaults.MaxPacketBytes != DefaultMaxPacketBytes {
		t.Fatalf("defaults = %#v, %v", defaults, err)
	}
}

func TestValidatePacketUsesMediaBounds(t *testing.T) {
	packet := media.PCM16{SampleRate: 48_000, Channels: 2, Data: make([]byte, 4)}
	if err := ValidatePacket(packet, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePacket(packet, over(Limits{MaxPacketBytes: 2})); !errors.Is(err, ErrLimit) {
		t.Fatalf("packet bound = %v", err)
	}
	if err := ValidatePacket(media.PCM16{SampleRate: 48_000, Channels: 2, Data: []byte{1}}, DefaultLimits()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("misaligned packet = %v", err)
	}
}

func TestSelectRequiresKindMatch(t *testing.T) {
	snapshot := validSnapshot()
	if device, err := Select(snapshot, KindInput, ""); err != nil || device.ID != "source-1" {
		t.Fatalf("first input = %#v, %v", device, err)
	}
	if device, err := Select(snapshot, KindMonitor, "sink-1.monitor"); err != nil || device.Kind != KindMonitor {
		t.Fatalf("explicit monitor = %#v, %v", device, err)
	}
	if _, err := Select(snapshot, KindInput, "sink-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong kind = %v", err)
	}
	if _, err := Select(snapshot, KindInput, "missing"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing device = %v", err)
	}
	if _, err := Select(Snapshot{Devices: []Device{}}, KindOutput, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty snapshot = %v", err)
	}
}

func TestObserverFuncMatchesInterface(t *testing.T) {
	var observer Observer = ObserverFunc(func(context.Context, Limits) (Snapshot, error) {
		return validSnapshot(), nil
	})
	if snapshot, err := observer.Observe(context.Background(), DefaultLimits()); err != nil || len(snapshot.Devices) != 3 {
		t.Fatalf("observation = %#v, %v", snapshot, err)
	}
}

// over returns the default limits with the fields set in l in their place.
func over(l Limits) Limits {
	out := DefaultLimits()
	if l.MaxDevices != 0 {
		out.MaxDevices = l.MaxDevices
	}
	if l.MaxPacketBytes != 0 {
		out.MaxPacketBytes = l.MaxPacketBytes
	}
	return out
}
