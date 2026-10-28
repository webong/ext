// Package pipewire observes PipeWire endpoint state and opens bounded capture
// or playback streams through pw-cli, pw-record, and pw-play. The caller
// selects the device and owns routing policy; no device is chosen or connected
// implicitly.
//
// PipeWire is a selectable native service, so this implementation lives in an
// adapter instead of res/system. The PulseAudio adapter remains separate and
// the shared library never selects between them.
package pipewire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/webong/ext/res/system/audio"
)

var nodeSpec = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)

// CommandRunner is the native command boundary. Output is captured and bounded
// by the caller's runner.
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
	snapshot := audio.Snapshot{
		Backend: "pipewire", ObservedAt: time.Now().UTC(),
		Devices: []audio.Device{},
	}
	payload, err := o.run(ctx, "pw-cli", "list-objects")
	if err != nil {
		snapshot.Status = audio.Status{State: "unsupported", Detail: "pw-cli is unavailable"}
		return snapshot, fmt.Errorf("%w: pw-cli failed", audio.ErrUnavailable)
	}
	var nodes []pipewireNode
	if decodeErr := json.Unmarshal(payload, &nodes); decodeErr != nil {
		snapshot.Status = audio.Status{State: "partial", Detail: "PipeWire object list could not be parsed"}
		return snapshot, fmt.Errorf("%w: pw-cli output could not be parsed", audio.ErrUnavailable)
	}
	var skipped int
	for _, node := range nodes {
		if node.Type != "PipeWire:Interface:Node" {
			continue
		}
		props := node.Info.Props
		mediaClass := strings.ToLower(strings.TrimSpace(props["media.class"]))
		var kind audio.Kind
		switch {
		case strings.HasPrefix(mediaClass, "audio/sink"):
			kind = audio.KindOutput
		case strings.HasPrefix(mediaClass, "audio/source"):
			kind = audio.KindInput
		case strings.HasPrefix(mediaClass, "audio/monitor"):
			kind = audio.KindMonitor
		default:
			continue
		}
		name := strings.TrimSpace(props["node.name"])
		if name == "" || len(name) > 256 || strings.ContainsRune(name, 0) ||
			strings.HasPrefix(name, "-") || strings.ContainsAny(name, "/\\\x00\r\n") {
			skipped++
			continue
		}
		if len(snapshot.Devices) >= limits.MaxDevices {
			snapshot.Status = audio.Status{State: "partial", Detail: "PipeWire device list exceeded the bound"}
			break
		}
		device := audio.Device{
			ID: name, Name: name, Kind: kind,
			State: strings.ToLower(strings.TrimSpace(props["node.state"])),
		}
		if rate, parseErr := strconv.Atoi(strings.TrimSpace(props["audio.rate"])); parseErr == nil && rate > 0 {
			device.SampleRate = rate
		}
		if channels, parseErr := strconv.Atoi(strings.TrimSpace(props["audio.channels"])); parseErr == nil && channels > 0 {
			device.Channels = channels
		}
		device.SampleFormat = "s16le"
		snapshot.Devices = append(snapshot.Devices, device)
	}
	if skipped > 0 {
		if snapshot.Status.State == "" {
			snapshot.Status = audio.Status{State: "partial", Detail: fmt.Sprintf("%d malformed PipeWire records", skipped)}
		}
	}
	if snapshot.Status.State == "" {
		snapshot.Status = audio.Status{State: "complete"}
	}
	if err := audio.ValidateSnapshot(snapshot, limits); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

type pipewireNode struct {
	Type string       `json:"type"`
	Info pipewireInfo `json:"info"`
}

type pipewireInfo struct {
	Props pipewireProps `json:"props"`
}

type pipewireProps map[string]string

// ErrInvalidDevice reports a device ID that cannot be passed to a native tool.
var ErrInvalidDevice = errors.New("invalid PipeWire device")

func validateDevice(id string) error {
	if id == "" || len(id) > 256 || strings.HasPrefix(id, "-") ||
		strings.ContainsAny(id, "/\\\x00\r\n") {
		return fmt.Errorf("%w: device ID is required and must be a bare PipeWire node name", ErrInvalidDevice)
	}
	return nil
}

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
