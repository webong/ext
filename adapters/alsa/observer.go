// Package alsa observes ALSA PCM devices and opens bounded capture or playback
// streams through arecord and aplay. The caller selects the device; no card,
// routing, mixer, or default device is chosen here.
//
// ALSA command-line tools are Linux-native and selectable, so this backend lives
// in an adapter instead of res/system. PulseAudio and PipeWire have their own
// adapters, and the shared library never chooses between them.
package alsa

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/webong/ext/res/system/audio"
)

// CommandRunner is the native command boundary.
type CommandRunner func(context.Context, string, ...string) ([]byte, error)

type Observer struct {
	run CommandRunner
}

func NewObserver(run CommandRunner) *Observer {
	if run == nil {
		run = defaultRunner
	}
	return &Observer{run: run}
}

func (o *Observer) Observe(ctx context.Context, limits audio.Limits) (audio.Snapshot, error) {
	limits, err := limits.Normalize()
	if err != nil {
		return audio.Snapshot{}, err
	}
	playback, playbackErr := o.run(ctx, "aplay", "-L")
	capture, captureErr := o.run(ctx, "arecord", "-L")
	snapshot := audio.Snapshot{
		Backend: "alsa", ObservedAt: time.Now().UTC(),
		Devices: []audio.Device{},
	}
	if playbackErr != nil && captureErr != nil {
		snapshot.Status = audio.Status{State: "unsupported", Detail: "aplay and arecord are unavailable"}
		return snapshot, fmt.Errorf("%w: ALSA tools failed", audio.ErrUnavailable)
	}
	var malformed int
	reasons := []string{}
	for _, source := range []struct {
		payload []byte
		err     error
		kind    audio.Kind
	}{
		{playback, playbackErr, audio.KindOutput},
		{capture, captureErr, audio.KindInput},
	} {
		if source.err != nil {
			reasons = append(reasons, "one ALSA direction is unavailable")
			continue
		}
		devices, skipped, limited := parseALSA(string(source.payload), source.kind, limits, len(snapshot.Devices))
		snapshot.Devices = append(snapshot.Devices, devices...)
		malformed += skipped
		if limited {
			reasons = append(reasons, "ALSA device list exceeded the bound")
		}
	}
	if malformed > 0 {
		reasons = append(reasons, fmt.Sprintf("%d unusable ALSA records", malformed))
	}
	if len(reasons) > 0 {
		snapshot.Status = audio.Status{State: "partial", Detail: strings.Join(reasons, "; ")}
	} else {
		snapshot.Status = audio.Status{State: "complete"}
	}
	if err := audio.ValidateSnapshot(snapshot, limits); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

// parseALSA reads the `aplay -L`/`arecord -L` listing: an unindented PCM name
// followed by indented description lines. IDs are prefixed with the direction
// because ALSA lists the same `default` PCM for both directions.
func parseALSA(payload string, kind audio.Kind, limits audio.Limits, used int) ([]audio.Device, int, bool) {
	devices := []audio.Device{}
	seen := map[string]bool{}
	var current string
	var description string
	malformed := 0
	limited := false
	flush := func() {
		if current == "" {
			return
		}
		if current == "null" {
			current, description = "", ""
			return
		}
		id := string(kind) + ":" + current
		if seen[id] {
			current, description = "", ""
			return
		}
		seen[id] = true
		if used+len(devices) >= limits.MaxDevices {
			limited = true
			current, description = "", ""
			return
		}
		name := description
		if name == "" {
			name = current
		}
		devices = append(devices, audio.Device{
			ID: id, Name: name, Kind: kind, State: "",
		})
		current, description = "", ""
	}
	for _, line := range strings.Split(payload, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "***") {
			continue
		}
		indented := line != "" && (line[0] == ' ' || line[0] == '\t')
		if !indented {
			flush()
			current = trimmed
			description = ""
			continue
		}
		if current == "" {
			malformed++
			continue
		}
		if description == "" {
			description = trimmed
		}
	}
	flush()
	return devices, malformed, limited
}

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ErrInvalidDevice reports an ID that cannot be passed to arecord or aplay.
var ErrInvalidDevice = errors.New("invalid ALSA device")

func splitDevice(id string, kind audio.Kind) (string, error) {
	prefix := string(kind) + ":"
	if !strings.HasPrefix(id, prefix) {
		return "", fmt.Errorf("%w: expected a %s device", ErrInvalidDevice, kind)
	}
	pcm := strings.TrimPrefix(id, prefix)
	if pcm == "" || len(pcm) > 256 || strings.HasPrefix(pcm, "-") ||
		strings.ContainsAny(pcm, "\x00\r\n") {
		return "", fmt.Errorf("%w: ALSA PCM name is required and must not be an option", ErrInvalidDevice)
	}
	return pcm, nil
}
