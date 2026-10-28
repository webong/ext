// Package pipewire observes PipeWire endpoint state and opens bounded capture
// or playback streams through pw-dump, pw-record, and pw-play. The caller
// selects the device and owns routing policy; no device is chosen or connected
// implicitly.
//
// PipeWire is a selectable native service, so this implementation lives in an
// adapter instead of res/system. The PulseAudio adapter remains separate and
// the shared library never selects between them.
package pipewire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webong/ext/res/system/audio"
)

const (
	// DefaultMaxOutputBytes bounds the pw-dump payload before JSON decoding.
	DefaultMaxOutputBytes = 8 << 20
	// MaxOutputBytesCeiling is the largest accepted output bound.
	MaxOutputBytesCeiling = 32 << 20
	// maxDumpObjects bounds the number of top-level pw-dump records inspected.
	maxDumpObjects = 16_384
)

var errOutputLimit = errors.New("PipeWire command output exceeded the bound")

// CommandRunner is the native command boundary. Output is captured and bounded
// by the observer before it is decoded.
type CommandRunner func(context.Context, string, ...string) ([]byte, error)

type Observer struct {
	run CommandRunner
	// MaxOutputBytes bounds the pw-dump payload. Zero selects
	// DefaultMaxOutputBytes.
	MaxOutputBytes int
}

func NewObserver(run CommandRunner) *Observer {
	if run == nil {
		run = defaultRunner
	}
	return &Observer{run: run, MaxOutputBytes: DefaultMaxOutputBytes}
}

func (o *Observer) Observe(ctx context.Context, limits audio.Limits) (audio.Snapshot, error) {
	limits, err := limits.Normalize()
	if err != nil {
		return audio.Snapshot{}, err
	}
	maxBytes := o.MaxOutputBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxOutputBytes
	}
	if maxBytes < 1 || maxBytes > MaxOutputBytesCeiling {
		return audio.Snapshot{}, fmt.Errorf("%w: output bound must be between 1 and %d", audio.ErrInvalid, MaxOutputBytesCeiling)
	}
	snapshot := audio.Snapshot{
		Backend: "pipewire", ObservedAt: time.Now().UTC(),
		Devices: []audio.Device{},
	}
	payload, runErr := o.run(ctx, "pw-dump")
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return snapshot, ctxErr
		}
		if errors.Is(runErr, errOutputLimit) {
			snapshot.Status = audio.Status{State: "partial", Detail: "pw-dump output exceeded the bound"}
			return snapshot, fmt.Errorf("%w: pw-dump output exceeded the bound", audio.ErrLimit)
		}
		snapshot.Status = audio.Status{State: "unsupported", Detail: "pw-dump is unavailable"}
		return snapshot, fmt.Errorf("%w: pw-dump failed: %v", audio.ErrUnavailable, runErr)
	}
	if len(payload) > maxBytes {
		snapshot.Status = audio.Status{State: "partial", Detail: "pw-dump output exceeded the bound"}
		return snapshot, fmt.Errorf("%w: pw-dump output is %d bytes", audio.ErrLimit, len(payload))
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		snapshot.Status = audio.Status{State: "partial", Detail: "pw-dump returned no data"}
		return snapshot, fmt.Errorf("%w: pw-dump returned no data", audio.ErrUnavailable)
	}
	var objects []dumpObject
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decodeErr := decoder.Decode(&objects); decodeErr != nil {
		snapshot.Status = audio.Status{State: "partial", Detail: "PipeWire object list could not be parsed"}
		return snapshot, fmt.Errorf("%w: pw-dump output could not be parsed: %v", audio.ErrUnavailable, decodeErr)
	}
	if _, decodeErr := decoder.Token(); decodeErr != io.EOF {
		snapshot.Status = audio.Status{State: "partial", Detail: "PipeWire object list has trailing data"}
		return snapshot, fmt.Errorf("%w: pw-dump output has trailing data", audio.ErrUnavailable)
	}
	if len(objects) > maxDumpObjects {
		objects = objects[:maxDumpObjects]
		snapshot.Status = audio.Status{State: "partial", Detail: "PipeWire object list exceeded the bound"}
	}
	seen := map[string]bool{}
	var skipped int
	for _, object := range objects {
		if object.Type != "PipeWire:Interface:Node" || object.Info == nil {
			continue
		}
		props := object.Info.Props
		mediaClass := strings.ToLower(strings.TrimSpace(propertyString(props, "media.class")))
		name := strings.TrimSpace(propertyString(props, "node.name"))
		var kind audio.Kind
		switch {
		case strings.HasPrefix(mediaClass, "audio/monitor"):
			kind = audio.KindMonitor
		case strings.HasPrefix(mediaClass, "audio/sink"):
			kind = audio.KindOutput
		case strings.HasPrefix(mediaClass, "audio/source"):
			if strings.HasSuffix(name, ".monitor") {
				kind = audio.KindMonitor
			} else {
				kind = audio.KindInput
			}
		default:
			continue
		}
		if validateDevice(name) != nil || !printableName(name) {
			skipped++
			continue
		}
		if seen[name] {
			skipped++
			continue
		}
		if len(snapshot.Devices) >= limits.MaxDevices {
			snapshot.Status = audio.Status{State: "partial", Detail: "PipeWire device list exceeded the bound"}
			break
		}
		state := strings.ToLower(strings.TrimSpace(object.Info.State))
		if state == "" {
			state = strings.ToLower(strings.TrimSpace(propertyString(props, "node.state")))
		}
		if !printableName(state) || len(state) > 64 {
			state = ""
		}
		device := audio.Device{
			ID: name, Name: name, Kind: kind, State: state,
			SampleFormat: "s16",
		}
		if rate := deviceInt(props, object.Info.Params, "audio.rate", "rate"); rate >= 8_000 && rate <= 192_000 {
			device.SampleRate = rate
		}
		if channels := deviceInt(props, object.Info.Params, "audio.channels", "channels"); channels >= 1 && channels <= 8 {
			device.Channels = channels
		}
		seen[name] = true
		snapshot.Devices = append(snapshot.Devices, device)
	}
	if skipped > 0 && snapshot.Status.State == "" {
		snapshot.Status = audio.Status{State: "partial", Detail: fmt.Sprintf("%d malformed PipeWire records", skipped)}
	}
	if snapshot.Status.State == "" {
		snapshot.Status = audio.Status{State: "complete"}
	}
	if err := audio.ValidateSnapshot(snapshot, limits); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

// dumpObject is one object in the pw-dump JSON array. pw-dump wraps node
// metadata in info; state is a sibling of props, not a property.
type dumpObject struct {
	ID   int       `json:"id"`
	Type string    `json:"type"`
	Info *dumpInfo `json:"info"`
}

type dumpInfo struct {
	State  string                     `json:"state"`
	Props  map[string]json.RawMessage `json:"props"`
	Params map[string]json.RawMessage `json:"params"`
}

func propertyString(props map[string]json.RawMessage, key string) string {
	raw, ok := props[key]
	if !ok || len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	number, ok := rawNumber(raw)
	if ok {
		return number.String()
	}
	return ""
}

func deviceInt(props map[string]json.RawMessage, params map[string]json.RawMessage, property, parameter string) int {
	if value, ok := rawInt(props[property]); ok {
		return value
	}
	for _, key := range []string{"Format", "EnumFormat"} {
		raw, ok := params[key]
		if !ok || len(raw) == 0 {
			continue
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			continue
		}
		for _, entry := range entries {
			if value, ok := rawInt(entry[parameter]); ok {
				return value
			}
		}
	}
	return 0
}

func rawNumber(raw json.RawMessage) (json.Number, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value json.Number
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	return value, true
}

func rawInt(raw json.RawMessage) (int, bool) {
	number, ok := rawNumber(raw)
	if !ok {
		return 0, false
	}
	value, err := strconv.Atoi(number.String())
	if err != nil {
		return 0, false
	}
	return value, true
}

// ErrInvalidDevice reports a device ID that cannot be passed to a native tool.
var ErrInvalidDevice = errors.New("invalid PipeWire device")

func validateDevice(id string) error {
	if id == "" || len(id) > 256 || strings.HasPrefix(id, "-") ||
		strings.ContainsAny(id, "/\\\x00\r\n") || !utf8.ValidString(id) {
		return fmt.Errorf("%w: device ID is required and must be a bare PipeWire node name", ErrInvalidDevice)
	}
	return nil
}

func printableName(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

// boundedBuffer stops collecting once the hard command-output ceiling is
// crossed, so a misbehaving native tool cannot exhaust memory.
type boundedBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.buffer.Len()+len(data) > MaxOutputBytesCeiling {
		b.overflow = true
		return 0, errOutputLimit
	}
	return b.buffer.Write(data)
}

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	output := &boundedBuffer{}
	command.Stdout = output
	command.Stderr = io.Discard
	err := command.Run()
	if output.overflow {
		return nil, errOutputLimit
	}
	if err != nil {
		return output.buffer.Bytes(), err
	}
	return output.buffer.Bytes(), nil
}
