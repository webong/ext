// Package pulseaudio observes PulseAudio endpoint state and opens bounded
// capture or playback streams through parec and pacat. The caller selects the
// device and owns routing policy; no device is chosen or connected implicitly.
//
// PulseAudio is a selectable native service, so this implementation lives in an
// adapter instead of res/system. PipeWire's pulse compatibility presents the
// same protocol; a native PipeWire backend would be a separate adapter.
package pulseaudio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/webong/ext/res/system/audio"
)

// CommandRunner is the native command boundary. Output is captured and bounded
// by the caller's runner.
type CommandRunner func(context.Context, string, ...string) ([]byte, error)

var sampleSpec = regexp.MustCompile(`^(\S+) (\d+)ch (\d+)Hz$`)

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
	sinks, sinkErr := o.run(ctx, "pactl", "list", "short", "sinks")
	sources, sourceErr := o.run(ctx, "pactl", "list", "short", "sources")
	snapshot := audio.Snapshot{
		Backend: "pulseaudio", ObservedAt: time.Now().UTC(),
		Devices: []audio.Device{},
	}
	if sinkErr != nil && sourceErr != nil {
		snapshot.Status = audio.Status{State: "unsupported", Detail: "pactl is unavailable"}
		return snapshot, fmt.Errorf("%w: pactl failed", audio.ErrUnavailable)
	}
	var skipped int
	reasons := []string{}
	for _, parse := range []struct {
		payload []byte
		err     error
		kind    audio.Kind
	}{
		{sinks, sinkErr, audio.KindOutput},
		{sources, sourceErr, audio.KindInput},
	} {
		if parse.err != nil {
			reasons = append(reasons, "one PulseAudio device class is unavailable")
			continue
		}
		devices, malformed, limited := parsePulseDevices(string(parse.payload), parse.kind, limits, len(snapshot.Devices))
		skipped += malformed
		snapshot.Devices = append(snapshot.Devices, devices...)
		if limited {
			reasons = append(reasons, "PulseAudio device list exceeded the bound")
		}
	}
	if skipped > 0 {
		reasons = append(reasons, fmt.Sprintf("%d malformed PulseAudio records", skipped))
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

func parsePulseDevices(payload string, kind audio.Kind, limits audio.Limits, used int) ([]audio.Device, int, bool) {
	devices := []audio.Device{}
	malformed := 0
	limited := false
	for _, line := range strings.Split(payload, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			malformed++
			continue
		}
		id := strings.TrimSpace(fields[1])
		if id == "" || strings.ContainsRune(id, 0) {
			malformed++
			continue
		}
		if used+len(devices) >= limits.MaxDevices {
			limited = true
			break
		}
		deviceKind := kind
		if kind == audio.KindInput && strings.HasSuffix(id, ".monitor") {
			deviceKind = audio.KindMonitor
		}
		device := audio.Device{
			ID: id, Name: id, Kind: deviceKind,
			State: strings.ToLower(strings.TrimSpace(fields[4])),
		}
		if match := sampleSpec.FindStringSubmatch(strings.TrimSpace(fields[3])); match != nil {
			device.SampleFormat = match[1]
			device.Channels, _ = strconv.Atoi(match[2])
			device.SampleRate, _ = strconv.Atoi(match[3])
		}
		devices = append(devices, device)
	}
	return devices, malformed, limited
}

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ErrInvalidDevice reports a device ID that cannot be passed to a native tool.
var ErrInvalidDevice = errors.New("invalid PulseAudio device")

func validateDevice(id string) error {
	if id == "" || len(id) > 256 || strings.HasPrefix(id, "-") ||
		strings.ContainsAny(id, "/\\\x00\r\n") {
		return fmt.Errorf("%w: device ID is required and must be a bare PulseAudio name", ErrInvalidDevice)
	}
	return nil
}
