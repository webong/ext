package audio

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/webong/ext/res/system/capture/media"
	"github.com/webong/ext/res/system/internal/fixtures"
)

type audioCases struct {
	Defaults struct {
		MaxDevices     int `json:"maxDevices"`
		MaxPacketBytes int `json:"maxPacketBytes"`
	} `json:"defaults"`
	SelectDevices []Device `json:"selectDevices"`
	Packets       []struct {
		Name       string  `json:"name"`
		SampleRate int     `json:"sampleRate"`
		Channels   int     `json:"channels"`
		Bytes      int     `json:"bytes"`
		MaxBytes   int     `json:"maxBytes"`
		Code       *string `json:"code"`
	} `json:"packets"`
	Playback []struct {
		Name     string  `json:"name"`
		Bytes    int     `json:"bytes"`
		MaxBytes int     `json:"maxBytes"`
		Code     *string `json:"code"`
	} `json:"playback"`
	Limits []struct {
		Name           string  `json:"name"`
		MaxDevices     int     `json:"maxDevices"`
		MaxPacketBytes int     `json:"maxPacketBytes"`
		Code           *string `json:"code"`
	} `json:"limits"`
	Devices []struct {
		Name       string          `json:"name"`
		Snapshot   json.RawMessage `json:"snapshot"`
		MaxDevices int             `json:"maxDevices"`
		Code       *string         `json:"code"`
	} `json:"devices"`
	Select []struct {
		Name string  `json:"name"`
		Kind Kind    `json:"kind"`
		ID   string  `json:"id"`
		Want *string `json:"want"`
		Code *string `json:"code"`
	} `json:"select"`
}

func expectCode(t *testing.T, codes map[string]bool, want *string, err error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		return
	}
	if !codes[*want] {
		t.Fatalf("code %q is not in testdata/codes.json", *want)
	}
	if err == nil {
		t.Fatalf("accepted, want %s", *want)
	}
	if got := Code(err); got != *want {
		t.Fatalf("code %q (%v), want %s", got, err, *want)
	}
}

// TestFixtures runs testdata/audio/v1. The packet rule is shared with the Swift, Java and C# playback
// sinks; the device snapshot and the limits only exist in Go today.
func TestFixtures(t *testing.T) {
	var cases audioCases
	fixtures.Load(t, "audio", 1, &cases)
	codes := fixtures.Codes(t)
	if len(cases.Packets) == 0 || len(cases.Playback) == 0 || len(cases.Devices) == 0 {
		t.Fatal("no cases")
	}
	t.Run("defaults", func(t *testing.T) {
		got, err := (Limits{}).Normalize()
		if err != nil || got.MaxDevices != cases.Defaults.MaxDevices || got.MaxPacketBytes != cases.Defaults.MaxPacketBytes {
			t.Fatalf("%+v, %v; want %+v", got, err, cases.Defaults)
		}
	})
	for _, c := range cases.Packets {
		t.Run("packets/"+c.Name, func(t *testing.T) {
			packet := media.PCM16{SampleRate: c.SampleRate, Channels: c.Channels, Data: make([]byte, c.Bytes)}
			expectCode(t, codes, c.Code, ValidatePacket(packet, Limits{MaxPacketBytes: c.MaxBytes}))
		})
	}
	for _, c := range cases.Playback {
		t.Run("playback/"+c.Name, func(t *testing.T) {
			packet := media.PCM16{SampleRate: 48_000, Channels: 2, Data: make([]byte, c.Bytes)}
			expectCode(t, codes, c.Code, ValidatePacket(packet, Limits{MaxPacketBytes: c.MaxBytes}))
		})
	}
	for _, c := range cases.Limits {
		t.Run("limits/"+c.Name, func(t *testing.T) {
			_, err := Limits{MaxDevices: c.MaxDevices, MaxPacketBytes: c.MaxPacketBytes}.Normalize()
			expectCode(t, codes, c.Code, err)
		})
	}
	for _, c := range cases.Devices {
		t.Run("devices/"+c.Name, func(t *testing.T) {
			var snapshot Snapshot
			if err := json.Unmarshal(c.Snapshot, &snapshot); err != nil {
				expectCode(t, codes, c.Code, fmt.Errorf("%w: %v", ErrInvalid, err))
				return
			}
			expectCode(t, codes, c.Code, ValidateSnapshot(snapshot, Limits{MaxDevices: c.MaxDevices}))
		})
	}
	for _, c := range cases.Select {
		t.Run("select/"+c.Name, func(t *testing.T) {
			device, err := Select(Snapshot{Devices: cases.SelectDevices}, c.Kind, c.ID)
			expectCode(t, codes, c.Code, err)
			if c.Code == nil && (c.Want == nil || device.ID != *c.Want) {
				t.Fatalf("selected %q, want %v", device.ID, c.Want)
			}
		})
	}
}
