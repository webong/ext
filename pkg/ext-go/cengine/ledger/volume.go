package ledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PrivateVolumeLimits bounds native inspection and removal. Zero selects the
// ledger's default (10,000 entries, 1 GiB and 32 path components).
type PrivateVolumeLimits struct {
	MaxEntries int   `json:"max_entries"`
	MaxBytes   int64 `json:"max_bytes"`
	MaxDepth   int   `json:"max_depth"`
}

// PrivateVolume is the result of a ledger inspection. Only a value returned
// by CreatePrivateVolume or InspectPrivateVolume can be removed through Go.
type PrivateVolume struct {
	Path      string `json:"path"`
	Identity  string `json:"identity"`
	Created   bool   `json:"created"`
	Entries   int    `json:"entries"`
	Bytes     int64  `json:"bytes"`
	inspected bool
}

var (
	ErrUnsafeVolumePath = errors.New("unsafe private volume path")
	ErrStaleVolume      = errors.New("private volume identity changed")
	ErrVolumeLimit      = errors.New("private volume limit exceeded")
	ErrInvalidVolume    = errors.New("invalid private volume")
)

func volumeError(err error) error {
	if err == nil {
		return nil
	}
	switch Code(err) {
	case "invalid":
		return fmt.Errorf("%w: %v", ErrInvalidVolume, err)
	case "denied":
		return fmt.Errorf("%w: %v", ErrUnsafeVolumePath, err)
	case "changed":
		return fmt.Errorf("%w: %v", ErrStaleVolume, err)
	case "too_large":
		return fmt.Errorf("%w: %v", ErrVolumeLimit, err)
	case "not_found":
		return fmt.Errorf("%w: %v", os.ErrNotExist, err)
	default:
		return err
	}
}

func volumeInput(path string, limits PrivateVolumeLimits) map[string]any {
	return map[string]any{
		"path": path,
		"max_entries": limits.MaxEntries,
		"max_bytes": limits.MaxBytes,
		"max_depth": limits.MaxDepth,
	}
}

func (l *Library) privateVolume(ctx context.Context, operation, path string, limits PrivateVolumeLimits) (PrivateVolume, error) {
	var value PrivateVolume
	err := call(ctx, l.api.Library(), operation, volumeInput(path, limits), &value)
	if err != nil {
		return PrivateVolume{}, volumeError(err)
	}
	value.Path = filepath.Clean(value.Path)
	value.inspected = true
	return value, nil
}

// CreatePrivateVolume creates and inspects a caller-selected private directory.
// Ledger never chooses the path or authorizes its removal.
func (l *Library) CreatePrivateVolume(ctx context.Context, path string, limits PrivateVolumeLimits) (PrivateVolume, error) {
	return l.privateVolume(ctx, "volume_create", path, limits)
}

// InspectPrivateVolume validates a private directory and measures its contents.
func (l *Library) InspectPrivateVolume(ctx context.Context, path string, limits PrivateVolumeLimits) (PrivateVolume, error) {
	return l.privateVolume(ctx, "volume_inspect", path, limits)
}

// RemovePrivateVolume reopens and remeasures the exact inspected directory
// before removing it. The caller remains responsible for authorization.
func (l *Library) RemovePrivateVolume(ctx context.Context, volume PrivateVolume, limits PrivateVolumeLimits) error {
	if !volume.inspected || volume.Identity == "" || volume.Path == "" {
		return ErrInvalidVolume
	}
	input := volumeInput(volume.Path, limits)
	input["identity"] = volume.Identity
	return volumeError(call(ctx, l.api.Library(), "volume_remove", input, nil))
}
